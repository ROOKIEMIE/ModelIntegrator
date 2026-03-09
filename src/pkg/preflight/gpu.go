package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	driverVersionPattern = regexp.MustCompile(`Driver Version:\s*([0-9.]+)`)
	cudaVersionPattern   = regexp.MustCompile(`CUDA Version:\s*([0-9.]+)`)
)

const defaultGPUProbeImage = "nvidia/cuda:12.4.1-base-ubuntu22.04"

type GPUReport struct {
	Platform      string `json:"platform"`
	CUDAAvailable bool   `json:"cuda_available"`
	GPUName       string `json:"gpu_name,omitempty"`
	DriverVersion string `json:"driver_version,omitempty"`
	CUDAVersion   string `json:"cuda_version,omitempty"`
	Message       string `json:"message"`
}

type dockerHTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

func DetectGPU(ctx context.Context, dockerEndpoint string) GPUReport {
	localCtx, localCancel := context.WithTimeout(ctx, 4*time.Second)
	local := detectGPULocal(localCtx)
	localCancel()
	if local.CUDAAvailable {
		return local
	}

	dockerEndpoint = strings.TrimSpace(dockerEndpoint)
	if dockerEndpoint == "" {
		return local
	}

	dockerCtx, dockerCancel := context.WithTimeout(ctx, 18*time.Second)
	defer dockerCancel()
	report, err := detectGPUViaDocker(dockerCtx, dockerEndpoint)
	if err != nil {
		local.Message = local.Message + "; Docker 探针未检测到 GPU: " + err.Error()
		return local
	}
	return report
}

func detectGPULocal(ctx context.Context) GPUReport {
	nvidiaSMIPath, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return GPUReport{
			Platform:      "non-cuda",
			CUDAAvailable: false,
			Message:       "未检测到 nvidia-smi，当前平台被视为非 CUDA 平台",
		}
	}

	cmd := exec.CommandContext(ctx, nvidiaSMIPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return GPUReport{
			Platform:      "cuda-unknown",
			CUDAAvailable: false,
			Message:       "检测到 nvidia-smi 但执行失败: " + strings.TrimSpace(string(output)),
		}
	}

	raw := string(output)
	driverVersion := extractFirst(raw, driverVersionPattern)
	cudaVersion := extractFirst(raw, cudaVersionPattern)
	gpuName := detectGPUNameByQuery(ctx, nvidiaSMIPath)

	if driverVersion == "" {
		driverVersion = detectDriverVersionByQuery(ctx, nvidiaSMIPath)
	}

	if driverVersion == "" && cudaVersion == "" {
		return GPUReport{
			Platform:      "cuda-unknown",
			CUDAAvailable: false,
			Message:       "nvidia-smi 可执行，但未解析到 Driver/CUDA 版本信息",
		}
	}

	return GPUReport{
		Platform:      "cuda",
		CUDAAvailable: true,
		GPUName:       gpuName,
		DriverVersion: driverVersion,
		CUDAVersion:   cudaVersion,
		Message:       "检测到 CUDA 平台",
	}
}

func detectGPUViaDocker(ctx context.Context, endpoint string) (GPUReport, error) {
	client, err := newDockerHTTPClient(endpoint)
	if err != nil {
		return GPUReport{}, err
	}
	if err := client.ping(ctx); err != nil {
		return GPUReport{}, err
	}

	image := strings.TrimSpace(os.Getenv("MCP_GPU_PROBE_IMAGE"))
	if image == "" {
		image = defaultGPUProbeImage
	}

	output, err := client.runNvidiaSMIProbe(ctx, image)
	if err != nil {
		return GPUReport{}, err
	}

	gpuName, driver := parseProbeCSVLine(output)
	cuda := extractFirst(output, cudaVersionPattern)
	if driver == "" {
		driver = extractFirst(output, driverVersionPattern)
	}

	if gpuName == "" && driver == "" && cuda == "" {
		return GPUReport{}, fmt.Errorf("Docker 探针输出缺少 GPU/Driver/CUDA 信息")
	}

	return GPUReport{
		Platform:      "cuda",
		CUDAAvailable: true,
		GPUName:       gpuName,
		DriverVersion: driver,
		CUDAVersion:   cuda,
		Message:       "通过 Docker GPU 探针检测到 CUDA 平台",
	}, nil
}

