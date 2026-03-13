package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"model-control-plane/src/pkg/model"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

var (
	ErrTaskNotFound      = errors.New("task not found")
	ErrTaskStoreNotReady = errors.New("task store is not ready")
)

type AgentRuntimeReadinessTaskRequest struct {
	RuntimeInstanceID string
	AgentID           string
	NodeID            string
	ModelID           string
	Endpoint          string
	HealthPath        string
	TimeoutSeconds    int
	TriggeredBy       string
}

type AgentNodeLocalTaskRequest struct {
	AgentID           string
	NodeID            string
	ModelID           string
	RuntimeInstanceID string
	RuntimeBindingID  string
	RuntimeTemplateID string
	ManifestID        string
	TaskScope         string
	Payload           map[string]interface{}
	PayloadContext    map[string]interface{}
	ResolvedContext   map[string]interface{}
	TaskType          model.TaskType
	TriggeredBy       string
}

type AgentTaskReport struct {
	Status     model.TaskStatus       `json:"status"`
	Progress   int                    `json:"progress,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Detail     map[string]interface{} `json:"detail,omitempty"`
	Error      string                 `json:"error,omitempty"`
	AcceptedAt time.Time              `json:"accepted_at,omitempty"`
	StartedAt  time.Time              `json:"started_at,omitempty"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
}

type TaskService struct {
	store            *sqlitestore.Store
	modelSvc         *ModelService
	agentSvc         *AgentService
	nodeSvc          *NodeService
	runtimeObjectSvc *RuntimeObjectService
	logger           *slog.Logger
	taskTimeout      time.Duration
}

func NewTaskService(store *sqlitestore.Store, modelSvc *ModelService, logger *slog.Logger) *TaskService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskService{
		store:       store,
		modelSvc:    modelSvc,
		logger:      logger,
		taskTimeout: 2 * time.Minute,
	}
}

func (s *TaskService) SetAgentService(agentSvc *AgentService) {
	s.agentSvc = agentSvc
}

func (s *TaskService) SetNodeService(nodeSvc *NodeService) {
	s.nodeSvc = nodeSvc
}

func (s *TaskService) SetRuntimeObjectService(runtimeObjectSvc *RuntimeObjectService) {
	s.runtimeObjectSvc = runtimeObjectSvc
}

func (s *TaskService) CreateRuntimeTask(ctx context.Context, taskType model.TaskType, modelID, triggeredBy string) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return model.Task{}, fmt.Errorf("model_id is empty")
	}
	if !isRuntimeTaskType(taskType) {
		return model.Task{}, fmt.Errorf("unsupported runtime task type: %s", taskType)
	}

	now := time.Now().UTC()
	task := model.Task{
		ID:         newTaskID("task"),
		Type:       taskType,
		TargetType: model.TaskTargetRuntime,
		TargetID:   modelID,
		Status:     model.TaskStatusPending,
		Progress:   0,
		Message:    "任务已创建",
		Payload: map[string]interface{}{
			"model_id":     modelID,
			"triggered_by": strings.TrimSpace(triggeredBy),
		},
		CreatedAt: now,
	}
	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}
	go s.executeRuntimeTask(task.ID, taskType, modelID)
	return task, nil
}

func (s *TaskService) executeRuntimeTask(taskID string, taskType model.TaskType, modelID string) {
	if s.modelSvc == nil {
		s.failTask(taskID, model.TaskStatusFailed, "model service 未就绪", "model service is nil", nil)
		return
	}

	s.patchTask(taskID, func(task *model.Task) {
		task.Status = model.TaskStatusDispatched
		task.Progress = maxProgress(task.Progress, 10)
		task.Message = "任务已分发"
	})

	ctx, cancel := context.WithTimeout(context.Background(), s.taskTimeout)
	defer cancel()

	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = model.TaskStatusRunning
		task.StartedAt = now
		task.Progress = maxProgress(task.Progress, 30)
		task.Message = "任务执行中"
	})

	var (
		result model.ActionResult
		err    error
	)

	if viaAgentResult, used, viaAgentErr := s.tryExecuteRuntimeTaskViaAgent(ctx, taskID, taskType, modelID); used {
		if viaAgentErr == nil {
			result = viaAgentResult
		} else {
			s.patchTask(taskID, func(task *model.Task) {
				if task.Detail == nil {
					task.Detail = map[string]interface{}{}
				}
				task.Detail["local_agent_first"] = "fallback_to_controller_direct"
				task.Detail["local_agent_fallback_error"] = viaAgentErr.Error()
				task.Message = "agent 优先路径失败，回退 controller direct"
			})
			s.logger.Warn("runtime task local-agent-first failed, fallback controller-direct",
				"task_id", taskID, "task_type", taskType, "model_id", modelID, "error", viaAgentErr)
		}
	}

	if !result.Success && err == nil {
		switch taskType {
		case model.TaskTypeRuntimeStart:
			result, err = s.modelSvc.StartModel(ctx, modelID)
		case model.TaskTypeRuntimeStop:
			result, err = s.modelSvc.StopModel(ctx, modelID)
		case model.TaskTypeRuntimeRestart:
			_, stopErr := s.modelSvc.StopModel(ctx, modelID)
			if stopErr != nil {
				err = stopErr
				break
			}
			s.patchTask(taskID, func(task *model.Task) {
				task.Progress = maxProgress(task.Progress, 65)
				task.Message = "重启任务已完成停止阶段"
			})
			result, err = s.modelSvc.StartModel(ctx, modelID)
		case model.TaskTypeRuntimeRefresh:
			result, err = s.modelSvc.RefreshRuntimeStatus(ctx, modelID)
		default:
			err = fmt.Errorf("unsupported task type: %s", taskType)
		}
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.failTask(taskID, model.TaskStatusTimeout, "任务超时", ctx.Err().Error(), map[string]interface{}{"task_type": string(taskType)})
		return
	}
	if err != nil {
		s.failTask(taskID, model.TaskStatusFailed, "任务执行失败", err.Error(), map[string]interface{}{"task_type": string(taskType)})
		return
	}
	if !result.Success {
		detail := result.Detail
		if detail == nil {
			detail = map[string]interface{}{}
		}
		s.failTask(taskID, model.TaskStatusFailed, firstNonEmpty(result.Message, "任务执行失败"), readErrorFromDetail(detail), detail)
		return
	}

	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = model.TaskStatusSuccess
		task.Progress = 100
		task.Message = firstNonEmpty(result.Message, "任务执行成功")
		task.Detail = result.Detail
		task.Error = ""
		if task.StartedAt.IsZero() {
			task.StartedAt = now
		}
		task.FinishedAt = now
	})
}

