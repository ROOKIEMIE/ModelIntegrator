package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ModelIntegrator/src/pkg/health"
	"ModelIntegrator/src/pkg/service"
	"ModelIntegrator/src/pkg/version"
)

type Handler struct {
	nodeService  *service.NodeService
	modelService *service.ModelService
	logger       *slog.Logger
	version      version.Info
}

func NewHandler(nodeService *service.NodeService, modelService *service.ModelService, logger *slog.Logger, v version.Info) *Handler {
	return &Handler{
		nodeService:  nodeService,
		modelService: modelService,
		logger:       logger,
		version:      v,
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
