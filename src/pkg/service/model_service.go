package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"model-control-plane/src/pkg/adapter"
	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
	"model-control-plane/src/pkg/scheduler"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

var ErrModelNotFound = errors.New("model not found")

type ModelService struct {
	modelRegistry *registry.ModelRegistry
	nodeRegistry  *registry.NodeRegistry
	templates     *RuntimeTemplateService
	scheduler     *scheduler.Scheduler
	adapters      *adapter.Manager
	taskSvc       *TaskService
	store         *sqlitestore.Store
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
	models := s.modelRegistry.List()
	if s.store == nil {
		return models, nil
	}

	dbModels, err := s.store.ListModels(ctx)
	if err != nil {
		s.logger.Warn("从 sqlite 读取模型状态失败，返回内存状态", "error", err)
		return models, nil
	}
	merged := mergeModels(models, dbModels)
	for _, item := range merged {
		s.modelRegistry.Upsert(item)
	}
	return merged, nil
}

func (s *ModelService) GetModel(ctx context.Context, id string) (model.Model, error) {
	m, ok := s.modelRegistry.Get(id)
	if s.store == nil {
		if !ok {
			return model.Model{}, ErrModelNotFound
		}
		return m, nil
	}

	dbModel, dbOK, err := s.store.GetModelByID(ctx, id)
	if err != nil {
		s.logger.Warn("从 sqlite 读取模型详情失败，回退内存状态", "model_id", id, "error", err)
		if !ok {
			return model.Model{}, ErrModelNotFound
		}
		return m, nil
	}
	if ok && dbOK {
		merged := mergeOneModel(m, dbModel)
		s.modelRegistry.Upsert(merged)
		return merged, nil
	}
	if dbOK {
		s.modelRegistry.Upsert(dbModel)
		return dbModel, nil
	}
	if !ok {
		return model.Model{}, ErrModelNotFound
	}
	return m, nil
}

func (s *ModelService) SetStore(store *sqlitestore.Store) error {
	s.store = store
	if store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbModels, err := store.ListModels(ctx)
	if err != nil {
		return err
	}
	merged := mergeModels(s.modelRegistry.List(), dbModels)
	for _, item := range merged {
		s.modelRegistry.Upsert(item)
	}
	return store.UpsertModels(ctx, merged)
}

func (s *ModelService) SyncRegistryToStore(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertModels(ctx, s.modelRegistry.List())
}

func (s *ModelService) SetTaskService(taskSvc *TaskService) {
	s.taskSvc = taskSvc
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

func (s *ModelService) RefreshRuntimeStatus(ctx context.Context, id string) (model.ActionResult, error) {
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return model.ActionResult{}, ErrModelNotFound
	}
	if !isContainerRuntime(m.BackendType) {
		return actionResult(true, "非容器运行时，无需刷新", map[string]interface{}{
			"model_id":     m.ID,
			"backend_type": m.BackendType,
		}), nil
	}
	updated, err := s.reconcileContainerModel(ctx, m)
	if err != nil {
		return actionResult(false, "刷新 runtime 状态失败", map[string]interface{}{
			"model_id": updated.ID,
			"error":    err.Error(),
		}), nil
	}
	return actionResult(true, "runtime 状态刷新成功", map[string]interface{}{
		"model_id":           updated.ID,
		"desired_state":      updated.DesiredState,
		"observed_state":     updated.ObservedState,
		"readiness":          updated.Readiness,
		"health_message":     updated.HealthMessage,
		"last_reconciled_at": updated.LastReconciledAt,
	}), nil
}

func (s *ModelService) ApplyAgentReadiness(ctx context.Context, id string, ready bool, message string, detail map[string]interface{}) error {
	m, ok := s.modelRegistry.Get(id)
	if !ok {
		return ErrModelNotFound
	}
	now := time.Now().UTC()
	if ready {
		m.Readiness = model.ReadinessReady
		if strings.TrimSpace(m.ObservedState) == "" {
			m.ObservedState = string(model.ModelStateRunning)
		}
		m.HealthMessage = firstNonEmpty(strings.TrimSpace(message), "agent readiness 检查通过")
	} else {
		m.Readiness = model.ReadinessNotReady
		m.HealthMessage = firstNonEmpty(strings.TrimSpace(message), "agent readiness 检查失败")
		if detail != nil {
			if raw, ok := detail["observed_state"]; ok {
				if observed := strings.TrimSpace(fmt.Sprint(raw)); observed != "" {
					m.ObservedState = observed
				}
			}
		}
	}
	m.LastReconciledAt = now
	applyDrift(&m)
	s.modelRegistry.Upsert(m)
	return s.persistModel(ctx, m)
}

