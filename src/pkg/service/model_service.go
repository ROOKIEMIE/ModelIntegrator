package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"ModelIntegrator/src/pkg/adapter"
	"ModelIntegrator/src/pkg/model"
	"ModelIntegrator/src/pkg/registry"
	"ModelIntegrator/src/pkg/scheduler"
)

var ErrModelNotFound = errors.New("model not found")

type ModelService struct {
	modelRegistry *registry.ModelRegistry
	nodeRegistry  *registry.NodeRegistry
	scheduler     *scheduler.Scheduler
	adapters      *adapter.Manager
	logger        *slog.Logger
	refreshMu     sync.Mutex
	modelRootDir  string
	nodeActionMu  sync.Mutex
	nodeActionMap map[string]bool
}

func NewModelService(
	modelRegistry *registry.ModelRegistry,
	nodeRegistry *registry.NodeRegistry,
	scheduler *scheduler.Scheduler,
	adapters *adapter.Manager,
	logger *slog.Logger,
	modelRootDir string,
) *ModelService {
	return &ModelService{
		modelRegistry: modelRegistry,
		nodeRegistry:  nodeRegistry,
		scheduler:     scheduler,
		adapters:      adapters,
		logger:        logger,
		modelRootDir:  modelRootDir,
		nodeActionMap: make(map[string]bool),
	}
}

func (s *ModelService) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = s.RefreshModels(ctx)
	return s.modelRegistry.List(), nil
}

func (s *ModelService) GetModel(ctx context.Context, id string) (model.Model, error) {
	_ = ctx
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return model.Model{}, ErrModelNotFound
	}
	return m, nil
}

func (s *ModelService) LoadModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "load")
}

func (s *ModelService) UnloadModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "unload")
}

func (s *ModelService) StartModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "start")
}

func (s *ModelService) StopModel(ctx context.Context, id string) (model.ActionResult, error) {
	return s.executeAction(ctx, id, "stop")
}

func (s *ModelService) StartAutoRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	go func() {
		s.runRefreshWithTimeout(15 * time.Second)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runRefreshWithTimeout(15 * time.Second)
			}
		}
	}()
}

func (s *ModelService) RefreshModels(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	_ = s.refreshLocalModels(ctx)
	return s.refreshLMStudioModels(ctx)
}

func (s *ModelService) runRefreshWithTimeout(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.RefreshModels(ctx); err != nil {
		s.logger.Warn("后台模型刷新失败", "error", err)
	}
}

