package config

import (
	"testing"

	"model-control-plane/src/pkg/model"
)

func TestNormalizeNodesControllerManaged(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Nodes = []model.Node{
		{
			Type: model.NodeTypeLinux,
			Host: "127.0.0.1",
			Runtimes: []model.Runtime{
				{Type: model.RuntimeTypeDocker, Enabled: true},
			},
		},
		{
			Type: model.NodeTypeMac,
			Host: "192.168.1.20",
			Runtimes: []model.Runtime{
				{Type: model.RuntimeTypeLMStudio, Enabled: true},
			},
		},
	}
	cfg.Models = []model.Model{
		{
			ID:         "local-e5",
			HostNodeID: "node-controller",
			RuntimeID:  "node-controller-docker-1",
		},
	}

	cfg.normalizeNodes()

	if len(cfg.Nodes) != 2 {
		t.Fatalf("unexpected node count: %d", len(cfg.Nodes))
	}
	controllerNode := cfg.Nodes[0]
	if controllerNode.Role != model.NodeRoleController {
		t.Fatalf("unexpected controller role: %s", controllerNode.Role)
	}
	if controllerNode.ID != "node-controller" {
		t.Fatalf("unexpected controller node id: %s", controllerNode.ID)
	}
	metadata, ok := controllerNode.Metadata.(map[string]string)
	if !ok {
		t.Fatalf("unexpected metadata type: %T", controllerNode.Metadata)
	}
	if metadata["managed_node"] != "true" {
		t.Fatalf("expected managed_node=true, got=%q", metadata["managed_node"])
	}
	if metadata["controller_node"] != "true" {
		t.Fatalf("expected controller_node=true, got=%q", metadata["controller_node"])
	}

	if cfg.Models[0].HostNodeID != "node-controller" {
		t.Fatalf("host_node_id mapping failed: %s", cfg.Models[0].HostNodeID)
	}
	if cfg.Models[0].RuntimeID != "node-controller-docker-1" {
		t.Fatalf("runtime_id mapping failed: %s", cfg.Models[0].RuntimeID)
	}
}
