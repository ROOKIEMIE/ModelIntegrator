package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
	"ModelIntegrator/src/pkg/scheduler"
)

type captureAdapter struct {
	lastModel model.Model
	results   map[string]model.ActionResult
	calls     map[string]int
	callTrace []string
	listData  []model.Model
	listErr   error
}

func (a *captureAdapter) Name() string { return "capture" }

func (a *captureAdapter) HealthCheck(ctx context.Context) (model.ActionResult, error) {
	_ = ctx
	return model.ActionResult{Success: true}, nil
}

func (a *captureAdapter) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = ctx
	a.markCall("list")
	if a.listErr != nil {
		return nil, a.listErr
	}
	out := make([]model.Model, len(a.listData))
	copy(out, a.listData)
	return out, nil
}

func (a *captureAdapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.markCall("load")
	a.lastModel = m
	return a.resultFor("load"), nil
}

func (a *captureAdapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.markCall("unload")
	a.lastModel = m
	return a.resultFor("unload"), nil
}

func (a *captureAdapter) StartModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.markCall("start")
	a.lastModel = m
	return a.resultFor("start"), nil
}

func (a *captureAdapter) StopModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.markCall("stop")
	a.lastModel = m
	return a.resultFor("stop"), nil
}

func (a *captureAdapter) GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	a.markCall("status")
	a.lastModel = m
	return a.resultFor("status"), nil
}

func (a *captureAdapter) setResult(action string, result model.ActionResult) {
	if a.results == nil {
		a.results = map[string]model.ActionResult{}
	}
	a.results[action] = result
}

func (a *captureAdapter) resultFor(action string) model.ActionResult {
	if a.results != nil {
		if res, ok := a.results[action]; ok {
			return res
		}
	}
	return model.ActionResult{Success: true}
}

func (a *captureAdapter) markCall(action string) {
	if a.calls == nil {
		a.calls = map[string]int{}
	}
	a.calls[action]++
	a.callTrace = append(a.callTrace, action)
}

func (a *captureAdapter) countCall(action string) int {
	if a.calls == nil {
		return 0
	}
	return a.calls[action]
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
			State:       model.ModelStateLoaded,
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

func TestInferTemplateIDForLocalModel(t *testing.T) {
	if got := inferTemplateIDForLocalModel("multilingual-e5-base", "multilingual-e5-base"); got != DefaultEmbeddingTemplateID {
		t.Fatalf("embedding model should use embedding template, got=%s", got)
	}
	if got := inferTemplateIDForLocalModel("qwen2.5-7b-instruct", "qwen2.5-7b-instruct.gguf"); got != DefaultDockerTemplateID {
		t.Fatalf("non-embedding model should use default docker template, got=%s", got)
	}
}

func TestRefreshLocalModelsPreserveLoadedStateAndContainerMetadata(t *testing.T) {
	root := t.TempDir()
	modelDir := filepath.Join(root, "multilingual-e5-base")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir failed: %v", err)
	}

	adapters := adapter.NewManager()
	svc := NewModelService(
		registry.NewModelRegistry([]model.Model{
			{
				ID:          "local-multilingual-e5-base",
				Name:        "multilingual-e5-base",
				Provider:    "localfs",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				Endpoint:    "unix:///var/run/docker.sock",
				State:       model.ModelStateLoaded,
				Metadata: map[string]string{
					"source":               "local-scan",
					"path":                 modelDir,
					"runtime_template_id":  DefaultEmbeddingTemplateID,
					"runtime_container_id": "container-abc",
				},
			},
		}),
		registry.NewNodeRegistry([]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		}),
		nil,
		scheduler.NewScheduler(),
		adapters,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		root,
	)

	if err := svc.refreshLocalModels(context.Background()); err != nil {
		t.Fatalf("refreshLocalModels failed: %v", err)
	}

	got, ok := svc.modelRegistry.Get("local-multilingual-e5-base")
	if !ok {
		t.Fatalf("model not found after refresh")
	}
	if got.State != model.ModelStateLoaded {
		t.Fatalf("state should be preserved as loaded, got=%s", got.State)
	}
	if got.Metadata["runtime_template_id"] != DefaultEmbeddingTemplateID {
		t.Fatalf("template id should be preserved, got=%s", got.Metadata["runtime_template_id"])
	}
	if got.Metadata["runtime_container_id"] != "container-abc" {
		t.Fatalf("container metadata should be preserved, got=%s", got.Metadata["runtime_container_id"])
	}
}

func TestStopModelContainerKeepsLoadedState(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("stop", model.ActionResult{
		Success: true,
		Detail: map[string]interface{}{
			"runtime_container_id": "container-xyz",
			"runtime_running":      false,
		},
	})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		},
		[]model.Model{
			{
				ID:          "local-embed",
				Name:        "embed",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				State:       model.ModelStateRunning,
			},
		},
		adapters,
	)

	res, err := svc.StopModel(context.Background(), "local-embed")
	if err != nil {
		t.Fatalf("StopModel returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("StopModel should succeed")
	}

	got, _ := svc.modelRegistry.Get("local-embed")
	if got.State != model.ModelStateLoaded {
		t.Fatalf("expected state loaded after stop, got=%s", got.State)
	}
}

