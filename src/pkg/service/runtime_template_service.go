package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
)

const (
	DefaultDockerTemplateID    = "docker-generic"
	DefaultVLLMTemplateID      = "vllm-openai"
	DefaultEmbeddingTemplateID = "tei-embedding"
)

var (
	templateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
	envKeyPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type RuntimeTemplateService struct {
	registry *registry.RuntimeTemplateRegistry
	logger   *slog.Logger
}

func NewRuntimeTemplateService(reg *registry.RuntimeTemplateRegistry, logger *slog.Logger) *RuntimeTemplateService {
	return &RuntimeTemplateService{
		registry: reg,
		logger:   logger,
	}
}

func (s *RuntimeTemplateService) RegisterBuiltins() {
	for _, tpl := range builtinRuntimeTemplates() {
		s.registry.Upsert(tpl)
	}
}

func (s *RuntimeTemplateService) RegisterFromConfig(ctx context.Context, templates []model.RuntimeTemplate) error {
	_ = ctx
	errs := make([]string, 0)
	for _, tpl := range templates {
		res := s.ValidateTemplate(tpl)
		if !res.Valid {
			errs = append(errs, fmt.Sprintf("模板 %q 校验失败: %s", tpl.ID, strings.Join(res.Errors, "; ")))
			continue
		}
		normalized := res.Normalized
		normalized.Source = "config"
		s.registry.Upsert(normalized)
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, " | "))
	}
	return nil
}

func (s *RuntimeTemplateService) ListTemplates(ctx context.Context) []model.RuntimeTemplate {
	_ = ctx
	return s.registry.List()
}

func (s *RuntimeTemplateService) GetTemplate(ctx context.Context, id string) (model.RuntimeTemplate, bool) {
	_ = ctx
	return s.registry.Get(strings.TrimSpace(id))
}

func (s *RuntimeTemplateService) ValidateTemplate(tpl model.RuntimeTemplate) model.RuntimeTemplateValidationResult {
	normalized := tpl
	normalized.ID = strings.TrimSpace(normalized.ID)
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Description = strings.TrimSpace(normalized.Description)
	normalized.RuntimeType = model.RuntimeType(strings.ToLower(strings.TrimSpace(string(normalized.RuntimeType))))
	normalized.Image = strings.TrimSpace(normalized.Image)

	warnings := make([]string, 0)
	errors := make([]string, 0)

	if normalized.ID == "" {
		errors = append(errors, "id 不能为空")
	} else if !templateIDPattern.MatchString(normalized.ID) {
		errors = append(errors, "id 格式非法，需匹配 ^[a-z0-9][a-z0-9._-]{1,63}$")
	}

	if normalized.Name == "" {
		errors = append(errors, "name 不能为空")
	}

	if normalized.RuntimeType == "" {
		normalized.RuntimeType = model.RuntimeTypeDocker
		warnings = append(warnings, "runtime_type 为空，已默认设置为 docker")
	}

	switch normalized.RuntimeType {
	case model.RuntimeTypeDocker:
		if normalized.Image == "" {
			errors = append(errors, "docker 运行时模板必须配置 image")
		}
	case model.RuntimeTypePortainer:
		if normalized.Image == "" {
			errors = append(errors, "portainer 运行时模板必须配置 image")
		}
	default:
		errors = append(errors, fmt.Sprintf("runtime_type=%q 暂不支持", normalized.RuntimeType))
	}

	normalized.Command = normalizeStringList(normalized.Command)
	normalized.Volumes = normalizeStringList(normalized.Volumes)
	normalized.Ports = normalizeStringList(normalized.Ports)
	normalized.Env = normalizeEnvMap(normalized.Env)
	normalized.Metadata = normalizeStringMap(normalized.Metadata)

	for _, volume := range normalized.Volumes {
		if err := validateVolume(volume); err != nil {
			errors = append(errors, fmt.Sprintf("volumes %q 非法: %v", volume, err))
		}
	}
	for _, port := range normalized.Ports {
		if err := validatePort(port); err != nil {
			errors = append(errors, fmt.Sprintf("ports %q 非法: %v", port, err))
		}
	}
	for k := range normalized.Env {
		if !envKeyPattern.MatchString(k) {
			errors = append(errors, fmt.Sprintf("env key %q 非法", k))
		}
	}

	return model.RuntimeTemplateValidationResult{
		Valid:      len(errors) == 0,
		Errors:     errors,
		Warnings:   warnings,
		Normalized: normalized,
	}
}

