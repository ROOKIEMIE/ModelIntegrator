package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"model-control-plane/src/pkg/model"
)

type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

func Open(path string, logger *slog.Logger) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite failed: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite failed: %w", err)
	}

	store := &Store{db: db, logger: logger}
	if err := store.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys failed: %w", err)
	}
	for _, stmt := range schemaStatements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init sqlite schema failed: %w", err)
		}
	}
	// 对历史数据库做轻量增量迁移，确保新增字段存在。
	for _, migration := range []struct {
		table      string
		column     string
		definition string
	}{
		{table: "models", column: "desired_state", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "observed_state", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "readiness", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "health_message", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "models", column: "last_reconciled_at", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s);", table))
	if err != nil {
		return fmt.Errorf("read sqlite table info failed: table=%s err=%w", table, err)
	}
	defer rows.Close()

	exists := false
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			defaultV  sql.NullString
			primaryID int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryID); err != nil {
			return fmt.Errorf("scan sqlite table info failed: table=%s err=%w", table, err)
		}
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
			exists = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite table info failed: table=%s err=%w", table, err)
	}
	if exists {
		return nil
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s;", table, column, definition)
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("add sqlite column failed: table=%s column=%s err=%w", table, column, err)
	}
	return nil
}

func (s *Store) UpsertAgent(ctx context.Context, agent model.Agent) error {
	if strings.TrimSpace(agent.ID) == "" {
		return fmt.Errorf("agent id is empty")
	}
	now := time.Now().UTC()
	registeredAt := agent.RegisteredAt
	if registeredAt.IsZero() {
		registeredAt = now
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (
			id, node_id, name, version, address, status,
			capabilities_json, runtime_capabilities_json, metadata_json,
			registered_at, last_heartbeat_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			node_id = excluded.node_id,
			name = excluded.name,
			version = excluded.version,
			address = excluded.address,
			status = excluded.status,
			capabilities_json = excluded.capabilities_json,
			runtime_capabilities_json = excluded.runtime_capabilities_json,
			metadata_json = excluded.metadata_json,
			registered_at = CASE
				WHEN agents.registered_at = '' THEN excluded.registered_at
				ELSE agents.registered_at
			END,
			last_heartbeat_at = excluded.last_heartbeat_at,
			updated_at = excluded.updated_at;
	`,
		agent.ID,
		strings.TrimSpace(agent.NodeID),
		firstNonEmpty(strings.TrimSpace(agent.Name), strings.TrimSpace(agent.ID)),
		strings.TrimSpace(agent.Version),
		firstNonEmpty(strings.TrimSpace(agent.Address), strings.TrimSpace(agent.Host)),
		firstNonEmpty(string(agent.Status), string(model.AgentStatusNone)),
		mustJSON(agent.Capabilities, "[]"),
		mustJSON(agent.RuntimeCapabilities, "{}"),
		mustJSON(agent.Metadata, "{}"),
		timeToText(registeredAt),
		timeToText(agent.LastHeartbeatAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert agent failed: %w", err)
	}
	return nil
}

func (s *Store) ListAgents(ctx context.Context) ([]model.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, name, version, address, status,
		       capabilities_json, runtime_capabilities_json, metadata_json,
		       registered_at, last_heartbeat_at
		FROM agents
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.Agent, 0, 16)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetAgentByID(ctx context.Context, id string) (model.Agent, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, node_id, name, version, address, status,
		       capabilities_json, runtime_capabilities_json, metadata_json,
		       registered_at, last_heartbeat_at
		FROM agents WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		agent                   model.Agent
		statusRaw               string
		capabilitiesJSON        string
		runtimeCapabilitiesJSON string
		metadataJSON            string
		registeredAtRaw         string
		lastHeartbeatAtRaw      string
	)
	err := row.Scan(
		&agent.ID, &agent.NodeID, &agent.Name, &agent.Version, &agent.Address, &statusRaw,
		&capabilitiesJSON, &runtimeCapabilitiesJSON, &metadataJSON, &registeredAtRaw, &lastHeartbeatAtRaw,
	)
	if err == sql.ErrNoRows {
		return model.Agent{}, false, nil
	}
	if err != nil {
		return model.Agent{}, false, fmt.Errorf("scan agent by id failed: %w", err)
	}
	agent.AgentID = agent.ID
	agent.Status = model.AgentConnectionStatus(strings.TrimSpace(statusRaw))
	agent.Capabilities = parseStringSlice(capabilitiesJSON)
	agent.RuntimeCapabilities = parseStringSliceMap(runtimeCapabilitiesJSON)
	agent.Metadata = parseStringMap(metadataJSON)
	agent.RegisteredAt = textToTime(registeredAtRaw)
	agent.LastHeartbeatAt = textToTime(lastHeartbeatAtRaw)
	return agent, true, nil
}

func (s *Store) ListAgentsByNodeID(ctx context.Context, nodeID string) ([]model.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, name, version, address, status,
		       capabilities_json, runtime_capabilities_json, metadata_json,
		       registered_at, last_heartbeat_at
		FROM agents
		WHERE node_id = ?
		ORDER BY id;
	`, strings.TrimSpace(nodeID))
	if err != nil {
		return nil, fmt.Errorf("list agents by node failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.Agent, 0, 4)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents by node failed: %w", err)
	}
	return out, nil
}

func (s *Store) UpsertNodeWithRuntimes(ctx context.Context, node model.Node) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node tx failed: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := upsertNodeTx(ctx, tx, node); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runtimes WHERE node_id = ?`, node.ID); err != nil {
		return fmt.Errorf("delete node runtimes failed: %w", err)
	}
	for _, rt := range node.Runtimes {
		if strings.TrimSpace(rt.ID) == "" {
			continue
		}
		if err := upsertRuntimeTx(ctx, tx, node.ID, rt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit node tx failed: %w", err)
	}
	return nil
}

func (s *Store) UpsertNodesWithRuntimes(ctx context.Context, nodes []model.Node) error {
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			continue
		}
		if err := s.UpsertNodeWithRuntimes(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	nodeRows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, role, type, host, status,
		       classification, capability_tier, capability_source, agent_status,
		       metadata_json, last_seen_at
		FROM nodes
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list nodes failed: %w", err)
	}
	defer nodeRows.Close()

	nodes := make([]model.Node, 0, 16)
	nodesByID := make(map[string]*model.Node, 16)
	for nodeRows.Next() {
		var (
			n                   model.Node
			roleRaw             string
			typeRaw             string
			statusRaw           string
			classificationRaw   string
			capabilityTierRaw   string
			capabilitySourceRaw string
			agentStatusRaw      string
			metadataJSON        string
			lastSeenAtRaw       string
		)
		if err := nodeRows.Scan(
			&n.ID, &n.Name, &n.Description, &roleRaw, &typeRaw, &n.Host, &statusRaw,
			&classificationRaw, &capabilityTierRaw, &capabilitySourceRaw, &agentStatusRaw,
			&metadataJSON, &lastSeenAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan node failed: %w", err)
		}
		n.Role = model.NodeRole(strings.TrimSpace(roleRaw))
		n.Type = model.NodeType(strings.TrimSpace(typeRaw))
		n.Status = model.NodeStatus(strings.TrimSpace(statusRaw))
		n.Classification = model.NodeClassification(strings.TrimSpace(classificationRaw))
		n.CapabilityTier = model.CapabilityTier(strings.TrimSpace(capabilityTierRaw))
		n.CapabilitySource = model.CapabilitySource(strings.TrimSpace(capabilitySourceRaw))
		n.AgentStatus = model.AgentConnectionStatus(strings.TrimSpace(agentStatusRaw))
		n.LastSeenAt = textToTime(lastSeenAtRaw)
		n.Metadata = parseNodeMetadata(metadataJSON)
		n.Runtimes = []model.Runtime{}
		nodes = append(nodes, n)
		nodesByID[n.ID] = &nodes[len(nodes)-1]
	}
	if err := nodeRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes failed: %w", err)
	}

	rtRows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, type, endpoint, enabled, status, capability_source,
		       capabilities_json, actions_json, last_seen_at, metadata_json
		FROM runtimes
		ORDER BY node_id, id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtimes failed: %w", err)
	}
	defer rtRows.Close()

	for rtRows.Next() {
		var (
			rt                  model.Runtime
			nodeID              string
			typeRaw             string
			enabledRaw          int
			statusRaw           string
			capabilitySourceRaw string
			capabilitiesJSON    string
			actionsJSON         string
			lastSeenAtRaw       string
			metadataJSON        string
		)
		if err := rtRows.Scan(
			&rt.ID, &nodeID, &typeRaw, &rt.Endpoint, &enabledRaw, &statusRaw, &capabilitySourceRaw,
			&capabilitiesJSON, &actionsJSON, &lastSeenAtRaw, &metadataJSON,
		); err != nil {
			return nil, fmt.Errorf("scan runtime failed: %w", err)
		}
		node := nodesByID[strings.TrimSpace(nodeID)]
		if node == nil {
			continue
		}
		rt.Type = model.RuntimeType(strings.TrimSpace(typeRaw))
		rt.Enabled = enabledRaw == 1
		rt.Status = model.RuntimeStatus(strings.TrimSpace(statusRaw))
		rt.CapabilitySource = model.CapabilitySource(strings.TrimSpace(capabilitySourceRaw))
		rt.Capabilities = parseStringSlice(capabilitiesJSON)
		rt.Actions = parseStringSlice(actionsJSON)
		rt.LastSeenAt = textToTime(lastSeenAtRaw)
		rt.Metadata = parseStringMap(metadataJSON)
		node.Runtimes = append(node.Runtimes, rt)
	}
	if err := rtRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtimes failed: %w", err)
	}
	return nodes, nil
}

func (s *Store) UpsertModel(ctx context.Context, item model.Model) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("model id is empty")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO models (
			id, name, provider, backend_type, host_node_id, runtime_id,
			endpoint, state, desired_state, observed_state, readiness, health_message, last_reconciled_at,
			context_length, metadata_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			provider = excluded.provider,
			backend_type = excluded.backend_type,
			host_node_id = excluded.host_node_id,
			runtime_id = excluded.runtime_id,
			endpoint = excluded.endpoint,
			state = excluded.state,
			desired_state = excluded.desired_state,
			observed_state = excluded.observed_state,
			readiness = excluded.readiness,
			health_message = excluded.health_message,
			last_reconciled_at = excluded.last_reconciled_at,
			context_length = excluded.context_length,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at;
	`,
		item.ID,
		strings.TrimSpace(item.Name),
		strings.TrimSpace(item.Provider),
		strings.TrimSpace(string(item.BackendType)),
		strings.TrimSpace(item.HostNodeID),
		strings.TrimSpace(item.RuntimeID),
		strings.TrimSpace(item.Endpoint),
		firstNonEmpty(string(item.State), string(model.ModelStateUnknown)),
		firstNonEmpty(strings.TrimSpace(item.DesiredState), string(model.ModelStateUnknown)),
		firstNonEmpty(strings.TrimSpace(item.ObservedState), string(model.ModelStateUnknown)),
		firstNonEmpty(string(item.Readiness), string(model.ReadinessUnknown)),
		strings.TrimSpace(item.HealthMessage),
		timeToText(item.LastReconciledAt),
		item.ContextLength,
		mustJSON(item.Metadata, "{}"),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert model failed: %w", err)
	}
	return nil
}

