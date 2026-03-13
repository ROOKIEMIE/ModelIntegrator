package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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
	// 尽量减少多进程/多协程争用时的 SQLITE_BUSY，并提升读写并发容忍度。
	for _, stmt := range []string{
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA synchronous = NORMAL;`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			s.logger.Warn("set sqlite pragma failed (continue)", "stmt", stmt, "error", err)
		}
	}
	for _, stmt := range schemaStatements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			if isIgnorableLegacyIndexError(stmt, err) {
				s.logger.Warn("skip legacy index creation before column migration", "stmt", stmt, "error", err)
				continue
			}
			return fmt.Errorf("init sqlite schema failed: %w", err)
		}
	}
	// 对历史数据库做轻量增量迁移，确保新增字段存在。
	for _, migration := range []struct {
		table      string
		column     string
		definition string
	}{
		{table: "models", column: "display_name", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "models", column: "model_type", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "source_type", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "model_format", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "path_or_ref", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "models", column: "size_bytes", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "models", column: "default_args_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{table: "models", column: "requires_script", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "models", column: "script_ref", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "models", column: "tags_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "models", column: "desired_state", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "observed_state", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "readiness", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "models", column: "health_message", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "models", column: "last_reconciled_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "runtime_instances", column: "precheck_status", definition: "TEXT NOT NULL DEFAULT 'unknown'"},
		{table: "runtime_instances", column: "precheck_gating", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "runtime_instances", column: "precheck_reasons_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "runtime_instances", column: "last_precheck_task_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "runtime_instances", column: "last_precheck_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "runtime_instances", column: "precheck_result_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{table: "tasks", column: "assigned_agent_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", column: "worker_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", column: "detail_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{table: "tasks", column: "payload_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{table: "tasks", column: "accepted_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", column: "started_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", column: "finished_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", column: "updated_at", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}
	if err := s.ensureTaskIndexes(ctx); err != nil {
		return err
	}
	return nil
}

func isIgnorableLegacyIndexError(stmt string, err error) bool {
	stmt = strings.TrimSpace(strings.ToLower(stmt))
	if !strings.Contains(stmt, "create index if not exists idx_tasks_status_agent") &&
		!strings.Contains(stmt, "create index if not exists idx_tasks_target") {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(errText, "no such column")
}

func (s *Store) ensureTaskIndexes(ctx context.Context) error {
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_tasks_target ON tasks(target_type, target_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status_agent ON tasks(status, assigned_agent_id, created_at DESC);`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init sqlite task index failed: %w", err)
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
			id, name, display_name, model_type, source_type, model_format,
			path_or_ref, size_bytes, default_args_json, requires_script, script_ref, tags_json,
			provider, backend_type, host_node_id, runtime_id,
			endpoint, state, desired_state, observed_state, readiness, health_message, last_reconciled_at,
			context_length, metadata_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			display_name = excluded.display_name,
			model_type = excluded.model_type,
			source_type = excluded.source_type,
			model_format = excluded.model_format,
			path_or_ref = excluded.path_or_ref,
			size_bytes = excluded.size_bytes,
			default_args_json = excluded.default_args_json,
			requires_script = excluded.requires_script,
			script_ref = excluded.script_ref,
			tags_json = excluded.tags_json,
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
		strings.TrimSpace(item.DisplayName),
		firstNonEmpty(string(item.ModelType), string(model.ModelKindUnknown)),
		firstNonEmpty(string(item.SourceType), string(model.ModelSourceUnknown)),
		firstNonEmpty(string(item.Format), string(model.ModelFormatUnknown)),
		strings.TrimSpace(item.PathOrRef),
		item.SizeBytes,
		mustJSON(item.DefaultArgs, "{}"),
		boolToInt(item.RequiresScript),
		strings.TrimSpace(item.ScriptRef),
		mustJSON(item.Tags, "[]"),
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
		SELECT id, name, display_name, model_type, source_type, model_format,
		       path_or_ref, size_bytes, default_args_json, requires_script, script_ref, tags_json,
		       provider, backend_type, host_node_id, runtime_id,
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
			modelTypeRaw      string
			sourceTypeRaw     string
			formatRaw         string
			defaultArgsJSON   string
			requiresScriptRaw int
			tagsJSON          string
			backendRaw        string
			stateRaw          string
			desiredStateRaw   string
			observedStateRaw  string
			readinessRaw      string
			lastReconciledRaw string
			metadata          string
		)
		if err := rows.Scan(
			&item.ID, &item.Name, &item.DisplayName, &modelTypeRaw, &sourceTypeRaw, &formatRaw,
			&item.PathOrRef, &item.SizeBytes, &defaultArgsJSON, &requiresScriptRaw, &item.ScriptRef, &tagsJSON,
			&item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
			&item.Endpoint, &stateRaw, &desiredStateRaw, &observedStateRaw, &readinessRaw, &item.HealthMessage, &lastReconciledRaw,
			&item.ContextLength, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan model failed: %w", err)
		}
		item.ModelType = model.ModelKind(strings.TrimSpace(modelTypeRaw))
		item.SourceType = model.ModelSourceType(strings.TrimSpace(sourceTypeRaw))
		item.Format = model.ModelFormat(strings.TrimSpace(formatRaw))
		item.DefaultArgs = parseStringMap(defaultArgsJSON)
		item.RequiresScript = requiresScriptRaw == 1
		item.Tags = parseStringSlice(tagsJSON)
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
		SELECT id, name, display_name, model_type, source_type, model_format,
		       path_or_ref, size_bytes, default_args_json, requires_script, script_ref, tags_json,
		       provider, backend_type, host_node_id, runtime_id,
		       endpoint, state, desired_state, observed_state, readiness, health_message, last_reconciled_at,
		       context_length, metadata_json
		FROM models
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		item              model.Model
		modelTypeRaw      string
		sourceTypeRaw     string
		formatRaw         string
		defaultArgsJSON   string
		requiresScriptRaw int
		tagsJSON          string
		backendRaw        string
		stateRaw          string
		desiredStateRaw   string
		observedStateRaw  string
		readinessRaw      string
		lastReconciledRaw string
		metadata          string
	)
	err := row.Scan(
		&item.ID, &item.Name, &item.DisplayName, &modelTypeRaw, &sourceTypeRaw, &formatRaw,
		&item.PathOrRef, &item.SizeBytes, &defaultArgsJSON, &requiresScriptRaw, &item.ScriptRef, &tagsJSON,
		&item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
		&item.Endpoint, &stateRaw, &desiredStateRaw, &observedStateRaw, &readinessRaw, &item.HealthMessage, &lastReconciledRaw,
		&item.ContextLength, &metadata,
	)
	if err == sql.ErrNoRows {
		return model.Model{}, false, nil
	}
	if err != nil {
		return model.Model{}, false, fmt.Errorf("get model failed: %w", err)
	}
	item.ModelType = model.ModelKind(strings.TrimSpace(modelTypeRaw))
	item.SourceType = model.ModelSourceType(strings.TrimSpace(sourceTypeRaw))
	item.Format = model.ModelFormat(strings.TrimSpace(formatRaw))
	item.DefaultArgs = parseStringMap(defaultArgsJSON)
	item.RequiresScript = requiresScriptRaw == 1
	item.Tags = parseStringSlice(tagsJSON)
	item.BackendType = model.RuntimeType(strings.TrimSpace(backendRaw))
	item.State = model.ModelState(strings.TrimSpace(stateRaw))
	item.DesiredState = strings.TrimSpace(desiredStateRaw)
	item.ObservedState = strings.TrimSpace(observedStateRaw)
	item.Readiness = model.ReadinessState(strings.TrimSpace(readinessRaw))
	item.LastReconciledAt = textToTime(lastReconciledRaw)
	item.Metadata = parseStringMap(metadata)
	return item, true, nil
}