func LogGPUReport(logger *slog.Logger, report GPUReport) {
	if report.CUDAAvailable {
		logger.Info("GPU 预检查通过", "platform", report.Platform, "gpu_name", report.GPUName, "driver_version", report.DriverVersion, "cuda_version", report.CUDAVersion, "message", report.Message)
		return
	}
	logger.Warn("GPU 预检查提示", "platform", report.Platform, "message", report.Message)
}

func extractFirst(text string, re *regexp.Regexp) string {
	matches := re.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func detectDriverVersionByQuery(ctx context.Context, nvidiaSMIPath string) string {
	cmd := exec.CommandContext(ctx, nvidiaSMIPath, "--query-gpu=driver_version", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func parseProbeCSVLine(output string) (gpuName, driver string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "|") || strings.Contains(line, "NVIDIA-SMI") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	}
	return "", ""
}

func newDockerHTTPClient(endpoint string) (*dockerHTTPClient, error) {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case strings.HasPrefix(endpoint, "unix://"):
		socketPath := strings.TrimSpace(strings.TrimPrefix(endpoint, "unix://"))
		if socketPath == "" {
			return nil, fmt.Errorf("Docker unix endpoint 为空")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}
		return &dockerHTTPClient{
			baseURL: "http://docker",
			httpClient: &http.Client{
				Transport: transport,
				Timeout:   10 * time.Second,
			},
		}, nil
	case strings.HasPrefix(endpoint, "http://"), strings.HasPrefix(endpoint, "https://"):
		return &dockerHTTPClient{
			baseURL: strings.TrimRight(endpoint, "/"),
			httpClient: &http.Client{
				Timeout: 10 * time.Second,
			},
		}, nil
	default:
		return nil, fmt.Errorf("不支持的 Docker endpoint: %s", endpoint)
	}
}

