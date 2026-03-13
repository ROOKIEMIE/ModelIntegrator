package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
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
	normalized.TemplateType = model.RuntimeTemplateType(strings.ToLower(strings.TrimSpace(string(normalized.TemplateType))))
	normalized.RuntimeKind = model.RuntimeKind(strings.ToLower(strings.TrimSpace(string(normalized.RuntimeKind))))
	normalized.RuntimeType = model.RuntimeType(strings.ToLower(strings.TrimSpace(string(normalized.RuntimeType))))
	normalized.ComposeRef = strings.TrimSpace(normalized.ComposeRef)
	normalized.ImageRef = strings.TrimSpace(normalized.ImageRef)
	normalized.Image = strings.TrimSpace(normalized.Image)
	normalized.Dedicated = normalized.Dedicated

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
	if normalized.TemplateType == "" {
		normalized.TemplateType = inferTemplateTypeByRuntimeType(normalized.RuntimeType)
	}
	if normalized.RuntimeKind == "" {
		normalized.RuntimeKind = inferRuntimeKindByTemplateHint(normalized)
	}

	switch normalized.RuntimeType {
	case model.RuntimeTypeDocker:
		if normalized.ImageRef == "" {
			normalized.ImageRef = normalized.Image
		}
		if normalized.Image == "" {
			normalized.Image = normalized.ImageRef
		}
		if normalized.Image == "" {
			errors = append(errors, "docker 运行时模板必须配置 image")
		}
	case model.RuntimeTypePortainer:
		if normalized.ImageRef == "" {
			normalized.ImageRef = normalized.Image
		}
		if normalized.Image == "" {
			normalized.Image = normalized.ImageRef
		}
		if normalized.Image == "" {
			errors = append(errors, "portainer 运行时模板必须配置 image")
		}
	default:
		errors = append(errors, fmt.Sprintf("runtime_type=%q 暂不支持", normalized.RuntimeType))
	}

	normalized.Command = normalizeStringList(normalized.Command)
	normalized.CommandTemplate = normalizeStringList(normalized.CommandTemplate)
	if len(normalized.CommandTemplate) == 0 {
		normalized.CommandTemplate = normalizeStringList(normalized.Command)
	}
	if len(normalized.Command) == 0 {
		normalized.Command = normalizeStringList(normalized.CommandTemplate)
	}
	normalized.Volumes = normalizeStringList(normalized.Volumes)
	normalized.InjectableMounts = normalizeStringList(normalized.InjectableMounts)
	normalized.Ports = normalizeStringList(normalized.Ports)
	normalized.ExposedPorts = normalizeStringList(normalized.ExposedPorts)
	if len(normalized.ExposedPorts) == 0 {
		normalized.ExposedPorts = normalizeStringList(normalized.Ports)
	}
	if len(normalized.Ports) == 0 {
		normalized.Ports = normalizeStringList(normalized.ExposedPorts)
	}
	normalized.Env = normalizeEnvMap(normalized.Env)
	normalized.InjectableEnv = normalizeStringList(normalized.InjectableEnv)
	normalized.SupportedModelTypes = normalizeModelKindList(normalized.SupportedModelTypes)
	normalized.SupportedFormats = normalizeModelFormatList(normalized.SupportedFormats)
	normalized.Capabilities = normalizeModelKindList(normalized.Capabilities)
	normalized.Metadata = normalizeStringMap(normalized.Metadata)
	if len(normalized.Capabilities) == 0 {
		normalized.Capabilities = inferTemplateCapabilities(normalized)
	}
	if len(normalized.SupportedModelTypes) == 0 {
		normalized.SupportedModelTypes = append([]model.ModelKind(nil), normalized.Capabilities...)
	}

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
	if normalized.Manifest != nil {
		normalizedManifest, manifestErrors := normalizeAndValidateManifest(*normalized.Manifest)
		if len(manifestErrors) > 0 {
			for _, item := range manifestErrors {
				errors = append(errors, "manifest: "+item)
			}
		}
		normalizedManifest.TemplateID = firstNonEmpty(strings.TrimSpace(normalizedManifest.TemplateID), normalized.ID)
		normalized.Manifest = &normalizedManifest
	} else {
		manifest := manifestFromTemplate(normalized)
		normalized.Manifest = &manifest
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

func inferTemplateTypeByRuntimeType(runtimeType model.RuntimeType) model.RuntimeTemplateType {
	switch runtimeType {
	case model.RuntimeTypeDocker, model.RuntimeTypePortainer:
		return model.RuntimeTemplateTypeSingleContainer
	default:
		return model.RuntimeTemplateTypeUnknown
	}
}

func inferRuntimeKindByTemplateHint(tpl model.RuntimeTemplate) model.RuntimeKind {
	haystack := strings.ToLower(strings.Join([]string{
		tpl.ID,
		tpl.Name,
		tpl.Description,
		tpl.Image,
		tpl.ImageRef,
		readMetadataHint(tpl.Metadata, "runtime_kind"),
		readMetadataHint(tpl.Metadata, "category"),
	}, " "))
	switch {
	case strings.Contains(haystack, "tei"), strings.Contains(haystack, "embedding"):
		return model.RuntimeKindTEI
	case strings.Contains(haystack, "vllm"):
		return model.RuntimeKindVLLM
	case strings.Contains(haystack, "llama.cpp"), strings.Contains(haystack, "llamacpp"):
		return model.RuntimeKindLlamaCPP
	case strings.Contains(haystack, "lmstudio"):
		return model.RuntimeKindLMStudio
	default:
		return model.RuntimeKindCustom
	}
}

func inferTemplateCapabilities(tpl model.RuntimeTemplate) []model.ModelKind {
	haystack := strings.ToLower(strings.Join([]string{
		tpl.ID,
		tpl.Name,
		tpl.Description,
		readMetadataHint(tpl.Metadata, "category"),
	}, " "))
	if strings.Contains(haystack, "embedding") || strings.Contains(haystack, "e5") {
		return []model.ModelKind{model.ModelKindEmbedding}
	}
	if strings.Contains(haystack, "rerank") {
		return []model.ModelKind{model.ModelKindRerank}
	}
	if strings.Contains(haystack, "chat") || strings.Contains(haystack, "vllm") {
		return []model.ModelKind{model.ModelKindChat}
	}
	return []model.ModelKind{model.ModelKindUnknown}
}

func readMetadataHint(metadata map[string]string, key string) string {
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata[key])
}