func (s *TaskService) tryExecuteRuntimeTaskViaAgent(ctx context.Context, parentTaskID string, taskType model.TaskType, modelID string) (model.ActionResult, bool, error) {
	if s.agentSvc == nil || s.modelSvc == nil {
		return model.ActionResult{}, false, nil
	}
	if taskType != model.TaskTypeRuntimeStart && taskType != model.TaskTypeRuntimeStop && taskType != model.TaskTypeRuntimeRefresh {
		return model.ActionResult{}, false, nil
	}

	item, err := s.modelSvc.GetModel(ctx, modelID)
	if err != nil {
		return model.ActionResult{}, false, nil
	}
	if !isContainerRuntime(item.BackendType) {
		return model.ActionResult{}, false, nil
	}
	nodeID := strings.TrimSpace(item.HostNodeID)
	if nodeID == "" {
		return model.ActionResult{}, false, nil
	}
	if !shouldPreferRuntimeAgentPath(s.nodeSvc, ctx, nodeID) {
		return model.ActionResult{}, false, nil
	}

	agentID, preferAgent := s.resolvePreferredAgentForNode(ctx, nodeID)
	if !preferAgent || strings.TrimSpace(agentID) == "" {
		return model.ActionResult{}, false, nil
	}

	runtimeInstanceID := ""
	if s.runtimeObjectSvc != nil {
		if instance, getErr := s.runtimeObjectSvc.GetRuntimeInstanceByModelID(ctx, modelID); getErr == nil {
			runtimeInstanceID = strings.TrimSpace(instance.ID)
		}
	}

	payload := map[string]interface{}{
		"runtime_id":      strings.TrimSpace(item.RuntimeID),
		"node_id":         nodeID,
		"model_id":        modelID,
		"model_path":      strings.TrimSpace(item.PathOrRef),
		"endpoint":        strings.TrimSpace(item.Endpoint),
		"runtime_backend": strings.TrimSpace(string(item.BackendType)),
		"parent_task_id":  strings.TrimSpace(parentTaskID),
	}
	if containerID := readMetadataValue(item.Metadata, "runtime_container_id"); containerID != "" {
		payload["runtime_container_id"] = containerID
	}

	taskMapping := map[model.TaskType]model.TaskType{
		model.TaskTypeRuntimeStart:   model.TaskTypeAgentDockerStart,
		model.TaskTypeRuntimeStop:    model.TaskTypeAgentDockerStop,
		model.TaskTypeRuntimeRefresh: model.TaskTypeAgentDockerInspect,
	}
	agentTaskType, ok := taskMapping[taskType]
	if !ok {
		return model.ActionResult{}, false, nil
	}

	req := AgentNodeLocalTaskRequest{
		AgentID:           agentID,
		NodeID:            nodeID,
		ModelID:           modelID,
		RuntimeInstanceID: runtimeInstanceID,
		TaskType:          agentTaskType,
		Payload:           payload,
		TriggeredBy:       "controller.runtime_task.local_agent_first",
		PayloadContext: map[string]interface{}{
			"local_agent_first": true,
			"parent_task_id":    strings.TrimSpace(parentTaskID),
		},
	}
	created, createErr := s.CreateAgentNodeTask(ctx, req)
	if createErr != nil {
		return model.ActionResult{}, true, createErr
	}

	waitTimeout := 40 * time.Second
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	finalTask, awaitErr := s.AwaitTask(waitCtx, created.ID, 400*time.Millisecond)
	if awaitErr != nil {
		return model.ActionResult{}, true, fmt.Errorf("await agent task=%s failed: %w", created.ID, awaitErr)
	}

	detail := cloneObjectMap(finalTask.Detail)
	detail["parent_task_id"] = strings.TrimSpace(parentTaskID)
	detail["execution_path"] = "agent"
	detail["local_agent_first"] = true
	detail["agent_subtask_id"] = finalTask.ID
	detail["agent_subtask_type"] = string(finalTask.Type)
	detail["agent_subtask_status"] = string(finalTask.Status)
	detail["agent_subtask_message"] = finalTask.Message

	if taskType == model.TaskTypeRuntimeRefresh {
		_, _ = s.dispatchBestEffortResourceSnapshot(ctx, nodeID, modelID, runtimeInstanceID, agentID, parentTaskID)
	}

	if finalTask.Status == model.TaskStatusSuccess {
		return model.ActionResult{
			Success:   true,
			Message:   firstNonEmpty(finalTask.Message, "agent 执行成功"),
			Detail:    detail,
			Timestamp: time.Now().UTC(),
		}, true, nil
	}

	errText := firstNonEmpty(strings.TrimSpace(finalTask.Error), strings.TrimSpace(finalTask.Message), "agent task failed")
	detail["agent_subtask_error"] = errText
	return model.ActionResult{}, true, fmt.Errorf("agent subtask failed: %s", errText)
}

