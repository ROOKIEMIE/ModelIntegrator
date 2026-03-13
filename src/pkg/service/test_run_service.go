package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"model-control-plane/src/pkg/model"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

var (
	ErrTestRunNotFound = errors.New("test run not found")
)

type CreateTestRunRequest struct {
	Scenario    string `json:"scenario"`
	TriggeredBy string `json:"triggered_by"`
}

type TestRunService struct {
	store    *sqlitestore.Store
	taskSvc  *TaskService
	modelSvc *ModelService
	logger   *slog.Logger
	logRoot  string
}

func NewTestRunService(store *sqlitestore.Store, taskSvc *TaskService, modelSvc *ModelService, logger *slog.Logger, logRoot string) *TestRunService {
	if logger == nil {
		logger = slog.Default()
	}
	root := strings.TrimSpace(logRoot)
	if root == "" {
		root = "./testsystem/logs"
	}
	return &TestRunService{
		store:    store,
		taskSvc:  taskSvc,
		modelSvc: modelSvc,
		logger:   logger,
		logRoot:  root,
	}
}

func (s *TestRunService) CreateAndStart(ctx context.Context, req CreateTestRunRequest) (model.TestRun, error) {
	if s.store == nil {
		return model.TestRun{}, ErrTaskStoreNotReady
	}
	scenario := strings.TrimSpace(req.Scenario)
	if !isAllowedScenario(scenario) {
		return model.TestRun{}, fmt.Errorf("unsupported scenario: %s", scenario)
	}

	now := time.Now().UTC()
	run := model.TestRun{
		TestRunID:   fmt.Sprintf("testrun-%d", now.UnixNano()),
		Scenario:    scenario,
		Status:      model.TestRunStatusPending,
		TriggeredBy: strings.TrimSpace(req.TriggeredBy),
		CreatedAt:   now,
	}
	if err := s.store.UpsertTestRun(ctx, run); err != nil {
		return model.TestRun{}, err
	}
	go s.executeRun(run)
	return run, nil
}

func (s *TestRunService) GetTestRun(ctx context.Context, id string) (model.TestRun, error) {
	if s.store == nil {
		return model.TestRun{}, ErrTaskStoreNotReady
	}
	item, ok, err := s.store.GetTestRunByID(ctx, id)
	if err != nil {
		return model.TestRun{}, err
	}
	if !ok {
		return model.TestRun{}, ErrTestRunNotFound
	}
	return item, nil
}

func (s *TestRunService) ListTestRuns(ctx context.Context, limit int) ([]model.TestRun, error) {
	if s.store == nil {
		return nil, ErrTaskStoreNotReady
	}
	return s.store.ListTestRuns(ctx, limit)
}

func (s *TestRunService) executeRun(run model.TestRun) {
	runDir := filepath.Join(s.logRoot, run.TestRunID)
	logFile := filepath.Join(runDir, "run.log")
	summaryFile := filepath.Join(runDir, "summary.json")

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		s.failRun(run, "创建测试日志目录失败", err)
		return
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.failRun(run, "创建测试日志文件失败", err)
		return
	}
	defer func() {
		_ = f.Close()
	}()

	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(format, args...))
		_, _ = f.WriteString(line)
	}

	run.Status = model.TestRunStatusRunning
	run.StartedAt = time.Now().UTC()
	run.LogPath = runDir
	run.Summary = "测试执行中"
	_ = s.store.UpsertTestRun(context.Background(), run)

	logf("test run started: id=%s scenario=%s triggered_by=%s", run.TestRunID, run.Scenario, run.TriggeredBy)
	logf("parameters: log_dir=%s", runDir)

	summary := map[string]interface{}{
		"test_run_id": run.TestRunID,
		"scenario":    run.Scenario,
		"started_at":  run.StartedAt.Format(time.RFC3339Nano),
		"steps":       []map[string]interface{}{},
	}
	appendStep := func(name string, ok bool, message string, detail map[string]interface{}) {
		entry := map[string]interface{}{
			"name":    name,
			"ok":      ok,
			"message": message,
			"time":    time.Now().UTC().Format(time.RFC3339Nano),
		}
		if len(detail) > 0 {
			entry["detail"] = detail
		}
		summary["steps"] = append(summary["steps"].([]map[string]interface{}), entry)
		logf("step=%s ok=%t message=%s detail=%s", name, ok, message, mustJSONString(detail))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resultSummary, execErr := s.runScenario(ctx, run.Scenario, appendStep)
	if execErr != nil {
		run.Status = model.TestRunStatusFailed
		run.Error = execErr.Error()
		run.Summary = firstNonEmpty(resultSummary, "测试失败")
	} else {
		run.Status = model.TestRunStatusSuccess
		run.Error = ""
		run.Summary = firstNonEmpty(resultSummary, "测试成功")
	}
	run.FinishedAt = time.Now().UTC()
	_ = s.store.UpsertTestRun(context.Background(), run)

	summary["finished_at"] = run.FinishedAt.Format(time.RFC3339Nano)
	summary["status"] = run.Status
	summary["summary"] = run.Summary
	summary["error"] = run.Error
	raw, _ := json.MarshalIndent(summary, "", "  ")
	_ = os.WriteFile(summaryFile, raw, 0o644)
	logf("test run finished: status=%s summary=%s error=%s", run.Status, run.Summary, run.Error)
}

