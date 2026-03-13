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
	report := agentTaskReport{
		Progress:   100,
		Message:    message,
		Detail:     detail,
		Error:      errText,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
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
	NodeID             string
	ModelID            string
	BindingMode        string
	RuntimeKind        string
	TemplateType       string
	Endpoint           string
	HealthPath         string
	ModelPath          string
	ScriptRef          string
	RuntimeContainerID string
	ExposedPorts       []string
	RequiredEnv        []string
	OptionalEnv        []string
	MountPoints        []string
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
	appendString("node_id", c.NodeID)
	appendString("model_id", c.ModelID)
	appendString("binding_mode", c.BindingMode)
	appendString("runtime_kind", c.RuntimeKind)
	appendString("template_type", c.TemplateType)
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
	ctx.NodeID = get("node_id")
	ctx.ModelID = get("model_id")
	ctx.BindingMode = get("binding_mode")
	ctx.RuntimeKind = get("runtime_kind")
	ctx.TemplateType = firstNonEmpty(get("template_type"), get("runtime_template_type"))
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
	detail := map[string]interface{}{
		"task_type":           string(task.Type),
		"execution_path":      "agent",
		"precheck_target":     "runtime",
		"runtime_instance_id": strings.TrimSpace(stringValue(task.Payload, "runtime_instance_id")),
		"runtime_binding_id":  strings.TrimSpace(stringValue(task.Payload, "runtime_binding_id")),
		"runtime_template_id": strings.TrimSpace(stringValue(task.Payload, "runtime_template_id")),
		"manifest_id":         strings.TrimSpace(stringValue(task.Payload, "manifest_id")),
		"binding_mode":        strings.TrimSpace(stringValue(task.Payload, "binding_mode")),
		"runtime_kind":        strings.TrimSpace(stringValue(task.Payload, "runtime_kind")),
		"template_type":       strings.TrimSpace(firstNonEmpty(stringValue(task.Payload, "template_type"), stringValue(task.Payload, "runtime_template_type"))),
	}
	failures := make([]string, 0, 4)
	requiredEnv := stringSliceValue(task.Payload, "required_env")
	optionalEnv := stringSliceValue(task.Payload, "optional_env")
	exposedPorts := stringSliceValue(task.Payload, "exposed_ports")
	if len(requiredEnv) > 0 {
		detail["required_env"] = requiredEnv
	}
	if len(optionalEnv) > 0 {
		detail["optional_env"] = optionalEnv
	}
	if len(exposedPorts) > 0 {
		detail["exposed_ports"] = exposedPorts
	}

	endpoint := strings.TrimSpace(stringValue(task.Payload, "endpoint"))
	modelID := strings.TrimSpace(stringValue(task.Payload, "model_id"))
	if endpoint == "" && modelID != "" {
		if resolved, err := fetchModelEndpoint(ctx, client, cfg, modelID); err == nil {
			endpoint = resolved
		}
	}
	timeout := time.Duration(intValue(task.Payload, "timeout_seconds", 3)) * time.Second
	healthPath := strings.TrimSpace(stringValue(task.Payload, "health_path"))
	if healthPath == "" {
		healthPath = "/health"
	}

	if endpoint != "" {
		portOpen, portDetail, err := checkEndpointPort(endpoint, timeout)
		detail["port_check"] = portDetail
		if err != nil || !portOpen {
			failures = append(failures, "port_check_failed")
		}
		ready, readyDetail, err := checkEndpointReadiness(endpoint, healthPath, timeout)
		detail["runtime_readiness"] = readyDetail
		detail["runtime_ready"] = ready
		if err != nil || !ready {
			failures = append(failures, "runtime_readiness_failed")
		}
	} else {
		detail["runtime_ready"] = false
		failures = append(failures, "endpoint_missing")
	}

	modelPath := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "model_path"),
		stringValue(task.Payload, "path"),
	))
	if modelPath != "" {
		exists, pathDetail, err := checkModelPathExists(modelPath)
		detail["model_path_check"] = pathDetail
		if err != nil || !exists {
			failures = append(failures, "model_path_check_failed")
		}
	}

	containerID := strings.TrimSpace(firstNonEmpty(
		stringValue(task.Payload, "runtime_container_id"),
		stringValue(task.Payload, "container_id"),
		stringValue(task.Payload, "container"),
	))
	if containerID != "" {
		exists, running, inspectDetail, err := inspectDockerContainer(ctx, containerID)
		detail["docker_inspect"] = inspectDetail
		detail["runtime_exists"] = exists
		detail["runtime_running"] = running
		if err != nil {
			failures = append(failures, "docker_inspect_failed")
		} else if !exists {
			failures = append(failures, "runtime_container_missing")
		} else if !running {
			failures = append(failures, "runtime_container_not_running")
		}
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

	if len(failures) > 0 {
		message := "runtime precheck failed: " + strings.Join(failures, ",")
		detail["precheck_failures"] = failures
		return false, message, detail, message
	}
	return true, "runtime precheck passed", detail, ""
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
