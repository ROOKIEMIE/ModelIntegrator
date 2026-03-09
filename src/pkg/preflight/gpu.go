package preflight

import (
	"context"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
)

var (
	driverVersionPattern = regexp.MustCompile(`Driver Version:\s*([0-9.]+)`)
	cudaVersionPattern   = regexp.MustCompile(`CUDA Version:\s*([0-9.]+)`)
)

type GPUReport struct {
	Platform      string `json:"platform"`
	CUDAAvailable bool   `json:"cuda_available"`
	DriverVersion string `json:"driver_version,omitempty"`
	CUDAVersion   string `json:"cuda_version,omitempty"`
	Message       string `json:"message"`
}

func DetectGPU(ctx context.Context) GPUReport {
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
		DriverVersion: driverVersion,
		CUDAVersion:   cudaVersion,
		Message:       "检测到 CUDA 平台",
	}
}

func LogGPUReport(logger *slog.Logger, report GPUReport) {
	if report.CUDAAvailable {
		logger.Info("GPU 预检查通过", "platform", report.Platform, "driver_version", report.DriverVersion, "cuda_version", report.CUDAVersion, "message", report.Message)
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
