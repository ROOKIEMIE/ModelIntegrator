package service

import (
	"context"
	"encoding/json"
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
	templates     *RuntimeTemplateService
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
	templates *RuntimeTemplateService,
	scheduler *scheduler.Scheduler,
	adapters *adapter.Manager,
	logger *slog.Logger,
	modelRootDir string,
) *ModelService {
	return &ModelService{
		modelRegistry: modelRegistry,
		nodeRegistry:  nodeRegistry,
		templates:     templates,
		scheduler:     scheduler,
		adapters:      adapters,
		logger:        logger,
		modelRootDir:  modelRootDir,
		nodeActionMap: make(map[string]bool),
	}
}

func (s *ModelService) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = ctx
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

	var refreshErrors []error
	recordStepErr := func(step string, err error) {
		if err == nil {
			return
		}
		s.logger.Warn("模型刷新子步骤失败", "step", step, "error", err)
		refreshErrors = append(refreshErrors, fmt.Errorf("%s: %w", step, err))
	}

	recordStepErr("refresh_local_models", s.refreshLocalModels(ctx))
	recordStepErr("refresh_container_runtime_states", s.refreshContainerRuntimeStates(ctx))
	recordStepErr("refresh_lmstudio_models", s.refreshLMStudioModels(ctx))

	return errors.Join(refreshErrors...)
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

	actionModel := m
	actionModel.Metadata = cloneMetadataMap(m.Metadata)
	runtimeEndpoint, runtimeToken := s.resolveRuntimeConnection(actionModel)
	if runtimeEndpoint != "" {
		actionModel.Endpoint = runtimeEndpoint
		actionModel.Metadata["runtime_endpoint"] = runtimeEndpoint
	}
	if runtimeToken != "" {
		actionModel.Metadata["runtime_token"] = runtimeToken
	}

	templateID, templatePayload, templateErr := s.resolveRuntimeTemplateForModel(ctx, actionModel)
	if templateErr != nil {
		return actionResult(false, templateErr.Error(), map[string]interface{}{
			"model_id":            actionModel.ID,
			"backend_type":        actionModel.BackendType,
			"runtime_template_id": templateID,
		}), nil
	}
	if templateID != "" {
		actionModel.Metadata["runtime_template_id"] = templateID
	}
	if templatePayload != "" {
		actionModel.Metadata["runtime_template_payload"] = templatePayload
	}

	adapterInstance, err := s.adapters.Get(actionModel.BackendType)
	if err != nil {
		return actionResult(false, err.Error(), map[string]interface{}{"backend_type": actionModel.BackendType}), nil
	}
	if !actionAllowedForBackendAndState(action, actionModel.BackendType, m.State) {
		currentState := normalizeState(m.State)
		return actionResult(false, fmt.Sprintf("当前状态(%s)不允许执行 %s", currentState, action), map[string]interface{}{
			"model_id":     actionModel.ID,
			"backend_type": actionModel.BackendType,
			"state":        currentState,
			"action":       action,
		}), nil
	}

	if action == "load" || action == "start" {
		if canRun, reason := s.scheduler.CanRun(actionModel); !canRun {
			return actionResult(false, reason, map[string]interface{}{"model_id": actionModel.ID}), nil
		}
	}

	result := model.ActionResult{}
	switch action {
	case "load":
		result, err = adapterInstance.LoadModel(ctx, actionModel)
		if result.Success {
			if isContainerRuntime(actionModel.BackendType) {
				m.State = model.ModelStateLoaded
				s.scheduler.MarkStopped(m.ID)
			} else {
				m.State = model.ModelStateLoaded
				s.scheduler.MarkRunning(actionModel)
			}
		}
	case "unload":
		if isContainerRuntime(actionModel.BackendType) && (m.State == model.ModelStateRunning || m.State == model.ModelStateBusy) {
			stopResult, stopErr := adapterInstance.StopModel(ctx, actionModel)
			if stopErr != nil {
				s.logger.Error("卸载前停止容器失败", "action", action, "model_id", m.ID, "error", stopErr)
				return actionResult(false, "卸载前停止容器失败", map[string]interface{}{"error": stopErr.Error(), "model_id": m.ID}), nil
			}
			if !stopResult.Success {
				return actionResult(false, "卸载前停止容器失败", stopResult.Detail), nil
			}
			applyContainerRuntimeDetail(stopResult.Detail, &m)
			m.State = model.ModelStateLoaded
			s.modelRegistry.Upsert(m)
		}
		result, err = adapterInstance.UnloadModel(ctx, actionModel)
		if result.Success {
			m.State = model.ModelStateStopped
			s.scheduler.MarkStopped(m.ID)
		}
	case "start":
		result, err = adapterInstance.StartModel(ctx, actionModel)
		if result.Success {
			m.State = model.ModelStateRunning
			s.scheduler.MarkRunning(actionModel)
		}
	case "stop":
		result, err = adapterInstance.StopModel(ctx, actionModel)
		if result.Success {
			if isContainerRuntime(actionModel.BackendType) && hasNonEmptyDetail(result.Detail, "runtime_container_id") {
				m.State = model.ModelStateLoaded
			} else {
				m.State = model.ModelStateStopped
			}
			s.scheduler.MarkStopped(m.ID)
		}
	default:
		return model.ActionResult{}, fmt.Errorf("不支持的动作: %s", action)
	}

	if err != nil {
		s.logger.Error("调用适配器失败", "action", action, "model_id", m.ID, "error", err)
		return actionResult(false, "适配器调用失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}

	if result.Detail == nil {
		result.Detail = map[string]interface{}{}
	}
	if m.Metadata == nil {
		m.Metadata = map[string]string{}
	}
	if templateID != "" {
		m.Metadata["runtime_template_id"] = templateID
		result.Detail["runtime_template_id"] = templateID
	}
	if runtimeEndpoint != "" {
		m.Endpoint = runtimeEndpoint
	}
	applyContainerRuntimeDetail(result.Detail, &m)

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
		s.logger.Debug("跳过 LM Studio 模型刷新：未注册适配器", "runtime_type", model.RuntimeTypeLMStudio, "error", err)
		return nil
	}

	remoteModels, err := adapterInstance.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("刷新 LM Studio 模型失败: runtime=%s error=%w", model.RuntimeTypeLMStudio, err)
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

func (s *ModelService) resolveContainerRuntimeBinding() (backendType model.RuntimeType, hostNodeID, runtimeID, endpoint string) {
	hostNodeID, runtimeID, endpoint = s.resolveRuntimeBinding(model.RuntimeTypeDocker)
	if hostNodeID != "" {
		return model.RuntimeTypeDocker, hostNodeID, runtimeID, endpoint
	}
	hostNodeID, runtimeID, endpoint = s.resolveRuntimeBinding(model.RuntimeTypePortainer)
	if hostNodeID != "" {
		return model.RuntimeTypePortainer, hostNodeID, runtimeID, endpoint
	}
	return "", "", "", ""
}

func (s *ModelService) resolveRuntimeConnection(m model.Model) (endpoint, token string) {
	endpoint = firstNonEmpty(
		readMetadataValue(m.Metadata, "runtime_endpoint"),
		m.Endpoint,
	)
	token = firstNonEmpty(
		readMetadataValue(m.Metadata, "runtime_token"),
		readMetadataValue(m.Metadata, "runtime_api_key"),
		readMetadataValue(m.Metadata, "runtime_bearer_token"),
	)

	var match *model.Runtime
	node, ok := s.nodeRegistry.Get(strings.TrimSpace(m.HostNodeID))
	if ok {
		match = s.matchRuntimeForModel(node.Runtimes, m)
	}
	if match == nil {
		match = s.findRuntimeAcrossNodes(m)
	}
	if match == nil {
		return endpoint, token
	}

	endpoint = firstNonEmpty(endpoint, match.Endpoint)
	token = firstNonEmpty(
		token,
		readMetadataValue(match.Metadata, "token"),
		readMetadataValue(match.Metadata, "api_token"),
		readMetadataValue(match.Metadata, "bearer_token"),
	)
	return endpoint, token
}

func (s *ModelService) findRuntimeAcrossNodes(m model.Model) *model.Runtime {
	runtimeID := strings.TrimSpace(m.RuntimeID)
	if runtimeID == "" {
		return nil
	}
	nodes := s.nodeRegistry.List()
	for i := range nodes {
		for j := range nodes[i].Runtimes {
			rt := &nodes[i].Runtimes[j]
			if rt.Enabled && rt.Type == m.BackendType && rt.ID == runtimeID {
				return rt
			}
		}
	}
	return nil
}

func (s *ModelService) matchRuntimeForModel(runtimes []model.Runtime, m model.Model) *model.Runtime {
	runtimeID := strings.TrimSpace(m.RuntimeID)
	for i := range runtimes {
		rt := &runtimes[i]
		if !rt.Enabled || rt.Type != m.BackendType {
			continue
		}
		if runtimeID != "" && rt.ID == runtimeID {
			return rt
		}
	}
	if runtimeID == "" {
		for i := range runtimes {
			rt := &runtimes[i]
			if rt.Enabled && rt.Type == m.BackendType {
				return rt
			}
		}
	}
	return nil
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

	backendType, hostNodeID, runtimeID, endpoint := s.resolveContainerRuntimeBinding()
	if hostNodeID == "" {
		nodes := s.nodeRegistry.List()
		if len(nodes) > 0 {
			hostNodeID = nodes[0].ID
		}
	}
	if backendType == "" {
		backendType = model.RuntimeTypeDocker
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
		templateID := inferTemplateIDForLocalModel(modelName, name)
		existing, exists := s.modelRegistry.Get(modelID)
		if exists && existing.Metadata != nil {
			if configured := strings.TrimSpace(existing.Metadata["runtime_template_id"]); configured != "" {
				templateID = configured
			}
		}

		state := model.ModelStateStopped
		if exists && existing.State != "" {
			state = existing.State
		}
		metadata := map[string]string{
			"source":              "local-scan",
			"path":                path,
			"runtime_template_id": templateID,
		}
		preserveContainerMetadata(existing.Metadata, metadata)

		scanned = append(scanned, model.Model{
			ID:          modelID,
			Name:        modelName,
			Provider:    "localfs",
			BackendType: backendType,
			HostNodeID:  hostNodeID,
			RuntimeID:   runtimeID,
			Endpoint:    endpoint,
			State:       state,
			Metadata:    metadata,
		})
	}

	s.modelRegistry.ReplaceBySource("local-scan", scanned)
	s.logger.Debug("本地模型目录刷新完成", "count", len(scanned), "root", root)
	return nil
}

func (s *ModelService) refreshContainerRuntimeStates(ctx context.Context) error {
	models := s.modelRegistry.List()
	var refreshErrors []error

	for _, item := range models {
		if !isContainerRuntime(item.BackendType) {
			continue
		}

		adapterInstance, err := s.adapters.Get(item.BackendType)
		if err != nil {
			s.logger.Warn("刷新容器模型状态失败：获取适配器失败",
				"action", "refresh_container_runtime_status",
				"model_id", item.ID,
				"model_name", item.Name,
				"node_id", item.HostNodeID,
				"runtime_type", item.BackendType,
				"runtime_id", item.RuntimeID,
				"error", err,
			)
			refreshErrors = append(refreshErrors, fmt.Errorf("model=%s backend=%s get adapter failed: %w", item.ID, item.BackendType, err))
			continue
		}

		status, err := adapterInstance.GetStatus(ctx, item)
		if err != nil {
			s.logger.Warn("刷新容器模型状态失败",
				"action", "refresh_container_runtime_status",
				"model_id", item.ID,
				"model_name", item.Name,
				"node_id", item.HostNodeID,
				"runtime_type", item.BackendType,
				"runtime_id", item.RuntimeID,
				"error", err,
			)
			refreshErrors = append(refreshErrors, fmt.Errorf("model=%s backend=%s status failed: %w", item.ID, item.BackendType, err))
			continue
		}
		if !status.Success {
			s.logger.Warn("刷新容器模型状态失败：状态返回失败",
				"action", "refresh_container_runtime_status",
				"model_id", item.ID,
				"model_name", item.Name,
				"node_id", item.HostNodeID,
				"runtime_type", item.BackendType,
				"runtime_id", item.RuntimeID,
				"message", status.Message,
			)
			refreshErrors = append(refreshErrors, fmt.Errorf("model=%s backend=%s status unsuccessful: %s", item.ID, item.BackendType, status.Message))
			continue
		}

		updated := item
		updated.Metadata = cloneMetadataMap(item.Metadata)
		applyContainerRuntimeDetail(status.Detail, &updated)

		exists, hasExists := boolFromDetail(status.Detail, "runtime_exists")
		running, hasRunning := boolFromDetail(status.Detail, "runtime_running")

		switch {
		case hasExists && !exists:
			updated.State = model.ModelStateStopped
			clearContainerMetadata(&updated)
			s.scheduler.MarkStopped(updated.ID)
		case hasRunning && running:
			updated.State = model.ModelStateRunning
			s.scheduler.MarkRunning(updated)
		case (hasExists && exists) || hasNonEmptyDetail(status.Detail, "runtime_container_id"):
			updated.State = model.ModelStateLoaded
			s.scheduler.MarkStopped(updated.ID)
		default:
			continue
		}

		s.modelRegistry.Upsert(updated)
	}
	return errors.Join(refreshErrors...)
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

func inferTemplateIDForLocalModel(modelName, entryName string) string {
	lower := strings.ToLower(strings.TrimSpace(modelName + " " + entryName))
	embeddingHints := []string{
		"embedding",
		"embed",
		"e5",
		"bge",
		"gte",
		"m3e",
		"jina-emb",
	}
	for _, hint := range embeddingHints {
		if strings.Contains(lower, hint) {
			return DefaultEmbeddingTemplateID
		}
	}
	return DefaultDockerTemplateID
}

func preserveContainerMetadata(existing map[string]string, target map[string]string) {
	if existing == nil || target == nil {
		return
	}
	keys := []string{
		"runtime_container_id",
		"runtime_container",
		"runtime_image",
	}
	for _, key := range keys {
		if v := strings.TrimSpace(existing[key]); v != "" {
			target[key] = v
		}
	}
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

func (s *ModelService) resolveRuntimeTemplateForModel(ctx context.Context, m model.Model) (id string, payload string, err error) {
	if s.templates == nil {
		return "", "", nil
	}

	if m.BackendType != model.RuntimeTypeDocker && m.BackendType != model.RuntimeTypePortainer {
		return "", "", nil
	}

	templateID := DefaultDockerTemplateID
	if m.Metadata != nil {
		if configured := strings.TrimSpace(m.Metadata["runtime_template_id"]); configured != "" {
			templateID = configured
		}
	}

	tpl, ok := s.templates.GetTemplate(ctx, templateID)
	if !ok {
		return templateID, "", fmt.Errorf("运行时模板不存在: %s", templateID)
	}

	if m.BackendType == model.RuntimeTypeDocker && tpl.RuntimeType != model.RuntimeTypeDocker {
		return templateID, "", fmt.Errorf("模板 %s runtime_type=%s 与模型后端 docker 不匹配", templateID, tpl.RuntimeType)
	}
	if m.BackendType == model.RuntimeTypePortainer && tpl.RuntimeType != model.RuntimeTypeDocker && tpl.RuntimeType != model.RuntimeTypePortainer {
		return templateID, "", fmt.Errorf("模板 %s runtime_type=%s 与模型后端 portainer 不匹配", templateID, tpl.RuntimeType)
	}

	raw, err := json.Marshal(tpl)
	if err != nil {
		return templateID, "", fmt.Errorf("运行时模板序列化失败: %w", err)
	}
	return templateID, string(raw), nil
}

func applyContainerRuntimeDetail(detail map[string]interface{}, m *model.Model) {
	if m == nil || detail == nil {
		return
	}
	if m.Metadata == nil {
		m.Metadata = map[string]string{}
	}

	if v, ok := detail["runtime_container_id"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			m.Metadata["runtime_container_id"] = s
		}
	}
	if v, ok := detail["runtime_container"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			m.Metadata["runtime_container"] = s
		}
	}
	if v, ok := detail["runtime_image"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			m.Metadata["runtime_image"] = s
		}
	}
}

