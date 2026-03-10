package service

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"model-control-plane/src/pkg/adapter"
	"model-control-plane/src/pkg/adapter/dockerctl"
	"model-control-plane/src/pkg/adapter/lmstudio"
	"model-control-plane/src/pkg/capability"
	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

type NodeService struct {
	registry     *registry.NodeRegistry
	adapters     *adapter.Manager
	agentService *AgentService
	store        *sqlitestore.Store
	logger       *slog.Logger
}

func NewNodeService(reg *registry.NodeRegistry, adapters *adapter.Manager, agentService *AgentService, logger *slog.Logger) *NodeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeService{
		registry:     reg,
		adapters:     adapters,
		agentService: agentService,
		logger:       logger,
	}
}

func (s *NodeService) SetStore(store *sqlitestore.Store) {
	s.store = store
}

func (s *NodeService) SyncRegistryToStore(ctx context.Context) error {
	if s.store == nil || s.registry == nil {
		return nil
	}
	nodes := s.registry.List()
	if persisted, err := s.store.ListNodes(ctx); err == nil {
		nodes = mergeNodes(nodes, persisted)
		for _, item := range nodes {
			s.registry.Upsert(item)
		}
	}
	return s.store.UpsertNodesWithRuntimes(ctx, nodes)
}

func (s *NodeService) ListNodes(ctx context.Context) ([]model.Node, error) {
	nodes := s.registry.List()
	if s.store != nil {
		if dbNodes, err := s.store.ListNodes(ctx); err != nil {
			s.logger.Warn("读取节点持久化状态失败，将仅返回内存状态", "error", err)
		} else {
			nodes = mergeNodes(nodes, dbNodes)
		}
	}

	for i := range nodes {
		now := time.Now().UTC()
		status, runtimeStatuses := s.detectNodeStatus(ctx, nodes[i])
		nodes[i].Status = status
		for j := range nodes[i].Runtimes {
			runtimeID := nodes[i].Runtimes[j].ID
			rtStatus, ok := runtimeStatuses[runtimeID]
			if !ok {
				rtStatus = model.RuntimeStatusUnknown
			}
			nodes[i].Runtimes[j].Status = rtStatus
			if rtStatus == model.RuntimeStatusOnline || rtStatus == model.RuntimeStatusOffline {
				nodes[i].Runtimes[j].LastSeenAt = now
			}
		}
		if status == model.NodeStatusOnline {
			nodes[i].LastSeenAt = now
		}

		agentState := &model.AgentState{
			NodeID: nodes[i].ID,
			Status: model.AgentStatusNone,
		}
		if s.agentService != nil {
			if activeAgent, ok := s.agentService.GetByNodeID(nodes[i].ID); ok {
				agentState = activeAgent
				if activeAgent.LastHeartbeatAt.After(nodes[i].LastSeenAt) {
					nodes[i].LastSeenAt = activeAgent.LastHeartbeatAt
				}
			}
		}
		capability.EnrichNode(&nodes[i], agentState)
		if s.registry != nil {
			s.registry.Upsert(nodes[i])
		}
	}

	if s.store != nil {
		if err := s.store.UpsertNodesWithRuntimes(ctx, nodes); err != nil {
			s.logger.Warn("回写节点聚合状态到 sqlite 失败", "error", err)
		}
	}

	return nodes, nil
}

func (s *NodeService) GetNode(ctx context.Context, id string) (model.Node, bool) {
	if s.registry != nil {
		if node, ok := s.registry.Get(id); ok {
			return node, true
		}
	}
	if s.store != nil {
		if nodes, err := s.store.ListNodes(ctx); err == nil {
			for _, item := range nodes {
				if item.ID == id {
					return item, true
				}
			}
		}
	}
	return model.Node{}, false
}

