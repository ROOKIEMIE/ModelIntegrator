package service

import (
	"testing"

	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
)

func TestValidateTemplateSuccess(t *testing.T) {
	svc := NewRuntimeTemplateService(registry.NewRuntimeTemplateRegistry(nil), nil)
	res := svc.ValidateTemplate(model.RuntimeTemplate{
		ID:          "vllm-qwen",
		Name:        "vLLM Qwen",
		RuntimeType: model.RuntimeTypeDocker,
		Image:       "vllm/vllm-openai:latest",
		Command:     []string{"--model", "Qwen/Qwen2.5-7B-Instruct"},
		Volumes:     []string{"./resources/models:/models", "./resources/download-cache/hf:/data/hf-cache"},
		Ports:       []string{"58000:8000"},
		Env: map[string]string{
			"HF_HOME": "/data/hf-cache",
		},
	})
	if !res.Valid {
		t.Fatalf("expected valid template, got errors: %v", res.Errors)
	}
}

func TestValidateTemplateInvalid(t *testing.T) {
	svc := NewRuntimeTemplateService(registry.NewRuntimeTemplateRegistry(nil), nil)
	res := svc.ValidateTemplate(model.RuntimeTemplate{
		ID:          "INVALID ID",
		Name:        "",
		RuntimeType: model.RuntimeTypeDocker,
		Image:       "",
		Volumes:     []string{"models"},
		Ports:       []string{"abc"},
	})
	if res.Valid {
		t.Fatalf("expected invalid template")
	}
	if len(res.Errors) == 0 {
		t.Fatalf("expected validation errors")
	}
}

func TestRegisterTemplate(t *testing.T) {
	reg := registry.NewRuntimeTemplateRegistry(nil)
	svc := NewRuntimeTemplateService(reg, nil)
	res := svc.RegisterTemplate(nil, model.RuntimeTemplate{
		ID:          "docker-custom-a",
		Name:        "Custom A",
		RuntimeType: model.RuntimeTypeDocker,
		Image:       "ubuntu:22.04",
	})
	if !res.Valid {
		t.Fatalf("register should pass, errors=%v", res.Errors)
	}
	if got, ok := reg.Get("docker-custom-a"); !ok || got.ID != "docker-custom-a" {
		t.Fatalf("template not found in registry")
	}
}
