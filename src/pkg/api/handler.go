package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"model-control-plane/src/pkg/health"
	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/service"
	"model-control-plane/src/pkg/version"
)

type Handler struct {
	nodeService            *service.NodeService
	modelService           *service.ModelService
	runtimeTemplateService *service.RuntimeTemplateService
	agentService           *service.AgentService
	taskService            *service.TaskService
	testRunService         *service.TestRunService
	logger                 *slog.Logger
	version                version.Info
}

func NewHandler(
	nodeService *service.NodeService,
	modelService *service.ModelService,
	runtimeTemplateService *service.RuntimeTemplateService,
	agentService *service.AgentService,
	taskService *service.TaskService,
	testRunService *service.TestRunService,
	logger *slog.Logger,
	v version.Info,
) *Handler {
	return &Handler{
		nodeService:            nodeService,
		modelService:           modelService,
		runtimeTemplateService: runtimeTemplateService,
		agentService:           agentService,
		taskService:            taskService,
		testRunService:         testRunService,
		logger:                 logger,
		version:                v,
	}
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	OK(w, health.NewStatus(h.version))
}

func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
	OK(w, h.version)
}

func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.nodeService.ListNodes(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "获取节点列表失败", err.Error())
		return
	}
	OK(w, nodes)
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	if shouldRefreshModels(r) {
		if err := h.modelService.RefreshModels(r.Context()); err != nil {
			h.logger.Warn("手动刷新模型失败，返回缓存结果", "path", r.URL.Path, "error", err)
		}
	}

	models, err := h.modelService.ListModels(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "获取模型列表失败", err.Error())
		return
	}
	OK(w, models)
}

func shouldRefreshModels(r *http.Request) bool {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("refresh")))
	return raw == "1" || raw == "true" || raw == "yes"
}

func (h *Handler) GetModel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.modelService.GetModel(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrModelNotFound) {
			Fail(w, http.StatusNotFound, "模型不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询模型失败", err.Error())
		return
	}
	OK(w, m)
}

func (h *Handler) LoadModel(w http.ResponseWriter, r *http.Request) {
	h.modelAction(w, r, "load")
}

func (h *Handler) UnloadModel(w http.ResponseWriter, r *http.Request) {
	h.modelAction(w, r, "unload")
}

func (h *Handler) StartModel(w http.ResponseWriter, r *http.Request) {
	h.modelAction(w, r, "start")
}

func (h *Handler) StopModel(w http.ResponseWriter, r *http.Request) {
	h.modelAction(w, r, "stop")
}

func (h *Handler) ListRuntimeTemplates(w http.ResponseWriter, r *http.Request) {
	if h.runtimeTemplateService == nil {
		Fail(w, http.StatusServiceUnavailable, "运行时模板服务未就绪", nil)
		return
	}
	templates := h.runtimeTemplateService.ListTemplates(r.Context())
	OK(w, templates)
}

func (h *Handler) ValidateRuntimeTemplate(w http.ResponseWriter, r *http.Request) {
	if h.runtimeTemplateService == nil {
		Fail(w, http.StatusServiceUnavailable, "运行时模板服务未就绪", nil)
		return
	}
	tpl, err := parseTemplatePayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "请求体格式错误", err.Error())
		return
	}
	res := h.runtimeTemplateService.ValidateTemplate(tpl)
	OK(w, res)
}

func (h *Handler) RegisterRuntimeTemplate(w http.ResponseWriter, r *http.Request) {
	if h.runtimeTemplateService == nil {
		Fail(w, http.StatusServiceUnavailable, "运行时模板服务未就绪", nil)
		return
	}
	tpl, err := parseTemplatePayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "请求体格式错误", err.Error())
		return
	}
	res := h.runtimeTemplateService.RegisterTemplate(r.Context(), tpl)
	if !res.Valid {
		Fail(w, http.StatusBadRequest, "运行时模板校验失败", res)
		return
	}
	OK(w, res)
}

func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request) {
	if h.agentService == nil {
		Fail(w, http.StatusServiceUnavailable, "agent 服务未就绪", nil)
		return
	}
	OK(w, h.agentService.List(r.Context()))
}

func (h *Handler) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	if h.agentService == nil {
		Fail(w, http.StatusServiceUnavailable, "agent 服务未就绪", nil)
		return
	}
	req, err := parseAgentRegisterPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent 注册请求体错误", err.Error())
		return
	}
	res, err := h.agentService.Register(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidAgent) {
			Fail(w, http.StatusBadRequest, "agent 注册参数错误", err.Error())
			return
		}
		Fail(w, http.StatusInternalServerError, "agent 注册失败", err.Error())
		return
	}
	OK(w, res)
}

func (h *Handler) AgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if h.agentService == nil {
		Fail(w, http.StatusServiceUnavailable, "agent 服务未就绪", nil)
		return
	}
	agentID := agentIDFromPath(r)
	req, err := parseAgentHeartbeatPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent 心跳请求体错误", err.Error())
		return
	}
	res, err := h.agentService.Heartbeat(r.Context(), agentID, req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidAgent):
			Fail(w, http.StatusBadRequest, "agent 参数错误", err.Error())
		case errors.Is(err, service.ErrAgentNotFound):
			Fail(w, http.StatusNotFound, "agent 不存在", map[string]string{"agent_id": agentID})
		default:
			Fail(w, http.StatusInternalServerError, "agent 心跳失败", err.Error())
		}
		return
	}
	OK(w, res)
}

