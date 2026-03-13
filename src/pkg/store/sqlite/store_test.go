package sqlite

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
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
		NodeID:              "node-controller",
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
	if got.NodeID != "node-controller" || got.Status != model.AgentStatusOnline {
		t.Fatalf("unexpected agent snapshot: %+v", got)
	}
}

func TestNodeAndRuntimeRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	node := model.Node{
		ID:               "node-controller",
		Name:             "Controller Node",
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
		HostNodeID:    "node-controller",
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
	if got.State != model.ModelStateLoaded || got.HostNodeID != "node-controller" {
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

func TestRuntimeBindingInstanceManifestRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.UpsertModel(ctx, model.Model{
		ID:          "local-multilingual-e5-base",
		Name:        "multilingual-e5-base",
		DisplayName: "Multilingual E5 Base",
		ModelType:   model.ModelKindEmbedding,
		SourceType:  model.ModelSourceLocalPath,
		Format:      model.ModelFormatSafeTensors,
		PathOrRef:   "./resources/models/multilingual-e5-base",
		BackendType: model.RuntimeTypeDocker,
		State:       model.ModelStateStopped,
	}); err != nil {
		t.Fatalf("upsert model failed: %v", err)
	}

	manifest := model.RuntimeBundleManifest{
		ID:                  "tei-embedding-e5-local",
		TemplateID:          "tei-embedding-e5-local",
		ManifestVersion:     "v1alpha1",
		TemplateType:        model.RuntimeTemplateTypeSingleContainer,
		RuntimeKind:         model.RuntimeKindTEI,
		SupportedModelTypes: []model.ModelKind{model.ModelKindEmbedding},
		SupportedFormats:    []model.ModelFormat{model.ModelFormatSafeTensors},
		Capabilities:        []model.ModelKind{model.ModelKindEmbedding},
		MountPoints:         []string{"/models"},
		ModelInjectionMode:  model.RuntimeBindingModeGenericInjected,
		ExposedPorts:        []string{"58001:80"},
	}
	if err := store.UpsertRuntimeBundleManifest(ctx, manifest); err != nil {
		t.Fatalf("upsert runtime bundle manifest failed: %v", err)
	}

	binding := model.RuntimeBinding{
		ID:                  "rb-local-multilingual-e5-base-tei-embedding-e5-local",
		ModelID:             "local-multilingual-e5-base",
		TemplateID:          "tei-embedding-e5-local",
		BindingMode:         model.RuntimeBindingModeGenericInjected,
		PreferredNode:       "node-controller",
		MountRules:          []string{"./resources/models:/models:ro"},
		CompatibilityStatus: model.CompatibilityCompatible,
		Enabled:             true,
		ManifestID:          manifest.ID,
	}
	if err := store.UpsertRuntimeBinding(ctx, binding); err != nil {
		t.Fatalf("upsert runtime binding failed: %v", err)
	}

	instance := model.RuntimeInstance{
		ID:            "ri-local-multilingual-e5-base",
		ModelID:       "local-multilingual-e5-base",
		TemplateID:    "tei-embedding-e5-local",
		BindingID:     binding.ID,
		NodeID:        "node-controller",
		DesiredState:  "running",
		ObservedState: "stopped",
		Readiness:     model.ReadinessNotReady,
		Endpoint:      "http://127.0.0.1:58001",
	}
	if err := store.UpsertRuntimeInstance(ctx, instance); err != nil {
		t.Fatalf("upsert runtime instance failed: %v", err)
	}

	gotBinding, ok, err := store.GetRuntimeBindingByID(ctx, binding.ID)
	if err != nil {
		t.Fatalf("get runtime binding failed: %v", err)
	}
	if !ok || gotBinding.BindingMode != model.RuntimeBindingModeGenericInjected {
		t.Fatalf("unexpected binding snapshot: %+v", gotBinding)
	}

	gotInstance, ok, err := store.GetRuntimeInstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("get runtime instance failed: %v", err)
	}
	if !ok || gotInstance.BindingID != binding.ID {
		t.Fatalf("unexpected runtime instance snapshot: %+v", gotInstance)
	}

	gotManifest, ok, err := store.GetRuntimeBundleManifestByTemplateID(ctx, "tei-embedding-e5-local")
	if err != nil {
		t.Fatalf("get runtime manifest failed: %v", err)
	}
	if !ok || gotManifest.RuntimeKind != model.RuntimeKindTEI {
		t.Fatalf("unexpected runtime manifest snapshot: %+v", gotManifest)
	}
}

func TestOpenMigratesLegacyTasksTable(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite failed: %v", err)
	}
	legacyDDL := `
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			progress TEXT NOT NULL DEFAULT '',
			message TEXT,
			error TEXT,
			created_at TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO tasks (id, type, target_type, target_id, status, progress, message, error, created_at)
		VALUES ('legacy-task-1', 'agent.runtime_precheck', 'runtime', 'ri-1', 'pending', '', NULL, NULL, '2026-03-14T00:00:00Z');
	`
	if _, err := rawDB.Exec(legacyDDL); err != nil {
		_ = rawDB.Close()
		t.Fatalf("seed legacy tasks schema failed: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite failed: %v", err)
	}

	store, err := Open(dbPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open store with legacy db failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	tasks, err := store.ListTasks(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("list tasks on migrated legacy db failed: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("expected legacy task to be queryable after migration")
	}
	if tasks[0].ID == "legacy-task-1" && tasks[0].Progress != 0 {
		t.Fatalf("expected legacy progress to be normalized to 0, got %d", tasks[0].Progress)
	}

	newTask := model.Task{
		ID:              "task-migrated-1",
		Type:            model.TaskTypeAgentRuntimePrecheck,
		TargetType:      model.TaskTargetRuntime,
		TargetID:        "ri-1",
		AssignedAgentID: "agent-controller-local",
		Status:          model.TaskStatusPending,
		Progress:        5,
		Message:         "created",
		Detail:          map[string]interface{}{"phase": "bootstrap"},
		Payload:         map[string]interface{}{"runtime_instance_id": "ri-1"},
		CreatedAt:       time.Now().UTC(),
	}
	if err := store.UpsertTask(ctx, newTask); err != nil {
		t.Fatalf("upsert task after legacy migration failed: %v", err)
	}

	claimed, ok, err := store.ClaimPendingTaskForAgent(ctx, "agent-controller-local", []model.TaskType{model.TaskTypeAgentRuntimePrecheck})
	if err != nil {
		t.Fatalf("claim pending task after migration failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected claimable task after migration")
	}
	if claimed.ID != newTask.ID {
		t.Fatalf("unexpected claimed task id: %s", claimed.ID)
	}
}
