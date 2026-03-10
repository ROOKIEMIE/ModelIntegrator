package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"ModelIntegrator/src/pkg/model"
)

func newTestAgentService(ttl time.Duration) *AgentService {
	return NewAgentService(ttl, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestAgentRegisterHeartbeatAndCapabilities(t *testing.T) {
	svc := newTestAgentService(2 * time.Second)
	ctx := context.Background()

	registerResp, err := svc.Register(ctx, model.AgentRegisterRequest{
		AgentID: "agent-1",
		NodeID:  "node-sub-1",
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
		NodeID:              "node-sub-1",
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

	agent, ok := svc.GetByNodeID("node-sub-1")
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
		NodeID:  "node-sub-2",
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	agent, ok := svc.GetByNodeID("node-sub-2")
	if !ok {
		t.Fatalf("agent should be found by node")
	}
	if agent.Status != model.AgentStatusOffline {
		t.Fatalf("expected offline status after ttl, got=%s", agent.Status)
	}
}