func (c *dockerHTTPClient) ping(ctx context.Context) error {
	status, body, err := c.request(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return fmt.Errorf("Docker ping 失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("Docker ping 返回状态码 %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *dockerHTTPClient) runNvidiaSMIProbe(ctx context.Context, image string) (string, error) {
	containerID, err := c.createProbeContainer(ctx, image)
	if err != nil {
		return "", err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.removeContainer(cleanupCtx, containerID, true)
	}()

	if err := c.startContainer(ctx, containerID); err != nil {
		return "", err
	}

	exitCode, waitErr, err := c.waitContainer(ctx, containerID)
	if err != nil {
		return "", err
	}

	logs, err := c.containerLogs(ctx, containerID)
	if err != nil {
		return "", err
	}
	logs = strings.TrimSpace(logs)
	if exitCode != 0 {
		msg := strings.TrimSpace(waitErr)
		if msg == "" {
			msg = strings.TrimSpace(logs)
		}
		if msg == "" {
			msg = "未知错误"
		}
		return "", fmt.Errorf("GPU 探针容器退出码 %d: %s", exitCode, msg)
	}
	if logs == "" {
		return "", fmt.Errorf("GPU 探针容器无日志输出")
	}
	return logs, nil
}

func (c *dockerHTTPClient) createProbeContainer(ctx context.Context, image string) (string, error) {
	type deviceRequest struct {
		Driver       string     `json:"Driver,omitempty"`
		Count        int        `json:"Count"`
		Capabilities [][]string `json:"Capabilities"`
	}
	type hostConfig struct {
		AutoRemove     bool            `json:"AutoRemove"`
		DeviceRequests []deviceRequest `json:"DeviceRequests"`
	}
	type createRequest struct {
		Image      string     `json:"Image"`
		Cmd        []string   `json:"Cmd"`
		Tty        bool       `json:"Tty"`
		HostConfig hostConfig `json:"HostConfig"`
	}

	reqBody := createRequest{
		Image: image,
		Cmd: []string{
			"sh", "-lc", "nvidia-smi --query-gpu=name,driver_version --format=csv,noheader && nvidia-smi",
		},
		Tty: true,
		HostConfig: hostConfig{
			AutoRemove: false,
			DeviceRequests: []deviceRequest{
				{
					Driver:       "nvidia",
					Count:        1,
					Capabilities: [][]string{{"gpu"}},
				},
			},
		},
	}

	q := url.Values{}
	q.Set("name", fmt.Sprintf("model-integrator-gpu-probe-%d", time.Now().UnixNano()))
	status, body, err := c.request(ctx, http.MethodPost, "/containers/create", q, reqBody)
	if err != nil {
		return "", fmt.Errorf("创建 GPU 探针容器失败: %w", err)
	}
	if status < 200 || status >= 300 {
		msg := strings.TrimSpace(string(body))
		if status == http.StatusNotFound {
			return "", fmt.Errorf("GPU 探针镜像不存在（%s），请先拉取或设置 MCP_GPU_PROBE_IMAGE: %s", image, msg)
		}
		return "", fmt.Errorf("创建 GPU 探针容器失败，状态码 %d: %s", status, msg)
	}

	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("解析 GPU 探针容器 ID 失败: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("创建 GPU 探针容器成功但未返回 ID")
	}
	return created.ID, nil
}

func (c *dockerHTTPClient) startContainer(ctx context.Context, containerID string) error {
	path := "/containers/" + containerID + "/start"
	status, body, err := c.request(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return fmt.Errorf("启动 GPU 探针容器失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("启动 GPU 探针容器失败，状态码 %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *dockerHTTPClient) waitContainer(ctx context.Context, containerID string) (int, string, error) {
	path := "/containers/" + containerID + "/wait"
	q := url.Values{}
	q.Set("condition", "not-running")
	status, body, err := c.request(ctx, http.MethodPost, path, q, nil)
	if err != nil {
		return -1, "", fmt.Errorf("等待 GPU 探针容器失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return -1, "", fmt.Errorf("等待 GPU 探针容器失败，状态码 %d: %s", status, strings.TrimSpace(string(body)))
	}

	var waited struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := json.Unmarshal(body, &waited); err != nil {
		return -1, "", fmt.Errorf("解析 GPU 探针容器 wait 响应失败: %w", err)
	}
	errMsg := ""
	if waited.Error != nil {
		errMsg = strings.TrimSpace(waited.Error.Message)
	}
	return waited.StatusCode, errMsg, nil
}

func (c *dockerHTTPClient) containerLogs(ctx context.Context, containerID string) (string, error) {
	path := "/containers/" + containerID + "/logs"
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	status, body, err := c.request(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return "", fmt.Errorf("读取 GPU 探针容器日志失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("读取 GPU 探针容器日志失败，状态码 %d: %s", status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func (c *dockerHTTPClient) removeContainer(ctx context.Context, containerID string, force bool) error {
	path := "/containers/" + containerID
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	status, body, err := c.request(ctx, http.MethodDelete, path, q, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("删除 GPU 探针容器失败，状态码 %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *dockerHTTPClient) request(ctx context.Context, method, path string, q url.Values, payload interface{}) (int, []byte, error) {
	fullURL := c.baseURL + path
	if len(q) > 0 {
		fullURL += "?" + q.Encode()
	}

	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("请求序列化失败: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func detectGPUNameByQuery(ctx context.Context, nvidiaSMIPath string) string {
	cmd := exec.CommandContext(ctx, nvidiaSMIPath, "--query-gpu=name", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}
