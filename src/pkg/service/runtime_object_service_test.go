package service

import (
	"context"
	"io"
	"log/slog"
	"strings"
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

func TestResolveRuntimeInstanceContext(t *testing.T) {
	svc := newRuntimeObjectTestService(
		[]model.Model{
			{
				ID:           "local-multilingual-e5-base",
				Name:         "multilingual-e5-base",
				ModelType:    model.ModelKindEmbedding,
				SourceType:   model.ModelSourceLocalPath,
				Format:       model.ModelFormatSafeTensors,
				BackendType:  model.RuntimeTypeDocker,
				HostNodeID:   "node-controller",
				DesiredState: "running",
				Readiness:    model.ReadinessUnknown,
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
	instance, err := svc.GetRuntimeInstanceByModelID(context.Background(), "local-multilingual-e5-base")
	if err != nil {
		t.Fatalf("get runtime instance by model failed: %v", err)
	}
	resolved, err := svc.ResolveRuntimeInstanceContext(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("resolve runtime instance context failed: %v", err)
	}
	if resolved.Instance.ID != instance.ID {
		t.Fatalf("unexpected resolved instance id: %s", resolved.Instance.ID)
	}
	if resolved.Binding.ID == "" {
		t.Fatalf("resolved binding should not be empty")
	}
	if resolved.Template.ID == "" {
		t.Fatalf("resolved template should not be empty")
	}
	if resolved.Manifest.ID == "" {
		t.Fatalf("resolved manifest should not be empty")
	}
}

func TestApplyAgentTaskObservationUpdatesRuntimeInstance(t *testing.T) {
	svc := newRuntimeObjectTestService(
		[]model.Model{
			{
				ID:           "local-multilingual-e5-base",
				Name:         "multilingual-e5-base",
				ModelType:    model.ModelKindEmbedding,
				SourceType:   model.ModelSourceLocalPath,
				Format:       model.ModelFormatSafeTensors,
				BackendType:  model.RuntimeTypeDocker,
				HostNodeID:   "node-controller",
				DesiredState: "running",
				Readiness:    model.ReadinessUnknown,
				Metadata: map[string]string{
					"runtime_template_id": "tei-embedding",
				},
			},
		},
		[]model.Node{
			{ID: "node-controller", Role: model.NodeRoleController},
		},
	)
	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	instance, err := svc.GetRuntimeInstanceByModelID(context.Background(), "local-multilingual-e5-base")
	if err != nil {
		t.Fatalf("get runtime instance by model failed: %v", err)
	}

	task := model.Task{
		ID:         "task-agent-docker-start-1",
		Type:       model.TaskTypeAgentDockerStart,
		TargetType: model.TaskTargetRuntime,
		TargetID:   "local-multilingual-e5-base",
		Status:     model.TaskStatusSuccess,
		Message:    "docker start 完成",
		Payload: map[string]interface{}{
			"runtime_instance_id": instance.ID,
		},
		Detail: map[string]interface{}{
			"runtime_instance_id": instance.ID,
			"runtime_exists":      true,
			"runtime_running":     true,
			"observed_state":      "running",
		},
	}
	if err := svc.ApplyAgentTaskObservation(context.Background(), task); err != nil {
		t.Fatalf("apply agent task observation failed: %v", err)
	}

	updated, err := svc.GetRuntimeInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("get runtime instance failed: %v", err)
	}
	if updated.ObservedState != "running" {
		t.Fatalf("unexpected observed_state: %s", updated.ObservedState)
	}
	if updated.Readiness != model.ReadinessReady {
		t.Fatalf("unexpected readiness: %s", updated.Readiness)
	}
	if updated.HealthMessage == "" {
		t.Fatalf("health message should be updated")
	}
}

func TestApplyAgentTaskObservationMapsChecksToRuntimeInstance(t *testing.T) {
	svc := newRuntimeObjectTestService(
		[]model.Model{
			{
				ID:           "local-multilingual-e5-base",
				Name:         "multilingual-e5-base",
				ModelType:    model.ModelKindEmbedding,
				SourceType:   model.ModelSourceLocalPath,
				Format:       model.ModelFormatSafeTensors,
				BackendType:  model.RuntimeTypeDocker,
				HostNodeID:   "node-controller",
				DesiredState: "running",
				Readiness:    model.ReadinessUnknown,
				Metadata: map[string]string{
					"runtime_template_id": "tei-embedding",
				},
			},
		},
		[]model.Node{
			{ID: "node-controller", Role: model.NodeRoleController},
		},
	)
	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	instance, err := svc.GetRuntimeInstanceByModelID(context.Background(), "local-multilingual-e5-base")
	if err != nil {
		t.Fatalf("get runtime instance by model failed: %v", err)
	}

	tasks := []model.Task{
		{
			ID:         "task-precheck-1",
			Type:       model.TaskTypeAgentRuntimePrecheck,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "runtime precheck passed",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"runtime_binding_id":  instance.BindingID,
				"binding_mode":        "generic_with_script",
				"manifest_id":         "manifest-e5",
			},
			Detail: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"precheck_result": map[string]interface{}{
					"overall_status": "warning",
					"gating":         false,
					"reasons": []map[string]interface{}{
						{"code": "script_missing", "message": "script missing", "blocking": false},
					},
					"resolved_mounts": []string{"/models", "/opt/controller/models"},
					"resolved_ports":  []string{"58001"},
					"resolved_script": "/opt/controller/scripts/start-e5.sh",
				},
			},
		},
		{
			ID:         "task-readiness-1",
			Type:       model.TaskTypeAgentRuntimeReadiness,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "runtime ready",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
			},
			Detail: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"runtime_ready":       true,
			},
		},
		{
			ID:         "task-port-1",
			Type:       model.TaskTypeAgentPortCheck,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "port check pass",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
			},
			Detail: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"host_port":           "127.0.0.1:58001",
			},
		},
		{
			ID:         "task-path-1",
			Type:       model.TaskTypeAgentModelPathCheck,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "model path check pass",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
			},
			Detail: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"abs_path":            "/opt/controller/models/multilingual-e5-base",
				"exists":              true,
			},
		},
		{
			ID:         "task-snapshot-1",
			Type:       model.TaskTypeAgentResourceSnapshot,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "resource snapshot ok",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
			},
			Detail: map[string]interface{}{
				"runtime_instance_id": instance.ID,
				"resource_snapshot": map[string]interface{}{
					"hostname": "node-controller",
					"docker_access": map[string]interface{}{
						"api_reachable": true,
					},
				},
			},
		},
		{
			ID:         "task-inspect-1",
			Type:       model.TaskTypeAgentDockerInspect,
			TargetType: model.TaskTargetRuntime,
			TargetID:   "local-multilingual-e5-base",
			Status:     model.TaskStatusSuccess,
			Message:    "docker inspect success",
			Payload: map[string]interface{}{
				"runtime_instance_id": instance.ID,
			},
			Detail: map[string]interface{}{
				"runtime_instance_id":      instance.ID,
				"runtime_exists":           true,
				"runtime_running":          true,
				"runtime_service_endpoint": "http://127.0.0.1:58001",
				"observed_state":           "running",
			},
		},
	}
	for _, task := range tasks {
		if err := svc.ApplyAgentTaskObservation(context.Background(), task); err != nil {
			t.Fatalf("apply task=%s failed: %v", task.Type, err)
		}
	}

	updated, err := svc.GetRuntimeInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("get runtime instance failed: %v", err)
	}
	if updated.BindingMode != model.RuntimeBindingModeGenericWithScript {
		t.Fatalf("unexpected binding mode: %s", updated.BindingMode)
	}
	if updated.ManifestID != "manifest-e5" {
		t.Fatalf("unexpected manifest id: %s", updated.ManifestID)
	}
	if updated.PrecheckStatus != model.PrecheckStatusWarning {
		t.Fatalf("unexpected precheck status: %s", updated.PrecheckStatus)
	}
	if updated.PrecheckGating {
		t.Fatalf("precheck gating should be false")
	}
	if updated.Readiness != model.ReadinessReady {
		t.Fatalf("unexpected readiness: %s", updated.Readiness)
	}
	if updated.Endpoint != "http://127.0.0.1:58001" {
		t.Fatalf("unexpected endpoint: %s", updated.Endpoint)
	}
	if len(updated.ResolvedMounts) == 0 {
		t.Fatalf("resolved_mounts should not be empty")
	}
	if len(updated.ResolvedPorts) == 0 {
		t.Fatalf("resolved_ports should not be empty")
	}
	if strings.TrimSpace(updated.ResolvedScript) == "" {
		t.Fatalf("resolved_script should not be empty")
	}
	if updated.LastAgentTask == nil {
		t.Fatalf("last_agent_task should be recorded")
	}
	if updated.LastAgentTask.TaskType != model.TaskTypeAgentDockerInspect {
		t.Fatalf("unexpected last agent task type: %s", updated.LastAgentTask.TaskType)
	}
	if strings.TrimSpace(updated.Metadata["snapshot_hostname"]) != "node-controller" {
		t.Fatalf("snapshot hostname should be updated")
	}
}
