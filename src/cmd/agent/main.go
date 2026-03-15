package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"model-control-plane/src/pkg/fit"
	"model-control-plane/src/pkg/model"
)

type llmfitConfig struct {
	enabled             bool
	binaryPath          string
	endpoint            string
	healthPath          string
	serveArgs           []string
	startupTimeout      time.Duration
	healthCheckInterval time.Duration
	healthCheckTimeout  time.Duration
	failureThreshold    int
}

type agentConfig struct {
	controllerEndpoint string
	authToken          string
	agentID            string
	nodeID             string
	host               string
	version            string
	heartbeatInterval  time.Duration
	taskPollInterval   time.Duration
	capabilities       []string
	runtimeCaps        map[string][]string
	llmfit             llmfitConfig
}

type apiResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func main() {
	cfg, err := loadAgentConfig()
	if err != nil {
		os.Stderr.WriteString("agent 配置错误: " + err.Error() + "\n")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fitManager, cfg := startManagedLLMFit(ctx, cfg)

	if err := retryControllerBootstrap(ctx, "注册", func(attempt int) error {
		return registerAgent(ctx, client, cfg, fitManager)
	}); err != nil {
		os.Stderr.WriteString("agent 注册失败: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := retryControllerBootstrap(ctx, "能力上报", func(attempt int) error {
		return reportCapabilities(ctx, client, cfg, fitManager)
	}); err != nil {
		os.Stderr.WriteString("agent 能力上报失败: " + err.Error() + "\n")
		os.Exit(1)
	}
	fmt.Printf("agent 已注册: agent_id=%s node_id=%s controller=%s\n", cfg.agentID, cfg.nodeID, cfg.controllerEndpoint)

	heartbeatTicker := time.NewTicker(cfg.heartbeatInterval)
	defer heartbeatTicker.Stop()
	taskTicker := time.NewTicker(cfg.taskPollInterval)
	defer taskTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			if fitManager != nil {
				_ = fitManager.Stop(context.Background())
			}
			fmt.Println("agent 退出")
			return
		case <-heartbeatTicker.C:
			if err := sendHeartbeat(ctx, client, cfg, fitManager); err != nil {
				fmt.Printf("agent 心跳失败: %v\n", err)
			}
		case <-taskTicker.C:
			if err := pollAndExecuteTask(ctx, client, cfg); err != nil {
				fmt.Printf("agent 任务执行失败: %v\n", err)
			}
		}
	}
}

func loadAgentConfig() (agentConfig, error) {
	hostname, _ := os.Hostname()
	cfg := agentConfig{
		controllerEndpoint: strings.TrimRight(firstNonEmpty(os.Getenv("AGENT_CONTROLLER_ENDPOINT"), "http://127.0.0.1:8080"), "/"),
		authToken:          strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_AUTH_TOKEN"), os.Getenv("MCP_AUTH_TOKEN"))),
		agentID:            strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_ID"), hostname)),
		nodeID:             strings.TrimSpace(os.Getenv("AGENT_NODE_ID")),
		host:               strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_HOST"), hostname)),
		version:            strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_VERSION"), "dev")),
		heartbeatInterval:  durationFromEnv("AGENT_HEARTBEAT_SECONDS", 15*time.Second),
		taskPollInterval:   durationFromEnv("AGENT_TASK_POLL_SECONDS", 5*time.Second),
		capabilities:       splitList(firstNonEmpty(os.Getenv("AGENT_CAPABILITIES"), "resource-snapshot,docker-manage,download")),
		llmfit: llmfitConfig{
			enabled:             boolFromEnv("AGENT_LLMFIT_ENABLED", false),
			binaryPath:          strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_LLMFIT_BINARY"), "llmfit")),
			endpoint:            strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_LLMFIT_ENDPOINT"), "http://127.0.0.1:18123")),
			healthPath:          strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_LLMFIT_HEALTH_PATH"), "/health")),
			serveArgs:           parseArgList(os.Getenv("AGENT_LLMFIT_SERVE_ARGS")),
			startupTimeout:      durationFromEnv("AGENT_LLMFIT_STARTUP_TIMEOUT_SECONDS", 20*time.Second),
			healthCheckInterval: durationFromEnv("AGENT_LLMFIT_HEALTH_INTERVAL_SECONDS", 10*time.Second),
			healthCheckTimeout:  durationFromEnv("AGENT_LLMFIT_HEALTH_TIMEOUT_SECONDS", 2*time.Second),
			failureThreshold:    intFromEnv("AGENT_LLMFIT_FAILURE_THRESHOLD", 3),
		},
	}

	runtimeCaps, err := parseRuntimeCapsEnv(os.Getenv("AGENT_RUNTIME_CAPABILITIES_JSON"))
	if err != nil {
		return cfg, err
	}
	cfg.runtimeCaps = runtimeCaps

	if cfg.nodeID == "" {
		return cfg, errors.New("AGENT_NODE_ID 不能为空")
	}
	if cfg.agentID == "" {
		return cfg, errors.New("AGENT_ID 不能为空")
	}
	return cfg, nil
}

func startManagedLLMFit(ctx context.Context, cfg agentConfig) (*fit.ManagedServe, agentConfig) {
	if !cfg.llmfit.enabled {
		return nil, cfg
	}

	manager := fit.NewManagedServe(fit.ManagedServeConfig{
		Enabled:             cfg.llmfit.enabled,
		BinaryPath:          cfg.llmfit.binaryPath,
		Endpoint:            cfg.llmfit.endpoint,
		HealthPath:          cfg.llmfit.healthPath,
		ServeArgs:           cfg.llmfit.serveArgs,
		StartupTimeout:      cfg.llmfit.startupTimeout,
		HealthCheckInterval: cfg.llmfit.healthCheckInterval,
		HealthCheckTimeout:  cfg.llmfit.healthCheckTimeout,
		FailureThreshold:    cfg.llmfit.failureThreshold,
	})
	if err := manager.Start(ctx); err != nil {
		fmt.Printf("llmfit managed serve 启动失败，将关闭 fit 能力: %v\n", err)
		cfg.capabilities = removeItem(cfg.capabilities, "fit")
		delete(cfg.runtimeCaps, "fit")
		return nil, cfg
	}

	snapshot := manager.Snapshot()
	fmt.Printf("llmfit managed serve 已启动: endpoint=%s health=%s pid=%d\n", snapshot.Endpoint, snapshot.HealthURL, snapshot.PID)
	cfg.capabilities = mergeItems(cfg.capabilities, []string{"fit"})
	if cfg.runtimeCaps == nil {
		cfg.runtimeCaps = map[string][]string{}
	}
	cfg.runtimeCaps["fit"] = mergeItems(cfg.runtimeCaps["fit"], []string{"health", "profile", "analyze", "recommend"})
	return manager, cfg
}

func registerAgent(ctx context.Context, client *http.Client, cfg agentConfig, fitManager *fit.ManagedServe) error {
	payload := model.AgentRegisterRequest{
		ID:       cfg.agentID,
		NodeID:   cfg.nodeID,
		Name:     cfg.agentID,
		Address:  cfg.host,
		Host:     cfg.host,
		Version:  cfg.version,
		Metadata: buildAgentMetadata(cfg, fitManager),
	}
	return doJSON(ctx, client, cfg, http.MethodPost, cfg.controllerEndpoint+"/api/v1/agents/register", payload, nil)
}

func reportCapabilities(ctx context.Context, client *http.Client, cfg agentConfig, fitManager *fit.ManagedServe) error {
	payload := model.AgentCapabilitiesReportRequest{
		NodeID:              cfg.nodeID,
		Capabilities:        cfg.capabilities,
		RuntimeCapabilities: cfg.runtimeCaps,
		Metadata:            buildAgentMetadata(cfg, fitManager),
	}
	return doJSON(ctx, client, cfg, http.MethodPost, cfg.controllerEndpoint+"/api/v1/agents/"+cfg.agentID+"/capabilities", payload, nil)
}

func sendHeartbeat(ctx context.Context, client *http.Client, cfg agentConfig, fitManager *fit.ManagedServe) error {
	payload := model.AgentHeartbeatRequest{
		NodeID:   cfg.nodeID,
		Status:   model.AgentStatusOnline,
		Metadata: buildAgentMetadata(cfg, fitManager),
	}
	return doJSON(ctx, client, cfg, http.MethodPost, cfg.controllerEndpoint+"/api/v1/agents/"+cfg.agentID+"/heartbeat", payload, nil)
}

type agentTaskEnvelope struct {
	Task *model.Task `json:"task"`
}

type agentTaskReport struct {
	Status     model.TaskStatus       `json:"status"`
	Progress   int                    `json:"progress,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Detail     map[string]interface{} `json:"detail,omitempty"`
	Error      string                 `json:"error,omitempty"`
	AcceptedAt time.Time              `json:"accepted_at,omitempty"`
	StartedAt  time.Time              `json:"started_at,omitempty"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
}