func (s *TaskService) dispatchBestEffortResourceSnapshot(ctx context.Context, nodeID, modelID, runtimeInstanceID, agentID, parentTaskID string) (string, error) {
	if s.agentSvc == nil {
		return "", nil
	}
	req := AgentNodeLocalTaskRequest{
		AgentID:           strings.TrimSpace(agentID),
		NodeID:            strings.TrimSpace(nodeID),
		ModelID:           strings.TrimSpace(modelID),
		RuntimeInstanceID: strings.TrimSpace(runtimeInstanceID),
		TaskType:          model.TaskTypeAgentResourceSnapshot,
		TriggeredBy:       "controller.runtime_refresh.local_agent_first",
		Payload: map[string]interface{}{
			"parent_task_id": strings.TrimSpace(parentTaskID),
			"model_id":       strings.TrimSpace(modelID),
		},
		PayloadContext: map[string]interface{}{
			"local_agent_first": true,
			"best_effort":       true,
			"parent_task_id":    strings.TrimSpace(parentTaskID),
		},
	}
	created, err := s.CreateAgentNodeTask(ctx, req)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (s *TaskService) CreateAgentRuntimeReadinessTask(ctx context.Context, req AgentRuntimeReadinessTaskRequest) (model.Task, error) {
	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 3
	}
	healthPath := strings.TrimSpace(req.HealthPath)
	if healthPath == "" {
		healthPath = "/health"
	}
	return s.CreateAgentNodeTask(ctx, AgentNodeLocalTaskRequest{
		AgentID:           req.AgentID,
		NodeID:            req.NodeID,
		ModelID:           req.ModelID,
		RuntimeInstanceID: req.RuntimeInstanceID,
		TaskType:          model.TaskTypeAgentRuntimeReadiness,
		Payload: map[string]interface{}{
			"endpoint":        strings.TrimSpace(req.Endpoint),
			"health_path":     healthPath,
			"timeout_seconds": timeoutSeconds,
		},
		TriggeredBy: req.TriggeredBy,
	})
}

func (s *TaskService) CreateAgentNodeTask(ctx context.Context, req AgentNodeLocalTaskRequest) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	taskType := req.TaskType
	if !isSupportedAgentTaskType(taskType) {
		return model.Task{}, fmt.Errorf("unsupported agent task type: %s", taskType)
	}
	resolved, err := s.resolveAgentTaskCreationInput(ctx, req)
	if err != nil {
		return model.Task{}, err
	}
	payload := cloneObjectMap(req.Payload)
	payloadContext := mergeObjectMaps(req.PayloadContext, map[string]interface{}{
		"requested_runtime_instance_id": strings.TrimSpace(req.RuntimeInstanceID),
		"requested_runtime_binding_id":  strings.TrimSpace(req.RuntimeBindingID),
		"requested_runtime_template_id": strings.TrimSpace(req.RuntimeTemplateID),
		"requested_manifest_id":         strings.TrimSpace(req.ManifestID),
	})
	for k, v := range payloadContext {
		if strings.TrimSpace(k) == "" || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" || fmt.Sprint(v) == "<nil>" {
			delete(payloadContext, k)
		}
	}

	resolvedContext := mergeObjectMaps(agentTaskResolvedContextToMap(resolved.Context), req.ResolvedContext)
	payload["task_scope"] = resolved.Context.TaskScope
	payload["runtime_instance_id"] = resolved.Context.RuntimeInstanceID
	payload["runtime_binding_id"] = resolved.Context.RuntimeBindingID
	payload["runtime_template_id"] = resolved.Context.RuntimeTemplateID
	payload["manifest_id"] = resolved.Context.ManifestID
	payload["node_id"] = resolved.NodeID
	payload["model_id"] = resolved.ModelID
	payload["task_type"] = string(taskType)
	payload["triggered_by"] = strings.TrimSpace(req.TriggeredBy)
	if len(payloadContext) > 0 {
		payload["payload_context"] = payloadContext
	}
	if len(resolvedContext) > 0 {
		payload["resolved_context"] = resolvedContext
	}

	targetType := model.TaskTargetNode
	targetID := resolved.NodeID
	if resolved.ModelID != "" {
		targetType = model.TaskTargetRuntime
		targetID = resolved.ModelID
	}

	detail := map[string]interface{}{
		"execution_path":        "agent-dispatched",
		"task_scope":            resolved.Context.TaskScope,
		"runtime_instance_id":   resolved.Context.RuntimeInstanceID,
		"runtime_binding_id":    resolved.Context.RuntimeBindingID,
		"runtime_template_id":   resolved.Context.RuntimeTemplateID,
		"manifest_id":           resolved.Context.ManifestID,
		"binding_mode":          string(resolved.Context.BindingMode),
		"runtime_kind":          string(resolved.Context.RuntimeKind),
		"runtime_template_type": string(resolved.Context.TemplateType),
	}
	if strings.HasPrefix(resolved.Context.TaskScope, "legacy") {
		detail["compatibility_path"] = "legacy_model_or_node"
	}
	task := model.Task{
		ID:              newTaskID("task"),
		Type:            taskType,
		TargetType:      targetType,
		TargetID:        targetID,
		AssignedAgentID: resolved.AgentID,
		Status:          model.TaskStatusPending,
		Message:         "等待 agent 拉取任务",
		Payload:         payload,
		CreatedAt:       time.Now().UTC(),
		Detail:          detail,
	}
	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}
	return task, nil
}

type resolvedAgentTaskCreationInput struct {
	AgentID string
	NodeID  string
	ModelID string
	Context model.AgentTaskResolvedContext
}

