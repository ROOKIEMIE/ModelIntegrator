package service

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
)

type NodeService struct {
	registry *registry.NodeRegistry
}

func NewNodeService(reg *registry.NodeRegistry) *NodeService {
	return &NodeService{registry: reg}
}

func (s *NodeService) ListNodes(ctx context.Context) ([]model.Node, error) {
	nodes := s.registry.List()
	for i := range nodes {
		if nodes[i].Role != model.NodeRoleSub {
			continue
		}
		if strings.TrimSpace(nodes[i].Host) == "" {
			nodes[i].Status = model.NodeStatusUnknown
			continue
		}

		reachable, tried := icmpPing(ctx, nodes[i].Host)
		if !tried {
			nodes[i].Status = model.NodeStatusUnknown
			continue
		}
		if reachable {
			nodes[i].Status = model.NodeStatusOnline
		} else {
			nodes[i].Status = model.NodeStatusOffline
		}
	}
	return nodes, nil
}

func (s *NodeService) GetNode(ctx context.Context, id string) (model.Node, bool) {
	_ = ctx
	return s.registry.Get(id)
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
