package registry

import (
	"sort"
	"sync"

	"ModelIntegrator/src/pkg/model"
)

type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[string]model.Node
}

func NewNodeRegistry(initial []model.Node) *NodeRegistry {
	r := &NodeRegistry{nodes: make(map[string]model.Node)}
	for _, n := range initial {
		r.nodes[n.ID] = n
	}
	return r
}

func (r *NodeRegistry) List() []model.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]model.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		result = append(result, n)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r *NodeRegistry) Get(id string) (model.Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return n, ok
}

func (r *NodeRegistry) Upsert(n model.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
}

type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]model.Model
}

func NewModelRegistry(initial []model.Model) *ModelRegistry {
	r := &ModelRegistry{models: make(map[string]model.Model)}
	for _, m := range initial {
		r.models[m.ID] = m
	}
	return r
}

func (r *ModelRegistry) List() []model.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]model.Model, 0, len(r.models))
	for _, m := range r.models {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r *ModelRegistry) Get(id string) (model.Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

func (r *ModelRegistry) Upsert(m model.Model) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[m.ID] = m
}