func (s *TaskService) resolveAgentTaskCreationInput(ctx context.Context, req AgentNodeLocalTaskRequest) (resolvedAgentTaskCreationInput, error) {
	contextInfo := model.AgentTaskResolvedContext{
		TaskScope:         strings.TrimSpace(req.TaskScope),
		RuntimeInstanceID: strings.TrimSpace(req.RuntimeInstanceID),
		RuntimeBindingID:  strings.TrimSpace(req.RuntimeBindingID),
		RuntimeTemplateID: strings.TrimSpace(req.RuntimeTemplateID),
		ManifestID:        strings.TrimSpace(req.ManifestID),
		NodeID:            strings.TrimSpace(req.NodeID),
		ModelID:           strings.TrimSpace(req.ModelID),
		Metadata:          map[string]string{},
	}

	if contextInfo.RuntimeInstanceID != "" {
		if s.runtimeObjectSvc == nil {
			return resolvedAgentTaskCreationInput{}, fmt.Errorf("runtime_object_service is required when runtime_instance_id is provided")
		}
		resolved, err := s.runtimeObjectSvc.ResolveRuntimeInstanceContext(ctx, contextInfo.RuntimeInstanceID)
		if err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}
		if err := ensureFieldMatch("runtime_binding_id", strings.TrimSpace(req.RuntimeBindingID), resolved.Binding.ID); err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}
		if err := ensureFieldMatch("runtime_template_id", strings.TrimSpace(req.RuntimeTemplateID), resolved.Template.ID); err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}
		if err := ensureFieldMatch("manifest_id", strings.TrimSpace(req.ManifestID), resolved.Manifest.ID); err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}
		if err := ensureFieldMatch("model_id", strings.TrimSpace(req.ModelID), resolved.Instance.ModelID); err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}
		if err := ensureFieldMatch("node_id", strings.TrimSpace(req.NodeID), firstNonEmpty(strings.TrimSpace(resolved.Instance.NodeID), strings.TrimSpace(resolved.Binding.PreferredNode))); err != nil {
			return resolvedAgentTaskCreationInput{}, err
		}

		contextInfo.RuntimeBindingID = strings.TrimSpace(resolved.Binding.ID)
		contextInfo.RuntimeTemplateID = strings.TrimSpace(resolved.Template.ID)
		contextInfo.ManifestID = strings.TrimSpace(resolved.Manifest.ID)
		contextInfo.NodeID = firstNonEmpty(strings.TrimSpace(resolved.Instance.NodeID), strings.TrimSpace(resolved.Binding.PreferredNode))
		contextInfo.ModelID = strings.TrimSpace(resolved.Instance.ModelID)
		contextInfo.BindingMode = resolved.Binding.BindingMode
		contextInfo.RuntimeKind = resolved.Manifest.RuntimeKind
		contextInfo.TemplateType = resolved.Manifest.TemplateType
		contextInfo.Endpoint = strings.TrimSpace(resolved.Instance.Endpoint)
		contextInfo.HealthPath = strings.TrimSpace(resolved.Manifest.Healthcheck.Path)
		contextInfo.ScriptRef = strings.TrimSpace(resolved.Binding.ScriptRef)
		contextInfo.ExposedPorts = cloneStringSlice(resolved.Manifest.ExposedPorts)
		contextInfo.RequiredEnv = cloneStringSlice(resolved.Manifest.RequiredEnv)
		contextInfo.OptionalEnv = cloneStringSlice(resolved.Manifest.OptionalEnv)
		contextInfo.MountPoints = cloneStringSlice(resolved.Manifest.MountPoints)
		if contextInfo.TaskScope == "" {
			contextInfo.TaskScope = "runtime_instance"
		}
		contextInfo.Metadata["resolved_path"] = "instance-binding-template-manifest"
	} else {
		if contextInfo.TaskScope == "" {
			contextInfo.TaskScope = "legacy_model_or_node"
		}
		if contextInfo.ModelID != "" && s.runtimeObjectSvc != nil {
			if instance, err := s.runtimeObjectSvc.GetRuntimeInstanceByModelID(ctx, contextInfo.ModelID); err == nil {
				contextInfo.RuntimeInstanceID = firstNonEmpty(contextInfo.RuntimeInstanceID, strings.TrimSpace(instance.ID))
				contextInfo.RuntimeBindingID = firstNonEmpty(contextInfo.RuntimeBindingID, strings.TrimSpace(instance.BindingID))
				contextInfo.RuntimeTemplateID = firstNonEmpty(contextInfo.RuntimeTemplateID, strings.TrimSpace(instance.TemplateID))
				contextInfo.NodeID = firstNonEmpty(contextInfo.NodeID, strings.TrimSpace(instance.NodeID))
			}
		}
		contextInfo.Metadata["resolved_path"] = "legacy_model_or_node"
	}

	if s.modelSvc != nil && contextInfo.ModelID != "" {
		if item, err := s.modelSvc.GetModel(ctx, contextInfo.ModelID); err == nil {
			contextInfo.NodeID = firstNonEmpty(contextInfo.NodeID, strings.TrimSpace(item.HostNodeID))
			contextInfo.Endpoint = firstNonEmpty(contextInfo.Endpoint, strings.TrimSpace(item.Endpoint), readMetadataValue(item.Metadata, "runtime_service_endpoint"))
			contextInfo.ModelPath = firstNonEmpty(contextInfo.ModelPath, strings.TrimSpace(item.PathOrRef), readMetadataValue(item.Metadata, "path"))
			contextInfo.ScriptRef = firstNonEmpty(contextInfo.ScriptRef, strings.TrimSpace(item.ScriptRef), readMetadataValue(item.Metadata, "script_ref"))
			contextInfo.RuntimeContainerID = firstNonEmpty(contextInfo.RuntimeContainerID, readMetadataValue(item.Metadata, "runtime_container_id"))
			if contextInfo.RuntimeTemplateID == "" {
				contextInfo.RuntimeTemplateID = readMetadataValue(item.Metadata, "runtime_template_id")
			}
			if strings.TrimSpace(string(item.BackendType)) != "" {
				contextInfo.Metadata["backend_type"] = strings.TrimSpace(string(item.BackendType))
			}
		}
	}

	if strings.TrimSpace(req.NodeID) != "" {
		contextInfo.NodeID = strings.TrimSpace(req.NodeID)
	}
	if strings.TrimSpace(req.ModelID) != "" {
		contextInfo.ModelID = strings.TrimSpace(req.ModelID)
	}

	agentID := strings.TrimSpace(req.AgentID)
	nodeID := strings.TrimSpace(contextInfo.NodeID)
	modelID := strings.TrimSpace(contextInfo.ModelID)
	if nodeID == "" && modelID != "" && s.modelSvc != nil {
		if item, err := s.modelSvc.GetModel(ctx, modelID); err == nil {
			nodeID = strings.TrimSpace(item.HostNodeID)
		}
	}
	if nodeID == "" && agentID != "" && s.agentSvc != nil {
		if agent, ok := s.agentSvc.GetByID(agentID); ok {
			nodeID = strings.TrimSpace(agent.NodeID)
		}
	}
	if agentID == "" && nodeID != "" {
		if selected, preferred := s.resolvePreferredAgentForNode(ctx, nodeID); selected != "" {
			agentID = selected
			if preferred {
				contextInfo.Metadata["agent_selection"] = "preferred_local_agent"
			}
		}
	}
	if agentID == "" {
		if nodeID != "" && isNodeLocalAgentExpected(s.nodeSvc, ctx, nodeID) {
			return resolvedAgentTaskCreationInput{}, fmt.Errorf("local-agent expected but no online agent on node=%s", nodeID)
		}
		return resolvedAgentTaskCreationInput{}, fmt.Errorf("agent_id is required")
	}
	if modelID == "" && nodeID == "" {
		return resolvedAgentTaskCreationInput{}, fmt.Errorf("model_id/node_id is required")
	}
	contextInfo.NodeID = nodeID
	contextInfo.ModelID = modelID
	if contextInfo.RuntimeInstanceID != "" && contextInfo.ManifestID == "" {
		return resolvedAgentTaskCreationInput{}, fmt.Errorf("resolve runtime_instance_id=%s failed: manifest_id is empty", contextInfo.RuntimeInstanceID)
	}
	contextInfo.Metadata["phase"] = "stage-a-step1"
	contextInfo.Metadata["task_protocol"] = "instance-first"

	return resolvedAgentTaskCreationInput{
		AgentID: agentID,
		NodeID:  nodeID,
		ModelID: modelID,
		Context: contextInfo,
	}, nil
}