func clearContainerMetadata(m *model.Model) {
	if m == nil || m.Metadata == nil {
		return
	}
	delete(m.Metadata, "runtime_container_id")
	delete(m.Metadata, "runtime_container")
	delete(m.Metadata, "runtime_image")
}

func cloneMetadataMap(in map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func readMetadataValue(metadata map[string]string, key string) string {
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata[key])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func actionAllowedForBackendAndState(action string, backendType model.RuntimeType, state model.ModelState) bool {
	if isContainerRuntime(backendType) {
		normalized := normalizeState(state)
		switch action {
		case "load":
			return normalized == model.ModelStateStopped || normalized == model.ModelStateUnknown || normalized == model.ModelStateError
		case "unload":
			return normalized == model.ModelStateLoaded || normalized == model.ModelStateRunning || normalized == model.ModelStateBusy
		case "start":
			return normalized == model.ModelStateLoaded
		case "stop":
			return normalized == model.ModelStateRunning || normalized == model.ModelStateBusy
		default:
			return false
		}
	}
	switch action {
	case "load", "start":
		return !isLoadedState(state)
	case "unload", "stop":
		return isLoadedState(state)
	default:
		return false
	}
}

func isLoadedState(state model.ModelState) bool {
	return state == model.ModelStateLoaded || state == model.ModelStateRunning || state == model.ModelStateBusy
}

func normalizeState(state model.ModelState) model.ModelState {
	if strings.TrimSpace(string(state)) == "" {
		return model.ModelStateUnknown
	}
	return state
}

func isContainerRuntime(runtimeType model.RuntimeType) bool {
	return runtimeType == model.RuntimeTypeDocker || runtimeType == model.RuntimeTypePortainer
}

func boolFromDetail(detail map[string]interface{}, key string) (bool, bool) {
	if detail == nil {
		return false, false
	}
	raw, ok := detail[key]
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func hasNonEmptyDetail(detail map[string]interface{}, key string) bool {
	if detail == nil {
		return false
	}
	raw, ok := detail[key]
	if !ok {
		return false
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	return value != "" && value != "<nil>"
}
