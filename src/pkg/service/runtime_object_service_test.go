package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
)

func newRuntimeObjectTestService(models []model.Model, nodes []model.Node) *RuntimeObjectService {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	templateSvc := NewRuntimeTemplateService(registry.NewRuntimeTemplateRegistry(nil), logger)
	templateSvc.RegisterBuiltins()
	return NewRuntimeObjectService(
		registry.NewModelRegistry(models),
		registry.NewNodeRegistry(nodes),
		templateSvc,
		logger,
	)
}

func TestRuntimeObjectBootstrapCreatesBindingAndInstanceForE5(t *testing.T) {
	svc := newRuntimeObjectTestService(
		[]model.Model{
			{
				ID:            "local-multilingual-e5-base",
				Name:          "multilingual-e5-base",
				ModelType:     model.ModelKindEmbedding,
				SourceType:    model.ModelSourceLocalPath,
				Format:        model.ModelFormatSafeTensors,
				BackendType:   model.RuntimeTypeDocker,
				HostNodeID:    "node-controller",
				DesiredState:  "stopped",
				ObservedState: "stopped",
				Readiness:     model.ReadinessUnknown,
				Metadata: map[string]string{
					"runtime_template_id": "tei-embedding",
				},
			},
		},
		[]model.Node{
			{ID: "node-controller"},
		},
	)

	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	bindings, err := svc.ListBindings(context.Background())
	if err != nil {
		t.Fatalf("list bindings failed: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected one binding, got=%d", len(bindings))
	}
	if bindings[0].BindingMode != model.RuntimeBindingModeGenericInjected {
		t.Fatalf("unexpected binding mode: %s", bindings[0].BindingMode)
	}

	instances, err := svc.ListRuntimeInstances(context.Background())
	if err != nil {
		t.Fatalf("list runtime instances failed: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected one runtime instance, got=%d", len(instances))
	}
	if instances[0].BindingID != bindings[0].ID {
		t.Fatalf("instance should reference binding: instance=%s binding=%s", instances[0].BindingID, bindings[0].ID)
	}
}

func TestCreateBindingCustomBundleRequiresManifest(t *testing.T) {
	svc := newRuntimeObjectTestService(
		[]model.Model{
			{
				ID:         "local-multilingual-e5-base",
				Name:       "multilingual-e5-base",
				ModelType:  model.ModelKindEmbedding,
				SourceType: model.ModelSourceLocalPath,
			},
		},
		nil,
	)

	_, err := svc.CreateBinding(context.Background(), model.RuntimeBinding{
		ModelID:     "local-multilingual-e5-base",
		TemplateID:  "tei-embedding",
		BindingMode: model.RuntimeBindingModeCustomBundle,
		Enabled:     true,
	})
	if err == nil {
		t.Fatalf("expected custom_bundle validation error")
	}
}
