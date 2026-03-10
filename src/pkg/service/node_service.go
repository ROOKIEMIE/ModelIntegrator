package service

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/adapter/dockerctl"
	"ModelIntegrator/src/pkg/adapter/lmstudio"
	"ModelIntegrator/src/pkg/capability"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
)

type NodeService struct {
	registry     *registry.NodeRegistry
	adapters     *adapter.Manager
	agentService *AgentService
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

func (s *NodeService) ListNodes(ctx context.Context) ([]model.Node, error) {
	nodes := s.registry.List()
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
	}
	return nodes, nil
}

func (s *NodeService) GetNode(ctx context.Context, id string) (model.Node, bool) {
	_ = ctx
	return s.registry.Get(id)
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
