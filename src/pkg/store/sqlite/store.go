package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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
			endpoint, state, context_length, metadata_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			provider = excluded.provider,
			backend_type = excluded.backend_type,
			host_node_id = excluded.host_node_id,
			runtime_id = excluded.runtime_id,
			endpoint = excluded.endpoint,
			state = excluded.state,
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
		       endpoint, state, context_length, metadata_json
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
			item       model.Model
			backendRaw string
			stateRaw   string
			metadata   string
		)
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
			&item.Endpoint, &stateRaw, &item.ContextLength, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan model failed: %w", err)
		}
		item.BackendType = model.RuntimeType(strings.TrimSpace(backendRaw))
		item.State = model.ModelState(strings.TrimSpace(stateRaw))
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
		       endpoint, state, context_length, metadata_json
		FROM models
		WHERE id = ? LIMIT 1;
	`, strings.TrimSpace(id))

	var (
		item       model.Model
		backendRaw string
		stateRaw   string
		metadata   string
	)
	err := row.Scan(
		&item.ID, &item.Name, &item.Provider, &backendRaw, &item.HostNodeID, &item.RuntimeID,
		&item.Endpoint, &stateRaw, &item.ContextLength, &metadata,
	)
	if err == sql.ErrNoRows {
		return model.Model{}, false, nil
	}
	if err != nil {
		return model.Model{}, false, fmt.Errorf("get model failed: %w", err)
	}
	item.BackendType = model.RuntimeType(strings.TrimSpace(backendRaw))
	item.State = model.ModelState(strings.TrimSpace(stateRaw))
	item.Metadata = parseStringMap(metadata)
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