func pollAndExecuteTask(ctx context.Context, client *http.Client, cfg agentConfig) error {
	var envelope agentTaskEnvelope
	if err := doJSON(ctx, client, cfg, http.MethodGet, cfg.controllerEndpoint+"/api/v1/agents/"+cfg.agentID+"/tasks/next", nil, &envelope); err != nil {
		return err
	}
	if envelope.Task == nil || strings.TrimSpace(envelope.Task.ID) == "" {
		return nil
	}
	task := *envelope.Task
	startedAt := time.Now().UTC()
	if err := reportAgentTask(ctx, client, cfg, task.ID, agentTaskReport{
		Status:    model.TaskStatusRunning,
		Progress:  70,
		Message:   "agent 开始执行任务",
		StartedAt: startedAt,
	}); err != nil {
		return err
	}

	success, message, detail, errText := executeAgentTask(ctx, client, cfg, task)
	finishedAt := time.Now().UTC()
	detail = normalizeTaskResultEnvelope(task, detail, success, message, startedAt, finishedAt)
	report := agentTaskReport{
		Progress:   100,
		Message:    message,
		Detail:     detail,
		Error:      errText,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	if success {
		report.Status = model.TaskStatusSuccess
	} else {
		report.Status = model.TaskStatusFailed
	}
	return reportAgentTask(ctx, client, cfg, task.ID, report)
}

func reportAgentTask(ctx context.Context, client *http.Client, cfg agentConfig, taskID string, report agentTaskReport) error {
	url := cfg.controllerEndpoint + "/api/v1/agents/" + cfg.agentID + "/tasks/" + taskID + "/report"
	return doJSON(ctx, client, cfg, http.MethodPost, url, report, nil)
}

type resolvedAgentTaskContext struct {
	TaskScope          string
	RuntimeInstanceID  string
	RuntimeBindingID   string
	RuntimeTemplateID  string
	ManifestID         string
	ManifestVersion    string
	NodeID             string
	ModelID            string
	BindingMode        string
	RuntimeKind        string
	TemplateType       string
	ModelType          string
	ModelFormat        string
	Endpoint           string
	HealthPath         string
	ModelPath          string
	ScriptRef          string
	RuntimeContainerID string
	ExposedPorts       []string
	RequiredEnv        []string
	OptionalEnv        []string
	MountPoints        []string
	CommandOverride    []string
	BindingMountRules  []string
	BindingEnvOverride map[string]string
	SupportedModelType []string
	SupportedFormats   []string
	CommandOverrideOK  *bool
	ScriptMountOK      *bool
	Metadata           map[string]string
}

func (c resolvedAgentTaskContext) toPayloadMap() map[string]interface{} {
	out := map[string]interface{}{}
	appendString := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			out[key] = value
		}
	}
	appendString("task_scope", c.TaskScope)
	appendString("runtime_instance_id", c.RuntimeInstanceID)
	appendString("runtime_binding_id", c.RuntimeBindingID)
	appendString("runtime_template_id", c.RuntimeTemplateID)
	appendString("manifest_id", c.ManifestID)
	appendString("manifest_version", c.ManifestVersion)
	appendString("node_id", c.NodeID)
	appendString("model_id", c.ModelID)
	appendString("binding_mode", c.BindingMode)
	appendString("runtime_kind", c.RuntimeKind)
	appendString("template_type", c.TemplateType)
	appendString("model_type", c.ModelType)
	appendString("model_format", c.ModelFormat)
	appendString("endpoint", c.Endpoint)
	appendString("health_path", c.HealthPath)
	appendString("model_path", c.ModelPath)
	appendString("script_ref", c.ScriptRef)
	appendString("runtime_container_id", c.RuntimeContainerID)
	if len(c.ExposedPorts) > 0 {
		out["exposed_ports"] = append([]string(nil), c.ExposedPorts...)
	}
	if len(c.RequiredEnv) > 0 {
		out["required_env"] = append([]string(nil), c.RequiredEnv...)
	}
	if len(c.OptionalEnv) > 0 {
		out["optional_env"] = append([]string(nil), c.OptionalEnv...)
	}
	if len(c.MountPoints) > 0 {
		out["mount_points"] = append([]string(nil), c.MountPoints...)
	}
	if len(c.CommandOverride) > 0 {
		out["command_override"] = append([]string(nil), c.CommandOverride...)
	}
	if len(c.BindingMountRules) > 0 {
		out["binding_mount_rules"] = append([]string(nil), c.BindingMountRules...)
	}
	if len(c.BindingEnvOverride) > 0 {
		bindingEnv := map[string]string{}
		for k, v := range c.BindingEnvOverride {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			bindingEnv[key] = strings.TrimSpace(v)
		}
		if len(bindingEnv) > 0 {
			out["binding_env_overrides"] = bindingEnv
		}
	}
	if len(c.SupportedModelType) > 0 {
		out["supported_model_types"] = append([]string(nil), c.SupportedModelType...)
	}
	if len(c.SupportedFormats) > 0 {
		out["supported_formats"] = append([]string(nil), c.SupportedFormats...)
	}
	if c.CommandOverrideOK != nil {
		out["command_override_allowed"] = *c.CommandOverrideOK
	}
	if c.ScriptMountOK != nil {
		out["script_mount_allowed"] = *c.ScriptMountOK
	}
	if len(c.Metadata) > 0 {
		meta := map[string]string{}
		for k, v := range c.Metadata {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			meta[key] = strings.TrimSpace(v)
		}
		if len(meta) > 0 {
			out["metadata"] = meta
		}
	}
	return out
}

func resolveAgentTaskContext(task model.Task) (resolvedAgentTaskContext, *model.AgentTaskProtocolError) {
	ctx := resolvedAgentTaskContext{
		TaskScope: strings.TrimSpace(firstNonEmpty(
			stringValue(task.Payload, "task_scope"),
			stringValueFromNestedMap(task.Payload, "resolved_context", "task_scope"),
		)),
	}
	get := func(key string) string {
		return strings.TrimSpace(firstNonEmpty(
			stringValue(task.Payload, key),
			stringValueFromNestedMap(task.Payload, "resolved_context", key),
		))
	}
	ctx.RuntimeInstanceID = get("runtime_instance_id")
	ctx.RuntimeBindingID = get("runtime_binding_id")
	ctx.RuntimeTemplateID = get("runtime_template_id")
	ctx.ManifestID = get("manifest_id")
	ctx.ManifestVersion = get("manifest_version")
	ctx.NodeID = get("node_id")
	ctx.ModelID = get("model_id")
	ctx.BindingMode = get("binding_mode")
	ctx.RuntimeKind = get("runtime_kind")
	ctx.TemplateType = firstNonEmpty(get("template_type"), get("runtime_template_type"))
	ctx.ModelType = get("model_type")
	ctx.ModelFormat = get("model_format")
	ctx.Endpoint = get("endpoint")
	ctx.HealthPath = get("health_path")
	ctx.ModelPath = firstNonEmpty(get("model_path"), get("path"))
	ctx.ScriptRef = get("script_ref")
	ctx.RuntimeContainerID = firstNonEmpty(get("runtime_container_id"), get("container_id"), get("container"))
	ctx.ExposedPorts = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "exposed_ports"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "exposed_ports"),
	)
	ctx.RequiredEnv = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "required_env"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "required_env"),
	)
	ctx.OptionalEnv = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "optional_env"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "optional_env"),
	)
	ctx.MountPoints = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "mount_points"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "mount_points"),
	)
	ctx.CommandOverride = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "command_override"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "command_override"),
	)
	ctx.BindingMountRules = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "binding_mount_rules"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "binding_mount_rules"),
	)
	ctx.BindingEnvOverride = firstNonEmptyStringMap(
		stringMapFromValue(task.Payload["binding_env_overrides"]),
		stringMapFromValue(nestedMapValue(task.Payload, "resolved_context")["binding_env_overrides"]),
	)
	ctx.SupportedModelType = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "supported_model_types"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "supported_model_types"),
	)
	ctx.SupportedFormats = firstNonEmptyStringSlice(
		stringSliceValue(task.Payload, "supported_formats"),
		stringSliceValueFromNested(task.Payload, "resolved_context", "supported_formats"),
	)
	if value, ok := boolValueFromPayload(task.Payload, "command_override_allowed"); ok {
		ctx.CommandOverrideOK = &value
	}
	if value, ok := boolValueFromNested(task.Payload, "resolved_context", "command_override_allowed"); ok {
		ctx.CommandOverrideOK = &value
	}
	if value, ok := boolValueFromPayload(task.Payload, "script_mount_allowed"); ok {
		ctx.ScriptMountOK = &value
	}
	if value, ok := boolValueFromNested(task.Payload, "resolved_context", "script_mount_allowed"); ok {
		ctx.ScriptMountOK = &value
	}
	ctx.Metadata = stringMapFromValue(task.Payload["metadata"])
	if len(ctx.Metadata) == 0 {
		if nested := nestedMapValue(task.Payload, "resolved_context"); nested != nil {
			ctx.Metadata = stringMapFromValue(nested["metadata"])
		}
	}
	if ctx.TaskScope == "" {
		if ctx.RuntimeInstanceID != "" {
			ctx.TaskScope = "runtime_instance"
		} else {
			ctx.TaskScope = "legacy_model_or_node"
		}
	}

	if ctx.TaskScope == "runtime_instance" {
		missing := make([]string, 0, 4)
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "runtime_instance_id", value: ctx.RuntimeInstanceID},
			{name: "runtime_binding_id", value: ctx.RuntimeBindingID},
			{name: "runtime_template_id", value: ctx.RuntimeTemplateID},
			{name: "manifest_id", value: ctx.ManifestID},
		} {
			if strings.TrimSpace(field.value) == "" {
				missing = append(missing, field.name)
			}
		}
		if len(missing) > 0 {
			errDetail := map[string]interface{}{
				"task_id":    task.ID,
				"task_type":  string(task.Type),
				"task_scope": ctx.TaskScope,
			}
			return resolvedAgentTaskContext{}, &model.AgentTaskProtocolError{
				Code:          "agent_task_context_missing_fields",
				Message:       "agent task context missing required fields",
				MissingFields: missing,
				Recoverable:   false,
				Detail:        errDetail,
			}
		}
	}
	return ctx, nil
}

func applyResolvedContextToTask(task model.Task, ctx resolvedAgentTaskContext) model.Task {
	if task.Payload == nil {
		task.Payload = map[string]interface{}{}
	}
	resolvedMap := ctx.toPayloadMap()
	for key, value := range resolvedMap {
		task.Payload[key] = value
	}
	task.Payload["resolved_context"] = mergeObjectMaps(task.Payload["resolved_context"], resolvedMap)
	return task
}

func ensureContextDetail(detail map[string]interface{}, ctx resolvedAgentTaskContext) map[string]interface{} {
	if detail == nil {
		detail = map[string]interface{}{}
	}
	detail["task_context"] = ctx.toPayloadMap()
	return detail
}

func normalizeTaskResultEnvelope(task model.Task, rawDetail map[string]interface{}, success bool, message string, startedAt, finishedAt time.Time) map[string]interface{} {
	detail := cloneInterfaceMap(rawDetail)
	if detail == nil {
		detail = map[string]interface{}{}
	}
	rawSnapshot := cloneInterfaceMap(detail)
	for _, key := range []string{
		"task_type", "overall_status", "message", "detail", "structured_result",
		"started_at", "finished_at", "node_id", "runtime_instance_id", "runtime_binding_id",
		"manifest_summary", "execution_path",
	} {
		delete(rawSnapshot, key)
	}

	overall := strings.TrimSpace(stringValue(detail, "overall_status"))
	if overall == "" {
		if success {
			overall = "ok"
		} else {
			overall = "failed"
		}
	}
	if !success && overall == "ok" {
		overall = "failed"
	}

	structuredResult := mapFromAny(detail["structured_result"])
	if len(structuredResult) == 0 {
		structuredResult = cloneInterfaceMap(rawSnapshot)
	}
	if len(structuredResult) == 0 {
		structuredResult = map[string]interface{}{"success": success}
	}

	nodeID := resolveTaskContextField(task, detail, "node_id")
	runtimeInstanceID := resolveTaskContextField(task, detail, "runtime_instance_id")
	runtimeBindingID := resolveTaskContextField(task, detail, "runtime_binding_id")
	manifestSummary := buildTaskManifestSummary(task, detail)

	detail["task_type"] = string(task.Type)
	detail["overall_status"] = overall
	detail["message"] = firstNonEmpty(strings.TrimSpace(message), strings.TrimSpace(stringValue(detail, "message")))
	detail["detail"] = rawSnapshot
	detail["structured_result"] = structuredResult
	detail["started_at"] = startedAt.UTC().Format(time.RFC3339Nano)
	detail["finished_at"] = finishedAt.UTC().Format(time.RFC3339Nano)
	if nodeID != "" {
		detail["node_id"] = nodeID
	}
	if runtimeInstanceID != "" {
		detail["runtime_instance_id"] = runtimeInstanceID
	}
	if runtimeBindingID != "" {
		detail["runtime_binding_id"] = runtimeBindingID
	}
	if len(manifestSummary) > 0 {
		detail["manifest_summary"] = manifestSummary
	}
	detail["execution_path"] = "agent"
	return detail
}