func (s *NodeService) detectNodeStatus(ctx context.Context, node model.Node) (model.NodeStatus, map[string]model.RuntimeStatus) {
	runtimeStatuses := make(map[string]model.RuntimeStatus, len(node.Runtimes))
	if online, checked := s.runtimeHealthCheckFirst(ctx, node, runtimeStatuses); checked {
		if online {
			return model.NodeStatusOnline, runtimeStatuses
		}
		return model.NodeStatusOffline, runtimeStatuses
	}

	for _, rt := range node.Runtimes {
		if _, ok := runtimeStatuses[rt.ID]; !ok {
			runtimeStatuses[rt.ID] = model.RuntimeStatusUnknown
		}
	}

	host := strings.TrimSpace(node.Host)
	if host == "" {
		return model.NodeStatusUnknown, runtimeStatuses
	}
	reachable, pingTried := icmpPing(ctx, host)
	if !pingTried {
		return model.NodeStatusUnknown, runtimeStatuses
	}
	if reachable {
		return model.NodeStatusOnline, runtimeStatuses
	}
	return model.NodeStatusOffline, runtimeStatuses
}

// 优先使用 runtime 健康检查来判断节点可用性；只有没有可用 runtime 检查能力时才 fallback 到 ping。
func (s *NodeService) runtimeHealthCheckFirst(ctx context.Context, node model.Node, runtimeStatuses map[string]model.RuntimeStatus) (online bool, checked bool) {
	for _, rt := range node.Runtimes {
		if runtimeStatuses != nil {
			runtimeStatuses[rt.ID] = model.RuntimeStatusUnknown
		}
		if !rt.Enabled {
			continue
		}
		rtOnline, rtChecked := s.healthCheckRuntime(ctx, node, rt)
		if !rtChecked {
			continue
		}
		if runtimeStatuses != nil {
			if rtOnline {
				runtimeStatuses[rt.ID] = model.RuntimeStatusOnline
			} else {
				runtimeStatuses[rt.ID] = model.RuntimeStatusOffline
			}
		}
		if rtOnline {
			return true, true
		}
		checked = true
	}
	return false, checked
}

func (s *NodeService) healthCheckRuntime(ctx context.Context, node model.Node, rt model.Runtime) (bool, bool) {
	runtimeAdapter, ok := s.runtimeAdapterForNode(rt)
	if !ok {
		return false, false
	}

	healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	result, err := runtimeAdapter.HealthCheck(healthCtx)
	if err != nil {
		s.logger.Warn("runtime 健康检查失败",
			"node_id", node.ID,
			"node_host", node.Host,
			"runtime_id", rt.ID,
			"runtime_type", rt.Type,
			"runtime_endpoint", rt.Endpoint,
			"error", err,
		)
		return false, true
	}
	if !result.Success {
		s.logger.Warn("runtime 健康检查返回失败",
			"node_id", node.ID,
			"node_host", node.Host,
			"runtime_id", rt.ID,
			"runtime_type", rt.Type,
			"runtime_endpoint", rt.Endpoint,
			"message", result.Message,
		)
		return false, true
	}

	s.logger.Debug("runtime 健康检查通过",
		"node_id", node.ID,
		"runtime_id", rt.ID,
		"runtime_type", rt.Type,
		"runtime_endpoint", rt.Endpoint,
	)
	return true, true
}

func (s *NodeService) runtimeAdapterForNode(rt model.Runtime) (adapter.RuntimeAdapter, bool) {
	token := readRuntimeToken(rt.Metadata)
	endpoint := strings.TrimSpace(rt.Endpoint)
	if endpoint != "" {
		switch rt.Type {
		case model.RuntimeTypeLMStudio:
			return lmstudio.NewAdapter(endpoint, token, 3*time.Second, false, 0), true
		case model.RuntimeTypeDocker:
			return dockerctl.NewAdapter("dockerctl", endpoint, token), true
		case model.RuntimeTypePortainer:
			return dockerctl.NewAdapter("portainer", endpoint, token), true
		default:
			return nil, false
		}
	}

	if s.adapters == nil {
		return nil, false
	}
	adapterInstance, err := s.adapters.Get(rt.Type)
	if err != nil {
		return nil, false
	}
	return adapterInstance, true
}

func readRuntimeToken(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"token", "api_token", "bearer_token"} {
		if v := strings.TrimSpace(metadata[key]); v != "" {
			return v
		}
	}
	return ""
}

func icmpPing(ctx context.Context, host string) (reachable bool, tried bool) {
	pingPath, err := exec.LookPath("ping")
	if err != nil {
		return false, false
	}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(pingCtx, pingPath, "-c", "1", "-t", "1", host)
	default:
		cmd = exec.CommandContext(pingCtx, pingPath, "-c", "1", "-W", "1", host)
	}

	err = cmd.Run()
	return err == nil, true
}