func ensureFieldMatch(fieldName, requestedValue, resolvedValue string) error {
	requestedValue = strings.TrimSpace(requestedValue)
	resolvedValue = strings.TrimSpace(resolvedValue)
	if requestedValue == "" || resolvedValue == "" {
		return nil
	}
	if requestedValue != resolvedValue {
		return fmt.Errorf("%s mismatch: requested=%s resolved=%s", fieldName, requestedValue, resolvedValue)
	}
	return nil
}

func (s *TaskService) resolvePreferredAgentForNode(ctx context.Context, nodeID string) (agentID string, preferred bool) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || s.agentSvc == nil {
		return "", false
	}
	preferredID := preferredLocalAgentIDForNode(s.nodeSvc, ctx, nodeID)
	if preferredID != "" {
		if item, ok := s.agentSvc.GetByID(preferredID); ok && strings.TrimSpace(item.NodeID) == nodeID && item.Status == model.AgentStatusOnline {
			return strings.TrimSpace(item.ID), true
		}
	}
	if item, ok := s.agentSvc.GetByNodeID(nodeID); ok && item.Status == model.AgentStatusOnline {
		return strings.TrimSpace(item.ID), false
	}
	return "", false
}

func preferredLocalAgentIDForNode(nodeSvc *NodeService, ctx context.Context, nodeID string) string {
	if nodeSvc == nil {
		return ""
	}
	node, ok := nodeSvc.GetNode(ctx, nodeID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(readNodeMetadataValue(node.Metadata, "preferred_local_agent_id"))
}

func isNodeLocalAgentExpected(nodeSvc *NodeService, ctx context.Context, nodeID string) bool {
	if nodeSvc == nil {
		return false
	}
	node, ok := nodeSvc.GetNode(ctx, nodeID)
	if !ok {
		return false
	}
	flag := strings.ToLower(strings.TrimSpace(readNodeMetadataValue(node.Metadata, "local_agent_expected")))
	return flag == "true" || flag == "1" || flag == "yes" || flag == "on"
}

func shouldPreferRuntimeAgentPath(nodeSvc *NodeService, ctx context.Context, nodeID string) bool {
	if nodeSvc == nil {
		return false
	}
	node, ok := nodeSvc.GetNode(ctx, nodeID)
	if !ok {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(string(node.Role)), string(model.NodeRoleController)) {
		return true
	}
	meta := readNodeMetadataValue(node.Metadata, "controller_node")
	if strings.EqualFold(strings.TrimSpace(meta), "true") {
		return true
	}
	return isNodeLocalAgentExpected(nodeSvc, ctx, nodeID)
}

func readNodeMetadataValue(raw interface{}, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || raw == nil {
		return ""
	}
	switch meta := raw.(type) {
	case map[string]string:
		return strings.TrimSpace(meta[key])
	case map[string]interface{}:
		value, ok := meta[key]
		if !ok || value == nil {
			return ""
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "<nil>" {
			return ""
		}
		return text
	default:
		return ""
	}
}

func agentTaskResolvedContextToMap(ctx model.AgentTaskResolvedContext) map[string]interface{} {
	out := map[string]interface{}{}
	appendString := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			out[key] = value
		}
	}
	appendString("task_scope", ctx.TaskScope)
	appendString("runtime_instance_id", ctx.RuntimeInstanceID)
	appendString("runtime_binding_id", ctx.RuntimeBindingID)
	appendString("runtime_template_id", ctx.RuntimeTemplateID)
	appendString("manifest_id", ctx.ManifestID)
	appendString("node_id", ctx.NodeID)
	appendString("model_id", ctx.ModelID)
	if strings.TrimSpace(string(ctx.BindingMode)) != "" {
		out["binding_mode"] = string(ctx.BindingMode)
	}
	if strings.TrimSpace(string(ctx.RuntimeKind)) != "" {
		out["runtime_kind"] = string(ctx.RuntimeKind)
	}
	if strings.TrimSpace(string(ctx.TemplateType)) != "" {
		out["template_type"] = string(ctx.TemplateType)
	}
	appendString("endpoint", ctx.Endpoint)
	appendString("health_path", ctx.HealthPath)
	appendString("model_path", ctx.ModelPath)
	appendString("script_ref", ctx.ScriptRef)
	appendString("runtime_container_id", ctx.RuntimeContainerID)
	if len(ctx.ExposedPorts) > 0 {
		out["exposed_ports"] = cloneStringSlice(ctx.ExposedPorts)
	}
	if len(ctx.RequiredEnv) > 0 {
		out["required_env"] = cloneStringSlice(ctx.RequiredEnv)
	}
	if len(ctx.OptionalEnv) > 0 {
		out["optional_env"] = cloneStringSlice(ctx.OptionalEnv)
	}
	if len(ctx.MountPoints) > 0 {
		out["mount_points"] = cloneStringSlice(ctx.MountPoints)
	}
	if len(ctx.Metadata) > 0 {
		out["metadata"] = cloneStringMap(ctx.Metadata)
	}
	return out
}

func cloneObjectMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func mergeObjectMaps(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	if len(base) == 0 && len(extra) == 0 {
		return map[string]interface{}{}
	}
	out := cloneObjectMap(base)
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func (s *TaskService) PullNextAgentTask(ctx context.Context, agentID string) (model.Task, bool, error) {
	if s.store == nil {
		return model.Task{}, false, ErrTaskStoreNotReady
	}
	item, ok, err := s.store.ClaimPendingTaskForAgent(ctx, agentID, supportedAgentTaskTypes())
	if err != nil || !ok {
		return item, ok, err
	}
	enrichTaskRuntimeFields(&item)
	return item, true, nil
}

func (s *TaskService) ReportAgentTask(ctx context.Context, agentID, taskID string, report AgentTaskReport) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	agentID = strings.TrimSpace(agentID)
	taskID = strings.TrimSpace(taskID)
	if agentID == "" || taskID == "" {
		return model.Task{}, fmt.Errorf("agent_id/task_id is required")
	}
	task, ok, err := s.store.GetTaskByID(ctx, taskID)
	if err != nil {
		return model.Task{}, err
	}
	if !ok {
		return model.Task{}, ErrTaskNotFound
	}
	enrichTaskRuntimeFields(&task)
	if strings.TrimSpace(task.AssignedAgentID) != "" && strings.TrimSpace(task.AssignedAgentID) != agentID {
		return model.Task{}, fmt.Errorf("task does not belong to agent: %s", agentID)
	}

	now := time.Now().UTC()
	task.WorkerID = agentID
	if !report.AcceptedAt.IsZero() {
		task.AcceptedAt = report.AcceptedAt.UTC()
	}
	if report.Status != "" {
		task.Status = report.Status
	}
	if report.Progress > 0 {
		task.Progress = normalizeProgress(report.Progress)
	}
	if msg := strings.TrimSpace(report.Message); msg != "" {
		task.Message = msg
	}
	if report.Detail != nil {
		task.Detail = report.Detail
	}
	if task.Detail == nil {
		task.Detail = map[string]interface{}{}
	}
	task.Detail["execution_path"] = "agent"
	if errText := strings.TrimSpace(report.Error); errText != "" {
		task.Error = errText
	}
	enrichTaskRuntimeFields(&task)

	switch task.Status {
	case model.TaskStatusRunning:
		if report.StartedAt.IsZero() {
			task.StartedAt = now
		} else {
			task.StartedAt = report.StartedAt.UTC()
		}
		if task.Progress == 0 {
			task.Progress = 70
		}
	case model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusTimeout, model.TaskStatusCanceled:
		if report.StartedAt.IsZero() {
			if task.StartedAt.IsZero() {
				task.StartedAt = now
			}
		} else {
			task.StartedAt = report.StartedAt.UTC()
		}
		if report.FinishedAt.IsZero() {
			task.FinishedAt = now
		} else {
			task.FinishedAt = report.FinishedAt.UTC()
		}
		if task.Progress < 100 {
			task.Progress = 100
		}
	}

	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}

	if isSupportedAgentTaskType(task.Type) && s.runtimeObjectSvc != nil {
		if applyErr := s.runtimeObjectSvc.ApplyAgentTaskObservation(ctx, task); applyErr != nil {
			s.logger.Warn("apply runtime instance observation failed", "task_id", task.ID, "task_type", task.Type, "error", applyErr)
		}
	}

	if isSupportedAgentTaskType(task.Type) && s.nodeSvc != nil {
		nodeID := strings.TrimSpace(fmt.Sprint(task.Payload["node_id"]))
		if nodeID == "" && s.modelSvc != nil && task.TargetType == model.TaskTargetRuntime {
			if item, err := s.modelSvc.GetModel(ctx, task.TargetID); err == nil {
				nodeID = strings.TrimSpace(item.HostNodeID)
			}
		}
		if nodeID == "" && s.agentSvc != nil {
			if agent, ok := s.agentSvc.GetByID(agentID); ok {
				nodeID = strings.TrimSpace(agent.NodeID)
			}
		}
		if nodeID != "" {
			if applyErr := s.nodeSvc.ApplyAgentTaskObservation(ctx, nodeID, task.Type, task.Status == model.TaskStatusSuccess, firstNonEmpty(task.Message, "agent task reported"), task.Detail); applyErr != nil {
				s.logger.Warn("apply node observation failed", "task_id", task.ID, "node_id", nodeID, "error", applyErr)
			}
		}
	}

	if isSupportedAgentTaskType(task.Type) && s.modelSvc != nil {
		if applyErr := s.modelSvc.ApplyAgentTaskObservation(ctx, task); applyErr != nil {
			s.logger.Warn("apply model observation failed", "task_id", task.ID, "task_type", task.Type, "target_id", task.TargetID, "error", applyErr)
		}
	}

	return task, nil
}