func (s *Store) UpsertRuntimeBinding(ctx context.Context, item model.RuntimeBinding) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("runtime binding id is empty")
	}
	now := time.Now().UTC()
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_bindings (
			id, model_id, template_id, binding_mode, node_selector_json, preferred_node,
			mount_rules_json, env_overrides_json, command_override_json, script_ref,
			compatibility_status, compatibility_message, enabled, manifest_id, metadata_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model_id = excluded.model_id,
			template_id = excluded.template_id,
			binding_mode = excluded.binding_mode,
			node_selector_json = excluded.node_selector_json,
			preferred_node = excluded.preferred_node,
			mount_rules_json = excluded.mount_rules_json,
			env_overrides_json = excluded.env_overrides_json,
			command_override_json = excluded.command_override_json,
			script_ref = excluded.script_ref,
			compatibility_status = excluded.compatibility_status,
			compatibility_message = excluded.compatibility_message,
			enabled = excluded.enabled,
			manifest_id = excluded.manifest_id,
			metadata_json = excluded.metadata_json,
			created_at = CASE WHEN runtime_bindings.created_at = '' THEN excluded.created_at ELSE runtime_bindings.created_at END,
			updated_at = excluded.updated_at;
	`,
		item.ID,
		strings.TrimSpace(item.ModelID),
		strings.TrimSpace(item.TemplateID),
		firstNonEmpty(string(item.BindingMode), string(model.RuntimeBindingModeGenericInjected)),
		mustJSON(item.NodeSelector, "{}"),
		strings.TrimSpace(item.PreferredNode),
		mustJSON(item.MountRules, "[]"),
		mustJSON(item.EnvOverrides, "{}"),
		mustJSON(item.CommandOverride, "[]"),
		strings.TrimSpace(item.ScriptRef),
		firstNonEmpty(string(item.CompatibilityStatus), string(model.CompatibilityUnknown)),
		strings.TrimSpace(item.CompatibilityMessage),
		boolToInt(item.Enabled),
		strings.TrimSpace(item.ManifestID),
		mustJSON(item.Metadata, "{}"),
		timeToText(createdAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert runtime binding failed: %w", err)
	}
	return nil
}

func (s *Store) ListRuntimeBindings(ctx context.Context) ([]model.RuntimeBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, model_id, template_id, binding_mode, node_selector_json, preferred_node,
		       mount_rules_json, env_overrides_json, command_override_json, script_ref,
		       compatibility_status, compatibility_message, enabled, manifest_id, metadata_json,
		       created_at, updated_at
		FROM runtime_bindings
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime bindings failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.RuntimeBinding, 0, 32)
	for rows.Next() {
		var (
			item                   model.RuntimeBinding
			modeRaw                string
			nodeSelectorJSON       string
			mountRulesJSON         string
			envOverridesJSON       string
			commandOverrideJSON    string
			compatibilityStatusRaw string
			enabledRaw             int
			metadataJSON           string
			createdAtRaw           string
			updatedAtRaw           string
		)
		if err := rows.Scan(
			&item.ID, &item.ModelID, &item.TemplateID, &modeRaw, &nodeSelectorJSON, &item.PreferredNode,
			&mountRulesJSON, &envOverridesJSON, &commandOverrideJSON, &item.ScriptRef,
			&compatibilityStatusRaw, &item.CompatibilityMessage, &enabledRaw, &item.ManifestID, &metadataJSON,
			&createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan runtime binding failed: %w", err)
		}
		item.BindingMode = model.RuntimeBindingMode(strings.TrimSpace(modeRaw))
		item.NodeSelector = parseStringMap(nodeSelectorJSON)
		item.MountRules = parseStringSlice(mountRulesJSON)
		item.EnvOverrides = parseStringMap(envOverridesJSON)
		item.CommandOverride = parseStringSlice(commandOverrideJSON)
		item.CompatibilityStatus = model.CompatibilityStatus(strings.TrimSpace(compatibilityStatusRaw))
		item.Enabled = enabledRaw == 1
		item.Metadata = parseStringMap(metadataJSON)
		item.CreatedAt = textToTime(createdAtRaw)
		item.UpdatedAt = textToTime(updatedAtRaw)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime bindings failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetRuntimeBindingByID(ctx context.Context, id string) (model.RuntimeBinding, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model_id, template_id, binding_mode, node_selector_json, preferred_node,
		       mount_rules_json, env_overrides_json, command_override_json, script_ref,
		       compatibility_status, compatibility_message, enabled, manifest_id, metadata_json,
		       created_at, updated_at
		FROM runtime_bindings
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		item                   model.RuntimeBinding
		modeRaw                string
		nodeSelectorJSON       string
		mountRulesJSON         string
		envOverridesJSON       string
		commandOverrideJSON    string
		compatibilityStatusRaw string
		enabledRaw             int
		metadataJSON           string
		createdAtRaw           string
		updatedAtRaw           string
	)
	err := row.Scan(
		&item.ID, &item.ModelID, &item.TemplateID, &modeRaw, &nodeSelectorJSON, &item.PreferredNode,
		&mountRulesJSON, &envOverridesJSON, &commandOverrideJSON, &item.ScriptRef,
		&compatibilityStatusRaw, &item.CompatibilityMessage, &enabledRaw, &item.ManifestID, &metadataJSON,
		&createdAtRaw, &updatedAtRaw,
	)
	if err == sql.ErrNoRows {
		return model.RuntimeBinding{}, false, nil
	}
	if err != nil {
		return model.RuntimeBinding{}, false, fmt.Errorf("get runtime binding failed: %w", err)
	}
	item.BindingMode = model.RuntimeBindingMode(strings.TrimSpace(modeRaw))
	item.NodeSelector = parseStringMap(nodeSelectorJSON)
	item.MountRules = parseStringSlice(mountRulesJSON)
	item.EnvOverrides = parseStringMap(envOverridesJSON)
	item.CommandOverride = parseStringSlice(commandOverrideJSON)
	item.CompatibilityStatus = model.CompatibilityStatus(strings.TrimSpace(compatibilityStatusRaw))
	item.Enabled = enabledRaw == 1
	item.Metadata = parseStringMap(metadataJSON)
	item.CreatedAt = textToTime(createdAtRaw)
	item.UpdatedAt = textToTime(updatedAtRaw)
	return item, true, nil
}

func (s *Store) UpsertRuntimeInstance(ctx context.Context, item model.RuntimeInstance) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("runtime instance id is empty")
	}
	now := time.Now().UTC()
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_instances (
			id, model_id, template_id, binding_id, node_id, desired_state, observed_state,
			readiness, health_message, drift_reason, endpoint,
			launched_command_json, mounted_paths_json, injected_env_json, script_used,
			last_reconciled_at, metadata_json,
			precheck_status, precheck_gating, precheck_reasons_json, last_precheck_task_id, last_precheck_at, precheck_result_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model_id = excluded.model_id,
			template_id = excluded.template_id,
			binding_id = excluded.binding_id,
			node_id = excluded.node_id,
			desired_state = excluded.desired_state,
			observed_state = excluded.observed_state,
			readiness = excluded.readiness,
			health_message = excluded.health_message,
			drift_reason = excluded.drift_reason,
			endpoint = excluded.endpoint,
			launched_command_json = excluded.launched_command_json,
			mounted_paths_json = excluded.mounted_paths_json,
			injected_env_json = excluded.injected_env_json,
			script_used = excluded.script_used,
			last_reconciled_at = excluded.last_reconciled_at,
			metadata_json = excluded.metadata_json,
			precheck_status = excluded.precheck_status,
			precheck_gating = excluded.precheck_gating,
			precheck_reasons_json = excluded.precheck_reasons_json,
			last_precheck_task_id = excluded.last_precheck_task_id,
			last_precheck_at = excluded.last_precheck_at,
			precheck_result_json = excluded.precheck_result_json,
			created_at = CASE WHEN runtime_instances.created_at = '' THEN excluded.created_at ELSE runtime_instances.created_at END,
			updated_at = excluded.updated_at;
	`,
		item.ID,
		strings.TrimSpace(item.ModelID),
		strings.TrimSpace(item.TemplateID),
		strings.TrimSpace(item.BindingID),
		strings.TrimSpace(item.NodeID),
		firstNonEmpty(strings.TrimSpace(item.DesiredState), string(model.ModelStateUnknown)),
		firstNonEmpty(strings.TrimSpace(item.ObservedState), string(model.ModelStateUnknown)),
		firstNonEmpty(string(item.Readiness), string(model.ReadinessUnknown)),
		strings.TrimSpace(item.HealthMessage),
		strings.TrimSpace(item.DriftReason),
		strings.TrimSpace(item.Endpoint),
		mustJSON(item.LaunchedCommand, "[]"),
		mustJSON(item.MountedPaths, "[]"),
		mustJSON(item.InjectedEnv, "{}"),
		strings.TrimSpace(item.ScriptUsed),
		timeToText(item.LastReconciledAt),
		mustJSON(item.Metadata, "{}"),
		firstNonEmpty(strings.TrimSpace(string(item.PrecheckStatus)), string(model.PrecheckStatusUnknown)),
		boolToInt(item.PrecheckGating),
		mustJSON(item.PrecheckReasons, "[]"),
		strings.TrimSpace(item.LastPrecheckTaskID),
		timeToText(item.LastPrecheckAt),
		mustJSON(item.PrecheckResult, "{}"),
		timeToText(createdAt),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert runtime instance failed: %w", err)
	}
	return nil
}

