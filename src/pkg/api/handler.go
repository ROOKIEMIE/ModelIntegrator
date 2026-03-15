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
	"time"

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
	runtimeObjectService   *service.RuntimeObjectService
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
	runtimeObjectService *service.RuntimeObjectService,
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
		runtimeObjectService:   runtimeObjectService,
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

func (h *Handler) ListRuntimeBindings(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	items, err := h.runtimeObjectService.ListBindings(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询 runtime bindings 失败", err.Error())
		return
	}
	OK(w, items)
}

func (h *Handler) CreateRuntimeBinding(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	item, err := parseRuntimeBindingPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "runtime binding 请求体错误", err.Error())
		return
	}
	created, err := h.runtimeObjectService.CreateBinding(r.Context(), item)
	if err != nil {
		Fail(w, http.StatusBadRequest, "创建 runtime binding 失败", err.Error())
		return
	}
	OK(w, created)
}

func (h *Handler) GetRuntimeBinding(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	item, err := h.runtimeObjectService.GetBinding(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrRuntimeBindingNotFound) {
			Fail(w, http.StatusNotFound, "runtime binding 不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询 runtime binding 失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) ListRuntimeInstances(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	items, err := h.runtimeObjectService.ListRuntimeInstances(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询 runtime instances 失败", err.Error())
		return
	}
	OK(w, items)
}

func (h *Handler) GetRuntimeInstance(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	item, err := h.runtimeObjectService.GetRuntimeInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrRuntimeInstanceNotFound) {
			Fail(w, http.StatusNotFound, "runtime instance 不存在", map[string]string{"id": id})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询 runtime instance 失败", err.Error())
		return
	}
	OK(w, item)
}

type runtimeInstanceStatusSummary struct {
	RuntimeInstance  model.RuntimeInstance                  `json:"runtime_instance"`
	RecentAgentTasks []model.Task                           `json:"recent_agent_tasks,omitempty"`
	DesiredState     string                                 `json:"desired_state,omitempty"`
	ObservedState    string                                 `json:"observed_state,omitempty"`
	PrecheckStatus   model.PrecheckOverallStatus            `json:"precheck_status,omitempty"`
	PrecheckGating   bool                                   `json:"precheck_gating"`
	PrecheckReasons  []string                               `json:"precheck_reasons,omitempty"`
	ConflictStatus   model.RuntimeConflictStatus            `json:"conflict_status,omitempty"`
	ConflictBlocking bool                                   `json:"conflict_blocking"`
	ConflictReasons  []string                               `json:"conflict_reasons,omitempty"`
	GatingStatus     model.RuntimeGatingStatus              `json:"gating_status,omitempty"`
	GatingAllowed    bool                                   `json:"gating_allowed"`
	GatingReasons    []string                               `json:"gating_reasons,omitempty"`
	LastPlanAction   model.RuntimeLifecycleAction           `json:"last_plan_action,omitempty"`
	LastPlanStatus   model.RuntimeLifecyclePlanStatus       `json:"last_plan_status,omitempty"`
	LastPlanReason   string                                 `json:"last_plan_reason,omitempty"`
	Readiness        model.ReadinessState                   `json:"readiness,omitempty"`
	HealthMessage    string                                 `json:"health_message,omitempty"`
	DriftReason      string                                 `json:"drift_reason,omitempty"`
	LastAgentTask    *model.RuntimeInstanceAgentTaskSummary `json:"last_agent_task,omitempty"`
	LastReconciledAt time.Time                              `json:"last_reconciled_at,omitempty"`
	ReconcileSummary model.RuntimeInstanceReconcileSummary  `json:"reconcile_summary,omitempty"`
}

