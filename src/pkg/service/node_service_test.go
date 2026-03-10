package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
)

func newTestNodeService(nodes []model.Node) *NodeService {
	return NewNodeService(
		registry.NewNodeRegistry(nodes),
		adapter.NewManager(),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func TestListNodesRuntimeHealthCheckTakesPriority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newTestNodeService([]model.Node{
		{
			ID:   "node-main",
			Host: "203.0.113.1",
			Runtimes: []model.Runtime{
				{
					ID:       "rt-lm-1",
					Type:     model.RuntimeTypeLMStudio,
					Endpoint: server.URL,
					Enabled:  true,
				},
			},
		},
	})

	nodes, err := svc.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("unexpected nodes length: %d", len(nodes))
	}
	if nodes[0].Status != model.NodeStatusOnline {
		t.Fatalf("runtime health check should make node online, got=%s", nodes[0].Status)
	}
}

func TestListNodesRuntimeFailureDoesNotFallbackToPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	svc := newTestNodeService([]model.Node{
		{
			ID:   "node-main",
			Host: "127.0.0.1",
			Runtimes: []model.Runtime{
				{
					ID:       "rt-lm-1",
					Type:     model.RuntimeTypeLMStudio,
					Endpoint: server.URL,
					Enabled:  true,
				},
			},
		},
	})

	nodes, err := svc.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("unexpected nodes length: %d", len(nodes))
	}
	if nodes[0].Status != model.NodeStatusOffline {
		t.Fatalf("runtime health check failed, status should be offline, got=%s", nodes[0].Status)
	}
}

func TestListNodesNoRuntimeAndNoHostReturnsUnknown(t *testing.T) {
	svc := newTestNodeService([]model.Node{
		{
			ID:       "node-main",
			Host:     "",
			Runtimes: nil,
		},
	})

	nodes, err := svc.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("unexpected nodes length: %d", len(nodes))
	}
	if nodes[0].Status != model.NodeStatusUnknown {
		t.Fatalf("status should be unknown without runtime/host, got=%s", nodes[0].Status)
	}
}