func (s *Store) ListRuntimeInstances(ctx context.Context) ([]model.RuntimeInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, model_id, template_id, binding_id, node_id, desired_state, observed_state,
		       readiness, health_message, drift_reason, endpoint,
		       launched_command_json, mounted_paths_json, injected_env_json, script_used,
		       last_reconciled_at, metadata_json,
		       precheck_status, precheck_gating, precheck_reasons_json, last_precheck_task_id, last_precheck_at, precheck_result_json,
		       created_at, updated_at
		FROM runtime_instances
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime instances failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.RuntimeInstance, 0, 32)
	for rows.Next() {
		var (
			item               model.RuntimeInstance
			readinessRaw       string
			launchedCommandRaw string
			mountedPathsRaw    string
			injectedEnvRaw     string
			metadataRaw        string
			lastReconciledRaw  string
			precheckStatusRaw  string
			precheckGatingRaw  int
			precheckReasonsRaw string
			lastPrecheckTaskID string
			lastPrecheckAtRaw  string
			precheckResultRaw  string
			createdAtRaw       string
			updatedAtRaw       string
		)
		if err := rows.Scan(
			&item.ID, &item.ModelID, &item.TemplateID, &item.BindingID, &item.NodeID, &item.DesiredState, &item.ObservedState,
			&readinessRaw, &item.HealthMessage, &item.DriftReason, &item.Endpoint,
			&launchedCommandRaw, &mountedPathsRaw, &injectedEnvRaw, &item.ScriptUsed,
			&lastReconciledRaw, &metadataRaw,
			&precheckStatusRaw, &precheckGatingRaw, &precheckReasonsRaw, &lastPrecheckTaskID, &lastPrecheckAtRaw, &precheckResultRaw,
			&createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan runtime instance failed: %w", err)
		}
		item.Readiness = model.ReadinessState(strings.TrimSpace(readinessRaw))
		item.LaunchedCommand = parseStringSlice(launchedCommandRaw)
		item.MountedPaths = parseStringSlice(mountedPathsRaw)
		item.InjectedEnv = parseStringMap(injectedEnvRaw)
		item.LastReconciledAt = textToTime(lastReconciledRaw)
		item.Metadata = parseStringMap(metadataRaw)
		item.PrecheckStatus = model.PrecheckOverallStatus(strings.TrimSpace(precheckStatusRaw))
		item.PrecheckGating = precheckGatingRaw == 1
		item.PrecheckReasons = parseStringSlice(precheckReasonsRaw)
		item.LastPrecheckTaskID = strings.TrimSpace(lastPrecheckTaskID)
		item.LastPrecheckAt = textToTime(lastPrecheckAtRaw)
		item.PrecheckResult = parseRuntimePrecheckResult(precheckResultRaw)
		item.CreatedAt = textToTime(createdAtRaw)
		item.UpdatedAt = textToTime(updatedAtRaw)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime instances failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetRuntimeInstanceByID(ctx context.Context, id string) (model.RuntimeInstance, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model_id, template_id, binding_id, node_id, desired_state, observed_state,
		       readiness, health_message, drift_reason, endpoint,
		       launched_command_json, mounted_paths_json, injected_env_json, script_used,
		       last_reconciled_at, metadata_json,
		       precheck_status, precheck_gating, precheck_reasons_json, last_precheck_task_id, last_precheck_at, precheck_result_json,
		       created_at, updated_at
		FROM runtime_instances
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		item               model.RuntimeInstance
		readinessRaw       string
		launchedCommandRaw string
		mountedPathsRaw    string
		injectedEnvRaw     string
		metadataRaw        string
		lastReconciledRaw  string
		precheckStatusRaw  string
		precheckGatingRaw  int
		precheckReasonsRaw string
		lastPrecheckTaskID string
		lastPrecheckAtRaw  string
		precheckResultRaw  string
		createdAtRaw       string
		updatedAtRaw       string
	)
	err := row.Scan(
		&item.ID, &item.ModelID, &item.TemplateID, &item.BindingID, &item.NodeID, &item.DesiredState, &item.ObservedState,
		&readinessRaw, &item.HealthMessage, &item.DriftReason, &item.Endpoint,
		&launchedCommandRaw, &mountedPathsRaw, &injectedEnvRaw, &item.ScriptUsed,
		&lastReconciledRaw, &metadataRaw,
		&precheckStatusRaw, &precheckGatingRaw, &precheckReasonsRaw, &lastPrecheckTaskID, &lastPrecheckAtRaw, &precheckResultRaw,
		&createdAtRaw, &updatedAtRaw,
	)
	if err == sql.ErrNoRows {
		return model.RuntimeInstance{}, false, nil
	}
	if err != nil {
		return model.RuntimeInstance{}, false, fmt.Errorf("get runtime instance failed: %w", err)
	}
	item.Readiness = model.ReadinessState(strings.TrimSpace(readinessRaw))
	item.LaunchedCommand = parseStringSlice(launchedCommandRaw)
	item.MountedPaths = parseStringSlice(mountedPathsRaw)
	item.InjectedEnv = parseStringMap(injectedEnvRaw)
	item.LastReconciledAt = textToTime(lastReconciledRaw)
	item.Metadata = parseStringMap(metadataRaw)
	item.PrecheckStatus = model.PrecheckOverallStatus(strings.TrimSpace(precheckStatusRaw))
	item.PrecheckGating = precheckGatingRaw == 1
	item.PrecheckReasons = parseStringSlice(precheckReasonsRaw)
	item.LastPrecheckTaskID = strings.TrimSpace(lastPrecheckTaskID)
	item.LastPrecheckAt = textToTime(lastPrecheckAtRaw)
	item.PrecheckResult = parseRuntimePrecheckResult(precheckResultRaw)
	item.CreatedAt = textToTime(createdAtRaw)
	item.UpdatedAt = textToTime(updatedAtRaw)
	return item, true, nil
}

