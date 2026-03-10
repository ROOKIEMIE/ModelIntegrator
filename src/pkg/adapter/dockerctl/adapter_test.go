package dockerctl

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestMaterializeRuntimeTemplate(t *testing.T) {
	m := model.Model{
		ID:   "local-multilingual-e5-base",
		Name: "multilingual-e5-base",
		Metadata: map[string]string{
			"path": "./resources/models/multilingual-e5-base",
		},
	}
	tpl := model.RuntimeTemplate{
		ID:      "tei-embedding",
		Image:   "ghcr.io/huggingface/text-embeddings-inference:cpu-latest",
		Command: []string{"--model-id", "{{MODEL_PATH_CONTAINER}}", "--port", "80"},
	}

	out := materializeRuntimeTemplate(tpl, m)
	if len(out.Command) < 2 {
		t.Fatalf("unexpected command length: %d", len(out.Command))
	}
	if out.Command[1] != "/models/multilingual-e5-base" {
		t.Fatalf("unexpected materialized model path: %s", out.Command[1])
	}
	if len(out.Volumes) != 0 {
		t.Fatalf("unexpected volumes, got=%v", out.Volumes)
	}
}

func TestNormalizeVolumeBinding(t *testing.T) {
	got := normalizeVolumeBinding("./resources/models:/models:ro")
	if !strings.HasSuffix(got, ":/models:ro") {
		t.Fatalf("unexpected normalized volume: %s", got)
	}
	if strings.HasPrefix(got, "./") {
		t.Fatalf("host path should be absolute, got=%s", got)
	}
}

func TestSplitImageReference(t *testing.T) {
	repo, tag := splitImageReference("ghcr.io/huggingface/text-embeddings-inference:cpu-latest")
	if repo != "ghcr.io/huggingface/text-embeddings-inference" || tag != "cpu-latest" {
		t.Fatalf("unexpected split result: repo=%s tag=%s", repo, tag)
	}

	repo, tag = splitImageReference("localhost:5000/demo/image:1.0")
	if repo != "localhost:5000/demo/image" || tag != "1.0" {
		t.Fatalf("unexpected split result for registry+port: repo=%s tag=%s", repo, tag)
	}

	repo, tag = splitImageReference("ghcr.io/huggingface/text-embeddings-inference@sha256:abc")
	if repo != "ghcr.io/huggingface/text-embeddings-inference@sha256:abc" || tag != "" {
		t.Fatalf("unexpected split result for digest: repo=%s tag=%s", repo, tag)
	}
}

func TestTranslateHostPathForDockerDaemon(t *testing.T) {
	got := translateHostPathForDockerDaemon(context.Background(), nil, "./resources/models")
	if !filepath.IsAbs(got) {
		t.Fatalf("host path should be absolute, got=%s", got)
	}
}

func TestTranslateContainerPathToHost(t *testing.T) {
	c := &dockerHTTPClient{}
	c.selfMountOnce.Do(func() {})
	c.selfMounts = []containerMount{
		{
			Source:      "/home/whoami/Dev/ModelIntegrator/resources",
			Destination: "/opt/modelintegrator/resources",
		},
	}

	got, ok := c.translateContainerPathToHost(context.Background(), "/opt/modelintegrator/resources/models")
	if !ok {
		t.Fatalf("expected path translation")
	}
	if got != "/home/whoami/Dev/ModelIntegrator/resources/models" {
		t.Fatalf("unexpected translated path: %s", got)
	}
}

func TestSplitAndJoinVolumeBinding(t *testing.T) {
	hostPath, containerPath, mode, ok := splitVolumeBinding("./resources/models:/models:ro")
	if !ok {
		t.Fatalf("splitVolumeBinding should succeed")
	}
	if hostPath != "./resources/models" || containerPath != "/models" || mode != "ro" {
		t.Fatalf("unexpected split result: host=%s container=%s mode=%s", hostPath, containerPath, mode)
	}

	joined := joinVolumeBinding("/abs/resources/models", "/models", "ro")
	if joined != "/abs/resources/models:/models:ro" {
		t.Fatalf("unexpected joined volume: %s", joined)
	}
}

func TestIsModelIntegratorManagedContainer(t *testing.T) {
	info := containerInspect{}
	info.Config.Labels = map[string]string{
		"com.modelintegrator.managed": "true",
	}
	if !isModelIntegratorManagedContainer(info) {
		t.Fatalf("container should be recognized as managed")
	}

	info.Config.Labels["com.modelintegrator.managed"] = "false"
	if isModelIntegratorManagedContainer(info) {
		t.Fatalf("container should not be recognized as managed")
	}
}

func TestIsContainerOwnedByModel(t *testing.T) {
	m := model.Model{ID: "local-embed"}
	info := containerInspect{}
	info.Config.Labels = map[string]string{
		"com.modelintegrator.managed":  "true",
		"com.modelintegrator.model_id": "local-embed",
	}
	if !isContainerOwnedByModel(info, m) {
		t.Fatalf("container should be recognized as owned by model")
	}

	info.Config.Labels["com.modelintegrator.model_id"] = "another-model"
	if isContainerOwnedByModel(info, m) {
		t.Fatalf("container should not be recognized as owned by model")
	}
}
