package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"model-control-plane/src/pkg/adapter"
	"model-control-plane/src/pkg/model"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

func newTaskServiceForTest(t *testing.T, models []model.Model, nodes []model.Node, capture *captureAdapter) (*TaskService, *ModelService) {
	t.Helper()
	store, err := sqlitestore.Open(":memory:", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open sqlite store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	adapters := adapter.NewManager()
	adapters.Register(model.RuntimeTypeDocker, capture)
	adapters.Register(model.RuntimeTypePortainer, capture)
	modelSvc := newTestModelService(nodes, models, adapters)
	if err := modelSvc.SetStore(store); err != nil {
		t.Fatalf("set model store failed: %v", err)
	}
	taskSvc := NewTaskService(store, modelSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return taskSvc, modelSvc
}

func TestRuntimeTaskStartLifecycle(t *testing.T) {
	capture := &captureAdapter{}
	capture.setResult("start", model.ActionResult{Success: true, Message: "started", Detail: map[string]interface{}{"runtime_running": true}})
	capture.setResult("status", model.ActionResult{Success: true, Message: "ok", Detail: map[string]interface{}{"runtime_exists": true, "runtime_running": true}})

	taskSvc, _ := newTaskServiceForTest(t,
		[]model.Model{{
			ID:           "local-multilingual-e5-base",
			Name:         "multilingual-e5-base",
			BackendType:  model.RuntimeTypeDocker,
			HostNodeID:   "node-controller",
			RuntimeID:    "rt-docker",
			State:        model.ModelStateLoaded,
			DesiredState: string(model.ModelStateLoaded),
			Metadata: map[string]string{
				"runtime_template_payload": `{"id":"tei-embedding","runtime_type":"docker","image":"ghcr.io/huggingface/text-embeddings-inference:cpu-latest","ports":["58001:80"]}`,
				"runtime_template_id":      "tei-embedding",
				"runtime_endpoint":         "unix:///var/run/docker.sock",
			},
		}},
		[]model.Node{{
			ID:       "node-controller",
			Runtimes: []model.Runtime{{ID: "rt-docker", Type: model.RuntimeTypeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true}},
		}},
		capture,
	)

	created, err := taskSvc.CreateRuntimeTask(context.Background(), model.TaskTypeRuntimeStart, "local-multilingual-e5-base", "test")
	if err != nil {
		t.Fatalf("create runtime task failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finalTask, err := taskSvc.AwaitTask(ctx, created.ID, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("await task failed: %v", err)
	}
	if finalTask.Status != model.TaskStatusSuccess {
		t.Fatalf("unexpected final task status: %s message=%s error=%s", finalTask.Status, finalTask.Message, finalTask.Error)
	}
}

func TestAgentTaskPullAndReport(t *testing.T) {
	capture := &captureAdapter{}
	taskSvc, modelSvc := newTaskServiceForTest(t,
		[]model.Model{{
			ID:           "local-multilingual-e5-base",
			Name:         "multilingual-e5-base",
			BackendType:  model.RuntimeTypeDocker,
			HostNodeID:   "node-controller",
			RuntimeID:    "rt-docker",
			State:        model.ModelStateRunning,
			DesiredState: string(model.ModelStateRunning),
			Metadata:     map[string]string{"runtime_template_id": "tei-embedding"},
		}},
		[]model.Node{{ID: "node-controller", Runtimes: []model.Runtime{{ID: "rt-docker", Type: model.RuntimeTypeDocker, Enabled: true}}}},
		capture,
	)

	created, err := taskSvc.CreateAgentRuntimeReadinessTask(context.Background(), AgentRuntimeReadinessTaskRequest{
		AgentID:    "agent-1",
		ModelID:    "local-multilingual-e5-base",
		Endpoint:   "http://127.0.0.1:58001",
		HealthPath: "/health",
	})
	if err != nil {
		t.Fatalf("create agent task failed: %v", err)
	}

	pulled, ok, err := taskSvc.PullNextAgentTask(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("pull agent task failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected one pending task")
	}
	if pulled.ID != created.ID {
		t.Fatalf("unexpected task id: %s", pulled.ID)
	}

	reported, err := taskSvc.ReportAgentTask(context.Background(), "agent-1", pulled.ID, AgentTaskReport{
		Status:   model.TaskStatusSuccess,
		Progress: 100,
		Message:  "runtime ready",
		Detail:   map[string]interface{}{"http_status": 200},
	})
	if err != nil {
		t.Fatalf("report task failed: %v", err)
	}
	if reported.Status != model.TaskStatusSuccess {
		t.Fatalf("unexpected report status: %s", reported.Status)
	}

	updatedModel, err := modelSvc.GetModel(context.Background(), "local-multilingual-e5-base")
	if err != nil {
		t.Fatalf("get model failed: %v", err)
	}
	if updatedModel.Readiness != model.ReadinessReady {
		t.Fatalf("agent readiness not applied to model, got=%s", updatedModel.Readiness)
	}
}

func TestCreateAgentNodeLocalTaskWithNodeAgentResolution(t *testing.T) {
	capture := &captureAdapter{}
	taskSvc, _ := newTaskServiceForTest(t,
		[]model.Model{{
			ID:          "local-multilingual-e5-base",
			Name:        "multilingual-e5-base",
			BackendType: model.RuntimeTypeDocker,
			HostNodeID:  "node-controller",
			RuntimeID:   "rt-docker",
			State:       model.ModelStateRunning,
			Metadata: map[string]string{
				"path":                 "./resources/models/multilingual-e5-base",
				"runtime_container_id": "mcp-model-local-multilingual-e5-base",
			},
		}},
		[]model.Node{{ID: "node-controller", Runtimes: []model.Runtime{{ID: "rt-docker", Type: model.RuntimeTypeDocker, Enabled: true}}}},
		capture,
	)

	agentSvc := NewAgentService(30*time.Second, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := agentSvc.Register(context.Background(), model.AgentRegisterRequest{
		ID:     "agent-controller-local",
		NodeID: "node-controller",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	taskSvc.SetAgentService(agentSvc)

	created, err := taskSvc.CreateAgentNodeTask(context.Background(), AgentNodeLocalTaskRequest{
		NodeID:   "node-controller",
		ModelID:  "local-multilingual-e5-base",
		TaskType: model.TaskTypeAgentResourceSnapshot,
		Payload:  map[string]interface{}{"runtime_id": "rt-docker"},
	})
	if err != nil {
		t.Fatalf("create agent node-local task failed: %v", err)
	}
	if created.AssignedAgentID != "agent-controller-local" {
		t.Fatalf("unexpected assigned agent: %s", created.AssignedAgentID)
	}
	if created.Type != model.TaskTypeAgentResourceSnapshot {
		t.Fatalf("unexpected task type: %s", created.Type)
	}
}