func (s *Store) UpsertRuntimeBundleManifest(ctx context.Context, item model.RuntimeBundleManifest) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("runtime bundle manifest id is empty")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_bundle_manifests (
			id, template_id, manifest_version, template_type, runtime_kind,
			supported_model_types_json, supported_formats_json, capabilities_json,
			mount_points_json, required_env_json, optional_env_json,
			command_override_allowed, script_mount_allowed, model_injection_mode,
			healthcheck_json, exposed_ports_json, notes, metadata_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			template_id = excluded.template_id,
			manifest_version = excluded.manifest_version,
			template_type = excluded.template_type,
			runtime_kind = excluded.runtime_kind,
			supported_model_types_json = excluded.supported_model_types_json,
			supported_formats_json = excluded.supported_formats_json,
			capabilities_json = excluded.capabilities_json,
			mount_points_json = excluded.mount_points_json,
			required_env_json = excluded.required_env_json,
			optional_env_json = excluded.optional_env_json,
			command_override_allowed = excluded.command_override_allowed,
			script_mount_allowed = excluded.script_mount_allowed,
			model_injection_mode = excluded.model_injection_mode,
			healthcheck_json = excluded.healthcheck_json,
			exposed_ports_json = excluded.exposed_ports_json,
			notes = excluded.notes,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at;
	`,
		item.ID,
		strings.TrimSpace(item.TemplateID),
		strings.TrimSpace(item.ManifestVersion),
		firstNonEmpty(string(item.TemplateType), string(model.RuntimeTemplateTypeUnknown)),
		firstNonEmpty(string(item.RuntimeKind), string(model.RuntimeKindUnknown)),
		mustJSON(item.SupportedModelTypes, "[]"),
		mustJSON(item.SupportedFormats, "[]"),
		mustJSON(item.Capabilities, "[]"),
		mustJSON(item.MountPoints, "[]"),
		mustJSON(item.RequiredEnv, "[]"),
		mustJSON(item.OptionalEnv, "[]"),
		boolToInt(item.CommandOverrideAllowed),
		boolToInt(item.ScriptMountAllowed),
		firstNonEmpty(string(item.ModelInjectionMode), string(model.RuntimeBindingModeGenericInjected)),
		mustJSON(item.Healthcheck, "{}"),
		mustJSON(item.ExposedPorts, "[]"),
		strings.TrimSpace(item.Notes),
		mustJSON(item.Metadata, "{}"),
		timeToText(now),
	)
	if err != nil {
		return fmt.Errorf("upsert runtime bundle manifest failed: %w", err)
	}
	return nil
}

func (s *Store) ListRuntimeBundleManifests(ctx context.Context) ([]model.RuntimeBundleManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, template_id, manifest_version, template_type, runtime_kind,
		       supported_model_types_json, supported_formats_json, capabilities_json,
		       mount_points_json, required_env_json, optional_env_json,
		       command_override_allowed, script_mount_allowed, model_injection_mode,
		       healthcheck_json, exposed_ports_json, notes, metadata_json
		FROM runtime_bundle_manifests
		ORDER BY id;
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime bundle manifests failed: %w", err)
	}
	defer rows.Close()

	out := make([]model.RuntimeBundleManifest, 0, 16)
	for rows.Next() {
		item, scanErr := scanRuntimeBundleManifest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime bundle manifests failed: %w", err)
	}
	return out, nil
}

func (s *Store) GetRuntimeBundleManifestByID(ctx context.Context, id string) (model.RuntimeBundleManifest, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, template_id, manifest_version, template_type, runtime_kind,
		       supported_model_types_json, supported_formats_json, capabilities_json,
		       mount_points_json, required_env_json, optional_env_json,
		       command_override_allowed, script_mount_allowed, model_injection_mode,
		       healthcheck_json, exposed_ports_json, notes, metadata_json
		FROM runtime_bundle_manifests
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))
	item, err := scanRuntimeBundleManifest(row)
	if err == sql.ErrNoRows {
		return model.RuntimeBundleManifest{}, false, nil
	}
	if err != nil {
		return model.RuntimeBundleManifest{}, false, err
	}
	return item, true, nil
}

func (s *Store) GetRuntimeBundleManifestByTemplateID(ctx context.Context, templateID string) (model.RuntimeBundleManifest, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, template_id, manifest_version, template_type, runtime_kind,
		       supported_model_types_json, supported_formats_json, capabilities_json,
		       mount_points_json, required_env_json, optional_env_json,
		       command_override_allowed, script_mount_allowed, model_injection_mode,
		       healthcheck_json, exposed_ports_json, notes, metadata_json
		FROM runtime_bundle_manifests
		WHERE template_id = ? OR id = ?
		ORDER BY CASE WHEN template_id = ? THEN 0 ELSE 1 END, id
		LIMIT 1;
	`, strings.TrimSpace(templateID), strings.TrimSpace(templateID), strings.TrimSpace(templateID))
	item, err := scanRuntimeBundleManifest(row)
	if err == sql.ErrNoRows {
		return model.RuntimeBundleManifest{}, false, nil
	}
	if err != nil {
		return model.RuntimeBundleManifest{}, false, err
	}
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
		item           model.Task
		idRaw          sql.NullString
		typeRaw        sql.NullString
		targetTypeRaw  sql.NullString
		targetIDRaw    sql.NullString
		assignedRaw    sql.NullString
		workerRaw      sql.NullString
		statusRaw      sql.NullString
		progressRaw    interface{}
		messageRaw     sql.NullString
		detailJSONRaw  sql.NullString
		payloadJSONRaw sql.NullString
		errorRaw       sql.NullString
		createdAtRaw   sql.NullString
		acceptedAtRaw  sql.NullString
		startedAtRaw   sql.NullString
		finishedAtRaw  sql.NullString
	)
	if err := scanner.Scan(
		&idRaw, &typeRaw, &targetTypeRaw, &targetIDRaw, &assignedRaw, &workerRaw,
		&statusRaw, &progressRaw, &messageRaw, &detailJSONRaw, &payloadJSONRaw, &errorRaw,
		&createdAtRaw, &acceptedAtRaw, &startedAtRaw, &finishedAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, sql.ErrNoRows
		}
		return model.Task{}, fmt.Errorf("scan task failed: %w", err)
	}
	item.ID = strings.TrimSpace(idRaw.String)
	item.Type = model.TaskType(strings.TrimSpace(typeRaw.String))
	item.TargetType = model.TaskTargetType(strings.TrimSpace(targetTypeRaw.String))
	item.TargetID = strings.TrimSpace(targetIDRaw.String)
	item.AssignedAgentID = strings.TrimSpace(assignedRaw.String)
	item.WorkerID = strings.TrimSpace(workerRaw.String)
	item.Status = model.TaskStatus(strings.TrimSpace(statusRaw.String))
	item.Progress = clampProgress(intFromDBValue(progressRaw))
	item.Message = strings.TrimSpace(messageRaw.String)
	item.Detail = parseObjectMap(strings.TrimSpace(detailJSONRaw.String))
	item.Payload = parseObjectMap(strings.TrimSpace(payloadJSONRaw.String))
	item.Error = strings.TrimSpace(errorRaw.String)
	item.CreatedAt = textToTime(strings.TrimSpace(createdAtRaw.String))
	item.AcceptedAt = textToTime(strings.TrimSpace(acceptedAtRaw.String))
	item.StartedAt = textToTime(strings.TrimSpace(startedAtRaw.String))
	item.FinishedAt = textToTime(strings.TrimSpace(finishedAtRaw.String))
	return item, nil
}