func resolveTaskContextField(task model.Task, detail map[string]interface{}, key string) string {
	return strings.TrimSpace(firstNonEmpty(
		stringValue(detail, key),
		stringValue(task.Payload, key),
		stringValueFromNestedMap(task.Payload, "resolved_context", key),
	))
}

func buildTaskManifestSummary(task model.Task, detail map[string]interface{}) map[string]interface{} {
	summary := map[string]interface{}{}
	appendString := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			summary[key] = value
		}
	}
	appendString("manifest_id", resolveTaskContextField(task, detail, "manifest_id"))
	appendString("manifest_version", resolveTaskContextField(task, detail, "manifest_version"))
	appendString("runtime_kind", resolveTaskContextField(task, detail, "runtime_kind"))
	appendString("template_type", firstNonEmpty(
		resolveTaskContextField(task, detail, "template_type"),
		resolveTaskContextField(task, detail, "runtime_template_type"),
	))
	appendString("binding_mode", resolveTaskContextField(task, detail, "binding_mode"))

	if value, ok := boolValueFromDetailOrPayload(detail, task.Payload, "command_override_allowed"); ok {
		summary["command_override_allowed"] = value
	}
	if value, ok := boolValueFromDetailOrPayload(detail, task.Payload, "script_mount_allowed"); ok {
		summary["script_mount_allowed"] = value
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

func executeAgentTask(ctx context.Context, client *http.Client, cfg agentConfig, task model.Task) (bool, string, map[string]interface{}, string) {
	resolvedCtx, parseErr := resolveAgentTaskContext(task)
	if parseErr != nil {
		detail := map[string]interface{}{
			"task_type":      string(task.Type),
			"protocol_error": parseErr,
		}
		return false, parseErr.Message, detail, parseErr.Message
	}
	task = applyResolvedContextToTask(task, resolvedCtx)
	finish := func(ok bool, message string, detail map[string]interface{}, errText string) (bool, string, map[string]interface{}, string) {
		return ok, message, ensureContextDetail(detail, resolvedCtx), errText
	}

	switch task.Type {
	case model.TaskTypeAgentRuntimeReadiness:
		endpoint := strings.TrimSpace(stringValue(task.Payload, "endpoint"))
		modelID := strings.TrimSpace(stringValue(task.Payload, "model_id"))
		if endpoint == "" && modelID != "" {
			if resolved, err := fetchModelEndpoint(ctx, client, cfg, modelID); err == nil {
				endpoint = resolved
			}
		}
		healthPath := strings.TrimSpace(stringValue(task.Payload, "health_path"))
		if healthPath == "" {
			healthPath = "/health"
		}
		timeout := time.Duration(intValue(task.Payload, "timeout_seconds", 3)) * time.Second
		ready, detail, err := checkEndpointReadiness(endpoint, healthPath, timeout)
		if err != nil {
			return finish(false, firstNonEmpty(err.Error(), "readiness 检查失败"), detail, err.Error())
		}
		if !ready {
			return finish(false, "runtime 未 ready", detail, "runtime not ready")
		}
		return finish(true, "runtime readiness 检查通过", detail, "")
	case model.TaskTypeAgentPortCheck:
		ok, msg, detail, errText := executePortCheckTask(task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentModelPathCheck:
		ok, msg, detail, errText := executeModelPathCheckTask(task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentResourceSnapshot:
		ok, msg, detail, errText := executeResourceSnapshotTask(ctx, task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentDockerInspect:
		ok, msg, detail, errText := executeDockerInspectTask(ctx, task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentDockerStart:
		ok, msg, detail, errText := executeDockerStartContainerTask(ctx, task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentDockerStop:
		ok, msg, detail, errText := executeDockerStopContainerTask(ctx, task)
		return finish(ok, msg, detail, errText)
	case model.TaskTypeAgentRuntimePrecheck:
		ok, msg, detail, errText := executeRuntimePrecheckTask(ctx, client, cfg, task)
		return finish(ok, msg, detail, errText)
	default:
		return finish(false, "不支持的任务类型", map[string]interface{}{"task_type": string(task.Type)}, "unsupported task type")
	}
}

func fetchModelEndpoint(ctx context.Context, client *http.Client, cfg agentConfig, modelID string) (string, error) {
	var item model.Model
	url := cfg.controllerEndpoint + "/api/v1/models/" + modelID
	if err := doJSON(ctx, client, cfg, http.MethodGet, url, nil, &item); err != nil {
		return "", err
	}
	endpoint := strings.TrimSpace(item.Endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("model endpoint is empty")
	}
	return endpoint, nil
}

func checkEndpointReadiness(endpoint, healthPath string, timeout time.Duration) (bool, map[string]interface{}, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false, nil, fmt.Errorf("endpoint is empty")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	originalEndpoint := endpoint
	endpoint = normalizeAgentAccessibleEndpoint(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil {
		return false, nil, fmt.Errorf("parse endpoint failed: %w", err)
	}
	hostPort := u.Host
	if !strings.Contains(hostPort, ":") {
		if u.Scheme == "https" {
			hostPort += ":443"
		} else {
			hostPort += ":80"
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", hostPort)
	if err != nil {
		return false, map[string]interface{}{"endpoint": endpoint, "tcp_alive": false, "host_port": hostPort}, fmt.Errorf("tcp dial failed: %w", err)
	}
	_ = conn.Close()

	client := &http.Client{Timeout: timeout}
	healthURL := strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(healthPath, "/")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
	if err != nil {
		return false, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, map[string]interface{}{"endpoint": endpoint, "health_url": healthURL, "tcp_alive": true}, fmt.Errorf("health request failed: %w", err)
	}
	defer resp.Body.Close()
	detail := map[string]interface{}{
		"endpoint":    endpoint,
		"health_url":  healthURL,
		"tcp_alive":   true,
		"http_status": resp.StatusCode,
	}
	if endpoint != originalEndpoint {
		detail["endpoint_original"] = originalEndpoint
		detail["endpoint_rewritten"] = true
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300, detail, nil
}

func executePortCheckTask(task model.Task) (bool, string, map[string]interface{}, string) {
	endpoint := strings.TrimSpace(stringValue(task.Payload, "endpoint"))
	if endpoint == "" {
		host := strings.TrimSpace(stringValue(task.Payload, "host"))
		port := strings.TrimSpace(stringValue(task.Payload, "port"))
		if host != "" && port != "" {
			endpoint = host + ":" + port
		}
	}
	timeout := time.Duration(intValue(task.Payload, "timeout_seconds", 3)) * time.Second
	open, detail, err := checkEndpointPort(endpoint, timeout)
	if err != nil {
		return false, firstNonEmpty(err.Error(), "端口检查失败"), detail, err.Error()
	}
	if !open {
		return false, "端口不可达", detail, "port not reachable"
	}
	return true, "端口检查通过", detail, ""
}

func executeModelPathCheckTask(task model.Task) (bool, string, map[string]interface{}, string) {
	modelPath := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "model_path"),
		stringValue(task.Payload, "path"),
	))
	if modelPath == "" {
		protocolErr := model.AgentTaskProtocolError{
			Code:          "agent_task_context_missing_fields",
			Message:       "model_path is empty",
			MissingFields: []string{"model_path"},
			Recoverable:   false,
			Detail: map[string]interface{}{
				"task_type": string(task.Type),
			},
		}
		return false, "缺少 model_path", map[string]interface{}{"protocol_error": protocolErr}, "model_path is empty"
	}
	exists, detail, err := checkModelPathExists(modelPath)
	if err != nil {
		return false, firstNonEmpty(err.Error(), "模型路径检查失败"), detail, err.Error()
	}
	if !exists {
		return false, "模型路径不存在", detail, "model path not found"
	}
	return true, "模型路径检查通过", detail, ""
}

func executeResourceSnapshotTask(ctx context.Context, task model.Task) (bool, string, map[string]interface{}, string) {
	detail := collectResourceSnapshot(ctx, task.Payload)
	detail["task_type"] = string(task.Type)
	detail["resource_snapshot_collected_at"] = time.Now().UTC().Format(time.RFC3339)
	return true, "资源快照采集完成", detail, ""
}

func executeDockerInspectTask(ctx context.Context, task model.Task) (bool, string, map[string]interface{}, string) {
	containerID := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "runtime_container_id"),
		stringValue(task.Payload, "container_id"),
		stringValue(task.Payload, "container"),
	))
	if containerID == "" {
		protocolErr := model.AgentTaskProtocolError{
			Code:          "agent_task_context_missing_fields",
			Message:       "runtime_container_id is empty",
			MissingFields: []string{"runtime_container_id"},
			Recoverable:   false,
			Detail: map[string]interface{}{
				"task_type": string(task.Type),
			},
		}
		return false, "缺少 runtime_container_id", map[string]interface{}{"protocol_error": protocolErr}, "runtime_container_id is empty"
	}
	exists, running, detail, err := inspectDockerContainer(ctx, containerID)
	if err != nil {
		return false, "docker inspect 失败", detail, err.Error()
	}
	if !exists {
		detail["observed_state"] = "stopped"
		return false, "容器不存在", detail, "runtime container not found"
	}
	if running {
		detail["observed_state"] = "running"
	} else {
		detail["observed_state"] = "loaded"
	}
	return true, "docker inspect 完成", detail, ""
}

func executeDockerStartContainerTask(ctx context.Context, task model.Task) (bool, string, map[string]interface{}, string) {
	containerID := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "runtime_container_id"),
		stringValue(task.Payload, "container_id"),
		stringValue(task.Payload, "container"),
	))
	if containerID == "" {
		protocolErr := model.AgentTaskProtocolError{
			Code:          "agent_task_context_missing_fields",
			Message:       "runtime_container_id is empty",
			MissingFields: []string{"runtime_container_id"},
			Recoverable:   false,
			Detail: map[string]interface{}{
				"task_type": string(task.Type),
			},
		}
		return false, "缺少 runtime_container_id", map[string]interface{}{"protocol_error": protocolErr}, "runtime_container_id is empty"
	}

	startDetail, err := startDockerContainer(ctx, containerID)
	if err != nil {
		startDetail["runtime_exists"] = false
		startDetail["runtime_running"] = false
		return false, "docker start 失败", startDetail, err.Error()
	}
	exists, running, inspectDetail, inspectErr := inspectDockerContainer(ctx, containerID)
	for k, v := range inspectDetail {
		startDetail[k] = v
	}
	startDetail["action"] = "docker_start_container"
	if inspectErr != nil {
		return false, "docker start 后探测失败", startDetail, inspectErr.Error()
	}
	startDetail["runtime_exists"] = exists
	startDetail["runtime_running"] = running
	if running {
		startDetail["observed_state"] = "running"
		return true, "docker start 完成", startDetail, ""
	}
	if exists {
		startDetail["observed_state"] = "loaded"
		return false, "docker start 后容器未运行", startDetail, "runtime container not running after start"
	}
	startDetail["observed_state"] = "stopped"
	return false, "docker start 目标容器不存在", startDetail, "runtime container not found after start"
}

func executeDockerStopContainerTask(ctx context.Context, task model.Task) (bool, string, map[string]interface{}, string) {
	containerID := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "runtime_container_id"),
		stringValue(task.Payload, "container_id"),
		stringValue(task.Payload, "container"),
	))
	if containerID == "" {
		protocolErr := model.AgentTaskProtocolError{
			Code:          "agent_task_context_missing_fields",
			Message:       "runtime_container_id is empty",
			MissingFields: []string{"runtime_container_id"},
			Recoverable:   false,
			Detail: map[string]interface{}{
				"task_type": string(task.Type),
			},
		}
		return false, "缺少 runtime_container_id", map[string]interface{}{"protocol_error": protocolErr}, "runtime_container_id is empty"
	}
	timeoutSeconds := intValue(task.Payload, "timeout_seconds", 10)
	stopDetail, err := stopDockerContainer(ctx, containerID, timeoutSeconds)
	if err != nil {
		return false, "docker stop 失败", stopDetail, err.Error()
	}
	exists, running, inspectDetail, inspectErr := inspectDockerContainer(ctx, containerID)
	for k, v := range inspectDetail {
		stopDetail[k] = v
	}
	stopDetail["action"] = "docker_stop_container"
	if inspectErr != nil {
		return false, "docker stop 后探测失败", stopDetail, inspectErr.Error()
	}
	stopDetail["runtime_exists"] = exists
	stopDetail["runtime_running"] = running
	if !exists {
		stopDetail["observed_state"] = "stopped"
		return true, "docker stop 完成（容器不存在，视为已停止）", stopDetail, ""
	}
	if !running {
		stopDetail["observed_state"] = "loaded"
		return true, "docker stop 完成", stopDetail, ""
	}
	stopDetail["observed_state"] = "running"
	return false, "docker stop 后容器仍在运行", stopDetail, "runtime container still running after stop"
}