func (s *ModelService) ApplyAgentTaskObservation(ctx context.Context, task model.Task) error {
	if !isSupportedAgentTaskType(task.Type) {
		return nil
	}
	if task.TargetType != model.TaskTargetRuntime {
		return nil
	}
	modelID := strings.TrimSpace(task.TargetID)
	if modelID == "" {
		return nil
	}
	m, ok := s.modelRegistry.Get(modelID)
	if !ok {
		return ErrModelNotFound
	}
	now := time.Now().UTC()
	success := task.Status == model.TaskStatusSuccess
	message := firstNonEmpty(strings.TrimSpace(task.Message), strings.TrimSpace(task.Error))
	if task.Detail == nil {
		task.Detail = map[string]interface{}{}
	}
	task.Detail["task_type"] = string(task.Type)
	task.Detail["execution_path"] = "agent"

	switch task.Type {
	case model.TaskTypeAgentRuntimeReadiness, model.TaskTypeAgentRuntimePrecheck:
		if success {
			m.Readiness = model.ReadinessReady
			if strings.TrimSpace(m.ObservedState) == "" {
				m.ObservedState = string(model.ModelStateRunning)
			}
			m.HealthMessage = firstNonEmpty(message, "agent precheck passed")
		} else {
			m.Readiness = model.ReadinessNotReady
			m.HealthMessage = firstNonEmpty(message, "agent precheck failed")
		}
	case model.TaskTypeAgentPortCheck, model.TaskTypeAgentModelPathCheck:
		if success {
			m.HealthMessage = firstNonEmpty(message, m.HealthMessage, "agent node-local check passed")
		} else {
			m.Readiness = model.ReadinessNotReady
			m.HealthMessage = firstNonEmpty(message, "agent node-local check failed")
		}
	case model.TaskTypeAgentDockerInspect:
		applyContainerRuntimeDetail(task.Detail, &m)
		exists, hasExists := boolFromDetail(task.Detail, "runtime_exists")
		running, hasRunning := boolFromDetail(task.Detail, "runtime_running")
		switch {
		case hasExists && !exists:
			m.State = model.ModelStateStopped
			m.ObservedState = string(model.ModelStateStopped)
			m.Readiness = model.ReadinessNotReady
			m.HealthMessage = firstNonEmpty(message, "agent docker inspect: container not exists")
			clearContainerMetadata(&m)
			s.scheduler.MarkStopped(m.ID)
		case hasRunning && running:
			m.State = model.ModelStateRunning
			m.ObservedState = string(model.ModelStateRunning)
			if ready, ok := boolFromDetail(task.Detail, "runtime_ready"); ok && !ready {
				m.Readiness = model.ReadinessNotReady
				m.HealthMessage = firstNonEmpty(message, "agent docker inspect: running but not ready")
			} else {
				m.Readiness = model.ReadinessReady
				m.HealthMessage = firstNonEmpty(message, "agent docker inspect: running")
			}
			s.scheduler.MarkRunning(m)
		case hasExists && exists:
			m.State = model.ModelStateLoaded
			m.ObservedState = string(model.ModelStateLoaded)
			m.Readiness = model.ReadinessNotReady
			m.HealthMessage = firstNonEmpty(message, "agent docker inspect: loaded")
			s.scheduler.MarkStopped(m.ID)
		default:
			m.HealthMessage = firstNonEmpty(message, m.HealthMessage)
		}
	case model.TaskTypeAgentResourceSnapshot:
		if m.Metadata == nil {
			m.Metadata = map[string]string{}
		}
		m.Metadata["last_resource_snapshot_task_id"] = task.ID
		m.Metadata["last_resource_snapshot_at"] = now.Format(time.RFC3339)
		m.HealthMessage = firstNonEmpty(message, m.HealthMessage)
	}

	if observed, ok := task.Detail["observed_state"]; ok {
		if value := strings.TrimSpace(fmt.Sprint(observed)); value != "" {
			m.ObservedState = value
		}
	}
	if strings.TrimSpace(m.DesiredState) == "" {
		m.DesiredState = string(m.State)
	}
	m.LastReconciledAt = now
	applyDrift(&m)
	s.modelRegistry.Upsert(m)
	return s.persistModel(ctx, m)
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
	recordStepErr("persist_models", s.SyncRegistryToStore(ctx))

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
	if desired := desiredStateForAction(action, m); desired != "" {
		m.DesiredState = desired
		if m.LastReconciledAt.IsZero() {
			m.LastReconciledAt = time.Now().UTC()
		}
		s.modelRegistry.Upsert(m)
		if err := s.persistModel(ctx, m); err != nil {
			s.logger.Warn("写入 desired_state 失败，继续执行动作", "model_id", m.ID, "error", err)
		}
	}
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
			if err := s.persistModel(ctx, m); err != nil {
				return actionResult(false, "写入模型持久化状态失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
			}
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
	if err := s.persistModel(ctx, m); err != nil {
		return actionResult(false, "写入模型持久化状态失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	if isContainerRuntime(m.BackendType) {
		if reconciled, recErr := s.reconcileContainerModel(ctx, m); recErr != nil {
			s.logger.Warn("动作后 runtime reconcile 失败", "model_id", m.ID, "action", action, "error", recErr)
		} else {
			m = reconciled
		}
	}
	if result.Detail == nil {
		result.Detail = map[string]interface{}{}
	}
	result.Detail["desired_state"] = m.DesiredState
	result.Detail["observed_state"] = m.ObservedState
	result.Detail["readiness"] = m.Readiness
	result.Detail["health_message"] = m.HealthMessage
	result.Detail["last_reconciled_at"] = m.LastReconciledAt
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
		if strings.TrimSpace(endpoint) != "" {
			metadata["runtime_endpoint"] = endpoint
		}
		preserveContainerMetadata(existing.Metadata, metadata)
		modelEndpoint := endpoint
		if inferredEndpoint := s.inferServiceEndpointFromTemplateID(templateID); inferredEndpoint != "" {
			modelEndpoint = inferredEndpoint
			metadata["runtime_service_endpoint"] = inferredEndpoint
		}
		desiredState := strings.TrimSpace(existing.DesiredState)
		if desiredState == "" {
			desiredState = string(state)
		}
		observedState := strings.TrimSpace(existing.ObservedState)
		if observedState == "" {
			observedState = string(state)
		}
		readiness := existing.Readiness
		if strings.TrimSpace(string(readiness)) == "" {
			readiness = model.ReadinessUnknown
		}
		healthMessage := strings.TrimSpace(existing.HealthMessage)
		lastReconciledAt := existing.LastReconciledAt

		scanned = append(scanned, model.Model{
			ID:               modelID,
			Name:             modelName,
			Provider:         "localfs",
			BackendType:      backendType,
			HostNodeID:       hostNodeID,
			RuntimeID:        runtimeID,
			Endpoint:         modelEndpoint,
			State:            state,
			DesiredState:     desiredState,
			ObservedState:    observedState,
			Readiness:        readiness,
			HealthMessage:    healthMessage,
			LastReconciledAt: lastReconciledAt,
			Metadata:         metadata,
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
		if _, err := s.reconcileContainerModel(ctx, item); err != nil {
			s.logger.Warn("刷新容器模型状态失败",
				"action", "refresh_container_runtime_status",
				"model_id", item.ID,
				"model_name", item.Name,
				"node_id", item.HostNodeID,
				"runtime_type", item.BackendType,
				"runtime_id", item.RuntimeID,
				"error", err,
			)
			refreshErrors = append(refreshErrors, fmt.Errorf("model=%s backend=%s reconcile failed: %w", item.ID, item.BackendType, err))
		}
	}
	return errors.Join(refreshErrors...)
}

func (s *ModelService) persistModel(ctx context.Context, item model.Model) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertModel(ctx, item)
}

func desiredStateForAction(action string, m model.Model) string {
	switch action {
	case "start":
		return string(model.ModelStateRunning)
	case "stop", "unload":
		return string(model.ModelStateStopped)
	case "load":
		if isContainerRuntime(m.BackendType) {
			return string(model.ModelStateLoaded)
		}
		return string(model.ModelStateRunning)
	default:
		return strings.TrimSpace(m.DesiredState)
	}
}

func (s *ModelService) reconcileContainerModel(ctx context.Context, item model.Model) (model.Model, error) {
	adapterInstance, err := s.adapters.Get(item.BackendType)
	if err != nil {
		return item, fmt.Errorf("get adapter failed: %w", err)
	}
	status, err := adapterInstance.GetStatus(ctx, item)
	if err != nil {
		item.Readiness = model.ReadinessNotReady
		item.HealthMessage = fmt.Sprintf("runtime status 查询失败: %v", err)
		item.LastReconciledAt = time.Now().UTC()
		applyDrift(&item)
		s.modelRegistry.Upsert(item)
		_ = s.persistModel(ctx, item)
		return item, err
	}
	if !status.Success {
		item.Readiness = model.ReadinessNotReady
		item.HealthMessage = firstNonEmpty(strings.TrimSpace(status.Message), "runtime status 返回失败")
		item.LastReconciledAt = time.Now().UTC()
		applyDrift(&item)
		s.modelRegistry.Upsert(item)
		_ = s.persistModel(ctx, item)
		return item, fmt.Errorf("runtime status unsuccessful: %s", status.Message)
	}

	updated := item
	updated.Metadata = cloneMetadataMap(item.Metadata)
	applyContainerRuntimeDetail(status.Detail, &updated)

	exists, hasExists := boolFromDetail(status.Detail, "runtime_exists")
	running, hasRunning := boolFromDetail(status.Detail, "runtime_running")
	updated.ObservedState = string(model.ModelStateUnknown)

	switch {
	case hasExists && !exists:
		updated.State = model.ModelStateStopped
		updated.ObservedState = string(model.ModelStateStopped)
		updated.Readiness = model.ReadinessNotReady
		updated.HealthMessage = "runtime 容器不存在"
		clearContainerMetadata(&updated)
		s.scheduler.MarkStopped(updated.ID)
	case hasRunning && running:
		updated.State = model.ModelStateRunning
		updated.ObservedState = string(model.ModelStateRunning)
		s.scheduler.MarkRunning(updated)
		ready, message, endpoint := s.checkRuntimeReadiness(ctx, updated)
		if endpoint != "" {
			updated.Endpoint = endpoint
		}
		if ready {
			updated.Readiness = model.ReadinessReady
			updated.HealthMessage = firstNonEmpty(message, "runtime ready")
		} else {
			updated.Readiness = model.ReadinessNotReady
			updated.HealthMessage = firstNonEmpty(message, "runtime running but not ready")
		}
	case (hasExists && exists) || hasNonEmptyDetail(status.Detail, "runtime_container_id"):
		updated.State = model.ModelStateLoaded
		updated.ObservedState = string(model.ModelStateLoaded)
		updated.Readiness = model.ReadinessNotReady
		updated.HealthMessage = "runtime 已装载但未运行"
		s.scheduler.MarkStopped(updated.ID)
	default:
		updated.Readiness = model.ReadinessUnknown
		updated.HealthMessage = "runtime 状态未知"
	}

	if strings.TrimSpace(updated.DesiredState) == "" {
		updated.DesiredState = string(updated.State)
	}
	updated.LastReconciledAt = time.Now().UTC()
	applyDrift(&updated)
	s.modelRegistry.Upsert(updated)
	if err := s.persistModel(ctx, updated); err != nil {
		return updated, err
	}
	return updated, nil
}

func applyDrift(item *model.Model) {
	if item == nil {
		return
	}
	desired := strings.TrimSpace(strings.ToLower(item.DesiredState))
	observed := strings.TrimSpace(strings.ToLower(item.ObservedState))
	if desired == "" || observed == "" || desired == observed {
		return
	}
	if strings.Contains(strings.ToLower(item.HealthMessage), "drift") {
		return
	}
	if strings.TrimSpace(item.HealthMessage) == "" {
		item.HealthMessage = fmt.Sprintf("drift: desired=%s observed=%s", item.DesiredState, item.ObservedState)
		return
	}
	item.HealthMessage = fmt.Sprintf("%s; drift: desired=%s observed=%s", item.HealthMessage, item.DesiredState, item.ObservedState)
}

func (s *ModelService) checkRuntimeReadiness(ctx context.Context, item model.Model) (bool, string, string) {
	templateID := strings.TrimSpace(readMetadataValue(item.Metadata, "runtime_template_id"))
	endpoint := strings.TrimSpace(firstNonEmpty(
		readMetadataValue(item.Metadata, "runtime_service_endpoint"),
		item.Endpoint,
	))
	if endpoint == "" {
		if inferred := s.inferServiceEndpointFromTemplateID(templateID); inferred != "" {
			endpoint = inferred
		}
	}
	if s.taskSvc != nil {
		ready, msg, detail, used, err := s.taskSvc.TryRunRuntimePrecheckViaAgent(ctx, item)
		if used {
			if err != nil {
				s.logger.Warn("agent runtime precheck 失败，回退 controller 本地检查", "model_id", item.ID, "node_id", item.HostNodeID, "error", err)
			} else {
				if resolved := strings.TrimSpace(fmt.Sprint(detail["runtime_service_endpoint"])); resolved != "" {
					endpoint = resolved
				}
				return ready, firstNonEmpty(msg, "agent runtime precheck completed"), endpoint
			}
		}
	}
	if endpoint == "" {
		return true, "runtime running（无 endpoint）", endpoint
	}
	probeEndpoint := normalizeControllerAccessibleEndpoint(endpoint)
	// 只有 embedding 样板做严格 readiness，其他容器默认 running 即 ready。
	if !s.requiresStrictEmbeddingReadiness(ctx, item, templateID) {
		return true, "runtime running", endpoint
	}

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(probeEndpoint, "/")+"/health", nil)
	if err != nil {
		return false, fmt.Sprintf("health request build failed: %v", err), endpoint
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("health request failed: %v", err), endpoint
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, fmt.Sprintf("healthz ok: %d", resp.StatusCode), endpoint
	}
	return false, fmt.Sprintf("healthz status=%d", resp.StatusCode), endpoint
}

func (s *ModelService) requiresStrictEmbeddingReadiness(ctx context.Context, item model.Model, templateID string) bool {
	if isEmbeddingTemplateHint(templateID) {
		return true
	}
	if isEmbeddingTemplateHint(readMetadataValue(item.Metadata, "runtime_template_id")) {
		return true
	}
	if isEmbeddingTemplateHint(readMetadataValue(item.Metadata, "category")) {
		return true
	}
	if isEmbeddingTemplateHint(readMetadataValue(item.Metadata, "runtime_category")) {
		return true
	}
	if isEmbeddingTemplateHint(item.ID) || isEmbeddingTemplateHint(item.Name) {
		return true
	}
	if s.templates == nil || strings.TrimSpace(templateID) == "" {
		return false
	}
	tpl, ok := s.templates.GetTemplate(ctx, templateID)
	if !ok {
		return false
	}
	if isEmbeddingTemplateHint(readMetadataValue(tpl.Metadata, "category")) {
		return true
	}
	return isEmbeddingTemplateHint(tpl.ID) || isEmbeddingTemplateHint(tpl.Name) || isEmbeddingTemplateHint(tpl.Description)
}

func isEmbeddingTemplateHint(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	hints := []string{"embedding", "embed", "e5", "bge", "gte", "m3e", "jina"}
	for _, hint := range hints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func (s *ModelService) inferServiceEndpointFromTemplateID(templateID string) string {
	if s.templates == nil || strings.TrimSpace(templateID) == "" {
		return ""
	}
	tpl, ok := s.templates.GetTemplate(context.Background(), templateID)
	if !ok {
		return ""
	}
	return inferServiceEndpointFromTemplate(tpl)
}

func inferServiceEndpointFromTemplate(tpl model.RuntimeTemplate) string {
	if len(tpl.Ports) == 0 {
		return ""
	}
	for _, mapping := range tpl.Ports {
		if endpoint := inferServiceEndpointFromPortMapping(mapping); endpoint != "" {
			return endpoint
		}
	}
	return ""
}

func inferServiceEndpointFromPortMapping(mapping string) string {
	main := strings.TrimSpace(mapping)
	if main == "" {
		return ""
	}
	if idx := strings.Index(main, "/"); idx > 0 {
		main = main[:idx]
	}
	parts := strings.Split(main, ":")
	if len(parts) == 2 {
		if port := strings.TrimSpace(parts[0]); isDigits(port) {
			return "http://127.0.0.1:" + port
		}
	}
	if len(parts) == 3 {
		if port := strings.TrimSpace(parts[1]); isDigits(port) {
			host := strings.TrimSpace(parts[0])
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			return "http://" + host + ":" + port
		}
	}
	return ""
}

func isDigits(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
		"runtime_service_endpoint",
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
	if v, ok := detail["runtime_service_endpoint"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			m.Metadata["runtime_service_endpoint"] = s
			m.Endpoint = s
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
	delete(m.Metadata, "runtime_service_endpoint")
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
			return normalized == model.ModelStateLoaded ||
				normalized == model.ModelStateStopped ||
				normalized == model.ModelStateUnknown ||
				normalized == model.ModelStateError ||
				normalized == model.ModelStateRunning ||
				normalized == model.ModelStateBusy
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

func mergeModels(base []model.Model, persisted []model.Model) []model.Model {
	if len(persisted) == 0 {
		return base
	}
	out := make([]model.Model, 0, len(base)+len(persisted))
	indexByID := make(map[string]int, len(base))
	for _, item := range base {
		out = append(out, item)
		indexByID[item.ID] = len(out) - 1
	}
	for _, item := range persisted {
		idx, ok := indexByID[item.ID]
		if !ok {
			out = append(out, item)
			indexByID[item.ID] = len(out) - 1
			continue
		}
		out[idx] = mergeOneModel(out[idx], item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func mergeOneModel(base model.Model, persisted model.Model) model.Model {
	if strings.TrimSpace(base.ID) == "" {
		base.ID = persisted.ID
	}
	if strings.TrimSpace(base.Name) == "" {
		base.Name = persisted.Name
	}
	if strings.TrimSpace(base.Provider) == "" {
		base.Provider = persisted.Provider
	}
	if strings.TrimSpace(string(base.BackendType)) == "" {
		base.BackendType = persisted.BackendType
	}
	if strings.TrimSpace(base.HostNodeID) == "" {
		base.HostNodeID = persisted.HostNodeID
	}
	if strings.TrimSpace(base.RuntimeID) == "" {
		base.RuntimeID = persisted.RuntimeID
	}
	if strings.TrimSpace(persisted.Endpoint) != "" {
		base.Endpoint = persisted.Endpoint
	}
	if persisted.ContextLength > 0 {
		base.ContextLength = persisted.ContextLength
	}
	if strings.TrimSpace(string(persisted.State)) != "" {
		base.State = persisted.State
	}
	if strings.TrimSpace(persisted.DesiredState) != "" {
		base.DesiredState = persisted.DesiredState
	}
	if strings.TrimSpace(persisted.ObservedState) != "" {
		base.ObservedState = persisted.ObservedState
	}
	if strings.TrimSpace(string(persisted.Readiness)) != "" {
		base.Readiness = persisted.Readiness
	}
	if strings.TrimSpace(persisted.HealthMessage) != "" {
		base.HealthMessage = persisted.HealthMessage
	}
	if !persisted.LastReconciledAt.IsZero() {
		base.LastReconciledAt = persisted.LastReconciledAt
	}
	if base.Metadata == nil {
		base.Metadata = cloneMetadataMap(persisted.Metadata)
		return base
	}
	for k, v := range persisted.Metadata {
		base.Metadata[k] = v
	}
	return base
}