func mergeNodes(configNodes []model.Node, persistedNodes []model.Node) []model.Node {
	if len(persistedNodes) == 0 {
		return configNodes
	}

	merged := make([]model.Node, 0, len(configNodes)+len(persistedNodes))
	indexByID := make(map[string]int, len(configNodes))
	for _, node := range configNodes {
		merged = append(merged, node)
		indexByID[node.ID] = len(merged) - 1
	}

	for _, persisted := range persistedNodes {
		idx, ok := indexByID[persisted.ID]
		if !ok {
			merged = append(merged, persisted)
			indexByID[persisted.ID] = len(merged) - 1
			continue
		}
		merged[idx] = mergeNode(merged[idx], persisted)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].ID < merged[j].ID
	})
	return merged
}

func mergeNode(base model.Node, persisted model.Node) model.Node {
	if strings.TrimSpace(base.Name) == "" {
		base.Name = persisted.Name
	}
	if strings.TrimSpace(base.Description) == "" {
		base.Description = persisted.Description
	}
	if strings.TrimSpace(base.Host) == "" {
		base.Host = persisted.Host
	}
	if strings.TrimSpace(string(base.Type)) == "" {
		base.Type = persisted.Type
	}
	if strings.TrimSpace(string(base.Role)) == "" {
		base.Role = persisted.Role
	}
	if base.Metadata == nil {
		base.Metadata = persisted.Metadata
	}
	if base.LastSeenAt.IsZero() {
		base.LastSeenAt = persisted.LastSeenAt
	}
	if base.Status == "" || base.Status == model.NodeStatusUnknown {
		base.Status = persisted.Status
	}
	if base.Classification == "" || base.Classification == model.NodeClassificationUnknown {
		base.Classification = persisted.Classification
	}
	if base.CapabilityTier == "" || base.CapabilityTier == model.CapabilityTierUnknown {
		base.CapabilityTier = persisted.CapabilityTier
	}
	if base.CapabilitySource == "" || base.CapabilitySource == model.CapabilitySourceUnknown {
		base.CapabilitySource = persisted.CapabilitySource
	}
	if base.AgentStatus == "" || base.AgentStatus == model.AgentStatusNone {
		base.AgentStatus = persisted.AgentStatus
	}
	base.Runtimes = mergeRuntimes(base.Runtimes, persisted.Runtimes)
	return base
}

func mergeRuntimes(base []model.Runtime, persisted []model.Runtime) []model.Runtime {
	if len(persisted) == 0 {
		return base
	}
	if len(base) == 0 {
		return persisted
	}
	out := make([]model.Runtime, 0, len(base)+len(persisted))
	indexByID := make(map[string]int, len(base))
	for _, item := range base {
		out = append(out, item)
		indexByID[item.ID] = len(out) - 1
	}
	for _, item := range persisted {
		idx, ok := indexByID[item.ID]
		if !ok {
			out = append(out, item)
			indexByID[item.ID] = len(out) - 1
			continue
		}
		out[idx] = mergeRuntime(out[idx], item)
	}
	return out
}

func mergeRuntime(base model.Runtime, persisted model.Runtime) model.Runtime {
	if strings.TrimSpace(string(base.Type)) == "" {
		base.Type = persisted.Type
	}
	if strings.TrimSpace(base.Endpoint) == "" {
		base.Endpoint = persisted.Endpoint
	}
	if base.Metadata == nil {
		base.Metadata = persisted.Metadata
	}
	if base.Status == "" || base.Status == model.RuntimeStatusUnknown {
		base.Status = persisted.Status
	}
	if base.CapabilitySource == "" || base.CapabilitySource == model.CapabilitySourceUnknown {
		base.CapabilitySource = persisted.CapabilitySource
	}
	if len(base.Capabilities) == 0 {
		base.Capabilities = persisted.Capabilities
	}
	if len(base.Actions) == 0 {
		base.Actions = persisted.Actions
	}
	if base.LastSeenAt.IsZero() {
		base.LastSeenAt = persisted.LastSeenAt
	}
	return base
}
