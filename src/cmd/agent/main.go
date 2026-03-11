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
	"os/signal"
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

	if err := registerAgent(ctx, client, cfg, fitManager); err != nil {
		os.Stderr.WriteString("agent 注册失败: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := reportCapabilities(ctx, client, cfg, fitManager); err != nil {
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

func executeAgentTask(ctx context.Context, client *http.Client, cfg agentConfig, task model.Task) (bool, string, map[string]interface{}, string) {
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
			return false, firstNonEmpty(err.Error(), "readiness 检查失败"), detail, err.Error()
		}
		if !ready {
			return false, "runtime 未 ready", detail, "runtime not ready"
		}
		return true, "runtime readiness 检查通过", detail, ""
	default:
		return false, "不支持的任务类型", map[string]interface{}{"task_type": string(task.Type)}, "unsupported task type"
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
	return resp.StatusCode >= 200 && resp.StatusCode < 300, detail, nil
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