func executeRuntimePrecheckTask(ctx context.Context, client *http.Client, cfg agentConfig, task model.Task) (bool, string, map[string]interface{}, string) {
	resolvedCtx, _ := resolveAgentTaskContext(task)
	startedAt := time.Now().UTC()
	detail := map[string]interface{}{
		"task_type":           string(task.Type),
		"execution_path":      "agent",
		"precheck_target":     "runtime",
		"runtime_instance_id": strings.TrimSpace(resolvedCtx.RuntimeInstanceID),
		"runtime_binding_id":  strings.TrimSpace(resolvedCtx.RuntimeBindingID),
		"runtime_template_id": strings.TrimSpace(resolvedCtx.RuntimeTemplateID),
		"manifest_id":         strings.TrimSpace(resolvedCtx.ManifestID),
		"binding_mode":        strings.TrimSpace(resolvedCtx.BindingMode),
		"runtime_kind":        strings.TrimSpace(resolvedCtx.RuntimeKind),
		"template_type":       strings.TrimSpace(resolvedCtx.TemplateType),
		"manifest_version":    strings.TrimSpace(resolvedCtx.ManifestVersion),
	}

	precheck := model.RuntimePrecheckResult{
		OverallStatus: model.PrecheckStatusOK,
		Gating:        false,
		StartedAt:     startedAt,
		ResolvedEnv:   map[string]string{},
		CompatibilityResult: model.RuntimePrecheckCompatibilityResult{
			ModelType:           strings.TrimSpace(resolvedCtx.ModelType),
			ModelFormat:         strings.TrimSpace(resolvedCtx.ModelFormat),
			SupportedModelTypes: normalizeStringSlice(resolvedCtx.SupportedModelType),
			SupportedFormats:    normalizeStringSlice(resolvedCtx.SupportedFormats),
			ModelTypeMatched:    true,
			ModelFormatMatched:  true,
		},
	}
	if resolvedCtx.CommandOverrideOK != nil {
		allowed := *resolvedCtx.CommandOverrideOK
		precheck.CompatibilityResult.CommandOverrideAllowed = &allowed
	}
	if resolvedCtx.ScriptMountOK != nil {
		allowed := *resolvedCtx.ScriptMountOK
		precheck.CompatibilityResult.ScriptMountAllowed = &allowed
	}

	updateStatus := func(status model.PrecheckCheckStatus, blocking bool) {
		switch status {
		case model.PrecheckCheckFailed:
			if blocking {
				precheck.Gating = true
				precheck.OverallStatus = model.PrecheckStatusFailed
			} else if precheck.OverallStatus != model.PrecheckStatusFailed {
				precheck.OverallStatus = model.PrecheckStatusWarning
			}
		case model.PrecheckCheckWarning:
			if precheck.OverallStatus == model.PrecheckStatusOK {
				precheck.OverallStatus = model.PrecheckStatusWarning
			}
		}
	}
	addCheck := func(name string, status model.PrecheckCheckStatus, blocking bool, message string, checkDetail map[string]interface{}) {
		precheck.Checks = append(precheck.Checks, model.RuntimePrecheckCheckResult{
			Name:     strings.TrimSpace(name),
			Status:   status,
			Blocking: blocking,
			Message:  strings.TrimSpace(message),
			Detail:   cloneInterfaceMap(checkDetail),
		})
		updateStatus(status, blocking)
	}
	addReason := func(code model.PrecheckReasonCode, blocking bool, message string, reasonDetail map[string]interface{}) {
		precheck.Reasons = append(precheck.Reasons, model.RuntimePrecheckReason{
			Code:     code,
			Message:  strings.TrimSpace(message),
			Blocking: blocking,
			Detail:   cloneInterfaceMap(reasonDetail),
		})
		if blocking {
			precheck.Gating = true
			precheck.OverallStatus = model.PrecheckStatusFailed
		} else if precheck.OverallStatus == model.PrecheckStatusOK {
			precheck.OverallStatus = model.PrecheckStatusWarning
		}
	}

	modelPathTask := task
	modelPathTask.Payload = cloneInterfaceMap(task.Payload)
	if strings.TrimSpace(stringValue(modelPathTask.Payload, "model_path")) == "" && strings.TrimSpace(resolvedCtx.ModelPath) != "" {
		modelPathTask.Payload["model_path"] = strings.TrimSpace(resolvedCtx.ModelPath)
	}
	pathOK, _, pathDetail, pathErrText := executeModelPathCheckTask(modelPathTask)
	detail["model_path_check"] = cloneInterfaceMap(pathDetail)
	if pathOK {
		addCheck("model_path_exists", model.PrecheckCheckPass, true, "model path exists", pathDetail)
		if absPath := strings.TrimSpace(stringValue(pathDetail, "abs_path")); absPath != "" {
			precheck.ResolvedMounts = appendUniqueNormalized(precheck.ResolvedMounts, absPath)
		}
	} else {
		addCheck("model_path_exists", model.PrecheckCheckFailed, true, firstNonEmpty(pathErrText, "model path missing"), pathDetail)
		addReason(model.PrecheckReasonModelPathMissing, true, firstNonEmpty(pathErrText, "model path missing"), pathDetail)
	}

	bindingMountRules := normalizeStringSlice(resolvedCtx.BindingMountRules)
	if len(bindingMountRules) == 0 {
		bindingMountRules = normalizeStringSlice(stringSliceValue(task.Payload, "binding_mount_rules"))
	}
	manifestMounts := normalizeStringSlice(resolvedCtx.MountPoints)
	if len(manifestMounts) == 0 {
		manifestMounts = normalizeStringSlice(stringSliceValue(task.Payload, "mount_points"))
	}
	mountDetail := map[string]interface{}{
		"binding_mount_rules": bindingMountRules,
		"manifest_mounts":     manifestMounts,
	}
	missingMountHosts := make([]string, 0)
	invalidMountRules := make([]string, 0)
	for _, rule := range bindingMountRules {
		hostPath, containerPath := parseMountRule(rule)
		precheck.ResolvedMounts = appendUniqueNormalized(precheck.ResolvedMounts, rule)
		if hostPath != "" {
			if _, err := os.Stat(hostPath); err != nil {
				if os.IsNotExist(err) {
					missingMountHosts = append(missingMountHosts, hostPath)
				} else {
					missingMountHosts = append(missingMountHosts, hostPath+" ("+err.Error()+")")
				}
			}
		}
		if len(manifestMounts) > 0 && containerPath != "" && !isPathAllowedByMountPoints(containerPath, manifestMounts) {
			invalidMountRules = append(invalidMountRules, rule)
		}
	}
	if len(bindingMountRules) == 0 {
		if len(manifestMounts) > 0 {
			addCheck("binding_mount_rules", model.PrecheckCheckWarning, false, "binding mount rules empty; using template/manifest defaults", mountDetail)
		} else {
			addCheck("binding_mount_rules", model.PrecheckCheckPass, false, "no mount rules required", mountDetail)
		}
	} else if len(missingMountHosts) == 0 && len(invalidMountRules) == 0 {
		addCheck("binding_mount_rules", model.PrecheckCheckPass, true, "binding mount rules verified", mountDetail)
	} else {
		mountDetail["missing_host_paths"] = missingMountHosts
		mountDetail["invalid_mount_rules"] = invalidMountRules
		addCheck("binding_mount_rules", model.PrecheckCheckFailed, true, "binding mount rules invalid", mountDetail)
		addReason(model.PrecheckReasonBindingInvalid, true, "binding mount rules invalid", mountDetail)
	}

	requiredEnv := normalizeStringSlice(resolvedCtx.RequiredEnv)
	if len(requiredEnv) == 0 {
		requiredEnv = normalizeStringSlice(stringSliceValue(task.Payload, "required_env"))
	}
	optionalEnv := normalizeStringSlice(resolvedCtx.OptionalEnv)
	if len(optionalEnv) == 0 {
		optionalEnv = normalizeStringSlice(stringSliceValue(task.Payload, "optional_env"))
	}
	envOverride := firstNonEmptyStringMap(
		resolvedCtx.BindingEnvOverride,
		stringMapFromValue(task.Payload["binding_env_overrides"]),
		stringMapFromValue(stringValueMapFromNested(task.Payload, "resolved_context", "binding_env_overrides")),
	)
	missingRequiredEnv := make([]string, 0)
	for _, key := range requiredEnv {
		if value := strings.TrimSpace(envOverride[key]); value != "" {
			precheck.ResolvedEnv[key] = value
			continue
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			precheck.ResolvedEnv[key] = value
			continue
		}
		missingRequiredEnv = append(missingRequiredEnv, key)
	}
	for _, key := range optionalEnv {
		if value := strings.TrimSpace(envOverride[key]); value != "" {
			precheck.ResolvedEnv[key] = value
			continue
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			precheck.ResolvedEnv[key] = value
		}
	}
	envDetail := map[string]interface{}{
		"required_env":          requiredEnv,
		"optional_env":          optionalEnv,
		"binding_env_overrides": envOverride,
		"missing_required_env":  missingRequiredEnv,
	}
	if len(missingRequiredEnv) > 0 {
		addCheck("required_env", model.PrecheckCheckFailed, true, "required env missing", envDetail)
		addReason(model.PrecheckReasonRequiredEnvMissing, true, "required env missing", envDetail)
	} else {
		addCheck("required_env", model.PrecheckCheckPass, true, "required env satisfied", envDetail)
	}

	scriptRef := strings.TrimSpace(firstNonEmpty(resolvedCtx.ScriptRef, stringValue(task.Payload, "script_ref")))
	scriptRequired := strings.EqualFold(strings.TrimSpace(resolvedCtx.BindingMode), string(model.RuntimeBindingModeGenericWithScript)) || scriptRef != ""
	scriptDetail := map[string]interface{}{
		"binding_mode":    strings.TrimSpace(resolvedCtx.BindingMode),
		"script_ref":      scriptRef,
		"script_required": scriptRequired,
	}
	if scriptRequired {
		if scriptRef == "" {
			addCheck("script_ref", model.PrecheckCheckFailed, true, "script_ref is required", scriptDetail)
			addReason(model.PrecheckReasonScriptMissing, true, "script_ref is required", scriptDetail)
		} else if info, err := os.Stat(scriptRef); err != nil {
			scriptDetail["error"] = err.Error()
			addCheck("script_ref", model.PrecheckCheckFailed, true, "script file missing", scriptDetail)
			addReason(model.PrecheckReasonScriptMissing, true, "script file missing", scriptDetail)
		} else {
			scriptDetail["size"] = info.Size()
			precheck.ResolvedScript = scriptRef
			addCheck("script_ref", model.PrecheckCheckPass, true, "script file exists", scriptDetail)
		}
	} else {
		addCheck("script_ref", model.PrecheckCheckSkipped, false, "script not required", scriptDetail)
	}

	commandOverride := normalizeStringSlice(resolvedCtx.CommandOverride)
	if len(commandOverride) == 0 {
		commandOverride = normalizeStringSlice(stringSliceValue(task.Payload, "command_override"))
	}
	commandOverrideAllowed := true
	hasCommandOverrideAllowed := false
	if resolvedCtx.CommandOverrideOK != nil {
		commandOverrideAllowed = *resolvedCtx.CommandOverrideOK
		hasCommandOverrideAllowed = true
	} else if value, ok := boolValueFromPayload(task.Payload, "command_override_allowed"); ok {
		commandOverrideAllowed = value
		hasCommandOverrideAllowed = true
	}
	commandOverrideDetail := map[string]interface{}{
		"command_override":         commandOverride,
		"command_override_allowed": commandOverrideAllowed,
	}
	if hasCommandOverrideAllowed && !commandOverrideAllowed && len(commandOverride) > 0 {
		addCheck("command_override_policy", model.PrecheckCheckFailed, true, "command override not allowed by manifest", commandOverrideDetail)
		addReason(model.PrecheckReasonCommandOverrideNotAllowed, true, "command override not allowed by manifest", commandOverrideDetail)
	} else {
		addCheck("command_override_policy", model.PrecheckCheckPass, true, "command override policy satisfied", commandOverrideDetail)
	}

	scriptMountAllowed := true
	hasScriptMountAllowed := false
	if resolvedCtx.ScriptMountOK != nil {
		scriptMountAllowed = *resolvedCtx.ScriptMountOK
		hasScriptMountAllowed = true
	} else if value, ok := boolValueFromPayload(task.Payload, "script_mount_allowed"); ok {
		scriptMountAllowed = value
		hasScriptMountAllowed = true
	}
	scriptMountDetail := map[string]interface{}{
		"script_mount_allowed": scriptMountAllowed,
		"script_required":      scriptRequired,
		"script_ref":           scriptRef,
	}
	if hasScriptMountAllowed && !scriptMountAllowed && scriptRequired {
		addCheck("script_mount_policy", model.PrecheckCheckFailed, true, "script mount not allowed by manifest", scriptMountDetail)
		addReason(model.PrecheckReasonScriptMountNotAllowed, true, "script mount not allowed by manifest", scriptMountDetail)
	} else {
		addCheck("script_mount_policy", model.PrecheckCheckPass, true, "script mount policy satisfied", scriptMountDetail)
	}

	modelType := strings.TrimSpace(resolvedCtx.ModelType)
	modelFormat := strings.TrimSpace(resolvedCtx.ModelFormat)
	supportedModelTypes := normalizeStringSlice(resolvedCtx.SupportedModelType)
	supportedFormats := normalizeStringSlice(resolvedCtx.SupportedFormats)
	precheck.CompatibilityResult.ModelType = modelType
	precheck.CompatibilityResult.ModelFormat = modelFormat
	precheck.CompatibilityResult.SupportedModelTypes = supportedModelTypes
	precheck.CompatibilityResult.SupportedFormats = supportedFormats

	typeCompatible := true
	formatCompatible := true
	if len(supportedModelTypes) > 0 {
		typeCompatible = containsFolded(supportedModelTypes, modelType) || containsFolded(supportedModelTypes, string(model.ModelKindUnknown))
		if modelType == "" {
			typeCompatible = false
		}
	}
	if len(supportedFormats) > 0 {
		formatCompatible = containsFolded(supportedFormats, modelFormat) || containsFolded(supportedFormats, string(model.ModelFormatUnknown))
		if modelFormat == "" {
			formatCompatible = false
		}
	}
	precheck.CompatibilityResult.ModelTypeMatched = typeCompatible
	precheck.CompatibilityResult.ModelFormatMatched = formatCompatible
	compatDetail := map[string]interface{}{
		"model_type":            modelType,
		"model_format":          modelFormat,
		"supported_model_types": supportedModelTypes,
		"supported_formats":     supportedFormats,
	}
	if !typeCompatible {
		addCheck("model_type_compatibility", model.PrecheckCheckFailed, true, "model type incompatible with manifest", compatDetail)
		addReason(model.PrecheckReasonModelTypeMismatch, true, "model type incompatible with manifest", compatDetail)
	} else {
		addCheck("model_type_compatibility", model.PrecheckCheckPass, true, "model type compatible", compatDetail)
	}
	if !formatCompatible {
		addCheck("model_format_compatibility", model.PrecheckCheckFailed, true, "model format incompatible with manifest", compatDetail)
		addReason(model.PrecheckReasonModelFormatMismatch, true, "model format incompatible with manifest", compatDetail)
	} else {
		addCheck("model_format_compatibility", model.PrecheckCheckPass, true, "model format compatible", compatDetail)
	}

	containerID := strings.TrimSpace(firstNonEmpty(
		resolvedCtx.RuntimeContainerID,
		stringValue(task.Payload, "runtime_container_id"),
		stringValue(task.Payload, "container_id"),
		stringValue(task.Payload, "container"),
	))
	runtimeRunning := false
	if containerID != "" {
		exists, running, inspectDetail, err := inspectDockerContainer(ctx, containerID)
		detail["docker_inspect"] = cloneInterfaceMap(inspectDetail)
		detail["runtime_exists"] = exists
		detail["runtime_running"] = running
		runtimeRunning = exists && running
		if err != nil {
			addCheck("runtime_container_inspect", model.PrecheckCheckWarning, false, "docker inspect failed", inspectDetail)
		} else if exists && running {
			addCheck("runtime_container_inspect", model.PrecheckCheckPass, false, "runtime container is running", inspectDetail)
		} else if exists {
			addCheck("runtime_container_inspect", model.PrecheckCheckWarning, false, "runtime container exists but not running", inspectDetail)
		} else {
			addCheck("runtime_container_inspect", model.PrecheckCheckWarning, false, "runtime container missing", inspectDetail)
		}
	}

	endpoint := strings.TrimSpace(firstNonEmpty(resolvedCtx.Endpoint, stringValue(task.Payload, "endpoint")))
	modelID := strings.TrimSpace(firstNonEmpty(resolvedCtx.ModelID, stringValue(task.Payload, "model_id")))
	if endpoint == "" && modelID != "" {
		if resolved, err := fetchModelEndpoint(ctx, client, cfg, modelID); err == nil {
			endpoint = strings.TrimSpace(resolved)
		}
	}
	if endpoint != "" {
		portTask := task
		portTask.Payload = cloneInterfaceMap(task.Payload)
		portTask.Payload["endpoint"] = endpoint
		portOpen, _, portDetail, portErrText := executePortCheckTask(portTask)
		detail["port_check"] = cloneInterfaceMap(portDetail)
		if portOpen {
			addCheck("runtime_endpoint_port_check", model.PrecheckCheckPass, false, "runtime endpoint tcp reachable", portDetail)
		} else {
			addCheck("runtime_endpoint_port_check", model.PrecheckCheckWarning, false, firstNonEmpty(portErrText, "runtime endpoint tcp unreachable"), portDetail)
		}
	}

	exposedPorts := normalizeStringSlice(resolvedCtx.ExposedPorts)
	if len(exposedPorts) == 0 {
		exposedPorts = normalizeStringSlice(stringSliceValue(task.Payload, "exposed_ports"))
	}
	precheck.ResolvedPorts = normalizePortCandidates(resolveHostPorts(exposedPorts, endpoint))
	conflictingPorts := make([]string, 0)
	inUseByRuntime := make([]string, 0)
	for _, hostPort := range precheck.ResolvedPorts {
		inUse, checkDetail := checkLocalPortInUse(hostPort)
		if !inUse {
			continue
		}
		if runtimeRunning && endpointHostPort(endpoint) == hostPort {
			inUseByRuntime = append(inUseByRuntime, hostPort)
			continue
		}
		conflictingPorts = append(conflictingPorts, hostPort)
		_ = checkDetail
	}
	portConflictDetail := map[string]interface{}{
		"manifest_exposed_ports": exposedPorts,
		"resolved_host_ports":    precheck.ResolvedPorts,
		"conflicting_ports":      conflictingPorts,
		"in_use_by_runtime":      inUseByRuntime,
	}
	if len(conflictingPorts) > 0 {
		addCheck("manifest_port_conflicts", model.PrecheckCheckFailed, true, "manifest exposed ports conflict", portConflictDetail)
		addReason(model.PrecheckReasonPortConflict, true, "manifest exposed ports conflict", portConflictDetail)
	} else {
		addCheck("manifest_port_conflicts", model.PrecheckCheckPass, true, "manifest exposed ports available", portConflictDetail)
	}

	if strings.EqualFold(strings.TrimSpace(resolvedCtx.BindingMode), string(model.RuntimeBindingModeCustomBundle)) {
		customBundleDetail := map[string]interface{}{
			"binding_mode":     resolvedCtx.BindingMode,
			"manifest_id":      resolvedCtx.ManifestID,
			"manifest_version": resolvedCtx.ManifestVersion,
			"runtime_kind":     resolvedCtx.RuntimeKind,
			"template_type":    resolvedCtx.TemplateType,
			"required_env":     requiredEnv,
			"manifest_mounts":  manifestMounts,
			"manifest_ports":   exposedPorts,
			"command_override": commandOverride,
			"script_ref":       scriptRef,
		}
		missingManifestFields := make([]string, 0, 4)
		if strings.TrimSpace(resolvedCtx.ManifestID) == "" {
			missingManifestFields = append(missingManifestFields, "manifest_id")
		}
		if strings.TrimSpace(resolvedCtx.ManifestVersion) == "" {
			missingManifestFields = append(missingManifestFields, "manifest_version")
		}
		if strings.TrimSpace(resolvedCtx.RuntimeKind) == "" {
			missingManifestFields = append(missingManifestFields, "runtime_kind")
		}
		if strings.TrimSpace(resolvedCtx.TemplateType) == "" {
			missingManifestFields = append(missingManifestFields, "template_type")
		}
		if len(missingManifestFields) > 0 {
			customBundleDetail["missing_fields"] = missingManifestFields
			addCheck("custom_bundle_manifest_minimal", model.PrecheckCheckFailed, true, "custom bundle manifest is invalid", customBundleDetail)
			addReason(model.PrecheckReasonManifestInvalid, true, "custom bundle manifest is invalid", customBundleDetail)
		} else {
			addCheck("custom_bundle_manifest_minimal", model.PrecheckCheckPass, true, "custom bundle manifest minimal validation passed", customBundleDetail)
		}
	} else {
		addCheck("custom_bundle_manifest_minimal", model.PrecheckCheckSkipped, false, "binding mode is not custom_bundle", map[string]interface{}{"binding_mode": resolvedCtx.BindingMode})
	}

	resourceSnapshot := collectResourceSnapshot(ctx, task.Payload)
	detail["resource_snapshot"] = resourceSnapshot
	detail["resource_snapshot_collected_at"] = time.Now().UTC().Format(time.RFC3339)

	if running, ok := detail["runtime_running"].(bool); ok {
		if running {
			detail["observed_state"] = "running"
		} else {
			detail["observed_state"] = "loaded"
		}
	} else if exists, ok := detail["runtime_exists"].(bool); ok && !exists {
		detail["observed_state"] = "stopped"
	}

	if precheck.Gating {
		precheck.OverallStatus = model.PrecheckStatusFailed
	}
	precheck.FinishedAt = time.Now().UTC()
	precheckMap := mustMap(precheck)
	detail["precheck_result"] = precheckMap
	detail["structured_result"] = precheckMap
	detail["overall_status"] = string(precheck.OverallStatus)
	detail["gating"] = precheck.Gating
	detail["reasons"] = precheckMap["reasons"]
	detail["checks"] = precheckMap["checks"]
	detail["resolved_mounts"] = precheck.ResolvedMounts
	detail["resolved_env"] = precheck.ResolvedEnv
	detail["resolved_script"] = precheck.ResolvedScript
	detail["resolved_ports"] = precheck.ResolvedPorts
	detail["compatibility_result"] = precheckMap["compatibility_result"]
	detail["runtime_ready"] = !precheck.Gating
	detail["precheck_failures"] = extractPrecheckFailureCodes(precheck.Reasons)

	if precheck.Gating {
		msg := firstNonEmpty(buildPrecheckFailureMessage(precheck.Reasons), "runtime precheck failed")
		return false, msg, detail, msg
	}
	if precheck.OverallStatus == model.PrecheckStatusWarning {
		return true, "runtime precheck completed with warnings", detail, ""
	}
	return true, "runtime precheck passed", detail, ""
}

