package api

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(h *Handler, staticDir, authToken string, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(requestLogger(logger))

	r.Get("/healthz", h.Healthz)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(bearerAuthMiddleware(authToken, logger))
		r.Get("/version", h.GetVersion)
		r.Get("/nodes", h.ListNodes)
		r.Get("/models", h.ListModels)
		r.Get("/models/{id}", h.GetModel)
		r.Post("/models/{id}/load", h.LoadModel)
		r.Post("/models/{id}/unload", h.UnloadModel)
		r.Post("/models/{id}/start", h.StartModel)
		r.Post("/models/{id}/stop", h.StopModel)
		r.Get("/runtime-templates", h.ListRuntimeTemplates)
		r.Post("/runtime-templates/validate", h.ValidateRuntimeTemplate)
		r.Post("/runtime-templates", h.RegisterRuntimeTemplate)
		r.Get("/agents", h.ListAgents)
		r.Post("/agents/register", h.RegisterAgent)
		r.Post("/agents/{id}/heartbeat", h.AgentHeartbeat)
		r.Post("/agents/{id}/capabilities", h.ReportAgentCapabilities)
		r.Get("/agents/{id}/tasks/next", h.PullAgentTask)
		r.Post("/agents/{id}/tasks/{taskID}/report", h.ReportAgentTask)
		// 兼容旧路由参数名，后续可移除。
		r.Post("/agents/{agentID}/heartbeat", h.AgentHeartbeat)
		r.Post("/agents/{agentID}/capabilities", h.ReportAgentCapabilities)
		r.Get("/agents/{agentID}/tasks/next", h.PullAgentTask)
		r.Post("/agents/{agentID}/tasks/{taskID}/report", h.ReportAgentTask)
		r.Post("/tasks/runtime/start", h.CreateRuntimeTaskStart)
		r.Post("/tasks/runtime/stop", h.CreateRuntimeTaskStop)
		r.Post("/tasks/runtime/restart", h.CreateRuntimeTaskRestart)
		r.Post("/tasks/runtime/refresh", h.CreateRuntimeTaskRefresh)
		r.Get("/tasks", h.ListTasks)
		r.Get("/tasks/{id}", h.GetTask)
		r.Post("/tasks/agent/runtime-readiness", h.CreateAgentRuntimeReadinessTask)
		r.Post("/test-runs", h.CreateTestRun)
		r.Get("/test-runs", h.ListTestRuns)
		r.Get("/test-runs/{id}", h.GetTestRun)
	})

	if staticDir == "" {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "controller is running")
		})
		return r
	}

	indexFile := filepath.Join(staticDir, "index.html")
	cssFile := filepath.Join(staticDir, "app.css")
	jsFile := filepath.Join(staticDir, "app.js")

	// 静态资源暂不强制鉴权，便于控制台页面加载；API 通过 /api/v1 统一鉴权保护。

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, indexFile)
	})
	r.Get("/console", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, indexFile)
	})
	r.Get("/app.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, cssFile)
	})
	r.Get("/app.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, jsFile)
	})

	return r
}

func requestLogger(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Info("http_request", "method", r.Method, "path", r.URL.Path, "elapsed", time.Since(start).String())
		})
	}
}