func TestLoadModelContainerAlwaysEntersLoadedState(t *testing.T) {
	tests := []struct {
		name          string
		runtimeRunVal bool
	}{
		{name: "created", runtimeRunVal: false},
		{name: "already_running", runtimeRunVal: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &captureAdapter{}
			capture.setResult("load", model.ActionResult{
				Success: true,
				Detail: map[string]interface{}{
					"runtime_running": tc.runtimeRunVal,
				},
			})

			adapters := adapter.NewManager()
			adapters.Register(model.RuntimeTypeDocker, capture)

			svc := newTestModelService(
				[]model.Node{
					{
						ID: "node-main",
						Runtimes: []model.Runtime{
							{
								ID:       "rt-docker-1",
								Type:     model.RuntimeTypeDocker,
								Endpoint: "unix:///var/run/docker.sock",
								Enabled:  true,
							},
						},
					},
				},
				[]model.Model{
					{
						ID:          "local-embed",
						Name:        "embed",
						BackendType: model.RuntimeTypeDocker,
						HostNodeID:  "node-main",
						RuntimeID:   "rt-docker-1",
						State:       model.ModelStateStopped,
					},
				},
				adapters,
			)

			res, err := svc.LoadModel(context.Background(), "local-embed")
			if err != nil {
				t.Fatalf("LoadModel returned error: %v", err)
			}
			if !res.Success {
				t.Fatalf("LoadModel should succeed")
			}

			got, _ := svc.modelRegistry.Get("local-embed")
			if got.State != model.ModelStateLoaded {
				t.Fatalf("unexpected state, expected=%s got=%s", model.ModelStateLoaded, got.State)
			}
		})
	}
}

func TestActionAllowedForBackendAndStateContainerRuntime(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		state   model.ModelState
		allowed bool
	}{
		{name: "stopped_load", action: "load", state: model.ModelStateStopped, allowed: true},
		{name: "unknown_load", action: "load", state: model.ModelStateUnknown, allowed: true},
		{name: "error_load", action: "load", state: model.ModelStateError, allowed: true},
		{name: "stopped_start", action: "start", state: model.ModelStateStopped, allowed: false},
		{name: "stopped_unload", action: "unload", state: model.ModelStateStopped, allowed: false},
		{name: "stopped_stop", action: "stop", state: model.ModelStateStopped, allowed: false},
		{name: "loaded_load", action: "load", state: model.ModelStateLoaded, allowed: false},
		{name: "loaded_start", action: "start", state: model.ModelStateLoaded, allowed: true},
		{name: "loaded_unload", action: "unload", state: model.ModelStateLoaded, allowed: true},
		{name: "loaded_stop", action: "stop", state: model.ModelStateLoaded, allowed: false},
		{name: "running_load", action: "load", state: model.ModelStateRunning, allowed: false},
		{name: "running_start", action: "start", state: model.ModelStateRunning, allowed: false},
		{name: "running_unload", action: "unload", state: model.ModelStateRunning, allowed: true},
		{name: "running_stop", action: "stop", state: model.ModelStateRunning, allowed: true},
		{name: "busy_unload", action: "unload", state: model.ModelStateBusy, allowed: true},
		{name: "busy_stop", action: "stop", state: model.ModelStateBusy, allowed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := actionAllowedForBackendAndState(tc.action, model.RuntimeTypeDocker, tc.state)
			if got != tc.allowed {
				t.Fatalf("unexpected allow result, action=%s state=%s allowed=%t got=%t", tc.action, tc.state, tc.allowed, got)
			}
		})
	}
}

func TestStartModelDisallowedWhenContainerModelStopped(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("start", model.ActionResult{Success: true})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		},
		[]model.Model{
			{
				ID:          "local-embed",
				Name:        "embed",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				State:       model.ModelStateStopped,
			},
		},
		adapters,
	)

	res, err := svc.StartModel(context.Background(), "local-embed")
	if err != nil {
		t.Fatalf("StartModel returned error: %v", err)
	}
	if res.Success {
		t.Fatalf("StartModel should be rejected when state is stopped")
	}
	if capture.countCall("start") != 0 {
		t.Fatalf("adapter start should not be called when action is disallowed")
	}
	got, _ := svc.modelRegistry.Get("local-embed")
	if got.State != model.ModelStateStopped {
		t.Fatalf("state should remain stopped, got=%s", got.State)
	}
}

