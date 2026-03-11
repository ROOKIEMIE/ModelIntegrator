package sqlite

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"model-control-plane/src/pkg/model"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open sqlite store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestAgentRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	agent := model.Agent{
		ID:                  "agent-1",
		NodeID:              "node-main",
		Name:                "agent-1",
		Status:              model.AgentStatusOnline,
		Capabilities:        []string{"fit", "docker-manage"},
		RuntimeCapabilities: map[string][]string{"docker": []string{"start", "stop"}},
		Metadata:            map[string]string{"source": "test"},
		RegisteredAt:        now,
		LastHeartbeatAt:     now,
	}
	if err := store.UpsertAgent(ctx, agent); err != nil {
		t.Fatalf("upsert agent failed: %v", err)
	}

	got, ok, err := store.GetAgentByID(ctx, "agent-1")
	if err != nil {
		t.Fatalf("get agent failed: %v", err)
	}
	if !ok {
		t.Fatalf("agent not found")
	}
	if got.NodeID != "node-main" || got.Status != model.AgentStatusOnline {
		t.Fatalf("unexpected agent snapshot: %+v", got)
	}
}

func TestNodeAndRuntimeRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	node := model.Node{
		ID:               "node-main",
		Name:             "Main",
		Type:             model.NodeTypeLinux,
		Host:             "127.0.0.1",
		Status:           model.NodeStatusOnline,
		Classification:   model.NodeClassificationHybrid,
		CapabilityTier:   model.CapabilityTier2,
		CapabilitySource: model.CapabilitySourceMerged,
		AgentStatus:      model.AgentStatusOnline,
		LastSeenAt:       now,
		Runtimes: []model.Runtime{
			{
				ID:           "rt-docker",
				Type:         model.RuntimeTypeDocker,
				Endpoint:     "unix:///var/run/docker.sock",
				Enabled:      true,
				Status:       model.RuntimeStatusOnline,
				Capabilities: []string{"list", "start"},
				Actions:      []string{"start"},
			},
		},
	}
	if err := store.UpsertNodeWithRuntimes(ctx, node); err != nil {
		t.Fatalf("upsert node failed: %v", err)
	}

	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("unexpected node count: %d", len(nodes))
	}
	if len(nodes[0].Runtimes) != 1 {
		t.Fatalf("unexpected runtime count: %d", len(nodes[0].Runtimes))
	}
	if nodes[0].Runtimes[0].ID != "rt-docker" {
		t.Fatalf("unexpected runtime id: %s", nodes[0].Runtimes[0].ID)
	}
}

func TestModelRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	item := model.Model{
		ID:            "local-embed",
		Name:          "local-embed",
		Provider:      "localfs",
		BackendType:   model.RuntimeTypeDocker,
		HostNodeID:    "node-main",
		RuntimeID:     "rt-docker",
		Endpoint:      "unix:///var/run/docker.sock",
		State:         model.ModelStateLoaded,
		DesiredState:  string(model.ModelStateRunning),
		ObservedState: string(model.ModelStateLoaded),
		Readiness:     model.ReadinessNotReady,
		HealthMessage: "runtime 已装载但未运行",
		Metadata:      map[string]string{"source": "test"},
	}
	if err := store.UpsertModel(ctx, item); err != nil {
		t.Fatalf("upsert model failed: %v", err)
	}

	got, ok, err := store.GetModelByID(ctx, "local-embed")
	if err != nil {
		t.Fatalf("get model failed: %v", err)
	}
	if !ok {
		t.Fatalf("model not found")
	}
	if got.State != model.ModelStateLoaded || got.HostNodeID != "node-main" {
		t.Fatalf("unexpected model snapshot: %+v", got)
	}
	if got.DesiredState != string(model.ModelStateRunning) || got.ObservedState != string(model.ModelStateLoaded) {
		t.Fatalf("unexpected model desired/observed: %+v", got)
	}
	if got.Readiness != model.ReadinessNotReady {
		t.Fatalf("unexpected model readiness: %s", got.Readiness)
	}
}

func TestTaskAndTestRunRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	task := model.Task{
		ID:              "task-1",
		Type:            model.TaskTypeRuntimeStart,
		TargetType:      model.TaskTargetRuntime,
		TargetID:        "local-multilingual-e5-base",
		AssignedAgentID: "agent-1",
		Status:          model.TaskStatusPending,
		Progress:        10,
		Message:         "created",
		Detail:          map[string]interface{}{"phase": "create"},
		Payload:         map[string]interface{}{"model_id": "local-multilingual-e5-base"},
		CreatedAt:       time.Now().UTC(),
	}
	if err := store.UpsertTask(ctx, task); err != nil {
		t.Fatalf("upsert task failed: %v", err)
	}

	tasks, err := store.ListTasks(ctx, string(model.TaskTargetRuntime), "local-multilingual-e5-base", 10)
	if err != nil {
		t.Fatalf("list tasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("unexpected task count: %d", len(tasks))
	}
	if tasks[0].Type != model.TaskTypeRuntimeStart {
		t.Fatalf("unexpected task type: %s", tasks[0].Type)
	}

	run := model.TestRun{
		TestRunID:   "testrun-1",
		Scenario:    "e5_embedding_smoke",
		Status:      model.TestRunStatusRunning,
		StartedAt:   time.Now().UTC(),
		LogPath:     "/tmp/logs/testrun-1",
		Summary:     "running",
		TriggeredBy: "test",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertTestRun(ctx, run); err != nil {
		t.Fatalf("upsert test run failed: %v", err)
	}
	got, ok, err := store.GetTestRunByID(ctx, "testrun-1")
	if err != nil {
		t.Fatalf("get test run failed: %v", err)
	}
	if !ok {
		t.Fatalf("test run not found")
	}
	if got.Scenario != "e5_embedding_smoke" || got.Status != model.TestRunStatusRunning {
		t.Fatalf("unexpected test run snapshot: %+v", got)
	}
}
