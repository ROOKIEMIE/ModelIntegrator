package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ModelIntegrator/src/pkg/fit"
	"ModelIntegrator/src/pkg/model"
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

	ticker := time.NewTicker(cfg.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if fitManager != nil {
				_ = fitManager.Stop(context.Background())
			}
			fmt.Println("agent 退出")
			return
		case <-ticker.C:
			if err := sendHeartbeat(ctx, client, cfg, fitManager); err != nil {
				fmt.Printf("agent 心跳失败: %v\n", err)
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
