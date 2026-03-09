package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/adapter/dockerctl"
	"ModelIntegrator/src/pkg/adapter/lmstudio"
	"ModelIntegrator/src/pkg/api"
	"ModelIntegrator/src/pkg/config"
	"ModelIntegrator/src/pkg/logger"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/preflight"
	"ModelIntegrator/src/pkg/registry"
	"ModelIntegrator/src/pkg/scheduler"
	"ModelIntegrator/src/pkg/server"
	"ModelIntegrator/src/pkg/service"
	"ModelIntegrator/src/pkg/storage"
	"ModelIntegrator/src/pkg/version"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		os.Stderr.WriteString("加载配置失败: " + err.Error() + "\n")
		os.Exit(1)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format)
	log.Info("配置加载成功", "path", cfg.SourcePath)
	preflight.LogGPUReport(log, preflight.DetectGPU(context.Background()))

	if err := storage.EnsureSQLitePath(cfg.Storage.SQLitePath); err != nil {
		log.Warn("SQLite 路径准备失败", "path", cfg.Storage.SQLitePath, "error", err)
	} else {
		log.Info("SQLite 路径准备完成", "path", cfg.Storage.SQLitePath)
	}

	nodeRegistry := registry.NewNodeRegistry(cfg.Nodes)
	modelRegistry := registry.NewModelRegistry(cfg.Models)

	schedulerInstance := scheduler.NewScheduler()
	for _, m := range cfg.Models {
		if group, ok := m.Metadata["mutex_group"]; ok && group != "" {
			schedulerInstance.SetPolicy(m.ID, scheduler.ModelPolicy{MutualExclusionGroup: group})
		}
	}

	adapterManager := adapter.NewManager()
	adapterManager.Register(model.RuntimeTypeLMStudio, lmstudio.NewAdapter(
		cfg.Integrations.LMStudio.Endpoint,
		cfg.Integrations.LMStudio.Token,
		15*time.Second,
	))
	adapterManager.Register(model.RuntimeTypeDocker, dockerctl.NewAdapter(
		"dockerctl",
		cfg.Integrations.Docker.Endpoint,
		cfg.Integrations.Docker.Token,
	))
	adapterManager.Register(model.RuntimeTypePortainer, dockerctl.NewAdapter(
		"portainer",
		cfg.Integrations.Portainer.Endpoint,
		cfg.Integrations.Portainer.Token,
	))

	nodeService := service.NewNodeService(nodeRegistry)
	modelService := service.NewModelService(modelRegistry, nodeRegistry, schedulerInstance, adapterManager, log)

	handler := api.NewHandler(nodeService, modelService, log, version.Get())
	router := api.NewRouter(handler, cfg.Server.StaticDir, log)

	httpServer := server.New(cfg, router, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpServer.Start(ctx); err != nil {
		log.Error("服务退出", "error", err)
		os.Exit(1)
	}

	log.Info("服务已安全退出")
}
