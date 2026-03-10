package fit

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ManagedServeConfig defines how agent supervises a local llmfit serve process.
type ManagedServeConfig struct {
	Enabled             bool
	BinaryPath          string
	Endpoint            string
	HealthPath          string
	ServeArgs           []string
	StartupTimeout      time.Duration
	HealthCheckInterval time.Duration
	HealthCheckTimeout  time.Duration
	FailureThreshold    int
	Logger              *slog.Logger
}

// ManagedServeStatus is a read-only runtime snapshot.
type ManagedServeStatus struct {
	Enabled             bool      `json:"enabled"`
	Managed             bool      `json:"managed"`
	Healthy             bool      `json:"healthy"`
	Endpoint            string    `json:"endpoint"`
	HealthURL           string    `json:"health_url"`
	PID                 int       `json:"pid,omitempty"`
	LastStartedAt       time.Time `json:"last_started_at,omitempty"`
	LastCheckedAt       time.Time `json:"last_checked_at,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
}

type ManagedServe struct {
	cfg    ManagedServeConfig
	client *http.Client

	mu                  sync.RWMutex
	processCtx          context.Context
	processCancel       context.CancelFunc
	cmd                 *exec.Cmd
	managed             bool
	healthy             bool
	lastStartedAt       time.Time
	lastCheckedAt       time.Time
	lastError           string
	consecutiveFailures int
}

func NewManagedServe(cfg ManagedServeConfig) *ManagedServe {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = "llmfit"
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = "http://127.0.0.1:18123"
	}
	if strings.TrimSpace(cfg.HealthPath) == "" {
		cfg.HealthPath = "/health"
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 20 * time.Second
	}
	if cfg.HealthCheckInterval <= 0 {
		cfg.HealthCheckInterval = 10 * time.Second
	}
	if cfg.HealthCheckTimeout <= 0 {
		cfg.HealthCheckTimeout = 2 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &ManagedServe{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.HealthCheckTimeout,
		},
	}
}

func (m *ManagedServe) Start(parent context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	m.mu.Lock()
	if m.managed {
		m.mu.Unlock()
		return nil
	}
	m.processCtx, m.processCancel = context.WithCancel(context.Background())
	m.managed = true
	m.mu.Unlock()

	if err := m.startProcess(); err != nil {
		_ = m.Stop(context.Background())
		return err
	}
	if err := m.waitUntilHealthy(m.cfg.StartupTimeout); err != nil {
		_ = m.Stop(context.Background())
		return err
	}

	go m.supervise(parent)
	return nil
}

func (m *ManagedServe) Stop(_ context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	m.mu.Lock()
	if !m.managed {
		m.mu.Unlock()
		return nil
	}
	cancel := m.processCancel
	m.processCancel = nil
	m.processCtx = nil
	m.managed = false
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return m.stopProcess(2 * time.Second)
}

func (m *ManagedServe) Snapshot() ManagedServeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := ManagedServeStatus{
		Enabled:             m.cfg.Enabled,
		Managed:             m.managed,
		Healthy:             m.healthy,
		Endpoint:            strings.TrimSpace(m.cfg.Endpoint),
		HealthURL:           m.healthURL(),
		LastStartedAt:       m.lastStartedAt,
		LastCheckedAt:       m.lastCheckedAt,
		LastError:           m.lastError,
		ConsecutiveFailures: m.consecutiveFailures,
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.PID = m.cmd.Process.Pid
	}
	return status
}

func (m *ManagedServe) supervise(parent context.Context) {
	ticker := time.NewTicker(m.cfg.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-parent.Done():
			_ = m.Stop(context.Background())
			return
		case <-ticker.C:
			healthy, err := m.checkHealth()
			if healthy {
				m.markHealthy()
				continue
			}

			m.markUnhealthy(err)
			if m.needRestart() {
				if restartErr := m.restart(); restartErr != nil {
					m.cfg.Logger.Warn("llmfit 重启失败", "error", restartErr)
				}
			}
		}
	}
}

func (m *ManagedServe) startProcess() error {
	m.mu.RLock()
	processCtx := m.processCtx
	m.mu.RUnlock()
	if processCtx == nil {
		return fmt.Errorf("llmfit process context 未初始化")
	}

	args, err := buildServeArgs(strings.TrimSpace(m.cfg.Endpoint), m.cfg.ServeArgs)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(processCtx, m.cfg.BinaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 llmfit 失败: %w", err)
	}

	pid := cmd.Process.Pid
	now := time.Now().UTC()

	m.mu.Lock()
	m.cmd = cmd
	m.healthy = false
	m.lastError = ""
	m.lastStartedAt = now
	m.lastCheckedAt = now
	m.consecutiveFailures = 0
	m.mu.Unlock()

	m.cfg.Logger.Info("llmfit managed serve 已启动", "pid", pid, "endpoint", m.cfg.Endpoint)
	go m.watchProcess(cmd, pid)
	return nil
}

func (m *ManagedServe) watchProcess(cmd *exec.Cmd, pid int) {
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil || m.cmd.Process.Pid != pid {
		return
	}

	m.cmd = nil
	m.healthy = false
	m.lastCheckedAt = time.Now().UTC()
	if err != nil {
		m.lastError = fmt.Sprintf("llmfit 进程退出: %v", err)
	} else {
		m.lastError = "llmfit 进程已退出"
	}

	if m.managed && (m.processCtx != nil && m.processCtx.Err() == nil) {
		m.consecutiveFailures = m.cfg.FailureThreshold
	}
}

func (m *ManagedServe) restart() error {
	if err := m.stopProcess(2 * time.Second); err != nil {
		return err
	}
	if err := m.startProcess(); err != nil {
		return err
	}
	return m.waitUntilHealthy(m.cfg.StartupTimeout)
}

func (m *ManagedServe) stopProcess(grace time.Duration) error {
	m.mu.RLock()
	cmd := m.cmd
	m.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(grace)
	_ = cmd.Process.Kill()
	return nil
}

func (m *ManagedServe) waitUntilHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		healthy, err := m.checkHealth()
		if healthy {
			m.markHealthy()
			return nil
		}
		m.markUnhealthy(err)
		if time.Now().After(deadline) {
			return fmt.Errorf("llmfit 健康检查超时: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (m *ManagedServe) checkHealth() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, m.healthURL(), nil)
	if err != nil {
		return false, fmt.Errorf("构造健康检查请求失败: %w", err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("健康检查状态码异常: %d", resp.StatusCode)
	}
	return true, nil
}

func (m *ManagedServe) markHealthy() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.healthy = true
	m.lastCheckedAt = now
	m.consecutiveFailures = 0
	m.lastError = ""
}

func (m *ManagedServe) markUnhealthy(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthy = false
	m.lastCheckedAt = time.Now().UTC()
	m.consecutiveFailures++
	if err != nil {
		m.lastError = err.Error()
	}
}

func (m *ManagedServe) needRestart() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.managed && m.consecutiveFailures >= m.cfg.FailureThreshold
}

func (m *ManagedServe) healthURL() string {
	endpoint := strings.TrimRight(strings.TrimSpace(m.cfg.Endpoint), "/")
	path := strings.TrimSpace(m.cfg.HealthPath)
	if path == "" {
		path = "/health"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return endpoint + path
}

func buildServeArgs(endpoint string, serveArgs []string) ([]string, error) {
	args := normalizeServeArgs(serveArgs)
	if len(args) > 0 {
		return args, nil
	}

	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("解析 AGENT_LLMFIT_ENDPOINT 失败: %w", err)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		return nil, fmt.Errorf("AGENT_LLMFIT_ENDPOINT 缺少主机信息")
	}
	if port == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	return []string{"serve", "--host", host, "--port", port}, nil
}

func normalizeServeArgs(serveArgs []string) []string {
	if len(serveArgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(serveArgs)+1)
	for _, item := range serveArgs {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	if strings.ToLower(out[0]) != "serve" {
		out = append([]string{"serve"}, out...)
	}
	return out
}