func (s *TestRunService) runScenario(ctx context.Context, scenario string, step func(name string, ok bool, message string, detail map[string]interface{})) (string, error) {
	switch scenario {
	case "e5_embedding_smoke":
		return s.runE5EmbeddingSmoke(ctx, step)
	case "local_agent_execution_smoke":
		return s.runLocalAgentExecutionSmoke(ctx, step)
	default:
		return "", fmt.Errorf("unsupported scenario: %s", scenario)
	}
}

func (s *TestRunService) runLocalAgentExecutionSmoke(ctx context.Context, step func(name string, ok bool, message string, detail map[string]interface{})) (string, error) {
	if s.taskSvc == nil || s.modelSvc == nil {
		return "", fmt.Errorf("task/model service is not ready")
	}
	models, err := s.modelSvc.ListModels(ctx)
	if err != nil {
		step("discover_model", false, "读取模型列表失败", map[string]interface{}{"error": err.Error()})
		return "读取模型列表失败", err
	}
	if len(models) == 0 {
		err := fmt.Errorf("没有可用模型")
		step("discover_model", false, err.Error(), nil)
		return err.Error(), err
	}
	target := models[0]
	for _, item := range models {
		if strings.TrimSpace(item.HostNodeID) == "node-controller" {
			target = item
			break
		}
	}
	modelID := strings.TrimSpace(target.ID)
	if modelID == "" {
		err := fmt.Errorf("模型 ID 为空")
		step("discover_model", false, err.Error(), nil)
		return err.Error(), err
	}
	step("discover_model", true, "已选择模型", map[string]interface{}{"model_id": modelID, "node_id": target.HostNodeID})

	runAgentTask := func(name string, taskType model.TaskType, timeout time.Duration, requireSuccess bool) (model.Task, error) {
		payload := map[string]interface{}{}
		if taskType == model.TaskTypeAgentDockerInspect {
			if containerID := strings.TrimSpace(readMetadataValue(target.Metadata, "runtime_container_id")); containerID != "" {
				payload["runtime_container_id"] = containerID
			}
		}
		created, createErr := s.taskSvc.CreateAgentNodeTask(ctx, AgentNodeLocalTaskRequest{
			NodeID:      strings.TrimSpace(target.HostNodeID),
			ModelID:     modelID,
			TaskType:    taskType,
			Payload:     payload,
			TriggeredBy: "test-run.local-agent-smoke",
		})
		if createErr != nil {
			step(name, false, "创建 agent 任务失败", map[string]interface{}{"task_type": string(taskType), "error": createErr.Error()})
			return model.Task{}, createErr
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		finalTask, waitErr := s.taskSvc.AwaitTask(waitCtx, created.ID, 600*time.Millisecond)
		if waitErr != nil {
			step(name, false, "等待 agent 任务失败", map[string]interface{}{"task_id": created.ID, "error": waitErr.Error()})
			return model.Task{}, waitErr
		}
		ok := finalTask.Status == model.TaskStatusSuccess || !requireSuccess
		step(name, ok, "agent 任务已结束", map[string]interface{}{
			"task_id":    finalTask.ID,
			"task_type":  string(taskType),
			"status":     string(finalTask.Status),
			"worker_id":  finalTask.WorkerID,
			"agent_id":   finalTask.AssignedAgentID,
			"message":    finalTask.Message,
			"require_ok": requireSuccess,
		})
		if requireSuccess && finalTask.Status != model.TaskStatusSuccess {
			return finalTask, fmt.Errorf("task=%s status=%s message=%s error=%s", finalTask.ID, finalTask.Status, finalTask.Message, finalTask.Error)
		}
		return finalTask, nil
	}

	snapshotTask, err := runAgentTask("resource_snapshot", model.TaskTypeAgentResourceSnapshot, 30*time.Second, true)
	if err != nil {
		return "agent resource snapshot 失败", err
	}
	inspectTask, err := runAgentTask("docker_inspect", model.TaskTypeAgentDockerInspect, 30*time.Second, false)
	if err != nil {
		return "agent docker inspect 失败", err
	}
	precheckTask, err := runAgentTask("runtime_precheck", model.TaskTypeAgentRuntimePrecheck, 45*time.Second, false)
	if err != nil {
		return "agent runtime precheck 失败", err
	}

	if s.taskSvc.runtimeObjectSvc != nil {
		runtimeInstanceID := firstNonEmpty(
			readTaskRuntimeInstanceID(precheckTask),
			readTaskRuntimeInstanceID(inspectTask),
			readTaskRuntimeInstanceID(snapshotTask),
		)
		if runtimeInstanceID != "" {
			instance, getErr := s.taskSvc.runtimeObjectSvc.GetRuntimeInstance(ctx, runtimeInstanceID)
			if getErr != nil {
				step("runtime_instance_projection", false, "读取 runtime instance 失败", map[string]interface{}{"runtime_instance_id": runtimeInstanceID, "error": getErr.Error()})
				return "读取 runtime instance 失败", getErr
			}
			ok := strings.TrimSpace(string(instance.PrecheckStatus)) != "" && instance.PrecheckStatus != model.PrecheckStatusUnknown
			step("runtime_instance_projection", ok, "已读取 runtime instance 状态投影", map[string]interface{}{
				"runtime_instance_id": runtimeInstanceID,
				"precheck_status":     string(instance.PrecheckStatus),
				"precheck_gating":     instance.PrecheckGating,
				"readiness":           string(instance.Readiness),
				"health_message":      instance.HealthMessage,
				"last_agent_task":     instance.LastAgentTask,
			})
			if !ok {
				return "runtime instance 状态未完成收口", fmt.Errorf("runtime instance precheck status unknown: %s", runtimeInstanceID)
			}
		}
	}
	return "local_agent_execution_smoke 完成", nil
}

func (s *TestRunService) runE5EmbeddingSmoke(ctx context.Context, step func(name string, ok bool, message string, detail map[string]interface{})) (string, error) {
	if s.modelSvc == nil || s.taskSvc == nil {
		return "", fmt.Errorf("model/task service is not ready")
	}

	models, err := s.modelSvc.ListModels(ctx)
	if err != nil {
		step("discover_model", false, "读取模型列表失败", map[string]interface{}{"error": err.Error()})
		return "读取模型列表失败", err
	}
	modelID, expectedDim := pickE5Model(models)
	if modelID == "" {
		err := fmt.Errorf("未发现 E5 embedding 样板模型")
		step("discover_model", false, err.Error(), nil)
		return err.Error(), err
	}
	step("discover_model", true, "已找到 E5 模型", map[string]interface{}{"model_id": modelID, "expected_dim": expectedDim})

	startTask, err := s.taskSvc.CreateRuntimeTask(ctx, model.TaskTypeRuntimeStart, modelID, "test-run")
	if err != nil {
		step("runtime_start", false, "创建 runtime start 任务失败", map[string]interface{}{"error": err.Error()})
		return "创建 runtime start 任务失败", err
	}
	step("runtime_start", true, "已创建 runtime start 任务", map[string]interface{}{"task_id": startTask.ID})

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	finalTask, err := s.taskSvc.AwaitTask(waitCtx, startTask.ID, 1200*time.Millisecond)
	if err != nil {
		step("runtime_start_wait", false, "等待 runtime start 任务失败", map[string]interface{}{"error": err.Error()})
		return "等待 runtime start 任务失败", err
	}
	if finalTask.Status != model.TaskStatusSuccess {
		step("runtime_start_wait", false, "runtime start 任务未成功", map[string]interface{}{"status": finalTask.Status, "error": finalTask.Error, "message": finalTask.Message})
		return "runtime start 任务未成功", fmt.Errorf("task status=%s message=%s error=%s", finalTask.Status, finalTask.Message, finalTask.Error)
	}
	step("runtime_start_wait", true, "runtime start 任务成功", map[string]interface{}{"task_id": finalTask.ID})

	endpoint, err := s.waitModelReadiness(ctx, modelID, step)
	if err != nil {
		return "readiness 检查失败", err
	}

	dim, err := requestEmbedding(ctx, endpoint, "hello world")
	if err != nil {
		step("embedding_request", false, "embedding 请求失败", map[string]interface{}{"endpoint": endpoint, "error": err.Error()})
		return "embedding 请求失败", err
	}
	if expectedDim > 0 && dim != expectedDim {
		err = fmt.Errorf("embedding 向量维度不匹配: got=%d expected=%d", dim, expectedDim)
		step("embedding_validate", false, err.Error(), map[string]interface{}{"dimension": dim, "expected_dimension": expectedDim})
		return "embedding 维度校验失败", err
	}
	step("embedding_validate", true, "embedding 响应校验通过", map[string]interface{}{"dimension": dim, "expected_dimension": expectedDim, "endpoint": endpoint})

	return fmt.Sprintf("e5_embedding_smoke 成功，endpoint=%s dim=%d", endpoint, dim), nil
}

func (s *TestRunService) waitModelReadiness(ctx context.Context, modelID string, step func(name string, ok bool, message string, detail map[string]interface{})) (string, error) {
	deadline := time.Now().Add(90 * time.Second)
	for i := 0; i < 30; i++ {
		if _, err := s.modelSvc.RefreshRuntimeStatus(ctx, modelID); err != nil {
			step("runtime_refresh", false, "刷新 runtime 状态失败", map[string]interface{}{"error": err.Error(), "attempt": i + 1})
		}
		item, err := s.modelSvc.GetModel(ctx, modelID)
		if err != nil {
			step("readiness_poll", false, "读取模型状态失败", map[string]interface{}{"error": err.Error(), "attempt": i + 1})
		} else {
			endpoint := strings.TrimSpace(item.Endpoint)
			detail := map[string]interface{}{
				"attempt":        i + 1,
				"desired_state":  item.DesiredState,
				"observed_state": item.ObservedState,
				"readiness":      item.Readiness,
				"endpoint":       endpoint,
				"health_message": item.HealthMessage,
			}
			if item.Readiness == model.ReadinessReady && endpoint != "" {
				step("readiness_poll", true, "模型已就绪", detail)
				return endpoint, nil
			}
			step("readiness_poll", false, "模型未就绪，继续轮询", detail)
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return "", fmt.Errorf("模型在 90s 内未达到 ready 状态")
}

func (s *TestRunService) failRun(run model.TestRun, summary string, err error) {
	now := time.Now().UTC()
	run.Status = model.TestRunStatusFailed
	run.Summary = summary
	if err != nil {
		run.Error = err.Error()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.FinishedAt = now
	_ = s.store.UpsertTestRun(context.Background(), run)
}

func isAllowedScenario(scenario string) bool {
	switch strings.TrimSpace(scenario) {
	case "e5_embedding_smoke", "local_agent_execution_smoke":
		return true
	default:
		return false
	}
}

func pickE5Model(items []model.Model) (string, int) {
	for _, item := range items {
		if strings.TrimSpace(item.Metadata["runtime_template_id"]) == DefaultEmbeddingTemplateID {
			return item.ID, parseExpectedDimension(item.Metadata)
		}
	}
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item.Name + " " + item.ID))
		if strings.Contains(name, "e5") {
			return item.ID, parseExpectedDimension(item.Metadata)
		}
	}
	return "", 0
}

func parseExpectedDimension(metadata map[string]string) int {
	if metadata == nil {
		return 768
	}
	for _, key := range []string{"embedding_dimension", "expected_dimension", "vector_dim"} {
		if raw := strings.TrimSpace(metadata[key]); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				return n
			}
		}
	}
	return 768
}