func parseMountRule(rule string) (hostPath string, containerPath string) {
	trimmed := strings.TrimSpace(rule)
	if trimmed == "" {
		return "", ""
	}
	if idx := strings.Index(trimmed, ":"); idx < 0 {
		return trimmed, ""
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 2 {
		return strings.TrimSpace(parts[0]), ""
	}
	hostPath = strings.TrimSpace(parts[0])
	containerPath = strings.TrimSpace(parts[1])
	return hostPath, containerPath
}

func isPathAllowedByMountPoints(containerPath string, mountPoints []string) bool {
	containerPath = strings.TrimSpace(containerPath)
	if containerPath == "" {
		return true
	}
	for _, mountPoint := range mountPoints {
		candidate := strings.TrimSpace(mountPoint)
		if candidate == "" {
			continue
		}
		if containerPath == candidate || strings.HasPrefix(containerPath, strings.TrimRight(candidate, "/")+"/") {
			return true
		}
	}
	return false
}

func resolveHostPorts(exposedPorts []string, endpoint string) []string {
	out := make([]string, 0, len(exposedPorts)+1)
	for _, raw := range exposedPorts {
		if port := extractHostPort(raw); port != "" {
			out = append(out, port)
		}
	}
	if endpointPort := endpointHostPort(endpoint); endpointPort != "" {
		out = append(out, endpointPort)
	}
	return out
}

func extractHostPort(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "/"); idx > 0 {
		value = value[:idx]
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.TrimSpace(parsed.Port())
		}
	}
	parts := strings.Split(value, ":")
	switch len(parts) {
	case 1:
		if isDigits(parts[0]) {
			return strings.TrimSpace(parts[0])
		}
	case 2:
		if isDigits(strings.TrimSpace(parts[0])) {
			return strings.TrimSpace(parts[0])
		}
		if isDigits(strings.TrimSpace(parts[1])) {
			return strings.TrimSpace(parts[1])
		}
	default:
		if isDigits(strings.TrimSpace(parts[1])) {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func endpointHostPort(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	if parsed, err := url.Parse(endpoint); err == nil {
		port := strings.TrimSpace(parsed.Port())
		if port != "" {
			return port
		}
		if strings.EqualFold(parsed.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return ""
}

func checkLocalPortInUse(port string) (bool, map[string]interface{}) {
	port = strings.TrimSpace(port)
	detail := map[string]interface{}{"host_port": port}
	if port == "" || !isDigits(port) {
		detail["valid"] = false
		return false, detail
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		detail["in_use"] = true
		detail["error"] = err.Error()
		return true, detail
	}
	_ = ln.Close()
	detail["in_use"] = false
	return false, detail
}

func extractPrecheckFailureCodes(reasons []model.RuntimePrecheckReason) []string {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if !reason.Blocking {
			continue
		}
		code := strings.TrimSpace(string(reason.Code))
		if code == "" {
			continue
		}
		out = appendUniqueNormalized(out, code)
	}
	return out
}

func buildPrecheckFailureMessage(reasons []model.RuntimePrecheckReason) string {
	codes := extractPrecheckFailureCodes(reasons)
	if len(codes) == 0 {
		return ""
	}
	return "runtime precheck failed: " + strings.Join(codes, ",")
}

func checkEndpointPort(endpoint string, timeout time.Duration) (bool, map[string]interface{}, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false, nil, fmt.Errorf("endpoint is empty")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	originalEndpoint := endpoint
	endpoint = normalizeAgentAccessibleEndpoint(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil {
		return false, nil, fmt.Errorf("parse endpoint failed: %w", err)
	}
	hostPort := u.Host
	if !strings.Contains(hostPort, ":") {
		if u.Scheme == "https" {
			hostPort += ":443"
		} else {
			hostPort += ":80"
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", hostPort)
	if err != nil {
		detail := map[string]interface{}{"endpoint": endpoint, "host_port": hostPort, "tcp_alive": false}
		if endpoint != originalEndpoint {
			detail["endpoint_original"] = originalEndpoint
			detail["endpoint_rewritten"] = true
		}
		return false, detail, err
	}
	_ = conn.Close()
	detail := map[string]interface{}{"endpoint": endpoint, "host_port": hostPort, "tcp_alive": true}
	if endpoint != originalEndpoint {
		detail["endpoint_original"] = originalEndpoint
		detail["endpoint_rewritten"] = true
	}
	return true, detail, nil
}

func checkModelPathExists(path string) (bool, map[string]interface{}, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil, fmt.Errorf("model_path is empty")
	}
	absPath, _ := filepath.Abs(path)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, map[string]interface{}{
				"model_path": path,
				"abs_path":   absPath,
				"exists":     false,
			}, nil
		}
		return false, map[string]interface{}{"model_path": path, "abs_path": absPath, "exists": false}, err
	}
	return true, map[string]interface{}{
		"model_path": path,
		"abs_path":   absPath,
		"exists":     true,
		"is_dir":     info.IsDir(),
		"size":       info.Size(),
	}, nil
}

func collectResourceSnapshot(ctx context.Context, payload map[string]interface{}) map[string]interface{} {
	hostname, _ := os.Hostname()
	now := time.Now().UTC()
	snapshot := map[string]interface{}{
		"hostname":      hostname,
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"cpu_count":     runtime.NumCPU(),
		"goroutine_num": runtime.NumGoroutine(),
		"collected_at":  now.Format(time.RFC3339),
	}
	if memTotalKB, memAvailKB, ok := parseMemInfo(); ok {
		snapshot["mem_total_kb"] = memTotalKB
		snapshot["mem_available_kb"] = memAvailKB
	}
	diskPath := resolveSnapshotDiskPath(payload)
	if disk := statDiskPath(diskPath); len(disk) > 0 {
		snapshot["disk"] = disk
	}
	snapshot["model_paths"] = collectModelPathSummary(payload)
	snapshot["docker_access"] = collectDockerAccessSummary(ctx)
	snapshot["runtime_dependencies"] = collectRuntimeDependencySummary()
	return snapshot
}

func resolveSnapshotDiskPath(payload map[string]interface{}) string {
	candidates := []string{
		strings.TrimSpace(stringValue(payload, "model_path")),
		strings.TrimSpace(stringValue(payload, "path")),
		strings.TrimSpace(os.Getenv("AGENT_MODEL_ROOT_DIR")),
		"/opt/controller/models",
		".",
	}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "."
}

func statDiskPath(path string) map[string]interface{} {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	absPath, _ := filepath.Abs(path)
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return map[string]interface{}{
			"path":     absPath,
			"ok":       false,
			"error":    err.Error(),
			"platform": runtime.GOOS,
		}
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	free := stat.Bfree * blockSize
	avail := stat.Bavail * blockSize
	return map[string]interface{}{
		"path":            absPath,
		"ok":              true,
		"total_bytes":     total,
		"free_bytes":      free,
		"available_bytes": avail,
	}
}

func collectModelPathSummary(payload map[string]interface{}) map[string]interface{} {
	paths := []string{
		strings.TrimSpace(stringValue(payload, "model_path")),
		strings.TrimSpace(stringValue(payload, "path")),
		strings.TrimSpace(os.Getenv("AGENT_MODEL_ROOT_DIR")),
		"/opt/controller/models",
		"./resources/models",
	}
	seen := map[string]struct{}{}
	items := make([]map[string]interface{}, 0, len(paths))
	existsCount := 0
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		absPath, _ := filepath.Abs(path)
		entry := map[string]interface{}{
			"path":     path,
			"abs_path": absPath,
			"exists":   false,
		}
		info, err := os.Stat(path)
		if err == nil {
			entry["exists"] = true
			entry["is_dir"] = info.IsDir()
			entry["size"] = info.Size()
			existsCount++
		} else if !os.IsNotExist(err) {
			entry["error"] = err.Error()
		}
		items = append(items, entry)
	}
	return map[string]interface{}{
		"paths":         items,
		"exists_count":  existsCount,
		"checked_count": len(items),
	}
}

func collectDockerAccessSummary(ctx context.Context) map[string]interface{} {
	endpoint := strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_DOCKER_ENDPOINT"),
		os.Getenv("MCP_DOCKER_ENDPOINT"),
		"unix:///var/run/docker.sock",
	))
	out := map[string]interface{}{
		"endpoint": endpoint,
	}
	if _, err := exec.LookPath("docker"); err == nil {
		out["cli_available"] = true
	} else {
		out["cli_available"] = false
		out["cli_error"] = err.Error()
	}
	client, baseURL, err := newDockerInspectHTTPClient(endpoint, 3*time.Second)
	if err != nil {
		out["api_reachable"] = false
		out["api_error"] = err.Error()
		return out
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/_ping", nil)
	if err != nil {
		out["api_reachable"] = false
		out["api_error"] = err.Error()
		return out
	}
	resp, err := client.Do(req)
	if err != nil {
		out["api_reachable"] = false
		out["api_error"] = err.Error()
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
	out["api_http_status"] = resp.StatusCode
	out["api_ping"] = strings.TrimSpace(string(body))
	out["api_reachable"] = resp.StatusCode >= 200 && resp.StatusCode < 300
	return out
}

func collectRuntimeDependencySummary() map[string]interface{} {
	llmfitBinary := strings.TrimSpace(firstNonEmpty(os.Getenv("AGENT_LLMFIT_BINARY"), "llmfit"))
	_, llmfitErr := exec.LookPath(llmfitBinary)
	_, dockerErr := exec.LookPath("docker")
	return map[string]interface{}{
		"docker_cli_available": dockerErr == nil,
		"llmfit_binary":        llmfitBinary,
		"llmfit_available":     llmfitErr == nil,
	}
}

func parseMemInfo() (int64, int64, bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var total, available int64
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "MemTotal:") {
			_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &total)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &available)
		}
	}
	if total <= 0 {
		return 0, 0, false
	}
	return total, available, true
}

