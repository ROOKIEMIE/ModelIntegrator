package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"model-control-plane/src/pkg/model"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

func newTestAgentService(ttl time.Duration) *AgentService {
	return NewAgentService(ttl, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestAgentRegisterHeartbeatAndCapabilities(t *testing.T) {
	svc := newTestAgentService(2 * time.Second)
	ctx := context.Background()

	registerResp, err := svc.Register(ctx, model.AgentRegisterRequest{
		AgentID: "agent-1",
		NodeID:  "node-managed-1",
		Host:    "10.0.0.2",
		Version: "0.1.0",
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if registerResp.Agent.Status != model.AgentStatusOnline {
		t.Fatalf("unexpected status: %s", registerResp.Agent.Status)
	}

	_, err = svc.ReportCapabilities(ctx, "agent-1", model.AgentCapabilityReportRequest{
		NodeID:              "node-managed-1",
		Capabilities:        []string{"fit", "docker-manage"},
		RuntimeCapabilities: map[string][]string{"docker": []string{"load", "unload"}},
	})
	if err != nil {
		t.Fatalf("capability report failed: %v", err)
	}

	hbResp, err := svc.Heartbeat(ctx, "agent-1", model.AgentHeartbeatRequest{})
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if hbResp.Status != model.AgentStatusOnline {
		t.Fatalf("unexpected heartbeat status: %s", hbResp.Status)
	}

	agent, ok := svc.GetByNodeID("node-managed-1")
	if !ok {
		t.Fatalf("agent should be mapped by node id")
	}
	if len(agent.Capabilities) == 0 {
		t.Fatalf("expected capabilities to be stored")
	}
}

func TestAgentHeartbeatWithoutRegister(t *testing.T) {
	svc := newTestAgentService(2 * time.Second)
	_, err := svc.Heartbeat(context.Background(), "missing-agent", model.AgentHeartbeatRequest{})
	if err == nil {
		t.Fatalf("heartbeat should fail for missing agent")
	}
	if err != ErrAgentNotFound {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentStatusTurnsOfflineWhenHeartbeatExpired(t *testing.T) {
	svc := newTestAgentService(20 * time.Millisecond)
	ctx := context.Background()

	_, err := svc.Register(ctx, model.AgentRegisterRequest{
		AgentID: "agent-expire",
		NodeID:  "node-managed-2",
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	agent, ok := svc.GetByNodeID("node-managed-2")
	if !ok {
		t.Fatalf("agent should be found by node")
	}
	if agent.Status != model.AgentStatusOffline {
		t.Fatalf("expected offline status after ttl, got=%s", agent.Status)
	}
}

func TestAgentServiceSQLitePersistenceAndRecover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "controller.db")

	store1, err := sqlitestore.Open(dbPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open store1 failed: %v", err)
	}

	svc1 := newTestAgentService(2 * time.Second)
	if err := svc1.SetStore(store1); err != nil {
		t.Fatalf("set store1 failed: %v", err)
	}

	ctx := context.Background()
	if _, err := svc1.Register(ctx, model.AgentRegisterRequest{
		AgentID:      "agent-persist",
		NodeID:       "node-controller",
		Capabilities: []string{"fit"},
	}); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, err := svc1.ReportCapabilities(ctx, "agent-persist", model.AgentCapabilitiesReportRequest{
		NodeID:              "node-controller",
		RuntimeCapabilities: map[string][]string{"docker": {"load", "start"}},
	}); err != nil {
		t.Fatalf("report capabilities failed: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("close store1 failed: %v", err)
	}

	store2, err := sqlitestore.Open(dbPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open store2 failed: %v", err)
	}
	defer func() {
		_ = store2.Close()
	}()

	svc2 := newTestAgentService(2 * time.Second)
	if err := svc2.SetStore(store2); err != nil {
		t.Fatalf("set store2 failed: %v", err)
	}

	restored, ok := svc2.GetByID("agent-persist")
	if !ok {
		t.Fatalf("expected restored agent")
	}
	if restored.NodeID != "node-controller" {
		t.Fatalf("unexpected restored node id: %s", restored.NodeID)
	}
	if len(restored.Capabilities) == 0 {
		t.Fatalf("expected restored capabilities")
	}
	if len(restored.RuntimeCapabilities["docker"]) == 0 {
		t.Fatalf("expected restored runtime capabilities")
	}
}