func (s *TaskService) TryRunRuntimePrecheckViaAgent(ctx context.Context, item model.Model) (bool, string, map[string]interface{}, bool, error) {
	if s.store == nil || s.agentSvc == nil {
		return false, "", nil, false, nil
	}
	timeoutSeconds := 3
	payload := map[string]interface{}{
		"runtime_id":      strings.TrimSpace(item.RuntimeID),
		"endpoint":        strings.TrimSpace(item.Endpoint),
		"health_path":     "/health",
		"timeout_seconds": timeoutSeconds,
	}
	if path := readMetadataValue(item.Metadata, "path"); path != "" {
		payload["model_path"] = path
	}
	if runtimeEndpoint := readMetadataValue(item.Metadata, "runtime_service_endpoint"); runtimeEndpoint != "" && strings.TrimSpace(item.Endpoint) == "" {
		payload["endpoint"] = runtimeEndpoint
	}
	if containerID := readMetadataValue(item.Metadata, "runtime_container_id"); containerID != "" {
		payload["runtime_container_id"] = containerID
	}

	req := AgentNodeLocalTaskRequest{
		ModelID:     item.ID,
		TaskType:    model.TaskTypeAgentRuntimePrecheck,
		Payload:     payload,
		TriggeredBy: "controller.reconcile",
	}
	if s.runtimeObjectSvc != nil {
		if instance, err := s.runtimeObjectSvc.GetRuntimeInstanceByModelID(ctx, item.ID); err == nil {
			req.RuntimeInstanceID = instance.ID
		}
	}
	if req.RuntimeInstanceID == "" {
		// compatibility path: instance 未就绪时保留 model/node 粗粒度输入。
		req.TaskScope = "legacy_model_or_node"
		req.PayloadContext = map[string]interface{}{
			"compatibility_path": "legacy_model_or_node",
		}
		nodeID := strings.TrimSpace(item.HostNodeID)
		if nodeID == "" {
			return false, "", nil, false, nil
		}
		agentState, ok := s.agentSvc.GetByNodeID(nodeID)
		if !ok || agentState == nil || strings.TrimSpace(agentState.ID) == "" || agentState.Status != model.AgentStatusOnline {
			return false, "", nil, false, nil
		}
		req.AgentID = agentState.ID
		req.NodeID = nodeID
	}

	created, err := s.CreateAgentNodeTask(ctx, req)
	if err != nil {
		return false, "", nil, true, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+5)*time.Second)
	defer cancel()
	finalTask, err := s.AwaitTask(waitCtx, created.ID, 500*time.Millisecond)
	if err != nil {
		return false, "agent precheck await failed", map[string]interface{}{"task_id": created.ID, "error": err.Error()}, true, err
	}
	if finalTask.Status == model.TaskStatusSuccess {
		return true, firstNonEmpty(finalTask.Message, "agent runtime precheck success"), finalTask.Detail, true, nil
	}
	return false, firstNonEmpty(finalTask.Message, "agent runtime precheck failed"), finalTask.Detail, true, nil
}

func (s *TaskService) ListTasks(ctx context.Context, targetType, targetID string, limit int) ([]model.Task, error) {
	return s.ListTasksFiltered(ctx, targetType, targetID, "", false, limit)
}

