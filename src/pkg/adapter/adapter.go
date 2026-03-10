package adapter

import (
	"context"
	"fmt"
	"sync"

	"model-control-plane/src/pkg/model"
)

type RuntimeAdapter interface {
	Name() string
	HealthCheck(ctx context.Context) (model.ActionResult, error)
	ListModels(ctx context.Context) ([]model.Model, error)
	LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error)
	UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error)
	StartModel(ctx context.Context, m model.Model) (model.ActionResult, error)
	StopModel(ctx context.Context, m model.Model) (model.ActionResult, error)
	GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error)
}

type Manager struct {
	mu       sync.RWMutex
	adapters map[model.RuntimeType]RuntimeAdapter
}

func NewManager() *Manager {
	return &Manager{adapters: make(map[model.RuntimeType]RuntimeAdapter)}
}

func (m *Manager) Register(runtimeType model.RuntimeType, adapter RuntimeAdapter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[runtimeType] = adapter
}

func (m *Manager) Get(runtimeType model.RuntimeType) (RuntimeAdapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	adapter, ok := m.adapters[runtimeType]
	if !ok {
		return nil, fmt.Errorf("runtime %s 未注册适配器", runtimeType)
	}
	return adapter, nil
}
