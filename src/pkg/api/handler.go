package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ModelIntegrator/src/pkg/health"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/service"
	"ModelIntegrator/src/pkg/version"
)

type Handler struct {
	nodeService            *service.NodeService
	modelService           *service.ModelService
	runtimeTemplateService *service.RuntimeTemplateService
	logger                 *slog.Logger
	version                version.Info
}

func NewHandler(
	nodeService *service.NodeService,
	modelService *service.ModelService,
	runtimeTemplateService *service.RuntimeTemplateService,
	logger *slog.Logger,
	v version.Info,
) *Handler {
	return &Handler{
		nodeService:            nodeService,
		modelService:           modelService,
		runtimeTemplateService: runtimeTemplateService,
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
	models, err := h.modelService.ListModels(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "获取模型列表失败", err.Error())
		return
	}
	OK(w, models)
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