func (s *TaskService) ListTasksFiltered(ctx context.Context, targetType, targetID, runtimeInstanceID string, agentOnly bool, limit int) ([]model.Task, error) {
	if s.store == nil {
		return nil, ErrTaskStoreNotReady
	}
	if limit <= 0 {
		limit = 100
	}
	runtimeInstanceID = strings.TrimSpace(runtimeInstanceID)
	if !agentOnly && runtimeInstanceID == "" {
		items, err := s.store.ListTasks(ctx, targetType, targetID, limit)
		if err != nil {
			return nil, err
		}
		for i := range items {
			enrichTaskRuntimeFields(&items[i])
		}
		return items, nil
	}

	scanLimit := computeTaskScanLimit(limit)
	items, err := s.store.ListTasks(ctx, targetType, targetID, scanLimit)
	if err != nil {
		return nil, err
	}
	out := make([]model.Task, 0, minInt(limit, len(items)))
	for _, item := range items {
		enrichTaskRuntimeFields(&item)
		if runtimeInstanceID != "" && readTaskRuntimeInstanceID(item) != runtimeInstanceID {
			continue
		}
		if agentOnly && !isSupportedAgentTaskType(item.Type) {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *TaskService) ListRuntimeInstanceAgentTasks(ctx context.Context, runtimeInstanceID string, limit int) ([]model.Task, error) {
	return s.ListTasksFiltered(ctx, "", "", runtimeInstanceID, true, limit)
}

func (s *TaskService) GetTask(ctx context.Context, id string) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	item, ok, err := s.store.GetTaskByID(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	if !ok {
		return model.Task{}, ErrTaskNotFound
	}
	enrichTaskRuntimeFields(&item)
	return item, nil
}

func (s *TaskService) AwaitTask(ctx context.Context, id string, pollInterval time.Duration) (model.Task, error) {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		item, err := s.GetTask(ctx, id)
		if err != nil {
			return model.Task{}, err
		}
		if isTaskTerminal(item.Status) {
			return item, nil
		}
		select {
		case <-ctx.Done():
			return model.Task{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *TaskService) patchTask(taskID string, patch func(*model.Task)) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	item, ok, err := s.store.GetTaskByID(ctx, taskID)
	if err != nil || !ok {
		return
	}
	patch(&item)
	if err := s.store.UpsertTask(ctx, item); err != nil {
		s.logger.Warn("patch task failed", "task_id", taskID, "error", err)
	}
}

func (s *TaskService) failTask(taskID string, status model.TaskStatus, message, errText string, detail map[string]interface{}) {
	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = status
		task.Progress = 100
		task.Message = firstNonEmpty(message, "任务执行失败")
		task.Error = strings.TrimSpace(errText)
		task.Detail = detail
		if task.StartedAt.IsZero() {
			task.StartedAt = now
		}
		task.FinishedAt = now
	})
}

func enrichTaskRuntimeFields(task *model.Task) {
	if task == nil {
		return
	}
	if task.Payload == nil {
		task.Payload = map[string]interface{}{}
	}
	if task.Detail == nil {
		task.Detail = map[string]interface{}{}
	}
	resolvedContext, _ := task.Payload["resolved_context"].(map[string]interface{})
	if resolvedContext == nil {
		resolvedContext = map[string]interface{}{}
	}

	syncField := func(key string) {
		value := firstNonEmpty(
			readStringFromObjectMap(task.Payload, key),
			readStringFromObjectMap(resolvedContext, key),
			readStringFromObjectMap(task.Detail, key),
		)
		if value == "" {
			return
		}
		task.Payload[key] = value
		task.Detail[key] = value
		resolvedContext[key] = value
	}

	for _, key := range []string{
		"task_scope",
		"runtime_instance_id",
		"runtime_binding_id",
		"runtime_template_id",
		"manifest_id",
		"node_id",
		"model_id",
		"binding_mode",
		"runtime_kind",
		"template_type",
		"runtime_template_type",
		"endpoint",
		"health_path",
		"model_path",
		"script_ref",
		"runtime_container_id",
	} {
		syncField(key)
	}
	if _, ok := task.Payload["resolved_context"].(map[string]interface{}); !ok && len(resolvedContext) > 0 {
		task.Payload["resolved_context"] = resolvedContext
	}
}

func readTaskRuntimeInstanceID(task model.Task) string {
	return firstNonEmpty(
		readStringFromObjectMap(task.Payload, "runtime_instance_id"),
		readStringFromNestedObjectMap(task.Payload, "resolved_context", "runtime_instance_id"),
		readStringFromObjectMap(task.Detail, "runtime_instance_id"),
	)
}

func readStringFromNestedObjectMap(in map[string]interface{}, nestedKey, key string) string {
	if len(in) == 0 {
		return ""
	}
	nestedRaw, ok := in[nestedKey]
	if !ok {
		return ""
	}
	nested, ok := nestedRaw.(map[string]interface{})
	if !ok {
		return ""
	}
	return readStringFromObjectMap(nested, key)
}

func readStringFromObjectMap(in map[string]interface{}, key string) string {
	if len(in) == 0 {
		return ""
	}
	raw, ok := in[key]
	if !ok {
		return ""
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	if value == "" || value == "<nil>" {
		return ""
	}
	return value
}

func computeTaskScanLimit(limit int) int {
	if limit <= 0 {
		limit = 100
	}
	scanLimit := limit * 20
	if scanLimit < 200 {
		scanLimit = 200
	}
	if scanLimit > 2000 {
		scanLimit = 2000
	}
	return scanLimit
}

func minInt(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isRuntimeTaskType(taskType model.TaskType) bool {
	switch taskType {
	case model.TaskTypeRuntimeStart, model.TaskTypeRuntimeStop, model.TaskTypeRuntimeRestart, model.TaskTypeRuntimeRefresh:
		return true
	default:
		return false
	}
}

func supportedAgentTaskTypes() []model.TaskType {
	return []model.TaskType{
		model.TaskTypeAgentRuntimeReadiness,
		model.TaskTypeAgentRuntimePrecheck,
		model.TaskTypeAgentPortCheck,
		model.TaskTypeAgentModelPathCheck,
		model.TaskTypeAgentResourceSnapshot,
		model.TaskTypeAgentDockerInspect,
		model.TaskTypeAgentDockerStart,
		model.TaskTypeAgentDockerStop,
	}
}

func isSupportedAgentTaskType(taskType model.TaskType) bool {
	switch taskType {
	case model.TaskTypeAgentRuntimeReadiness,
		model.TaskTypeAgentRuntimePrecheck,
		model.TaskTypeAgentPortCheck,
		model.TaskTypeAgentModelPathCheck,
		model.TaskTypeAgentResourceSnapshot,
		model.TaskTypeAgentDockerInspect,
		model.TaskTypeAgentDockerStart,
		model.TaskTypeAgentDockerStop:
		return true
	default:
		return false
	}
}

func isTaskTerminal(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusTimeout, model.TaskStatusCanceled:
		return true
	default:
		return false
	}
}

func normalizeProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func maxProgress(current, incoming int) int {
	if incoming > current {
		return normalizeProgress(incoming)
	}
	return normalizeProgress(current)
}

func newTaskID(prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		prefix = "task"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func readErrorFromDetail(detail map[string]interface{}) string {
	if detail == nil {
		return ""
	}
	if raw, ok := detail["error"]; ok {
		v := strings.TrimSpace(fmt.Sprint(raw))
		if v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}