func (h *Handler) ReportAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	if h.agentService == nil {
		Fail(w, http.StatusServiceUnavailable, "agent 服务未就绪", nil)
		return
	}
	agentID := agentIDFromPath(r)
	req, err := parseAgentCapabilityPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent 能力上报请求体错误", err.Error())
		return
	}
	res, err := h.agentService.ReportCapabilities(r.Context(), agentID, req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidAgent) {
			Fail(w, http.StatusBadRequest, "agent 能力上报参数错误", err.Error())
			return
		}
		Fail(w, http.StatusInternalServerError, "agent 能力上报失败", err.Error())
		return
	}
	OK(w, res)
}

func (h *Handler) CreateRuntimeTaskStart(w http.ResponseWriter, r *http.Request) {
	h.createRuntimeTask(w, r, model.TaskTypeRuntimeStart)
}

func (h *Handler) CreateRuntimeTaskStop(w http.ResponseWriter, r *http.Request) {
	h.createRuntimeTask(w, r, model.TaskTypeRuntimeStop)
}

func (h *Handler) CreateRuntimeTaskRefresh(w http.ResponseWriter, r *http.Request) {
	h.createRuntimeTask(w, r, model.TaskTypeRuntimeRefresh)
}

func (h *Handler) CreateRuntimeTaskRestart(w http.ResponseWriter, r *http.Request) {
	h.createRuntimeTask(w, r, model.TaskTypeRuntimeRestart)
}