func normalizeModelKindList(items []model.ModelKind) []model.ModelKind {
	if len(items) == 0 {
		return nil
	}
	out := make([]model.ModelKind, 0, len(items))
	seen := map[model.ModelKind]struct{}{}
	for _, item := range items {
		normalized := model.ModelKind(strings.ToLower(strings.TrimSpace(string(item))))
		if normalized == "" {
			continue
		}
		switch normalized {
		case model.ModelKindChat, model.ModelKindEmbedding, model.ModelKindRerank, model.ModelKindUtility, model.ModelKindUnknown:
		default:
			normalized = model.ModelKindUnknown
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeModelFormatList(items []model.ModelFormat) []model.ModelFormat {
	if len(items) == 0 {
		return nil
	}
	out := make([]model.ModelFormat, 0, len(items))
	seen := map[model.ModelFormat]struct{}{}
	for _, item := range items {
		normalized := model.ModelFormat(strings.ToLower(strings.TrimSpace(string(item))))
		if normalized == "" {
			continue
		}
		switch normalized {
		case model.ModelFormatGGUF, model.ModelFormatSafeTensors, model.ModelFormatMLX, model.ModelFormatUnknown:
		default:
			normalized = model.ModelFormatUnknown
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeAndValidateManifest(input model.RuntimeBundleManifest) (model.RuntimeBundleManifest, []string) {
	normalized := input
	normalized.ID = strings.TrimSpace(normalized.ID)
	normalized.TemplateID = strings.TrimSpace(normalized.TemplateID)
	normalized.ManifestVersion = strings.TrimSpace(normalized.ManifestVersion)
	normalized.TemplateType = model.RuntimeTemplateType(strings.ToLower(strings.TrimSpace(string(normalized.TemplateType))))
	normalized.RuntimeKind = model.RuntimeKind(strings.ToLower(strings.TrimSpace(string(normalized.RuntimeKind))))
	normalized.SupportedModelTypes = normalizeModelKindList(normalized.SupportedModelTypes)
	normalized.SupportedFormats = normalizeModelFormatList(normalized.SupportedFormats)
	normalized.Capabilities = normalizeModelKindList(normalized.Capabilities)
	normalized.MountPoints = normalizeStringList(normalized.MountPoints)
	normalized.RequiredEnv = normalizeStringList(normalized.RequiredEnv)
	normalized.OptionalEnv = normalizeStringList(normalized.OptionalEnv)
	normalized.ModelInjectionMode = model.RuntimeBindingMode(strings.ToLower(strings.TrimSpace(string(normalized.ModelInjectionMode))))
	normalized.ExposedPorts = normalizeStringList(normalized.ExposedPorts)
	normalized.Notes = strings.TrimSpace(normalized.Notes)
	normalized.Metadata = normalizeStringMap(normalized.Metadata)
	normalized.Healthcheck.Path = strings.TrimSpace(normalized.Healthcheck.Path)
	normalized.Healthcheck.Method = strings.ToUpper(strings.TrimSpace(normalized.Healthcheck.Method))

	errs := make([]string, 0)
	if normalized.ManifestVersion == "" {
		errs = append(errs, "manifest_version 不能为空")
	}
	if normalized.TemplateType == "" {
		errs = append(errs, "template_type 不能为空")
	}
	if normalized.RuntimeKind == "" {
		errs = append(errs, "runtime_kind 不能为空")
	}
	if normalized.ModelInjectionMode == "" {
		normalized.ModelInjectionMode = model.RuntimeBindingModeGenericInjected
	}
	switch normalized.ModelInjectionMode {
	case model.RuntimeBindingModeDedicated, model.RuntimeBindingModeGenericInjected, model.RuntimeBindingModeGenericWithScript, model.RuntimeBindingModeCustomBundle:
	default:
		errs = append(errs, "model_injection_mode 非法")
	}
	return normalized, errs
}

func manifestFromTemplate(tpl model.RuntimeTemplate) model.RuntimeBundleManifest {
	manifest, _ := normalizeAndValidateManifest(model.RuntimeBundleManifest{
		ID:                     tpl.ID,
		TemplateID:             tpl.ID,
		ManifestVersion:        "v1alpha1",
		TemplateType:           tpl.TemplateType,
		RuntimeKind:            tpl.RuntimeKind,
		SupportedModelTypes:    tpl.SupportedModelTypes,
		SupportedFormats:       tpl.SupportedFormats,
		Capabilities:           tpl.Capabilities,
		MountPoints:            tpl.InjectableMounts,
		CommandOverrideAllowed: tpl.CommandOverrideAllowed,
		ScriptMountAllowed:     tpl.ScriptMountAllowed,
		ModelInjectionMode:     model.RuntimeBindingModeGenericInjected,
		Healthcheck:            tpl.Healthcheck,
		ExposedPorts:           tpl.ExposedPorts,
		Metadata: map[string]string{
			"generated_from_template": "true",
		},
	})
	return manifest
}

func builtinRuntimeTemplates() []model.RuntimeTemplate {
	return []model.RuntimeTemplate{
		{
			ID:                     DefaultDockerTemplateID,
			Name:                   "Docker Generic",
			Description:            "通用 Docker 运行模板（占位），用于本地扫描模型的默认绑定",
			TemplateType:           model.RuntimeTemplateTypeSingleContainer,
			RuntimeKind:            model.RuntimeKindCustom,
			SupportedModelTypes:    []model.ModelKind{model.ModelKindChat, model.ModelKindEmbedding, model.ModelKindUnknown},
			SupportedFormats:       []model.ModelFormat{model.ModelFormatGGUF, model.ModelFormatSafeTensors, model.ModelFormatUnknown},
			Capabilities:           []model.ModelKind{model.ModelKindUnknown},
			ImageRef:               "ubuntu:22.04",
			CommandTemplate:        []string{"sleep", "infinity"},
			InjectableMounts:       []string{"/models"},
			CommandOverrideAllowed: true,
			ScriptMountAllowed:     true,
			ExposedPorts:           nil,
			Dedicated:              false,
			RuntimeType:            model.RuntimeTypeDocker,
			Image:                  "ubuntu:22.04",
			Command:                []string{"sleep", "infinity"},
			Volumes:                []string{"./resources/models:/models:ro"},
			NeedsGPU:               false,
			Source:                 "builtin",
			Metadata: map[string]string{
				"category": "generic",
			},
			Manifest: &model.RuntimeBundleManifest{
				ID:                     DefaultDockerTemplateID,
				TemplateID:             DefaultDockerTemplateID,
				ManifestVersion:        "v1alpha1",
				TemplateType:           model.RuntimeTemplateTypeSingleContainer,
				RuntimeKind:            model.RuntimeKindCustom,
				SupportedModelTypes:    []model.ModelKind{model.ModelKindChat, model.ModelKindEmbedding, model.ModelKindUnknown},
				SupportedFormats:       []model.ModelFormat{model.ModelFormatGGUF, model.ModelFormatSafeTensors, model.ModelFormatUnknown},
				Capabilities:           []model.ModelKind{model.ModelKindUnknown},
				MountPoints:            []string{"/models"},
				CommandOverrideAllowed: true,
				ScriptMountAllowed:     true,
				ModelInjectionMode:     model.RuntimeBindingModeGenericInjected,
				Metadata: map[string]string{
					"source": "builtin",
				},
			},
		},
		{
			ID:                  DefaultVLLMTemplateID,
			Name:                "vLLM OpenAI",
			Description:         "vLLM 推理容器模板（NVIDIA GPU）",
			TemplateType:        model.RuntimeTemplateTypeSingleContainer,
			RuntimeKind:         model.RuntimeKindVLLM,
			SupportedModelTypes: []model.ModelKind{model.ModelKindChat},
			SupportedFormats:    []model.ModelFormat{model.ModelFormatSafeTensors},
			Capabilities:        []model.ModelKind{model.ModelKindChat},
			ImageRef:            "vllm/vllm-openai:latest",
			CommandTemplate: []string{
				"--host", "0.0.0.0",
				"--port", "8000",
				"--model", "Qwen/Qwen2.5-7B-Instruct",
				"--download-dir", "/models",
			},
			InjectableMounts: []string{"/models", "/data/hf-cache"},
			InjectableEnv:    []string{"HF_HOME"},
			Healthcheck: model.RuntimeHealthcheck{
				Path:            "/health",
				Method:          "GET",
				TimeoutSeconds:  3,
				IntervalSeconds: 10,
				SuccessCodes:    []int{200},
			},
			ExposedPorts: []string{"58000:8000"},
			Dedicated:    false,
			RuntimeType:  model.RuntimeTypeDocker,
			Image:        "vllm/vllm-openai:latest",
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
			Manifest: &model.RuntimeBundleManifest{
				ID:                  DefaultVLLMTemplateID,
				TemplateID:          DefaultVLLMTemplateID,
				ManifestVersion:     "v1alpha1",
				TemplateType:        model.RuntimeTemplateTypeSingleContainer,
				RuntimeKind:         model.RuntimeKindVLLM,
				SupportedModelTypes: []model.ModelKind{model.ModelKindChat},
				SupportedFormats:    []model.ModelFormat{model.ModelFormatSafeTensors},
				Capabilities:        []model.ModelKind{model.ModelKindChat},
				MountPoints:         []string{"/models", "/data/hf-cache"},
				RequiredEnv:         []string{"HF_HOME"},
				ModelInjectionMode:  model.RuntimeBindingModeGenericInjected,
				Healthcheck: model.RuntimeHealthcheck{
					Path:            "/health",
					Method:          "GET",
					TimeoutSeconds:  3,
					IntervalSeconds: 10,
					SuccessCodes:    []int{200},
				},
				ExposedPorts: []string{"58000:8000"},
				Metadata: map[string]string{
					"source": "builtin",
				},
			},
		},
		{
			ID:                  DefaultEmbeddingTemplateID,
			Name:                "TEI Embedding",
			Description:         "HuggingFace Text Embeddings Inference（CPU）模板，面向 embedding 模型快速验证",
			TemplateType:        model.RuntimeTemplateTypeSingleContainer,
			RuntimeKind:         model.RuntimeKindTEI,
			SupportedModelTypes: []model.ModelKind{model.ModelKindEmbedding},
			SupportedFormats:    []model.ModelFormat{model.ModelFormatSafeTensors, model.ModelFormatUnknown},
			Capabilities:        []model.ModelKind{model.ModelKindEmbedding},
			ImageRef:            "ghcr.io/huggingface/text-embeddings-inference:cpu-latest",
			CommandTemplate: []string{
				"--model-id", "{{MODEL_PATH_CONTAINER}}",
				"--port", "80",
			},
			InjectableMounts: []string{"/models"},
			Healthcheck: model.RuntimeHealthcheck{
				Path:            "/health",
				Method:          "GET",
				TimeoutSeconds:  3,
				IntervalSeconds: 10,
				SuccessCodes:    []int{200},
			},
			ExposedPorts: []string{"58001:80"},
			Dedicated:    false,
			RuntimeType:  model.RuntimeTypeDocker,
			Image:        "ghcr.io/huggingface/text-embeddings-inference:cpu-latest",
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
			Manifest: &model.RuntimeBundleManifest{
				ID:                  DefaultEmbeddingTemplateID,
				TemplateID:          DefaultEmbeddingTemplateID,
				ManifestVersion:     "v1alpha1",
				TemplateType:        model.RuntimeTemplateTypeSingleContainer,
				RuntimeKind:         model.RuntimeKindTEI,
				SupportedModelTypes: []model.ModelKind{model.ModelKindEmbedding},
				SupportedFormats:    []model.ModelFormat{model.ModelFormatSafeTensors, model.ModelFormatUnknown},
				Capabilities:        []model.ModelKind{model.ModelKindEmbedding},
				MountPoints:         []string{"/models"},
				ModelInjectionMode:  model.RuntimeBindingModeGenericInjected,
				Healthcheck: model.RuntimeHealthcheck{
					Path:            "/health",
					Method:          "GET",
					TimeoutSeconds:  3,
					IntervalSeconds: 10,
					SuccessCodes:    []int{200},
				},
				ExposedPorts: []string{"58001:80"},
				Metadata: map[string]string{
					"source": "builtin",
				},
			},
		},
	}
}
