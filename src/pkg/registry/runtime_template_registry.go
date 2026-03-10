package registry

import (
	"slices"
	"sync"

	"model-control-plane/src/pkg/model"
)

type RuntimeTemplateRegistry struct {
	mu        sync.RWMutex
	templates map[string]model.RuntimeTemplate
}

func NewRuntimeTemplateRegistry(initial []model.RuntimeTemplate) *RuntimeTemplateRegistry {
	r := &RuntimeTemplateRegistry{
		templates: make(map[string]model.RuntimeTemplate),
	}
	for _, tpl := range initial {
		if tpl.ID == "" {
			continue
		}
		r.templates[tpl.ID] = tpl
	}
	return r
}

func (r *RuntimeTemplateRegistry) List() []model.RuntimeTemplate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.templates))
	for id := range r.templates {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	result := make([]model.RuntimeTemplate, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.templates[id])
	}
	return result
}

func (r *RuntimeTemplateRegistry) Get(id string) (model.RuntimeTemplate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tpl, ok := r.templates[id]
	return tpl, ok
}

func (r *RuntimeTemplateRegistry) Upsert(tpl model.RuntimeTemplate) {
	if tpl.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates[tpl.ID] = tpl
}
