package service

import (
	"context"

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
	_ = ctx
	return s.registry.List(), nil
}

func (s *NodeService) GetNode(ctx context.Context, id string) (model.Node, bool) {
	_ = ctx
	return s.registry.Get(id)
}