func intFromDBValue(raw interface{}) int {
	switch value := raw.(type) {
	case nil:
		return 0
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case []byte:
		text := strings.TrimSpace(string(value))
		if text == "" {
			return 0
		}
		if parsed, err := strconv.Atoi(text); err == nil {
			return parsed
		}
		return 0
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return 0
		}
		if parsed, err := strconv.Atoi(text); err == nil {
			return parsed
		}
		return 0
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || text == "<nil>" {
			return 0
		}
		if parsed, err := strconv.Atoi(text); err == nil {
			return parsed
		}
		return 0
	}
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

func scanRuntimeBundleManifest(scanner agentScanner) (model.RuntimeBundleManifest, error) {
	var (
		item                   model.RuntimeBundleManifest
		templateTypeRaw        string
		runtimeKindRaw         string
		supportedModelTypesRaw string
		supportedFormatsRaw    string
		capabilitiesRaw        string
		mountPointsRaw         string
		requiredEnvRaw         string
		optionalEnvRaw         string
		commandOverrideRaw     int
		scriptMountRaw         int
		modelInjectionModeRaw  string
		healthcheckRaw         string
		exposedPortsRaw        string
		metadataRaw            string
	)
	if err := scanner.Scan(
		&item.ID, &item.TemplateID, &item.ManifestVersion, &templateTypeRaw, &runtimeKindRaw,
		&supportedModelTypesRaw, &supportedFormatsRaw, &capabilitiesRaw,
		&mountPointsRaw, &requiredEnvRaw, &optionalEnvRaw,
		&commandOverrideRaw, &scriptMountRaw, &modelInjectionModeRaw,
		&healthcheckRaw, &exposedPortsRaw, &item.Notes, &metadataRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.RuntimeBundleManifest{}, sql.ErrNoRows
		}
		return model.RuntimeBundleManifest{}, fmt.Errorf("scan runtime bundle manifest failed: %w", err)
	}
	item.TemplateType = model.RuntimeTemplateType(strings.TrimSpace(templateTypeRaw))
	item.RuntimeKind = model.RuntimeKind(strings.TrimSpace(runtimeKindRaw))
	item.SupportedModelTypes = parseModelKindSlice(supportedModelTypesRaw)
	item.SupportedFormats = parseModelFormatSlice(supportedFormatsRaw)
	item.Capabilities = parseModelKindSlice(capabilitiesRaw)
	item.MountPoints = parseStringSlice(mountPointsRaw)
	item.RequiredEnv = parseStringSlice(requiredEnvRaw)
	item.OptionalEnv = parseStringSlice(optionalEnvRaw)
	item.CommandOverrideAllowed = commandOverrideRaw == 1
	item.ScriptMountAllowed = scriptMountRaw == 1
	item.ModelInjectionMode = model.RuntimeBindingMode(strings.TrimSpace(modelInjectionModeRaw))
	item.Healthcheck = parseRuntimeHealthcheck(healthcheckRaw)
	item.ExposedPorts = parseStringSlice(exposedPortsRaw)
	item.Metadata = parseStringMap(metadataRaw)
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

func parseModelKindSlice(raw string) []model.ModelKind {
	values := parseStringSlice(raw)
	if len(values) == 0 {
		return nil
	}
	out := make([]model.ModelKind, 0, len(values))
	for _, item := range values {
		out = append(out, model.ModelKind(strings.TrimSpace(item)))
	}
	return out
}

func parseModelFormatSlice(raw string) []model.ModelFormat {
	values := parseStringSlice(raw)
	if len(values) == 0 {
		return nil
	}
	out := make([]model.ModelFormat, 0, len(values))
	for _, item := range values {
		out = append(out, model.ModelFormat(strings.TrimSpace(item)))
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

func parseRuntimeHealthcheck(raw string) model.RuntimeHealthcheck {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return model.RuntimeHealthcheck{}
	}
	var out model.RuntimeHealthcheck
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return model.RuntimeHealthcheck{}
	}
	return out
}

func parseRuntimePrecheckResult(raw string) *model.RuntimePrecheckResult {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out model.RuntimePrecheckResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return &out
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