func (h *Handler) createRuntimeTask(w http.ResponseWriter, r *http.Request, taskType model.TaskType) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	req, err := parseRuntimeTaskPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "runtime task 请求体错误", err.Error())
		return
	}
	task, err := h.taskService.CreateRuntimeTask(r.Context(), taskType, req.ModelID, req.TriggeredBy)
	if err != nil {
		if errors.Is(err, service.ErrTaskStoreNotReady) {
			Fail(w, http.StatusServiceUnavailable, "task store 未就绪", err.Error())
			return
		}
		Fail(w, http.StatusBadRequest, "创建 runtime task 失败", err.Error())
		return
	}
	OK(w, task)
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	targetType := strings.TrimSpace(r.URL.Query().Get("target_type"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.taskService.ListTasks(r.Context(), targetType, targetID, limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询任务列表失败", err.Error())
		return
	}
	OK(w, items)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	item, err := h.taskService.GetTask(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			Fail(w, http.StatusNotFound, "任务不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询任务失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) CreateAgentRuntimeReadinessTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	req, err := parseAgentRuntimeTaskPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent task 请求体错误", err.Error())
		return
	}
	item, err := h.taskService.CreateAgentRuntimeReadinessTask(r.Context(), req)
	if err != nil {
		Fail(w, http.StatusBadRequest, "创建 agent task 失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) PullAgentTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	agentID := agentIDFromPath(r)
	item, ok, err := h.taskService.PullNextAgentTask(r.Context(), agentID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "拉取 agent task 失败", err.Error())
		return
	}
	if !ok {
		OK(w, map[string]interface{}{"task": nil})
		return
	}
	OK(w, map[string]interface{}{"task": item})
}

func (h *Handler) ReportAgentTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	agentID := agentIDFromPath(r)
	taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
	report, err := parseAgentTaskReportPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent task 上报请求体错误", err.Error())
		return
	}
	item, err := h.taskService.ReportAgentTask(r.Context(), agentID, taskID, report)
	if err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			Fail(w, http.StatusNotFound, "任务不存在", map[string]string{"task_id": taskID})
			return
		}
		Fail(w, http.StatusBadRequest, "agent task 上报失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) CreateTestRun(w http.ResponseWriter, r *http.Request) {
	if h.testRunService == nil {
		Fail(w, http.StatusServiceUnavailable, "test run 服务未就绪", nil)
		return
	}
	req, err := parseCreateTestRunPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "test run 请求体错误", err.Error())
		return
	}
	item, err := h.testRunService.CreateAndStart(r.Context(), req)
	if err != nil {
		Fail(w, http.StatusBadRequest, "创建测试运行失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) ListTestRuns(w http.ResponseWriter, r *http.Request) {
	if h.testRunService == nil {
		Fail(w, http.StatusServiceUnavailable, "test run 服务未就绪", nil)
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.testRunService.ListTestRuns(r.Context(), limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询测试运行列表失败", err.Error())
		return
	}
	OK(w, items)
}

func (h *Handler) GetTestRun(w http.ResponseWriter, r *http.Request) {
	if h.testRunService == nil {
		Fail(w, http.StatusServiceUnavailable, "test run 服务未就绪", nil)
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	item, err := h.testRunService.GetTestRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrTestRunNotFound) {
			Fail(w, http.StatusNotFound, "测试运行不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询测试运行失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) modelAction(w http.ResponseWriter, r *http.Request, action string) {
	id := chi.URLParam(r, "id")

	var (
		result interface{}
		err    error
	)

	switch action {
	case "load":
		result, err = h.modelService.LoadModel(r.Context(), id)
	case "unload":
		result, err = h.modelService.UnloadModel(r.Context(), id)
	case "start":
		result, err = h.modelService.StartModel(r.Context(), id)
	case "stop":
		result, err = h.modelService.StopModel(r.Context(), id)
	default:
		Fail(w, http.StatusBadRequest, "不支持的操作", map[string]string{"action": action})
		return
	}

	if err != nil {
		if errors.Is(err, service.ErrModelNotFound) {
			Fail(w, http.StatusNotFound, "模型不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "执行模型操作失败", err.Error())
		return
	}

	OK(w, result)
}

func parseTemplatePayload(r *http.Request) (model.RuntimeTemplate, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return model.RuntimeTemplate{}, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(body) == 0 {
		return model.RuntimeTemplate{}, fmt.Errorf("请求体不能为空")
	}

	var direct model.RuntimeTemplate
	if err := json.Unmarshal(body, &direct); err == nil && !runtimeTemplateIsZero(direct) {
		return direct, nil
	}

	var envelope struct {
		Template model.RuntimeTemplate `json:"template"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && !runtimeTemplateIsZero(envelope.Template) {
		return envelope.Template, nil
	}
	return model.RuntimeTemplate{}, fmt.Errorf("模板字段解析失败，期望 JSON 对象或 {\"template\": {...}}")
}

func parseAgentRegisterPayload(r *http.Request) (model.AgentRegisterRequest, error) {
	var payload model.AgentRegisterRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return payload, nil
}

func parseAgentHeartbeatPayload(r *http.Request) (model.AgentHeartbeatRequest, error) {
	var payload model.AgentHeartbeatRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return payload, nil
}

func parseAgentCapabilityPayload(r *http.Request) (model.AgentCapabilitiesReportRequest, error) {
	var payload model.AgentCapabilitiesReportRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return payload, nil
}

type runtimeTaskPayload struct {
	ModelID     string `json:"model_id"`
	TargetID    string `json:"target_id"`
	TriggeredBy string `json:"triggered_by"`
}

func parseRuntimeTaskPayload(r *http.Request) (runtimeTaskPayload, error) {
	var payload runtimeTaskPayload
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	payload.ModelID = strings.TrimSpace(firstNonEmpty(payload.ModelID, payload.TargetID))
	if payload.ModelID == "" {
		return payload, fmt.Errorf("model_id 不能为空")
	}
	return payload, nil
}

type agentRuntimeTaskPayload struct {
	AgentID        string `json:"agent_id"`
	ModelID        string `json:"model_id"`
	TargetID       string `json:"target_id"`
	Endpoint       string `json:"endpoint"`
	HealthPath     string `json:"health_path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	TriggeredBy    string `json:"triggered_by"`
}

func parseAgentRuntimeTaskPayload(r *http.Request) (service.AgentRuntimeReadinessTaskRequest, error) {
	var payload agentRuntimeTaskPayload
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	modelID := strings.TrimSpace(firstNonEmpty(payload.ModelID, payload.TargetID))
	if strings.TrimSpace(payload.AgentID) == "" || modelID == "" {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("agent_id/model_id 不能为空")
	}
	return service.AgentRuntimeReadinessTaskRequest{
		AgentID:        strings.TrimSpace(payload.AgentID),
		ModelID:        modelID,
		Endpoint:       strings.TrimSpace(payload.Endpoint),
		HealthPath:     strings.TrimSpace(payload.HealthPath),
		TimeoutSeconds: payload.TimeoutSeconds,
		TriggeredBy:    strings.TrimSpace(payload.TriggeredBy),
	}, nil
}

func parseAgentTaskReportPayload(r *http.Request) (service.AgentTaskReport, error) {
	var payload service.AgentTaskReport
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	if strings.TrimSpace(string(payload.Status)) == "" {
		return payload, fmt.Errorf("status 不能为空")
	}
	return payload, nil
}

func parseCreateTestRunPayload(r *http.Request) (service.CreateTestRunRequest, error) {
	var payload service.CreateTestRunRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return payload, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return payload, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	payload.Scenario = strings.TrimSpace(payload.Scenario)
	if payload.Scenario == "" {
		return payload, fmt.Errorf("scenario 不能为空")
	}
	return payload, nil
}

func agentIDFromPath(r *http.Request) string {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id != "" {
		return id
	}
	return strings.TrimSpace(chi.URLParam(r, "agentID"))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runtimeTemplateIsZero(tpl model.RuntimeTemplate) bool {
	return tpl.ID == "" &&
		tpl.Name == "" &&
		tpl.Description == "" &&
		tpl.RuntimeType == "" &&
		tpl.Image == "" &&
		len(tpl.Command) == 0 &&
		len(tpl.Env) == 0 &&
		len(tpl.Volumes) == 0 &&
		len(tpl.Ports) == 0 &&
		len(tpl.Metadata) == 0
}