func (s *Store) UpsertModels(ctx context.Context, items []model.Model) error {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if err := s.UpsertModel(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListModels(ctx context.Context) ([]model.Model, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, provider, backend_type, host_node_id, runtime_id,
		       endpoint, state, desired_state, observed_state, readiness, health_message, last_reconciled_at,
		       context_length, metadata_json
		FROM models
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list models failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.Model, 0, 32)
	for rows.Next() {
		var (
			item              model.Model
			backendRaw        string
			stateRaw          string
			desiredStateRaw   string
			observedStateRaw  string
			readinessRaw      string
			lastReconciledRaw string
			metadata          string
		)
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
			&item.Endpoint, &stateRaw, &desiredStateRaw, &observedStateRaw, &readinessRaw, &item.HealthMessage, &lastReconciledRaw,
			&item.ContextLength, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan model failed: %w", err)
		}
		item.BackendType = model.RuntimeType(strings.TrimSpace(backendRaw))
		item.State = model.ModelState(strings.TrimSpace(stateRaw))
		item.DesiredState = strings.TrimSpace(desiredStateRaw)
		item.ObservedState = strings.TrimSpace(observedStateRaw)
		item.Readiness = model.ReadinessState(strings.TrimSpace(readinessRaw))
		item.LastReconciledAt = textToTime(lastReconciledRaw)
		item.Metadata = parseStringMap(metadata)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate models failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetModelByID(ctx context.Context, id string) (model.Model, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, provider, backend_type, host_node_id, runtime_id,
		       endpoint, state, desired_state, observed_state, readiness, health_message, last_reconciled_at,
		       context_length, metadata_json
		FROM models
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		item              model.Model
		backendRaw        string
		stateRaw          string
		desiredStateRaw   string
		observedStateRaw  string
		readinessRaw      string
		lastReconciledRaw string
		metadata          string
	)
	err := row.Scan(
		&item.ID, &item.Name, &item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
		&item.Endpoint, &stateRaw, &desiredStateRaw, &observedStateRaw, &readinessRaw, &item.HealthMessage, &lastReconciledRaw,
		&item.ContextLength, &metadata,
	)
	if err == sql.ErrNoRows {
		return model.Model{}, false, nil
	}
	if err != nil {
		return model.Model{}, false, fmt.Errorf("get model failed: %w", err)
	}
	item.BackendType = model.RuntimeType(strings.TrimSpace(backendRaw))
	item.State = model.ModelState(strings.TrimSpace(stateRaw))
	item.DesiredState = strings.TrimSpace(desiredStateRaw)
	item.ObservedState = strings.TrimSpace(observedStateRaw)
	item.Readiness = model.ReadinessState(strings.TrimSpace(readinessRaw))
	item.LastReconciledAt = textToTime(lastReconciledRaw)
	item.Metadata = parseStringMap(metadata)
	return item, true, nil
}

func (s *Store) UpsertTask(ctx context.Context, task model.Task) error {
	if strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("task id is empty")
	}
	now := time.Now().UTC()
	createdAt := task.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, type, target_type, target_id, assigned_agent_id, worker_id,
			status, progress, message, detail_json, payload_json, error,
			created_at, accepted_at, started_at, finished_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			target_type = excluded.target_type,
			target_id = excluded.target_id,
			assigned_agent_id = excluded.assigned_agent_id,
			worker_id = excluded.worker_id,
			status = excluded.status,
			progress = excluded.progress,
			message = excluded.message,
			detail_json = excluded.detail_json,
			payload_json = excluded.payload_json,
			error = excluded.error,
			created_at = CASE WHEN tasks.created_at = '' THEN excluded.created_at ELSE tasks.created_at END,
			accepted_at = excluded.accepted_at,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			updated_at = excluded.updated_at;
	`,
		strings.TrimSpace(task.ID),
		strings.TrimSpace(string(task.Type)),
		strings.TrimSpace(string(task.TargetType)),
		strings.TrimSpace(task.TargetID),
		strings.TrimSpace(task.AssignedAgentID),
		strings.TrimSpace(task.WorkerID),
		firstNonEmpty(string(task.Status), string(model.TaskStatusPending)),
		clampProgress(task.Progress),
		strings.TrimSpace(task.Message),
		mustJSON(task.Detail, "{}"),
		mustJSON(task.Payload, "{}"),
		strings.TrimSpace(task.Error),
		timeToText(createdAt),
		timeToText(task.AcceptedAt),
		timeToText(task.StartedAt),
		timeToText(task.FinishedAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert task failed: %w", err)
	}
	return nil
}

func (s *Store) ListTasks(ctx context.Context, targetType, targetID string, limit int) ([]model.Task, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, type, target_type, target_id, assigned_agent_id, worker_id,
		       status, progress, message, detail_json, payload_json, error,
		       created_at, accepted_at, started_at, finished_at
		FROM tasks
	`
	args := make([]interface{}, 0, 3)
	filters := make([]string, 0, 2)
	if strings.TrimSpace(targetType) != "" {
		filters = append(filters, "target_type = ?")
		args = append(args, strings.TrimSpace(targetType))
	}
	if strings.TrimSpace(targetID) != "" {
		filters = append(filters, "target_id = ?")
		args = append(args, strings.TrimSpace(targetID))
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.Task, 0, limit)
	for rows.Next() {
		item, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetTaskByID(ctx context.Context, id string) (model.Task, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, target_type, target_id, assigned_agent_id, worker_id,
		       status, progress, message, detail_json, payload_json, error,
		       created_at, accepted_at, started_at, finished_at
		FROM tasks
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	item, err := scanTask(row)
	if err == sql.ErrNoRows {
		return model.Task{}, false, nil
	}
	if err != nil {
		return model.Task{}, false, err
	}
	return item, true, nil
}

func (s *Store) ClaimPendingTaskForAgent(ctx context.Context, agentID string, allowTypes []model.TaskType) (model.Task, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return model.Task{}, false, fmt.Errorf("agent id is empty")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Task{}, false, fmt.Errorf("begin task claim tx failed: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	query := `
		SELECT id, type, target_type, target_id, assigned_agent_id, worker_id,
		       status, progress, message, detail_json, payload_json, error,
		       created_at, accepted_at, started_at, finished_at
		FROM tasks
		WHERE status = ? AND assigned_agent_id = ?
	`
	args := []interface{}{string(model.TaskStatusPending), agentID}
	if len(allowTypes) > 0 {
		holders := make([]string, 0, len(allowTypes))
		for _, tp := range allowTypes {
			holders = append(holders, "?")
			args = append(args, strings.TrimSpace(string(tp)))
		}
		query += " AND type IN (" + strings.Join(holders, ",") + ")"
	}
	query += " ORDER BY created_at ASC LIMIT 1;"

	row := tx.QueryRowContext(ctx, query, args...)
	item, scanErr := scanTask(row)
	if scanErr == sql.ErrNoRows {
		return model.Task{}, false, nil
	}
	if scanErr != nil {
		return model.Task{}, false, scanErr
	}

	now := time.Now().UTC()
	item.Status = model.TaskStatusDispatched
	item.AcceptedAt = now
	item.WorkerID = agentID
	item.Message = "任务已分发到 agent"
	item.Progress = max(item.Progress, 5)

	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, accepted_at = ?, worker_id = ?, message = ?, progress = ?, updated_at = ?
		WHERE id = ?;
	`,
		string(item.Status),
		timeToText(item.AcceptedAt),
		item.WorkerID,
		item.Message,
		item.Progress,
		timeToText(now),
		item.ID,
	); err != nil {
		return model.Task{}, false, fmt.Errorf("update claimed task failed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return model.Task{}, false, fmt.Errorf("commit task claim tx failed: %w", err)
	}
	return item, true, nil
}

func (s *Store) UpsertTestRun(ctx context.Context, run model.TestRun) error {
	if strings.TrimSpace(run.TestRunID) == "" {
		return fmt.Errorf("test run id is empty")
	}
	now := time.Now().UTC()
	createdAt := run.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO test_runs (
			id, scenario, status, started_at, finished_at, log_path, summary, triggered_by, error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scenario = excluded.scenario,
			status = excluded.status,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			log_path = excluded.log_path,
			summary = excluded.summary,
			triggered_by = excluded.triggered_by,
			error = excluded.error,
			created_at = CASE WHEN test_runs.created_at = '' THEN excluded.created_at ELSE test_runs.created_at END,
			updated_at = excluded.updated_at;
	`,
		run.TestRunID,
		strings.TrimSpace(run.Scenario),
		firstNonEmpty(string(run.Status), string(model.TestRunStatusPending)),
		timeToText(run.StartedAt),
		timeToText(run.FinishedAt),
		strings.TrimSpace(run.LogPath),
		strings.TrimSpace(run.Summary),
		strings.TrimSpace(run.TriggeredBy),
		strings.TrimSpace(run.Error),
		timeToText(createdAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert test run failed: %w", err)
	}
	return nil
}

func (s *Store) ListTestRuns(ctx context.Context, limit int) ([]model.TestRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scenario, status, started_at, finished_at, log_path, summary, triggered_by, error, created_at
		FROM test_runs
		ORDER BY created_at DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list test runs failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.TestRun, 0, limit)
	for rows.Next() {
		item, scanErr := scanTestRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate test runs failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetTestRunByID(ctx context.Context, id string) (model.TestRun, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, scenario, status, started_at, finished_at, log_path, summary, triggered_by, error, created_at
		FROM test_runs
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	item, err := scanTestRun(row)
	if err == sql.ErrNoRows {
		return model.TestRun{}, false, nil
	}
	if err != nil {
		return model.TestRun{}, false, err
	}
	return item, true, nil
}

func upsertNodeTx(ctx context.Context, tx *sql.Tx, node model.Node) error {
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (
			id, name, description, role, type, host, status,
			classification, capability_tier, capability_source, agent_status,
			metadata_json, last_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			role = excluded.role,
			type = excluded.type,
			host = excluded.host,
			status = excluded.status,
			classification = excluded.classification,
			capability_tier = excluded.capability_tier,
			capability_source = excluded.capability_source,
			agent_status = excluded.agent_status,
			metadata_json = excluded.metadata_json,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at;
	`,
		node.ID,
		strings.TrimSpace(node.Name),
		strings.TrimSpace(node.Description),
		strings.TrimSpace(string(node.Role)),
		strings.TrimSpace(string(node.Type)),
		strings.TrimSpace(node.Host),
		firstNonEmpty(string(node.Status), string(model.NodeStatusUnknown)),
		firstNonEmpty(string(node.Classification), string(model.NodeClassificationUnknown)),
		firstNonEmpty(string(node.CapabilityTier), string(model.CapabilityTierUnknown)),
		firstNonEmpty(string(node.CapabilitySource), string(model.CapabilitySourceUnknown)),
		firstNonEmpty(string(node.AgentStatus), string(model.AgentStatusNone)),
		mustJSON(node.Metadata, "{}"),
		timeToText(node.LastSeenAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert node failed: %w", err)
	}
	return nil
}

func upsertRuntimeTx(ctx context.Context, tx *sql.Tx, nodeID string, rt model.Runtime) error {
	now := time.Now().UTC()
	enabled := 0
	if rt.Enabled {
		enabled = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO runtimes (
			id, node_id, type, endpoint, enabled, status, capability_source,
			capabilities_json, actions_json, last_seen_at, metadata_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			node_id = excluded.node_id,
			type = excluded.type,
			endpoint = excluded.endpoint,
			enabled = excluded.enabled,
			status = excluded.status,
			capability_source = excluded.capability_source,
			capabilities_json = excluded.capabilities_json,
			actions_json = excluded.actions_json,
			last_seen_at = excluded.last_seen_at,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at;
	`,
		rt.ID,
		strings.TrimSpace(nodeID),
		strings.TrimSpace(string(rt.Type)),
		strings.TrimSpace(rt.Endpoint),
		enabled,
		firstNonEmpty(string(rt.Status), string(model.RuntimeStatusUnknown)),
		firstNonEmpty(string(rt.CapabilitySource), string(model.CapabilitySourceUnknown)),
		mustJSON(rt.Capabilities, "[]"),
		mustJSON(rt.Actions, "[]"),
		timeToText(rt.LastSeenAt),
		mustJSON(rt.Metadata, "{}"),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert runtime failed: %w", err)
	}
	return nil
}

type agentScanner interface {
	Scan(dest ...interface{}) error
}

func scanAgent(scanner agentScanner) (model.Agent, error) {
	var (
		agent                   model.Agent
		statusRaw               string
		capabilitiesJSON        string
		runtimeCapabilitiesJSON string
		metadataJSON            string
		registeredAtRaw         string
		lastHeartbeatAtRaw      string
	)
	if err := scanner.Scan(
		&agent.ID, &agent.NodeID, &agent.Name, &agent.Version, &agent.Address, &statusRaw,
		&capabilitiesJSON, &runtimeCapabilitiesJSON, &metadataJSON, &registeredAtRaw, &lastHeartbeatAtRaw,
	); err != nil {
		return model.Agent{}, fmt.Errorf("scan agent failed: %w", err)
	}
	agent.AgentID = agent.ID
	agent.Status = model.AgentConnectionStatus(strings.TrimSpace(statusRaw))
	agent.Capabilities = parseStringSlice(capabilitiesJSON)
	agent.RuntimeCapabilities = parseStringSliceMap(runtimeCapabilitiesJSON)
	agent.Metadata = parseStringMap(metadataJSON)
	agent.RegisteredAt = textToTime(registeredAtRaw)
	agent.LastHeartbeatAt = textToTime(lastHeartbeatAtRaw)
	return agent, nil
}

func scanTask(scanner agentScanner) (model.Task, error) {
	var (
		item          model.Task
		typeRaw       string
		targetTypeRaw string
		statusRaw     string
		detailJSON    string
		payloadJSON   string
		createdAtRaw  string
		acceptedAtRaw string
		startedAtRaw  string
		finishedAtRaw string
	)
	if err := scanner.Scan(
		&item.ID, &typeRaw, &targetTypeRaw, &item.TargetID, &item.AssignedAgentID, &item.WorkerID,
		&statusRaw, &item.Progress, &item.Message, &detailJSON, &payloadJSON, &item.Error,
		&createdAtRaw, &acceptedAtRaw, &startedAtRaw, &finishedAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, sql.ErrNoRows
		}
		return model.Task{}, fmt.Errorf("scan task failed: %w", err)
	}
	item.Type = model.TaskType(strings.TrimSpace(typeRaw))
	item.TargetType = model.TaskTargetType(strings.TrimSpace(targetTypeRaw))
	item.Status = model.TaskStatus(strings.TrimSpace(statusRaw))
	item.Progress = clampProgress(item.Progress)
	item.Detail = parseObjectMap(detailJSON)
	item.Payload = parseObjectMap(payloadJSON)
	item.CreatedAt = textToTime(createdAtRaw)
	item.AcceptedAt = textToTime(acceptedAtRaw)
	item.StartedAt = textToTime(startedAtRaw)
	item.FinishedAt = textToTime(finishedAtRaw)
	return item, nil
}

func scanTestRun(scanner agentScanner) (model.TestRun, error) {
	var (
		item         model.TestRun
		statusRaw    string
		startedAtRaw string
		finishedRaw  string
		createdAtRaw string
	)
	if err := scanner.Scan(
		&item.TestRunID, &item.Scenario, &statusRaw, &startedAtRaw, &finishedRaw, &item.LogPath, &item.Summary, &item.TriggeredBy, &item.Error, &createdAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.TestRun{}, sql.ErrNoRows
		}
		return model.TestRun{}, fmt.Errorf("scan test run failed: %w", err)
	}
	item.Status = model.TestRunStatus(strings.TrimSpace(statusRaw))
	item.StartedAt = textToTime(startedAtRaw)
	item.FinishedAt = textToTime(finishedRaw)
	item.CreatedAt = textToTime(createdAtRaw)
	return item, nil
}

func mustJSON(v interface{}, fallback string) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return fallback
	}
	return text
}

func parseStringSlice(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseStringSliceMap(raw string) map[string][]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string][]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseStringMap(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseObjectMap(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseNodeMetadata(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func timeToText(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func textToTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC()
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