func TestUnloadModelFromRunningStopsFirstThenUnloads(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("stop", model.ActionResult{
		Success: true,
		Detail: map[string]interface{}{
			"runtime_container_id": "container-abc",
			"runtime_running":      false,
		},
	})
	capture.setResult("unload", model.ActionResult{
		Success: true,
		Detail: map[string]interface{}{
			"runtime_removed": true,
		},
	})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		},
		[]model.Model{
			{
				ID:          "local-embed",
				Name:        "embed",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				State:       model.ModelStateRunning,
			},
		},
		adapters,
	)

	res, err := svc.UnloadModel(context.Background(), "local-embed")
	if err != nil {
		t.Fatalf("UnloadModel returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("UnloadModel should succeed")
	}
	if capture.countCall("stop") != 1 || capture.countCall("unload") != 1 {
		t.Fatalf("expected stop and unload each called once, got stop=%d unload=%d", capture.countCall("stop"), capture.countCall("unload"))
	}
	if len(capture.callTrace) < 2 || capture.callTrace[0] != "stop" || capture.callTrace[1] != "unload" {
		t.Fatalf("unexpected call trace: %v", capture.callTrace)
	}

	got, _ := svc.modelRegistry.Get("local-embed")
	if got.State != model.ModelStateStopped {
		t.Fatalf("state should become stopped after unload, got=%s", got.State)
	}
}

func TestRefreshContainerRuntimeStatesSyncToLoaded(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("status", model.ActionResult{
		Success: true,
		Detail: map[string]interface{}{
			"runtime_exists":       true,
			"runtime_running":      false,
			"runtime_container_id": "container-abc",
		},
	})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		},
		[]model.Model{
			{
				ID:          "local-embed",
				Name:        "embed",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				State:       model.ModelStateRunning,
				Metadata: map[string]string{
					"runtime_container_id": "container-abc",
				},
			},
		},
		adapters,
	)

	if err := svc.refreshContainerRuntimeStates(context.Background()); err != nil {
		t.Fatalf("refreshContainerRuntimeStates returned error: %v", err)
	}

	got, _ := svc.modelRegistry.Get("local-embed")
	if got.State != model.ModelStateLoaded {
		t.Fatalf("expected loaded after runtime stopped, got=%s", got.State)
	}
}

func TestRefreshContainerRuntimeStatesSyncToStoppedAndClearMetadata(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("status", model.ActionResult{
		Success: true,
		Detail: map[string]interface{}{
			"runtime_exists":  false,
			"runtime_running": false,
		},
	})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-docker-1",
						Type:     model.RuntimeTypeDocker,
						Endpoint: "unix:///var/run/docker.sock",
						Enabled:  true,
					},
				},
			},
		},
		[]model.Model{
			{
				ID:          "local-embed",
				Name:        "embed",
				BackendType: model.RuntimeTypeDocker,
				HostNodeID:  "node-main",
				RuntimeID:   "rt-docker-1",
				State:       model.ModelStateLoaded,
				Metadata: map[string]string{
					"runtime_container_id": "container-abc",
					"runtime_container":    "mcp-model-local-embed",
					"runtime_image":        "ghcr.io/demo/image:latest",
				},
			},
		},
		adapters,
	)

	if err := svc.refreshContainerRuntimeStates(context.Background()); err != nil {
		t.Fatalf("refreshContainerRuntimeStates returned error: %v", err)
	}

	got, _ := svc.modelRegistry.Get("local-embed")
	if got.State != model.ModelStateStopped {
		t.Fatalf("expected stopped after runtime missing, got=%s", got.State)
	}
	if got.Metadata["runtime_container_id"] != "" || got.Metadata["runtime_container"] != "" || got.Metadata["runtime_image"] != "" {
		t.Fatalf("container metadata should be cleared when runtime container is missing, got=%v", got.Metadata)
	}
}

func TestListModelsDoesNotTriggerRefresh(t *testing.T) {
	capture := &captureAdapter{
		listData: []model.Model{
			{ID: "lm-a", Name: "lm-a", BackendType: model.RuntimeTypeLMStudio},
		},
	}
	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeLMStudio, capture)

	svc := newTestModelService(
		nil,
		[]model.Model{
			{ID: "local-a", Name: "local-a", BackendType: model.RuntimeTypeDocker},
		},
		adapters,
	)

	models, err := svc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "local-a" {
		t.Fatalf("ListModels should return cached data, got=%+v", models)
	}
	if capture.countCall("list") != 0 {
		t.Fatalf("ListModels should not trigger refresh, list call=%d", capture.countCall("list"))
	}
}

func TestRefreshModelsUpdatesLMStudioData(t *testing.T) {
	capture := &captureAdapter{
		listData: []model.Model{
			{ID: "lm-remote-1", Name: "LM Remote 1"},
		},
	}
	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeLMStudio, capture)

	svc := newTestModelService(
		[]model.Node{
			{
				ID: "node-main",
				Runtimes: []model.Runtime{
					{
						ID:       "rt-lm-1",
						Type:     model.RuntimeTypeLMStudio,
						Endpoint: "http://127.0.0.1:1234",
						Enabled:  true,
					},
				},
			},
		},
		nil,
		adapters,
	)

	if err := svc.RefreshModels(context.Background()); err != nil {
		t.Fatalf("RefreshModels returned error: %v", err)
	}
	if capture.countCall("list") != 1 {
		t.Fatalf("RefreshModels should trigger LM Studio refresh once, list call=%d", capture.countCall("list"))
	}

	models, err := svc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "lm-remote-1" {
		t.Fatalf("unexpected models after refresh: %+v", models)
	}
}