func (s *RuntimeTemplateService) RegisterTemplate(ctx context.Context, tpl model.RuntimeTemplate) model.RuntimeTemplateValidationResult {
	_ = ctx
	res := s.ValidateTemplate(tpl)
	if !res.Valid {
		return res
	}
	normalized := res.Normalized
	if normalized.Source == "" {
		normalized.Source = "custom"
	}
	s.registry.Upsert(normalized)
	return model.RuntimeTemplateValidationResult{
		Valid:      true,
		Warnings:   res.Warnings,
		Normalized: normalized,
	}
}

func normalizeStringList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeEnvMap(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for k, v := range items {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateVolume(item string) error {
	parts := strings.Split(item, ":")
	if len(parts) < 2 {
		return fmt.Errorf("格式应为 host:container[:ro|rw]")
	}

	mode := ""
	if parts[len(parts)-1] == "ro" || parts[len(parts)-1] == "rw" {
		mode = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	if mode == "" && len(parts) < 2 {
		return fmt.Errorf("缺少 container path")
	}

	containerPath := strings.TrimSpace(parts[len(parts)-1])
	hostPath := strings.TrimSpace(strings.Join(parts[:len(parts)-1], ":"))
	if hostPath == "" {
		return fmt.Errorf("host path 不能为空")
	}
	if containerPath == "" {
		return fmt.Errorf("container path 不能为空")
	}
	if !strings.HasPrefix(containerPath, "/") {
		return fmt.Errorf("container path 需为绝对路径")
	}
	return nil
}

func validatePort(item string) error {
	v := strings.TrimSpace(item)
	if v == "" {
		return fmt.Errorf("不能为空")
	}
	main := v
	if strings.Contains(v, "/") {
		pair := strings.Split(v, "/")
		if len(pair) != 2 {
			return fmt.Errorf("协议格式非法")
		}
		main = pair[0]
		proto := strings.ToLower(strings.TrimSpace(pair[1]))
		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("协议仅支持 tcp 或 udp")
		}
	}

	parts := strings.Split(main, ":")
	if len(parts) != 2 {
		return fmt.Errorf("格式应为 host:container 或 host:container/proto")
	}
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n <= 0 || n > 65535 {
			return fmt.Errorf("端口范围应为 1-65535")
		}
	}
	return nil
}

func builtinRuntimeTemplates() []model.RuntimeTemplate {
	return []model.RuntimeTemplate{
		{
			ID:          DefaultDockerTemplateID,
			Name:        "Docker Generic",
			Description: "通用 Docker 运行模板（占位），用于本地扫描模型的默认绑定",
			RuntimeType: model.RuntimeTypeDocker,
			Image:       "ubuntu:22.04",
			Command:     []string{"sleep", "infinity"},
			Volumes:     []string{"./resources/models:/models:ro"},
			NeedsGPU:    false,
			Source:      "builtin",
			Metadata: map[string]string{
				"category": "generic",
			},
		},
		{
			ID:          DefaultVLLMTemplateID,
			Name:        "vLLM OpenAI",
			Description: "vLLM 推理容器模板（NVIDIA GPU）",
			RuntimeType: model.RuntimeTypeDocker,
			Image:       "vllm/vllm-openai:latest",
			Command: []string{
				"--host", "0.0.0.0",
				"--port", "8000",
				"--model", "Qwen/Qwen2.5-7B-Instruct",
				"--download-dir", "/models",
			},
			Volumes:  []string{"./resources/models:/models", "./resources/download-cache/hf:/data/hf-cache"},
			Ports:    []string{"58000:8000"},
			NeedsGPU: true,
			Source:   "builtin",
			Metadata: map[string]string{
				"category": "vllm",
			},
		},
		{
			ID:          DefaultEmbeddingTemplateID,
			Name:        "TEI Embedding",
			Description: "HuggingFace Text Embeddings Inference（CPU）模板，面向 embedding 模型快速验证",
			RuntimeType: model.RuntimeTypeDocker,
			Image:       "ghcr.io/huggingface/text-embeddings-inference:cpu-latest",
			Command: []string{
				"--model-id", "{{MODEL_PATH_CONTAINER}}",
				"--port", "80",
			},
			Volumes:  []string{"./resources/models:/models:ro"},
			Ports:    []string{"58001:80"},
			NeedsGPU: false,
			Source:   "builtin",
			Metadata: map[string]string{
				"category": "embedding",
			},
		},
	}
}
