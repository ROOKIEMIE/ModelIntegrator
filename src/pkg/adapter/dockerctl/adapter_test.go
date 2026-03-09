package dockerctl

import (
	"encoding/json"
	"testing"

	"ModelIntegrator/src/pkg/model"
)

func TestParsePortMapping(t *testing.T) {
	key, binding, err := parsePortMapping("58000:8000")
	if err != nil {
		t.Fatalf("parsePortMapping unexpected err: %v", err)
	}
	if key != "8000/tcp" {
		t.Fatalf("unexpected key: %s", key)
	}
	if binding.HostPort != "58000" {
		t.Fatalf("unexpected host port: %s", binding.HostPort)
	}
}

func TestParsePortMappingWithProtocol(t *testing.T) {
	key, _, err := parsePortMapping("6800:6800/udp")
	if err != nil {
		t.Fatalf("parsePortMapping unexpected err: %v", err)
	}
	if key != "6800/udp" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestParsePortMappingInvalid(t *testing.T) {
	if _, _, err := parsePortMapping("abc"); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestParseRuntimeTemplateFromModel(t *testing.T) {
	raw, _ := json.Marshal(model.RuntimeTemplate{
		ID:          "docker-generic",
		Name:        "Docker Generic",
		RuntimeType: model.RuntimeTypeDocker,
		Image:       "ubuntu:22.04",
	})
	m := model.Model{
		ID: "local-qwen",
		Metadata: map[string]string{
			"runtime_template_payload": string(raw),
		},
	}
	tpl, err := parseRuntimeTemplateFromModel(m)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if tpl.Image != "ubuntu:22.04" {
		t.Fatalf("unexpected image: %s", tpl.Image)
	}
}

func TestResolveConnectionForModelOverride(t *testing.T) {
	a := NewAdapter("dockerctl", "unix:///var/run/docker.sock", "global-token")
	m := model.Model{
		Endpoint: "tcp://192.168.1.2:2375",
		Metadata: map[string]string{
			"runtime_endpoint": "http://override:2375",
			"runtime_token":    "runtime-token",
		},
	}

	endpoint, token := a.resolveConnectionForModel(m)
	if endpoint != "http://override:2375" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	if token != "runtime-token" {
		t.Fatalf("unexpected token: %s", token)
	}
}

func TestResolveConnectionForModelFallback(t *testing.T) {
	a := NewAdapter("dockerctl", "unix:///var/run/docker.sock", "global-token")
	m := model.Model{}

	endpoint, token := a.resolveConnectionForModel(m)
	if endpoint != "unix:///var/run/docker.sock" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	if token != "global-token" {
		t.Fatalf("unexpected token: %s", token)
	}
}
