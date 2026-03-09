package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
	"ModelIntegrator/src/pkg/scheduler"
)

type captureAdapter struct {
	lastModel model.Model
}

func (a *captureAdapter) Name() string { return "capture" }

func (a *captureAdapter) HealthCheck(ctx context.Context) (model.ActionResult, error) {
	_ = ctx
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = ctx
	return nil, nil
}

func (a *captureAdapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.lastModel = m
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.lastModel = m
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) StartModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.lastModel = m
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) StopModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.lastModel = m
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.lastModel = m
	return model.ActionResult{Success: true}, nil
}

func newTestModelService(nodes []model.Node, models []model.Model, adapters *adapter.Manager) *ModelService {
	return NewModelService(
		registry.NewModelRegistry(models),
		registry.NewNodeRegistry(nodes),
		nil,
		scheduler.NewScheduler(),
		adapters,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"",
	)
}

func TestResolveContainerRuntimeBindingFallbackToPortainer(t *testing.T) {
	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-portainer-1",
						Type:     model.RuntimeTypePortainer,
						Endpoint: "http://portainer:9000",
						Enabled:  true,
					},
				},
			},
		},
		nil,
		adapter.NewManager(),
	)

	backend, nodeID, runtimeID, endpoint := svc.resolveContainerRuntimeBinding()
	if backend != model.RuntimeTypePortainer {
		t.Fatalf("unexpected backend: %s", backend)
	}
	if nodeID != "node-main" || runtimeID != "rt-portainer-1" {
		t.Fatalf("unexpected runtime binding: node=%s runtime=%s", nodeID, runtimeID)
	}
	if endpoint != "http://portainer:9000" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
}

func TestResolveRuntimeConnectionModelMetadataPriority(t *testing.T) {
	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "tcp://runtime-endpoint:2375",
						Enabled:  true,
						Metadata: map[string]string{
							"token": "runtime-token",
						},
					},
				},
			},
		},
		nil,
		adapter.NewManager(),
	)

	endpoint, token := svc.resolveRuntimeConnection(model.Model{
		BackendType: model.RuntimeTypeDocker,
		HostNodeID:  "node-main",
		RuntimeID:   "rt-docker-1",
		Endpoint:    "tcp://model-endpoint:2375",
		Metadata: map[string]string{
			"runtime_endpoint": "tcp://metadata-endpoint:2375",
			"runtime_token":    "metadata-token",
		},
	})
	if endpoint != "tcp://metadata-endpoint:2375" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	if token != "metadata-token" {
		t.Fatalf("unexpected token: %s", token)
	}
}

func TestStartModelUsesRuntimeBindingWithoutPersistingToken(t *testing.T) {
	capture := &captureAdapter{}
	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	nodes := []model.Node{
		{
			ID: "node-main",
			Runtimes: []model.Runtime{
				{
					ID:       "rt-docker-1",
					Type:     model.RuntimeTypeDocker,
					Endpoint: "tcp://docker-host:2375",
					Enabled:  true,
					Metadata: map[string]string{
						"token": "node-runtime-token",
					},
				},
			},
		},
	}
	models := []model.Model{
		{
			ID:          "local-qwen",
			Name:        "Qwen",
			Provider:    "localfs",
			BackendType: model.RuntimeTypeDocker,
			HostNodeID:  "node-main",
			RuntimeID:   "rt-docker-1",
			State:       model.ModelStateStopped,
			Metadata: map[string]string{
				"source": "local-scan",
			},
		},
	}
	svc := newTestModelService(nodes, models, adapters)

	result, err := svc.StartModel(context.Background(), "local-qwen")
	if err != nil {
		t.Fatalf("StartModel returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("StartModel should succeed, message=%s", result.Message)
	}

	if capture.lastModel.Endpoint != "tcp://docker-host:2375" {
		t.Fatalf("adapter received unexpected endpoint: %s", capture.lastModel.Endpoint)
	}
	if capture.lastModel.Metadata["runtime_token"] != "node-runtime-token" {
		t.Fatalf("adapter received unexpected runtime token: %s", capture.lastModel.Metadata["runtime_token"])
	}

	stored, ok := svc.modelRegistry.Get("local-qwen")
	if !ok {
		t.Fatalf("model not found in registry after action")
	}
	if _, exists := stored.Metadata["runtime_token"]; exists {
		t.Fatalf("runtime token should not be persisted in model metadata")
	}
	if stored.Endpoint != "tcp://docker-host:2375" {
		t.Fatalf("stored model endpoint not updated, got=%s", stored.Endpoint)
	}
}
