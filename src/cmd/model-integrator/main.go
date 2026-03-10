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
	gpuReport := preflight.DetectGPU(context.Background(), resolveDockerProbeEndpoint(cfg))
	preflight.LogGPUReport(log, gpuReport)
	applyNodePlatformInfo(cfg, gpuReport)

	if err := storage.EnsureSQLitePath(cfg.Storage.SQLitePath); err != nil {
		log.Warn("SQLite 路径准备失败", "path", cfg.Storage.SQLitePath, "error", err)
	} else {
		log.Info("SQLite 路径准备完成", "path", cfg.Storage.SQLitePath)
	}
	if err := storage.EnsureDirectory(cfg.Storage.ModelRootDir); err != nil {
		log.Warn("模型目录检查失败", "path", cfg.Storage.ModelRootDir, "error", err)
	} else {
		log.Info("模型目录就绪", "path", cfg.Storage.ModelRootDir)
	}

	nodeRegistry := registry.NewNodeRegistry(cfg.Nodes)
	modelRegistry := registry.NewModelRegistry(cfg.Models)
	templateRegistry := registry.NewRuntimeTemplateRegistry(nil)

	schedulerInstance := scheduler.NewScheduler()
	for _, m := range cfg.Models {
		if group, ok := m.Metadata["mutex_group"]; ok && group != "" {
			schedulerInstance.SetPolicy(m.ID, scheduler.ModelPolicy{MutualExclusionGroup: group})
		}
	}

	adapterManager := adapter.NewManager()
	lmstudioAdapter := lmstudio.NewAdapter(
		cfg.Integrations.LMStudio.Endpoint,
		cfg.Integrations.LMStudio.Token,
		15*time.Second,
		cfg.Integrations.LMStudio.CacheEnabled,
		time.Duration(cfg.Integrations.LMStudio.CacheRefreshSeconds)*time.Second,
	)
	adapterManager.Register(model.RuntimeTypeLMStudio, lmstudioAdapter)
	lmstudioAdapter.StartCacheSync()
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

	runtimeTemplateService := service.NewRuntimeTemplateService(templateRegistry, log)
	runtimeTemplateService.RegisterBuiltins()
	if err := runtimeTemplateService.RegisterFromConfig(context.Background(), cfg.RuntimeTemplates); err != nil {
		log.Error("运行时模板配置校验失败", "error", err)
		os.Exit(1)
	}

	nodeService := service.NewNodeService(nodeRegistry, adapterManager, log)
	modelService := service.NewModelService(modelRegistry, nodeRegistry, runtimeTemplateService, schedulerInstance, adapterManager, log, cfg.Storage.ModelRootDir)

	handler := api.NewHandler(nodeService, modelService, runtimeTemplateService, log, version.Get())
	router := api.NewRouter(handler, cfg.Server.StaticDir, cfg.Auth.Token, log)

	httpServer := server.New(cfg, router, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	modelService.StartAutoRefresh(ctx, time.Duration(cfg.Integrations.LMStudio.CacheRefreshSeconds)*time.Second)

	if err := httpServer.Start(ctx); err != nil {
		log.Error("服务退出", "error", err)
		os.Exit(1)
	}

	log.Info("服务已安全退出")
}

func applyNodePlatformInfo(cfg *config.Config, report preflight.GPUReport) {
	for i := range cfg.Nodes {
		node := &cfg.Nodes[i]
		node.Platform = model.PlatformInfo{
			Accelerator: "unknown",
			GPU:         "unknown",
			CUDAVersion: "unknown",
			Driver:      "unknown",
		}

		if !hasEnabledRuntime(node, model.RuntimeTypeDocker) {
			continue
		}

		if report.CUDAAvailable {
			node.Platform.Accelerator = "nvidia-cuda"
			if report.GPUName != "" {
				node.Platform.GPU = report.GPUName
			}
			if report.CUDAVersion != "" {
				node.Platform.CUDAVersion = report.CUDAVersion
			}
			if report.DriverVersion != "" {
				node.Platform.Driver = report.DriverVersion
			}
		}
	}
}

func hasEnabledRuntime(node *model.Node, runtimeType model.RuntimeType) bool {
	for _, rt := range node.Runtimes {
		if rt.Type == runtimeType && rt.Enabled {
			return true
		}
	}
	return false
}

func resolveDockerProbeEndpoint(cfg *config.Config) string {
	for i := range cfg.Nodes {
		node := cfg.Nodes[i]
		for _, rt := range node.Runtimes {
			if rt.Type == model.RuntimeTypeDocker && rt.Enabled && rt.Endpoint != "" {
				return rt.Endpoint
			}
		}
	}
	return cfg.Integrations.Docker.Endpoint
}