func inspectDockerContainer(ctx context.Context, containerID string) (bool, bool, map[string]interface{}, error) {
	detail := map[string]interface{}{
		"runtime_container_id": containerID,
	}
	cmd := exec.CommandContext(ctx, "docker", "inspect", containerID)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
			exists, running, apiDetail, apiErr := inspectDockerContainerViaAPI(ctx, containerID)
			for k, v := range apiDetail {
				detail[k] = v
			}
			detail["inspect_transport"] = "docker_api_fallback"
			if apiErr != nil {
				detail["runtime_exists"] = false
				detail["runtime_running"] = false
				detail["error"] = apiErr.Error()
				return false, false, detail, fmt.Errorf("docker inspect via api failed: %w", apiErr)
			}
			return exists, running, detail, nil
		}
		message := strings.ToLower(strings.TrimSpace(string(raw)))
		if strings.Contains(message, "no such object") || strings.Contains(message, "not found") {
			detail["runtime_exists"] = false
			detail["runtime_running"] = false
			return false, false, detail, nil
		}
		detail["runtime_exists"] = false
		detail["runtime_running"] = false
		detail["error"] = strings.TrimSpace(string(raw))
		if detail["error"] == "" {
			detail["error"] = err.Error()
		}
		return false, false, detail, fmt.Errorf("docker inspect failed: %w", err)
	}
	var parsed []map[string]interface{}
	if unmarshalErr := json.Unmarshal(raw, &parsed); unmarshalErr != nil {
		return false, false, detail, fmt.Errorf("parse docker inspect output failed: %w", unmarshalErr)
	}
	if len(parsed) == 0 {
		detail["runtime_exists"] = false
		detail["runtime_running"] = false
		return false, false, detail, nil
	}
	entry := parsed[0]
	stateRaw, _ := entry["State"].(map[string]interface{})
	running, _ := stateRaw["Running"].(bool)
	status := strings.TrimSpace(fmt.Sprint(stateRaw["Status"]))
	image := ""
	if configRaw, ok := entry["Config"].(map[string]interface{}); ok {
		image = strings.TrimSpace(fmt.Sprint(configRaw["Image"]))
	}
	name := strings.TrimPrefix(strings.TrimSpace(fmt.Sprint(entry["Name"])), "/")

	detail["runtime_exists"] = true
	detail["runtime_running"] = running
	detail["runtime_status"] = status
	detail["inspect_transport"] = "docker_cli"
	if name != "" {
		detail["runtime_container"] = name
	}
	if image != "" {
		detail["runtime_image"] = image
	}
	return true, running, detail, nil
}

