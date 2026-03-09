package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
	"ModelIntegrator/src/pkg/scheduler"
)

var ErrModelNotFound = errors.New("model not found")

type ModelService struct {
	modelRegistry *registry.ModelRegistry
	nodeRegistry  *registry.NodeRegistry
	scheduler     *scheduler.Scheduler
	adapters      *adapter.Manager
	logger        *slog.Logger
}

func NewModelService(
	modelRegistry *registry.ModelRegistry,
	nodeRegistry *registry.NodeRegistry,
	scheduler *scheduler.Scheduler,
	adapters *adapter.Manager,
	logger *slog.Logger,
) *ModelService {
	return &ModelService{
		modelRegistry: modelRegistry,
		nodeRegistry:  nodeRegistry,
		scheduler:     scheduler,
		adapters:      adapters,
		logger:        logger,
	}
}

func (s *ModelService) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = ctx
	return s.modelRegistry.List(), nil
}

func (s *ModelService) GetModel(ctx context.Context, id string) (model.Model, error) {
	_ = ctx
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return model.Model{}, ErrModelNotFound
	}
	return m, nil
}

func (s *ModelService) LoadModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "load")
}

func (s *ModelService) UnloadModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "unload")
}

func (s *ModelService) StartModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "start")
}

func (s *ModelService) StopModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "stop")
}

func (s *ModelService) executeAction(ctx context.Context, id, action string) (model.ActionResult, error) {
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return model.ActionResult{}, ErrModelNotFound
	}

	adapterInstance, err := s.adapters.Get(m.BackendType)
	if err != nil {
		return actionResult(false, err.Error(), map[string]interface{}{"backend_type": m.BackendType}), nil
	}

	if action == "load" || action == "start" {
		if canRun, reason := s.scheduler.CanRun(m); !canRun {
			return actionResult(false, reason, map[string]interface{}{"model_id": m.ID}), nil
		}
	}

	result := model.ActionResult{}
	switch action {
	case "load":
		result, err = adapterInstance.LoadModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateLoaded
			s.scheduler.MarkRunning(m)
		}
	case "unload":
		result, err = adapterInstance.UnloadModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateStopped
			s.scheduler.MarkStopped(m.ID)
		}
	case "start":
		result, err = adapterInstance.StartModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateRunning
			s.scheduler.MarkRunning(m)
		}
	case "stop":
		result, err = adapterInstance.StopModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateStopped
			s.scheduler.MarkStopped(m.ID)
		}
	default:
		return model.ActionResult{}, fmt.Errorf("不支持的动作: %s", action)
	}

	if err != nil {
		s.logger.Error("调用适配器失败", "action", action, "model_id", m.ID, "error", err)
		return actionResult(false, "适配器调用失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}

	s.modelRegistry.Upsert(m)
	return result, nil
}

func actionResult(success bool, message string, detail map[string]interface{}) model.ActionResult {
	return model.ActionResult{
		Success:   success,
		Message:   message,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}