func (h *Handler) GetRuntimeInstanceSummary(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	runtimeInstanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runtimeInstanceID == "" {
		Fail(w, http.StatusBadRequest, "runtime_instance_id 不能为空", nil)
		return
	}
	limit := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	instance, err := h.runtimeObjectService.GetRuntimeInstance(r.Context(), runtimeInstanceID)
	if err != nil {
		if errors.Is(err, service.ErrRuntimeInstanceNotFound) {
			Fail(w, http.StatusNotFound, "runtime instance 不存在", map[string]string{"id": runtimeInstanceID})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询 runtime instance 失败", err.Error())
		return
	}
	tasks, err := h.taskService.ListRuntimeInstanceAgentTasks(r.Context(), runtimeInstanceID, limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询 runtime instance tasks 失败", err.Error())
		return
	}
	reconcileSummary, err := h.runtimeObjectService.GetRuntimeInstanceReconcileSummary(r.Context(), runtimeInstanceID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询 runtime instance reconcile summary 失败", err.Error())
		return
	}
	OK(w, runtimeInstanceStatusSummary{
		RuntimeInstance:  instance,
		RecentAgentTasks: tasks,
		DesiredState:     instance.DesiredState,
		ObservedState:    instance.ObservedState,
		PrecheckStatus:   instance.PrecheckStatus,
		PrecheckGating:   instance.PrecheckGating,
		PrecheckReasons:  instance.PrecheckReasons,
		ConflictStatus:   model.RuntimeConflictStatus(firstNonEmpty(string(instance.ConflictStatus), string(reconcileSummary.ConflictStatus))),
		ConflictBlocking: instance.ConflictBlocking || reconcileSummary.ConflictBlocking,
		ConflictReasons:  mergeStringSlices(instance.ConflictReasons, reconcileSummary.ConflictReasons),
		GatingStatus:     model.RuntimeGatingStatus(firstNonEmpty(string(instance.GatingStatus), string(reconcileSummary.GatingStatus))),
		GatingAllowed:    instance.GatingAllowed || reconcileSummary.GatingAllowed,
		GatingReasons:    mergeStringSlices(instance.GatingReasons, reconcileSummary.GatingReasons),
		LastPlanAction:   firstNonEmptyLifecycleAction(instance.LastPlanAction, reconcileSummary.PlannedAction),
		LastPlanStatus:   instance.LastPlanStatus,
		LastPlanReason:   instance.LastPlanReason,
		Readiness:        instance.Readiness,
		HealthMessage:    instance.HealthMessage,
		DriftReason:      instance.DriftReason,
		LastAgentTask:    instance.LastAgentTask,
		LastReconciledAt: instance.LastReconciledAt,
		ReconcileSummary: reconcileSummary,
	})
}

func (h *Handler) GetRuntimeInstanceReconcileSummary(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	runtimeInstanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runtimeInstanceID == "" {
		Fail(w, http.StatusBadRequest, "runtime_instance_id 不能为空", nil)
		return
	}
	summary, err := h.runtimeObjectService.GetRuntimeInstanceReconcileSummary(r.Context(), runtimeInstanceID)
	if err != nil {
		if errors.Is(err, service.ErrRuntimeInstanceNotFound) {
			Fail(w, http.StatusNotFound, "runtime instance 不存在", map[string]string{"id": runtimeInstanceID})
			return
		}
		Fail(w, http.StatusInternalServerError, "查询 runtime instance reconcile summary 失败", err.Error())
		return
	}
	OK(w, summary)
}

func (h *Handler) ListRuntimeInstanceAgentTasks(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	runtimeInstanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	if runtimeInstanceID == "" {
		Fail(w, http.StatusBadRequest, "runtime_instance_id 不能为空", nil)
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.taskService.ListRuntimeInstanceAgentTasks(r.Context(), runtimeInstanceID, limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询 runtime instance agent tasks 失败", err.Error())
		return
	}
	OK(w, items)
}

func (h *Handler) GetRuntimeTemplateManifest(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObjectService == nil {
		Fail(w, http.StatusServiceUnavailable, "runtime object 服务未就绪", nil)
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	item, err := h.runtimeObjectService.GetTemplateManifest(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrRuntimeTemplateNotFound) || errors.Is(err, service.ErrRuntimeManifestNotFound) {
			Fail(w, http.StatusNotFound, "runtime template manifest 不存在", map[string]string{"template_id": id})
			return
		}
		Fail(w, http.StatusBadRequest, "查询 runtime template manifest 失败", err.Error())
		return
	}
	OK(w, item)
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
		var gatedErr *service.RuntimeActionGatedError
		if errors.As(err, &gatedErr) {
			Fail(w, http.StatusConflict, "runtime action blocked by gating", map[string]interface{}{
				"model_id":            strings.TrimSpace(gatedErr.ModelID),
				"runtime_instance_id": strings.TrimSpace(gatedErr.RuntimeInstanceID),
				"task_type":           strings.TrimSpace(string(gatedErr.TaskType)),
				"gating_status":       strings.TrimSpace(string(gatedErr.GatingStatus)),
				"gating_reasons":      gatedErr.GatingReasons,
			})
			return
		}
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
	runtimeInstanceID := strings.TrimSpace(r.URL.Query().Get("runtime_instance_id"))
	agentOnly := false
	if raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("agent_only"))); raw == "1" || raw == "true" || raw == "yes" {
		agentOnly = true
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.taskService.ListTasksFiltered(r.Context(), targetType, targetID, runtimeInstanceID, agentOnly, limit)
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

func (h *Handler) CreateAgentNodeLocalTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	req, err := parseAgentNodeLocalTaskPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent node-local task 请求体错误", err.Error())
		return
	}
	item, err := h.taskService.CreateAgentNodeTask(r.Context(), req)
	if err != nil {
		Fail(w, http.StatusBadRequest, "创建 agent node-local task 失败", err.Error())
		return
	}
	OK(w, item)
}