func inspectDockerContainerViaAPI(ctx context.Context, containerID string) (bool, bool, map[string]interface{}, error) {
	endpoint := strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_DOCKER_ENDPOINT"),
		os.Getenv("MCP_DOCKER_ENDPOINT"),
		"unix:///var/run/docker.sock",
	))
	detail := map[string]interface{}{
		"runtime_container_id": containerID,
		"docker_endpoint":      endpoint,
	}
	client, baseURL, err := newDockerInspectHTTPClient(endpoint, 5*time.Second)
	if err != nil {
		return false, false, detail, err
	}

	reqURL := strings.TrimRight(baseURL, "/") + "/containers/" + url.PathEscape(containerID) + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, false, detail, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, false, detail, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode == http.StatusNotFound {
		detail["runtime_exists"] = false
		detail["runtime_running"] = false
		return false, false, detail, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, detail, fmt.Errorf("docker api inspect failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Name   string `json:"Name"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
		State struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
		} `json:"State"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, false, detail, fmt.Errorf("parse docker api inspect output failed: %w", err)
	}

	detail["runtime_exists"] = true
	detail["runtime_running"] = parsed.State.Running
	detail["runtime_status"] = strings.TrimSpace(parsed.State.Status)
	if name := strings.TrimPrefix(strings.TrimSpace(parsed.Name), "/"); name != "" {
		detail["runtime_container"] = name
	}
	if image := strings.TrimSpace(parsed.Config.Image); image != "" {
		detail["runtime_image"] = image
	}
	return true, parsed.State.Running, detail, nil
}

func startDockerContainer(ctx context.Context, containerID string) (map[string]interface{}, error) {
	detail := map[string]interface{}{
		"runtime_container_id": containerID,
	}
	cmd := exec.CommandContext(ctx, "docker", "start", containerID)
	raw, err := cmd.CombinedOutput()
	if err == nil {
		detail["action_transport"] = "docker_cli"
		detail["action_output"] = strings.TrimSpace(string(raw))
		return detail, nil
	}
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
		apiDetail, apiErr := startDockerContainerViaAPI(ctx, containerID)
		for k, v := range apiDetail {
			detail[k] = v
		}
		detail["action_transport"] = "docker_api_fallback"
		return detail, apiErr
	}
	message := strings.ToLower(strings.TrimSpace(string(raw)))
	if strings.Contains(message, "no such container") || strings.Contains(message, "not found") {
		detail["runtime_exists"] = false
		return detail, fmt.Errorf("container not found")
	}
	detail["action_transport"] = "docker_cli"
	detail["error"] = strings.TrimSpace(string(raw))
	if detail["error"] == "" {
		detail["error"] = err.Error()
	}
	return detail, fmt.Errorf("docker start failed: %w", err)
}

func stopDockerContainer(ctx context.Context, containerID string, timeoutSeconds int) (map[string]interface{}, error) {
	detail := map[string]interface{}{
		"runtime_container_id": containerID,
		"timeout_seconds":      timeoutSeconds,
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", strconv.Itoa(timeoutSeconds), containerID)
	raw, err := cmd.CombinedOutput()
	if err == nil {
		detail["action_transport"] = "docker_cli"
		detail["action_output"] = strings.TrimSpace(string(raw))
		return detail, nil
	}
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
		apiDetail, apiErr := stopDockerContainerViaAPI(ctx, containerID, timeoutSeconds)
		for k, v := range apiDetail {
			detail[k] = v
		}
		detail["action_transport"] = "docker_api_fallback"
		return detail, apiErr
	}
	message := strings.ToLower(strings.TrimSpace(string(raw)))
	if strings.Contains(message, "no such container") || strings.Contains(message, "not found") {
		detail["runtime_exists"] = false
		return detail, nil
	}
	detail["action_transport"] = "docker_cli"
	detail["error"] = strings.TrimSpace(string(raw))
	if detail["error"] == "" {
		detail["error"] = err.Error()
	}
	return detail, fmt.Errorf("docker stop failed: %w", err)
}

func startDockerContainerViaAPI(ctx context.Context, containerID string) (map[string]interface{}, error) {
	return callDockerContainerActionViaAPI(ctx, containerID, http.MethodPost, "/start", "", 5*time.Second)
}

func stopDockerContainerViaAPI(ctx context.Context, containerID string, timeoutSeconds int) (map[string]interface{}, error) {
	query := ""
	if timeoutSeconds > 0 {
		query = "?t=" + strconv.Itoa(timeoutSeconds)
	}
	timeout := timeoutSeconds
	if timeout < 5 {
		timeout = 5
	}
	return callDockerContainerActionViaAPI(ctx, containerID, http.MethodPost, "/stop", query, time.Duration(timeout)*time.Second)
}

func callDockerContainerActionViaAPI(ctx context.Context, containerID, method, suffix, query string, timeout time.Duration) (map[string]interface{}, error) {
	endpoint := strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_DOCKER_ENDPOINT"),
		os.Getenv("MCP_DOCKER_ENDPOINT"),
		"unix:///var/run/docker.sock",
	))
	detail := map[string]interface{}{
		"runtime_container_id": containerID,
		"docker_endpoint":      endpoint,
	}
	client, baseURL, err := newDockerInspectHTTPClient(endpoint, timeout)
	if err != nil {
		return detail, err
	}
	reqURL := strings.TrimRight(baseURL, "/") + "/containers/" + url.PathEscape(containerID) + suffix + query
	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return detail, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return detail, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	detail["api_http_status"] = resp.StatusCode
	if text := strings.TrimSpace(string(body)); text != "" {
		detail["api_response_body"] = text
	}

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return detail, nil
	case http.StatusNotFound:
		detail["runtime_exists"] = false
		return detail, fmt.Errorf("container not found")
	default:
		return detail, fmt.Errorf("docker api action failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func newDockerInspectHTTPClient(endpoint string, timeout time.Duration) (*http.Client, string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, "", fmt.Errorf("docker endpoint is empty")
	}

	if strings.HasPrefix(endpoint, "unix://") {
		socketPath := strings.TrimSpace(strings.TrimPrefix(endpoint, "unix://"))
		if socketPath == "" {
			return nil, "", fmt.Errorf("docker unix endpoint missing socket path")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: timeout}
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		}
		return &http.Client{Timeout: timeout, Transport: transport}, "http://docker", nil
	}

	if strings.HasPrefix(endpoint, "tcp://") {
		endpoint = "http://" + strings.TrimPrefix(endpoint, "tcp://")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("parse docker endpoint failed: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return nil, "", fmt.Errorf("docker endpoint missing scheme or host")
	}
	return &http.Client{Timeout: timeout}, strings.TrimRight(endpoint, "/"), nil
}

func normalizeAgentAccessibleEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	if !isAgentRunningInContainer() {
		return raw
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return raw
	}
	alias := strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_CONTAINER_HOST_ALIAS"),
		os.Getenv("MCP_CONTAINER_HOST_ALIAS"),
		"host.docker.internal",
	))
	if alias == "" {
		return raw
	}
	port := strings.TrimSpace(parsed.Port())
	if port != "" {
		parsed.Host = net.JoinHostPort(alias, port)
	} else {
		parsed.Host = alias
	}
	return parsed.String()
}

func isAgentRunningInContainer() bool {
	if raw := strings.TrimSpace(os.Getenv("AGENT_RUNNING_IN_CONTAINER")); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func buildAgentMetadata(cfg agentConfig, fitManager *fit.ManagedServe) map[string]string {
	metadata := map[string]string{
		"source":         "agent-binary",
		"llmfit_enabled": strconv.FormatBool(cfg.llmfit.enabled),
	}
	if cfg.llmfit.enabled {
		metadata["llmfit_endpoint"] = cfg.llmfit.endpoint
	}
	if fitManager == nil {
		metadata["llmfit_managed"] = "false"
		metadata["llmfit_healthy"] = "false"
		return metadata
	}

	snapshot := fitManager.Snapshot()
	metadata["llmfit_managed"] = strconv.FormatBool(snapshot.Managed)
	metadata["llmfit_healthy"] = strconv.FormatBool(snapshot.Healthy)
	metadata["llmfit_endpoint"] = snapshot.Endpoint
	metadata["llmfit_health_url"] = snapshot.HealthURL
	if snapshot.PID > 0 {
		metadata["llmfit_pid"] = strconv.Itoa(snapshot.PID)
	}
	if snapshot.LastError != "" {
		metadata["llmfit_last_error"] = snapshot.LastError
	}
	if !snapshot.LastCheckedAt.IsZero() {
		metadata["llmfit_last_checked_at"] = snapshot.LastCheckedAt.Format(time.RFC3339)
	}
	return metadata
}

func retryControllerBootstrap(ctx context.Context, stage string, fn func(attempt int) error) error {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "bootstrap"
	}
	interval := 2 * time.Second
	for attempt := 1; ; attempt++ {
		if err := fn(attempt); err == nil {
			return nil
		} else {
			fmt.Printf("agent %s失败: %v（第 %d 次，%s 后重试）\n", stage, err, attempt, interval.String())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if interval < 10*time.Second {
			interval += 1 * time.Second
		}
	}
}

func doJSON(ctx context.Context, client *http.Client, cfg agentConfig, method, url string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	var envelope apiResponse
	if len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &envelope)
	}
	if resp.StatusCode >= 300 || !envelope.Success {
		msg := strings.TrimSpace(envelope.Message)
		if msg == "" {
			msg = fmt.Sprintf("http status=%d", resp.StatusCode)
		}
		if len(envelope.Data) > 0 {
			var detail interface{}
			if err := json.Unmarshal(envelope.Data, &detail); err == nil {
				detailText := strings.TrimSpace(fmt.Sprint(detail))
				if detailText != "" && detailText != "<nil>" && detailText != "map[]" {
					msg = msg + ": " + detailText
				}
			}
		}
		return errors.New(msg)
	}

	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return err
		}
	}
	return nil
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		item := strings.TrimSpace(strings.ToLower(part))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func parseRuntimeCapsEnv(raw string) (map[string][]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string][]string{
			"docker":   {"list", "load", "unload", "start", "stop", "pull"},
			"lmstudio": {"list", "load", "unload"},
		}, nil
	}
	out := map[string][]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("AGENT_RUNTIME_CAPABILITIES_JSON 解析失败: %w", err)
	}
	for key, value := range out {
		out[key] = splitList(strings.Join(value, ","))
	}
	return out, nil
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if d, err := time.ParseDuration(raw + "s"); err == nil && d > 0 {
		return d
	}
	return fallback
}

func boolFromEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func intFromEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseArgList(raw string) []string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func mergeItems(base []string, extra []string) []string {
	return splitList(strings.Join(append(base, extra...), ","))
}

func removeItem(base []string, target string) []string {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return base
	}
	out := make([]string, 0, len(base))
	for _, item := range base {
		key := strings.TrimSpace(strings.ToLower(item))
		if key == "" || key == target {
			continue
		}
		out = append(out, key)
	}
	return out
}

func mergeObjectMaps(existing interface{}, overlay map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	if existingMap, ok := existing.(map[string]interface{}); ok {
		for k, v := range existingMap {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			out[key] = v
		}
	}
	for k, v := range overlay {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func nestedMapValue(payload map[string]interface{}, key string) map[string]interface{} {
	if payload == nil {
		return nil
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	item, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	return item
}

func stringValueFromNestedMap(payload map[string]interface{}, nestedKey, key string) string {
	nested := nestedMapValue(payload, nestedKey)
	if nested == nil {
		return ""
	}
	return stringValue(nested, key)
}

func stringSliceValueFromNested(payload map[string]interface{}, nestedKey, key string) []string {
	nested := nestedMapValue(payload, nestedKey)
	if nested == nil {
		return nil
	}
	return stringSliceValue(nested, key)
}

func stringSliceValue(payload map[string]interface{}, key string) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" || text == "<nil>" {
				continue
			}
			out = append(out, text)
		}
		return out
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil
		}
		return []string{text}
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || text == "<nil>" {
			return nil
		}
		return []string{text}
	}
}

func firstNonEmptyStringSlice(candidates ...[]string) []string {
	for _, items := range candidates {
		if len(items) == 0 {
			continue
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			text := strings.TrimSpace(item)
			if text == "" {
				continue
			}
			out = append(out, text)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func stringMapFromValue(raw interface{}) map[string]string {
	switch value := raw.(type) {
	case map[string]string:
		out := map[string]string{}
		for k, v := range value {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			out[key] = strings.TrimSpace(v)
		}
		return out
	case map[string]interface{}:
		out := map[string]string{}
		for k, v := range value {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			text := strings.TrimSpace(fmt.Sprint(v))
			if text == "" || text == "<nil>" {
				continue
			}
			out[key] = text
		}
		return out
	default:
		return nil
	}
}

func stringValue(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func intValue(payload map[string]interface{}, key string, fallback int) int {
	if payload == nil {
		return fallback
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return fallback
	}
	switch value := raw.(type) {
	case float64:
		if int(value) > 0 {
			return int(value)
		}
	case int:
		if value > 0 {
			return value
		}
	case int64:
		if value > 0 {
			return int(value)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isDigits(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cloneInterfaceMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func mapFromAny(raw interface{}) map[string]interface{} {
	value, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	return cloneInterfaceMap(value)
}

func mustMap(v interface{}) map[string]interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func normalizeStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		lower := strings.ToLower(item)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendUniqueNormalized(in []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return normalizeStringSlice(in)
	}
	out := normalizeStringSlice(in)
	for _, item := range out {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return out
		}
	}
	return append(out, value)
}

func containsFolded(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func normalizePortCandidates(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		port := strings.TrimSpace(raw)
		if port == "" || !isDigits(port) {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmptyStringMap(candidates ...map[string]string) map[string]string {
	for _, candidate := range candidates {
		normalized := stringMapFromValue(candidate)
		if len(normalized) > 0 {
			return normalized
		}
	}
	return map[string]string{}
}

func stringValueMapFromNested(payload map[string]interface{}, nestedKey, key string) interface{} {
	nested := nestedMapValue(payload, nestedKey)
	if nested == nil {
		return nil
	}
	return nested[key]
}

func parseBoolLike(raw interface{}) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		default:
			return false, false
		}
	default:
		text := strings.TrimSpace(fmt.Sprint(raw))
		switch strings.ToLower(text) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		default:
			return false, false
		}
	}
}

func boolValueFromPayload(payload map[string]interface{}, key string) (bool, bool) {
	if payload == nil {
		return false, false
	}
	raw, ok := payload[key]
	if !ok {
		return false, false
	}
	return parseBoolLike(raw)
}

func boolValueFromNested(payload map[string]interface{}, nestedKey, key string) (bool, bool) {
	nested := nestedMapValue(payload, nestedKey)
	if nested == nil {
		return false, false
	}
	raw, ok := nested[key]
	if !ok {
		return false, false
	}
	return parseBoolLike(raw)
}

func boolValueFromDetailOrPayload(detail map[string]interface{}, payload map[string]interface{}, key string) (bool, bool) {
	if value, ok := boolValueFromPayload(detail, key); ok {
		return value, true
	}
	if value, ok := boolValueFromPayload(payload, key); ok {
		return value, true
	}
	return boolValueFromNested(payload, "resolved_context", key)
}