func (s *ModelService) executeAction(ctx context.Context, id, action string) (model.ActionResult, error) {
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return model.ActionResult{}, ErrModelNotFound
	}
	nodeID := strings.TrimSpace(m.HostNodeID)
	if nodeID == "" {
		nodeID = "unknown"
	}
	if !s.tryLockNodeAction(nodeID) {
		return actionResult(false, "当前节点正在执行其他模型操作，请稍后重试", map[string]interface{}{
			"node_id":  nodeID,
			"model_id": m.ID,
			"action":   action,
		}), nil
	}
	defer s.unlockNodeAction(nodeID)

	adapterInstance, err := s.adapters.Get(m.BackendType)
	if err != nil {
		return actionResult(false, err.Error(), map[string]interface{}{"backend_type": m.BackendType}), nil
	}

	if action == "load" || action == "start" {
		if canRun, reason := s.scheduler.CanRun(m); !canRun {
			return actionResult(false, reason, map[string]interface{}{"model_id": m.ID}), nil
		}
	}

	result := model.ActionResult{}
	switch action {
	case "load":
		result, err = adapterInstance.LoadModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateLoaded
			s.scheduler.MarkRunning(m)
		}
	case "unload":
		result, err = adapterInstance.UnloadModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateStopped
			s.scheduler.MarkStopped(m.ID)
		}
	case "start":
		result, err = adapterInstance.StartModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateRunning
			s.scheduler.MarkRunning(m)
		}
	case "stop":
		result, err = adapterInstance.StopModel(ctx, m)
		if result.Success {
			m.State = model.ModelStateStopped
			s.scheduler.MarkStopped(m.ID)
		}
	default:
		return model.ActionResult{}, fmt.Errorf("不支持的动作: %s", action)
	}

	if err != nil {
		s.logger.Error("调用适配器失败", "action", action, "model_id", m.ID, "error", err)
		return actionResult(false, "适配器调用失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}

	s.modelRegistry.Upsert(m)
	return result, nil
}

func actionResult(success bool, message string, detail map[string]interface{}) model.ActionResult {
	return model.ActionResult{
		Success:   success,
		Message:   message,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}

func (s *ModelService) tryLockNodeAction(nodeID string) bool {
	s.nodeActionMu.Lock()
	defer s.nodeActionMu.Unlock()
	if s.nodeActionMap[nodeID] {
		return false
	}
	s.nodeActionMap[nodeID] = true
	return true
}

func (s *ModelService) unlockNodeAction(nodeID string) {
	s.nodeActionMu.Lock()
	defer s.nodeActionMu.Unlock()
	delete(s.nodeActionMap, nodeID)
}

func (s *ModelService) refreshLMStudioModels(ctx context.Context) error {
	adapterInstance, err := s.adapters.Get(model.RuntimeTypeLMStudio)
	if err != nil {
		return nil
	}

	remoteModels, err := adapterInstance.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("刷新 LM Studio 模型失败: %w", err)
	}

	hostNodeID, runtimeID, endpoint := s.resolveRuntimeBinding(model.RuntimeTypeLMStudio)
	normalized := make([]model.Model, 0, len(remoteModels))
	for _, item := range remoteModels {
		m := item
		if m.ID == "" {
			m.ID = m.Name
		}
		if m.Name == "" {
			m.Name = m.ID
		}
		if m.Provider == "" {
			m.Provider = "lmstudio"
		}
		m.BackendType = model.RuntimeTypeLMStudio
		if m.HostNodeID == "" {
			m.HostNodeID = hostNodeID
		}
		if m.RuntimeID == "" {
			m.RuntimeID = runtimeID
		}
		if m.Endpoint == "" {
			m.Endpoint = endpoint
		}
		if m.State == "" {
			m.State = model.ModelStateUnknown
		}
		if m.State == model.ModelStateUnknown {
			if existing, ok := s.modelRegistry.Get(m.ID); ok && existing.State != "" {
				m.State = existing.State
			}
		}
		if m.Metadata == nil {
			m.Metadata = map[string]string{}
		}
		m.Metadata["source"] = "lmstudio-refresh"
		normalized = append(normalized, m)
	}

	s.modelRegistry.ReplaceByBackend(model.RuntimeTypeLMStudio, normalized)
	s.logger.Debug("LM Studio 模型刷新完成", "count", len(normalized))
	return nil
}

func (s *ModelService) resolveRuntimeBinding(runtimeType model.RuntimeType) (hostNodeID, runtimeID, endpoint string) {
	nodes := s.nodeRegistry.List()
	for _, n := range nodes {
		for _, rt := range n.Runtimes {
			if rt.Type == runtimeType && rt.Enabled {
				return n.ID, rt.ID, rt.Endpoint
			}
		}
	}
	return "", "", ""
}

func (s *ModelService) refreshLocalModels(ctx context.Context) error {
	_ = ctx
	root := strings.TrimSpace(s.modelRootDir)
	if root == "" {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("扫描本地模型目录失败 (%s): %w", root, err)
	}

	hostNodeID, runtimeID, endpoint := s.resolveRuntimeBinding(model.RuntimeTypeDocker)
	if hostNodeID == "" {
		nodes := s.nodeRegistry.List()
		if len(nodes) > 0 {
			hostNodeID = nodes[0].ID
		}
	}

	scanned := make([]model.Model, 0)
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}

		path := filepath.Join(root, name)
		modelName := normalizeModelName(name)
		if modelName == "" {
			continue
		}
		modelID := "local-" + slugify(modelName)

		scanned = append(scanned, model.Model{
			ID:          modelID,
			Name:        modelName,
			Provider:    "localfs",
			BackendType: model.RuntimeTypeDocker,
			HostNodeID:  hostNodeID,
			RuntimeID:   runtimeID,
			Endpoint:    endpoint,
			State:       model.ModelStateStopped,
			Metadata: map[string]string{
				"source": "local-scan",
				"path":   path,
			},
		})
	}

	s.modelRegistry.ReplaceBySource("local-scan", scanned)
	s.logger.Debug("本地模型目录刷新完成", "count", len(scanned), "root", root)
	return nil
}

func normalizeModelName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	base := name
	switch ext {
	case ".gguf", ".bin", ".safetensors", ".onnx", ".pt", ".pth":
		base = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return strings.TrimSpace(base)
}

func slugify(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	var b strings.Builder
	lastDash := false
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "model"
	}
	return out
}