func (h *Handler) CreateAgentInstanceLocalTask(w http.ResponseWriter, r *http.Request) {
	if h.taskService == nil {
		Fail(w, http.StatusServiceUnavailable, "task 服务未就绪", nil)
		return
	}
	req, err := parseAgentNodeLocalTaskPayload(r)
	if err != nil {
		Fail(w, http.StatusBadRequest, "agent instance-local task 请求体错误", err.Error())
		return
	}
	if strings.TrimSpace(req.RuntimeInstanceID) == "" {
		Fail(w, http.StatusBadRequest, "runtime_instance_id 不能为空", nil)
		return
	}
	if strings.TrimSpace(req.TaskScope) == "" {
		req.TaskScope = "runtime_instance"
	}
	item, err := h.taskService.CreateAgentNodeTask(r.Context(), req)
	if err != nil {
		Fail(w, http.StatusBadRequest, "创建 agent instance-local task 失败", err.Error())
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

func (h *Handler) ListTestRunScenarios(w http.ResponseWriter, r *http.Request) {
	if h.testRunService == nil {
		Fail(w, http.StatusServiceUnavailable, "test run 服务未就绪", nil)
		return
	}
	OK(w, h.testRunService.ListScenarios())
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

type runtimeBindingPayload struct {
	ID              string            `json:"id"`
	ModelID         string            `json:"model_id"`
	TemplateID      string            `json:"template_id"`
	BindingMode     string            `json:"binding_mode"`
	NodeSelector    map[string]string `json:"node_selector"`
	PreferredNode   string            `json:"preferred_node"`
	MountRules      []string          `json:"mount_rules"`
	EnvOverrides    map[string]string `json:"env_overrides"`
	CommandOverride []string          `json:"command_override"`
	ScriptRef       string            `json:"script_ref"`
	Enabled         *bool             `json:"enabled"`
	ManifestID      string            `json:"manifest_id"`
	Metadata        map[string]string `json:"metadata"`
}

func parseRuntimeBindingPayload(r *http.Request) (model.RuntimeBinding, error) {
	var payload runtimeBindingPayload
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return model.RuntimeBinding{}, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return model.RuntimeBinding{}, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return model.RuntimeBinding{}, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	modelID := strings.TrimSpace(payload.ModelID)
	templateID := strings.TrimSpace(payload.TemplateID)
	if modelID == "" || templateID == "" {
		return model.RuntimeBinding{}, fmt.Errorf("model_id/template_id 不能为空")
	}
	mode := strings.TrimSpace(strings.ToLower(payload.BindingMode))
	if mode == "" {
		mode = string(model.RuntimeBindingModeGenericInjected)
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	return model.RuntimeBinding{
		ID:              strings.TrimSpace(payload.ID),
		ModelID:         modelID,
		TemplateID:      templateID,
		BindingMode:     model.RuntimeBindingMode(mode),
		NodeSelector:    payload.NodeSelector,
		PreferredNode:   strings.TrimSpace(payload.PreferredNode),
		MountRules:      payload.MountRules,
		EnvOverrides:    payload.EnvOverrides,
		CommandOverride: payload.CommandOverride,
		ScriptRef:       strings.TrimSpace(payload.ScriptRef),
		Enabled:         enabled,
		ManifestID:      strings.TrimSpace(payload.ManifestID),
		Metadata:        payload.Metadata,
	}, nil
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
	RuntimeInstanceID string                 `json:"runtime_instance_id"`
	RuntimeBindingID  string                 `json:"runtime_binding_id"`
	RuntimeTemplateID string                 `json:"runtime_template_id"`
	ManifestID        string                 `json:"manifest_id"`
	AgentID           string                 `json:"agent_id"`
	NodeID            string                 `json:"node_id"`
	ModelID           string                 `json:"model_id"`
	TargetID          string                 `json:"target_id"`
	TaskScope         string                 `json:"task_scope"`
	PayloadContext    map[string]interface{} `json:"payload_context"`
	ResolvedContext   map[string]interface{} `json:"resolved_context"`
	Endpoint          string                 `json:"endpoint"`
	HealthPath        string                 `json:"health_path"`
	TimeoutSeconds    int                    `json:"timeout_seconds"`
	TriggeredBy       string                 `json:"triggered_by"`
}

type agentNodeLocalTaskPayload struct {
	AgentID           string                 `json:"agent_id"`
	NodeID            string                 `json:"node_id"`
	ModelID           string                 `json:"model_id"`
	RuntimeInstanceID string                 `json:"runtime_instance_id"`
	RuntimeBindingID  string                 `json:"runtime_binding_id"`
	RuntimeTemplateID string                 `json:"runtime_template_id"`
	ManifestID        string                 `json:"manifest_id"`
	TaskScope         string                 `json:"task_scope"`
	PayloadContext    map[string]interface{} `json:"payload_context"`
	ResolvedContext   map[string]interface{} `json:"resolved_context"`
	TaskType          string                 `json:"task_type"`
	Payload           map[string]interface{} `json:"payload"`
	TriggeredBy       string                 `json:"triggered_by"`
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
	runtimeInstanceID := strings.TrimSpace(payload.RuntimeInstanceID)
	if runtimeInstanceID == "" && modelID == "" {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("runtime_instance_id/model_id 不能为空")
	}
	if strings.TrimSpace(payload.AgentID) == "" && strings.TrimSpace(payload.NodeID) == "" && runtimeInstanceID == "" {
		return service.AgentRuntimeReadinessTaskRequest{}, fmt.Errorf("agent_id/node_id 不能为空（未提供 runtime_instance_id 时）")
	}
	return service.AgentRuntimeReadinessTaskRequest{
		RuntimeInstanceID: runtimeInstanceID,
		AgentID:           strings.TrimSpace(payload.AgentID),
		NodeID:            strings.TrimSpace(payload.NodeID),
		ModelID:           modelID,
		Endpoint:          strings.TrimSpace(payload.Endpoint),
		HealthPath:        strings.TrimSpace(payload.HealthPath),
		TimeoutSeconds:    payload.TimeoutSeconds,
		TriggeredBy:       strings.TrimSpace(payload.TriggeredBy),
	}, nil
}

func parseAgentNodeLocalTaskPayload(r *http.Request) (service.AgentNodeLocalTaskRequest, error) {
	var payload agentNodeLocalTaskPayload
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("读取请求体失败: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("请求体不能为空")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	taskType := model.TaskType(strings.TrimSpace(payload.TaskType))
	switch taskType {
	case model.TaskTypeAgentRuntimeReadiness,
		model.TaskTypeAgentRuntimePrecheck,
		model.TaskTypeAgentPortCheck,
		model.TaskTypeAgentModelPathCheck,
		model.TaskTypeAgentResourceSnapshot,
		model.TaskTypeAgentDockerInspect,
		model.TaskTypeAgentDockerStart,
		model.TaskTypeAgentDockerStop:
	default:
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("不支持的 agent task_type: %s", payload.TaskType)
	}
	if strings.TrimSpace(payload.RuntimeInstanceID) == "" && strings.TrimSpace(payload.AgentID) == "" && strings.TrimSpace(payload.NodeID) == "" {
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("agent_id / node_id / runtime_instance_id 至少需要一个")
	}
	if strings.TrimSpace(payload.RuntimeInstanceID) == "" && strings.TrimSpace(payload.ModelID) == "" && strings.TrimSpace(payload.NodeID) == "" {
		return service.AgentNodeLocalTaskRequest{}, fmt.Errorf("runtime_instance_id/model_id/node_id 至少需要一个")
	}
	return service.AgentNodeLocalTaskRequest{
		AgentID:           strings.TrimSpace(payload.AgentID),
		NodeID:            strings.TrimSpace(payload.NodeID),
		ModelID:           strings.TrimSpace(payload.ModelID),
		RuntimeInstanceID: strings.TrimSpace(payload.RuntimeInstanceID),
		RuntimeBindingID:  strings.TrimSpace(payload.RuntimeBindingID),
		RuntimeTemplateID: strings.TrimSpace(payload.RuntimeTemplateID),
		ManifestID:        strings.TrimSpace(payload.ManifestID),
		TaskScope:         strings.TrimSpace(payload.TaskScope),
		PayloadContext:    payload.PayloadContext,
		ResolvedContext:   payload.ResolvedContext,
		TaskType:          taskType,
		Payload:           payload.Payload,
		TriggeredBy:       strings.TrimSpace(payload.TriggeredBy),
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

func mergeStringSlices(left, right []string) []string {
	out := make([]string, 0, len(left)+len(right))
	seen := map[string]struct{}{}
	appendSlice := func(values []string) {
		for _, raw := range values {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	appendSlice(left)
	appendSlice(right)
	return out
}

func firstNonEmptyLifecycleAction(primary, fallback model.RuntimeLifecycleAction) model.RuntimeLifecycleAction {
	if strings.TrimSpace(string(primary)) != "" && primary != model.RuntimeLifecycleActionNone {
		return primary
	}
	if strings.TrimSpace(string(fallback)) != "" {
		return fallback
	}
	return model.RuntimeLifecycleActionNone
}

func runtimeTemplateIsZero(tpl model.RuntimeTemplate) bool {
	return tpl.ID == "" &&
		tpl.Name == "" &&
		tpl.Description == "" &&
		tpl.TemplateType == "" &&
		tpl.RuntimeKind == "" &&
		len(tpl.SupportedModelTypes) == 0 &&
		len(tpl.SupportedFormats) == 0 &&
		len(tpl.Capabilities) == 0 &&
		tpl.ComposeRef == "" &&
		tpl.ImageRef == "" &&
		len(tpl.CommandTemplate) == 0 &&
		len(tpl.InjectableMounts) == 0 &&
		len(tpl.InjectableEnv) == 0 &&
		len(tpl.ExposedPorts) == 0 &&
		tpl.RuntimeType == "" &&
		tpl.Image == "" &&
		len(tpl.Command) == 0 &&
		len(tpl.Env) == 0 &&
		len(tpl.Volumes) == 0 &&
		len(tpl.Ports) == 0 &&
		len(tpl.Metadata) == 0 &&
		tpl.Manifest == nil
}