func requestEmbedding(ctx context.Context, endpoint, text string) (int, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(normalizeControllerAccessibleEndpoint(endpoint)), "/")
	if endpoint == "" {
		return 0, fmt.Errorf("empty embedding endpoint")
	}
	client := &http.Client{Timeout: 10 * time.Second}

	// TEI 原生接口
	raw, status, err := postJSON(ctx, client, endpoint+"/embed", map[string]interface{}{"inputs": text})
	if err == nil && status >= 200 && status < 300 {
		if dim := parseEmbeddingDimension(raw); dim > 0 {
			return dim, nil
		}
	}

	// OpenAI 兼容接口兜底
	raw, status, err = postJSON(ctx, client, endpoint+"/v1/embeddings", map[string]interface{}{"input": []string{text}})
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("embedding http status=%d body=%s", status, string(raw))
	}
	dim := parseEmbeddingDimension(raw)
	if dim <= 0 {
		return 0, fmt.Errorf("cannot parse embedding dimension from response")
	}
	return dim, nil
}

func postJSON(ctx context.Context, client *http.Client, url string, payload interface{}) ([]byte, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func parseEmbeddingDimension(raw []byte) int {
	// /embed: [[...]] 或 [...]
	var vector []float64
	if err := json.Unmarshal(raw, &vector); err == nil && len(vector) > 0 {
		return len(vector)
	}
	var matrix [][]float64
	if err := json.Unmarshal(raw, &matrix); err == nil && len(matrix) > 0 && len(matrix[0]) > 0 {
		return len(matrix[0])
	}
	// openai 风格
	var envelope struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Data) > 0 {
		return len(envelope.Data[0].Embedding)
	}
	return 0
}

func mustJSONString(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
