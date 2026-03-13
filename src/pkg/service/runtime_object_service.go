package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"model-control-plane/src/pkg/model"
	"model-control-plane/src/pkg/registry"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

var (
	ErrRuntimeBindingNotFound   = errors.New("runtime binding not found")
	ErrRuntimeInstanceNotFound  = errors.New("runtime instance not found")
	ErrRuntimeTemplateNotFound  = errors.New("runtime template not found")
	ErrRuntimeManifestNotFound  = errors.New("runtime manifest not found")
	ErrRuntimeBindingValidation = errors.New("runtime binding validation failed")
)

type RuntimeInstanceResolvedContext struct {
	Instance model.RuntimeInstance
	Binding  model.RuntimeBinding
	Template model.RuntimeTemplate
	Manifest model.RuntimeBundleManifest
}

type RuntimeObjectService struct {
	modelRegistry *registry.ModelRegistry
	nodeRegistry  *registry.NodeRegistry
	templates     *RuntimeTemplateService
	store         *sqlitestore.Store
	logger        *slog.Logger

	mu        sync.RWMutex
	bindings  map[string]model.RuntimeBinding
	instances map[string]model.RuntimeInstance
	manifests map[string]model.RuntimeBundleManifest
}

func NewRuntimeObjectService(
	modelRegistry *registry.ModelRegistry,
	nodeRegistry *registry.NodeRegistry,
	templates *RuntimeTemplateService,
	logger *slog.Logger,
) *RuntimeObjectService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RuntimeObjectService{
		modelRegistry: modelRegistry,
		nodeRegistry:  nodeRegistry,
		templates:     templates,
		logger:        logger,
		bindings:      make(map[string]model.RuntimeBinding),
		instances:     make(map[string]model.RuntimeInstance),
		manifests:     make(map[string]model.RuntimeBundleManifest),
	}
}

func (s *RuntimeObjectService) SetStore(ctx context.Context, store *sqlitestore.Store) error {
	s.store = store
	if store == nil {
		return nil
	}
	if err := s.reloadBindingsFromStore(ctx); err != nil {
		return err
	}
	if err := s.reloadInstancesFromStore(ctx); err != nil {
		return err
	}
	if err := s.reloadManifestsFromStore(ctx); err != nil {
		return err
	}
	return nil
}

func (s *RuntimeObjectService) Bootstrap(ctx context.Context) error {
	if err := s.SyncTemplateManifests(ctx); err != nil {
		return err
	}
	items := s.modelRegistry.List()
	var errs []error
	for _, item := range items {
		if err := s.SyncModelRuntimeObjects(ctx, item); err != nil {
			errs = append(errs, fmt.Errorf("model=%s: %w", item.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *RuntimeObjectService) ListBindings(ctx context.Context) ([]model.RuntimeBinding, error) {
	if s.store != nil {
		items, err := s.store.ListRuntimeBindings(ctx)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		for _, item := range items {
			s.bindings[item.ID] = item
		}
		s.mu.Unlock()
		return items, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.RuntimeBinding, 0, len(s.bindings))
	for _, item := range s.bindings {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *RuntimeObjectService) CreateBinding(ctx context.Context, input model.RuntimeBinding) (model.RuntimeBinding, error) {
	normalized, err := s.normalizeAndValidateBinding(ctx, input)
	if err != nil {
		return model.RuntimeBinding{}, err
	}
	if err := s.upsertBinding(ctx, normalized); err != nil {
		return model.RuntimeBinding{}, err
	}
	return normalized, nil
}

func (s *RuntimeObjectService) GetBinding(ctx context.Context, id string) (model.RuntimeBinding, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.RuntimeBinding{}, ErrRuntimeBindingNotFound
	}
	if s.store != nil {
		item, ok, err := s.store.GetRuntimeBindingByID(ctx, id)
		if err != nil {
			return model.RuntimeBinding{}, err
		}
		if !ok {
			return model.RuntimeBinding{}, ErrRuntimeBindingNotFound
		}
		s.mu.Lock()
		s.bindings[item.ID] = item
		s.mu.Unlock()
		return item, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.bindings[id]
	if !ok {
		return model.RuntimeBinding{}, ErrRuntimeBindingNotFound
	}
	return item, nil
}

func (s *RuntimeObjectService) ListRuntimeInstances(ctx context.Context) ([]model.RuntimeInstance, error) {
	if s.store != nil {
		items, err := s.store.ListRuntimeInstances(ctx)
		if err != nil {
			return nil, err
		}
		for i := range items {
			s.hydrateRuntimeInstanceDerivedFields(&items[i])
		}
		s.mu.Lock()
		for _, item := range items {
			s.instances[item.ID] = item
		}
		s.mu.Unlock()
		return items, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.RuntimeInstance, 0, len(s.instances))
	for _, item := range s.instances {
		s.hydrateRuntimeInstanceDerivedFields(&item)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *RuntimeObjectService) GetRuntimeInstance(ctx context.Context, id string) (model.RuntimeInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.RuntimeInstance{}, ErrRuntimeInstanceNotFound
	}
	if s.store != nil {
		item, ok, err := s.store.GetRuntimeInstanceByID(ctx, id)
		if err != nil {
			return model.RuntimeInstance{}, err
		}
		if !ok {
			return model.RuntimeInstance{}, ErrRuntimeInstanceNotFound
		}
		s.hydrateRuntimeInstanceDerivedFields(&item)
		s.mu.Lock()
		s.instances[item.ID] = item
		s.mu.Unlock()
		return item, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.instances[id]
	if !ok {
		return model.RuntimeInstance{}, ErrRuntimeInstanceNotFound
	}
	s.hydrateRuntimeInstanceDerivedFields(&item)
	return item, nil
}

func (s *RuntimeObjectService) GetRuntimeInstanceByModelID(ctx context.Context, modelID string) (model.RuntimeInstance, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return model.RuntimeInstance{}, ErrRuntimeInstanceNotFound
	}
	items, err := s.ListRuntimeInstances(ctx)
	if err != nil {
		return model.RuntimeInstance{}, err
	}
	for _, item := range items {
		if strings.TrimSpace(item.ModelID) == modelID {
			return item, nil
		}
	}
	return model.RuntimeInstance{}, ErrRuntimeInstanceNotFound
}

func (s *RuntimeObjectService) GetManifestByID(ctx context.Context, manifestID string) (model.RuntimeBundleManifest, error) {
	manifestID = strings.TrimSpace(manifestID)
	if manifestID == "" {
		return model.RuntimeBundleManifest{}, ErrRuntimeManifestNotFound
	}
	if s.store != nil {
		item, ok, err := s.store.GetRuntimeBundleManifestByID(ctx, manifestID)
		if err != nil {
			return model.RuntimeBundleManifest{}, err
		}
		if ok {
			s.mu.Lock()
			s.manifests[item.ID] = item
			s.mu.Unlock()
			return item, nil
		}
	}
	s.mu.RLock()
	item, ok := s.manifests[manifestID]
	s.mu.RUnlock()
	if !ok {
		return model.RuntimeBundleManifest{}, ErrRuntimeManifestNotFound
	}
	return item, nil
}

func (s *RuntimeObjectService) ResolveRuntimeInstanceContext(ctx context.Context, runtimeInstanceID string) (RuntimeInstanceResolvedContext, error) {
	if s.templates == nil {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_instance_id=%s failed: runtime template service is nil", strings.TrimSpace(runtimeInstanceID))
	}
	instance, err := s.GetRuntimeInstance(ctx, runtimeInstanceID)
	if err != nil {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_instance_id=%s failed: %w", strings.TrimSpace(runtimeInstanceID), err)
	}
	bindingID := strings.TrimSpace(instance.BindingID)
	if bindingID == "" {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_instance_id=%s failed: binding_id is empty", strings.TrimSpace(runtimeInstanceID))
	}
	binding, err := s.GetBinding(ctx, bindingID)
	if err != nil {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_binding_id=%s failed: %w", bindingID, err)
	}

	templateID := firstNonEmpty(strings.TrimSpace(instance.TemplateID), strings.TrimSpace(binding.TemplateID))
	if templateID == "" {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_instance_id=%s failed: runtime_template_id is empty", strings.TrimSpace(runtimeInstanceID))
	}
	template, ok := s.templates.GetTemplate(ctx, templateID)
	if !ok {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_template_id=%s failed: %w", templateID, ErrRuntimeTemplateNotFound)
	}

	manifestID := strings.TrimSpace(binding.ManifestID)
	var manifest model.RuntimeBundleManifest
	if manifestID != "" {
		manifest, err = s.GetManifestByID(ctx, manifestID)
		if err != nil {
			return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve manifest_id=%s failed: %w", manifestID, err)
		}
	} else {
		manifest, err = s.GetTemplateManifest(ctx, templateID)
		if err != nil {
			return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve manifest by template_id=%s failed: %w", templateID, err)
		}
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return RuntimeInstanceResolvedContext{}, fmt.Errorf("resolve runtime_instance_id=%s failed: %w", strings.TrimSpace(runtimeInstanceID), ErrRuntimeManifestNotFound)
	}

	return RuntimeInstanceResolvedContext{
		Instance: instance,
		Binding:  binding,
		Template: template,
		Manifest: manifest,
	}, nil
}

func (s *RuntimeObjectService) ApplyAgentTaskObservation(ctx context.Context, task model.Task) error {
	if !isSupportedAgentTaskType(task.Type) {
		return nil
	}

	instanceID := strings.TrimSpace(readStringFromObjectMap(task.Payload, "runtime_instance_id"))
	if instanceID == "" {
		instanceID = strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "runtime_instance_id"))
	}
	if instanceID == "" {
		instanceID = strings.TrimSpace(readStringFromObjectMap(task.Detail, "runtime_instance_id"))
	}
	if instanceID == "" && task.TargetType == model.TaskTargetRuntime {
		modelID := strings.TrimSpace(task.TargetID)
		if modelID != "" {
			if item, err := s.GetRuntimeInstanceByModelID(ctx, modelID); err == nil {
				instanceID = strings.TrimSpace(item.ID)
			}
		}
	}
	if instanceID == "" {
		return nil
	}

	item, err := s.GetRuntimeInstance(ctx, instanceID)
	if err != nil {
		if errors.Is(err, ErrRuntimeInstanceNotFound) {
			return nil
		}
		return err
	}

	now := time.Now().UTC()
	success := task.Status == model.TaskStatusSuccess
	msg := firstNonEmpty(strings.TrimSpace(task.Message), strings.TrimSpace(task.Error))
	if msg == "" {
		msg = "agent task reported"
	}
	s.hydrateRuntimeInstanceDerivedFields(&item)
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	item.LastAgentTask = &model.RuntimeInstanceAgentTaskSummary{
		TaskID:          strings.TrimSpace(task.ID),
		TaskType:        task.Type,
		TaskStatus:      task.Status,
		Message:         msg,
		WorkerID:        strings.TrimSpace(task.WorkerID),
		AssignedAgentID: strings.TrimSpace(task.AssignedAgentID),
		TriggeredBy:     strings.TrimSpace(fmt.Sprint(task.Payload["triggered_by"])),
		FinishedAt:      chooseTaskEventTime(task, now),
	}

	if item.NodeID == "" {
		item.NodeID = firstNonEmpty(
			strings.TrimSpace(readStringFromObjectMap(task.Payload, "node_id")),
			strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "node_id")),
			item.NodeID,
		)
	}
	item.BindingMode = model.RuntimeBindingMode(firstNonEmpty(
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "binding_mode")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "binding_mode")),
		strings.TrimSpace(string(item.BindingMode)),
	))
	item.ManifestID = firstNonEmpty(
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "manifest_id")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "manifest_id")),
		strings.TrimSpace(item.ManifestID),
	)

	if endpoint := firstNonEmpty(
		strings.TrimSpace(fmt.Sprint(task.Detail["runtime_service_endpoint"])),
		strings.TrimSpace(fmt.Sprint(task.Detail["endpoint"])),
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "endpoint")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "endpoint")),
	); endpoint != "" && endpoint != "<nil>" {
		item.Endpoint = endpoint
	}
	if containerID := firstNonEmpty(
		strings.TrimSpace(fmt.Sprint(task.Detail["runtime_container_id"])),
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "runtime_container_id")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "runtime_container_id")),
	); containerID != "" && containerID != "<nil>" {
		item.Metadata["runtime_container_id"] = containerID
	}
	if mounts := firstNonEmptyStringSlice(
		readStringSliceFromObjectMap(task.Detail, "resolved_mounts"),
		readStringSliceFromObjectMap(task.Payload, "binding_mount_rules"),
		readStringSliceFromObjectMap(task.Payload, "mount_points"),
	); len(mounts) > 0 {
		item.ResolvedMounts = cloneStringSlice(mounts)
		item.MountedPaths = cloneStringSlice(mounts)
	}
	if ports := firstNonEmptyStringSlice(
		readStringSliceFromObjectMap(task.Detail, "resolved_ports"),
		readStringSliceFromObjectMap(task.Payload, "exposed_ports"),
		readStringSliceFromObjectMap(task.Payload, "resolved_ports"),
	); len(ports) > 0 {
		item.ResolvedPorts = cloneStringSlice(ports)
	}
	if script := firstNonEmpty(
		strings.TrimSpace(fmt.Sprint(task.Detail["resolved_script"])),
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "script_ref")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "script_ref")),
	); script != "" && script != "<nil>" {
		item.ResolvedScript = script
		item.ScriptUsed = script
	}

	switch task.Type {
	case model.TaskTypeAgentRuntimePrecheck:
		item.LastPrecheckTaskID = task.ID
		item.LastPrecheckAt = now
		if success {
			item.PrecheckStatus = model.PrecheckStatusOK
			item.PrecheckGating = false
			item.PrecheckReasons = nil
		} else {
			item.PrecheckStatus = model.PrecheckStatusFailed
			item.PrecheckGating = true
			item.PrecheckReasons = cloneStringSlice(readStringSliceFromObjectMap(task.Detail, "precheck_failures"))
		}
		if precheck := decodeRuntimePrecheckResult(task.Detail["precheck_result"]); precheck != nil {
			item.PrecheckResult = precheck
			item.PrecheckStatus = precheck.OverallStatus
			item.PrecheckGating = precheck.Gating
			item.PrecheckReasons = extractPrecheckReasonCodes(precheck.Reasons)
			if len(precheck.ResolvedMounts) > 0 {
				item.ResolvedMounts = cloneStringSlice(precheck.ResolvedMounts)
				item.MountedPaths = cloneStringSlice(precheck.ResolvedMounts)
			}
			if len(precheck.ResolvedPorts) > 0 {
				item.ResolvedPorts = cloneStringSlice(precheck.ResolvedPorts)
			}
			if script := strings.TrimSpace(precheck.ResolvedScript); script != "" {
				item.ResolvedScript = script
				item.ScriptUsed = script
			}
			if len(precheck.ResolvedEnv) > 0 {
				item.InjectedEnv = cloneStringMap(precheck.ResolvedEnv)
			}
		}
		if item.PrecheckGating {
			item.Readiness = model.ReadinessNotReady
			item.DriftReason = firstNonEmpty(item.DriftReason, "precheck_gating=true")
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentRuntimeReadiness:
		ready, hasReady := boolFromTaskDetail(task.Detail, "runtime_ready")
		if !hasReady {
			ready, hasReady = boolFromTaskDetail(task.Detail, "ready")
		}
		if hasReady {
			if ready {
				item.Readiness = model.ReadinessReady
				item.ObservedState = firstNonEmpty(strings.TrimSpace(item.ObservedState), "running")
			} else {
				item.Readiness = model.ReadinessNotReady
			}
		} else if success {
			item.Readiness = model.ReadinessReady
		} else {
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentPortCheck:
		if hostPort := strings.TrimSpace(fmt.Sprint(task.Detail["host_port"])); hostPort != "" && hostPort != "<nil>" {
			item.ResolvedPorts = appendUniqueString(item.ResolvedPorts, hostPort)
		}
		if !success {
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentModelPathCheck:
		if absPath := strings.TrimSpace(fmt.Sprint(task.Detail["abs_path"])); absPath != "" && absPath != "<nil>" {
			item.ResolvedMounts = appendUniqueString(item.ResolvedMounts, absPath)
			item.MountedPaths = appendUniqueString(item.MountedPaths, absPath)
		}
		if exists, ok := boolFromTaskDetail(task.Detail, "exists"); ok && !exists {
			item.Readiness = model.ReadinessNotReady
		}
		if !success {
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentDockerInspect, model.TaskTypeAgentDockerStart, model.TaskTypeAgentDockerStop:
		exists, hasExists := boolFromTaskDetail(task.Detail, "runtime_exists")
		running, hasRunning := boolFromTaskDetail(task.Detail, "runtime_running")
		if hasExists && !exists {
			item.ObservedState = "stopped"
			item.Readiness = model.ReadinessNotReady
		} else if hasRunning && running {
			item.ObservedState = "running"
			if ready, ok := boolFromTaskDetail(task.Detail, "runtime_ready"); ok && !ready {
				item.Readiness = model.ReadinessNotReady
			} else {
				item.Readiness = model.ReadinessReady
			}
		} else if hasExists && exists {
			item.ObservedState = "loaded"
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentResourceSnapshot:
		item.Metadata["last_resource_snapshot_task_id"] = task.ID
		item.Metadata["last_resource_snapshot_at"] = now.Format(time.RFC3339)
		if snapshot, ok := task.Detail["resource_snapshot"].(map[string]interface{}); ok {
			if hostname := strings.TrimSpace(fmt.Sprint(snapshot["hostname"])); hostname != "" && hostname != "<nil>" {
				item.Metadata["snapshot_hostname"] = hostname
			}
			if dockerRaw, ok := snapshot["docker_access"].(map[string]interface{}); ok {
				item.Metadata["snapshot_docker_accessible"] = strings.TrimSpace(fmt.Sprint(dockerRaw["api_reachable"]))
			}
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	}

	if observed := strings.TrimSpace(fmt.Sprint(task.Detail["observed_state"])); observed != "" && observed != "<nil>" {
		item.ObservedState = observed
	}
	if item.PrecheckGating && item.Readiness == model.ReadinessReady {
		item.Readiness = model.ReadinessNotReady
	}
	if item.DriftReason == "" {
		if drift := inferDriftReasonFromInstance(item); drift != "" {
			item.DriftReason = drift
		}
	}
	item.LastReconciledAt = now
	return s.upsertRuntimeInstance(ctx, item)
}

func decodeRuntimePrecheckResult(raw interface{}) *model.RuntimePrecheckResult {
	if raw == nil {
		return nil
	}
	bytes, err := json.Marshal(raw)
	if err != nil || len(bytes) == 0 {
		return nil
	}
	var out model.RuntimePrecheckResult
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil
	}
	if strings.TrimSpace(string(out.OverallStatus)) == "" {
		return nil
	}
	return &out
}

func extractPrecheckReasonCodes(reasons []model.RuntimePrecheckReason) []string {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(reasons))
	seen := map[string]struct{}{}
	for _, reason := range reasons {
		code := strings.TrimSpace(string(reason.Code))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func chooseTaskEventTime(task model.Task, fallback time.Time) time.Time {
	if !task.FinishedAt.IsZero() {
		return task.FinishedAt.UTC()
	}
	if !task.StartedAt.IsZero() {
		return task.StartedAt.UTC()
	}
	if !task.AcceptedAt.IsZero() {
		return task.AcceptedAt.UTC()
	}
	if !task.CreatedAt.IsZero() {
		return task.CreatedAt.UTC()
	}
	return fallback.UTC()
}

func firstNonEmptyStringSlice(candidates ...[]string) []string {
	for _, candidate := range candidates {
		normalized := cloneStringSlice(candidate)
		if len(normalized) > 0 {
			return normalized
		}
	}
	return nil
}

func appendUniqueString(in []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return cloneStringSlice(in)
	}
	out := cloneStringSlice(in)
	for _, existing := range out {
		if strings.EqualFold(strings.TrimSpace(existing), value) {
			return out
		}
	}
	out = append(out, value)
	return out
}

func (s *RuntimeObjectService) hydrateRuntimeInstanceDerivedFields(item *model.RuntimeInstance) {
	if item == nil {
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	if strings.TrimSpace(string(item.BindingMode)) == "" {
		item.BindingMode = model.RuntimeBindingMode(strings.TrimSpace(item.Metadata["binding_mode"]))
	}
	if strings.TrimSpace(item.ManifestID) == "" {
		item.ManifestID = strings.TrimSpace(item.Metadata["manifest_id"])
	}
	if len(item.ResolvedMounts) == 0 {
		item.ResolvedMounts = cloneStringSlice(item.MountedPaths)
	}
	if len(item.ResolvedMounts) == 0 {
		item.ResolvedMounts = parseJSONStringSlice(item.Metadata["resolved_mounts_json"])
	}
	if len(item.ResolvedPorts) == 0 {
		item.ResolvedPorts = parseJSONStringSlice(item.Metadata["resolved_ports_json"])
	}
	if strings.TrimSpace(item.ResolvedScript) == "" {
		item.ResolvedScript = firstNonEmpty(strings.TrimSpace(item.ScriptUsed), strings.TrimSpace(item.Metadata["resolved_script"]))
	}
	if item.LastAgentTask == nil {
		item.LastAgentTask = parseRuntimeInstanceAgentTaskSummary(item.Metadata["last_agent_task_json"])
	}
}

func (s *RuntimeObjectService) persistRuntimeInstanceDerivedFields(item *model.RuntimeInstance) {
	if item == nil {
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	if strings.TrimSpace(string(item.BindingMode)) != "" {
		item.Metadata["binding_mode"] = strings.TrimSpace(string(item.BindingMode))
	}
	if strings.TrimSpace(item.ManifestID) != "" {
		item.Metadata["manifest_id"] = strings.TrimSpace(item.ManifestID)
	}
	if len(item.ResolvedMounts) == 0 {
		item.ResolvedMounts = cloneStringSlice(item.MountedPaths)
	}
	if len(item.MountedPaths) == 0 {
		item.MountedPaths = cloneStringSlice(item.ResolvedMounts)
	}
	if len(item.ResolvedMounts) > 0 {
		item.Metadata["resolved_mounts_json"] = mustJSON(item.ResolvedMounts, "[]")
	}
	if len(item.ResolvedPorts) > 0 {
		item.Metadata["resolved_ports_json"] = mustJSON(item.ResolvedPorts, "[]")
	}
	if strings.TrimSpace(item.ResolvedScript) == "" {
		item.ResolvedScript = strings.TrimSpace(item.ScriptUsed)
	}
	if strings.TrimSpace(item.ScriptUsed) == "" {
		item.ScriptUsed = strings.TrimSpace(item.ResolvedScript)
	}
	if strings.TrimSpace(item.ResolvedScript) != "" {
		item.Metadata["resolved_script"] = strings.TrimSpace(item.ResolvedScript)
	}
	if item.LastAgentTask != nil {
		item.Metadata["last_agent_task_json"] = encodeRuntimeInstanceAgentTaskSummary(item.LastAgentTask)
		item.Metadata["last_agent_task_type"] = strings.TrimSpace(string(item.LastAgentTask.TaskType))
		item.Metadata["last_agent_task_status"] = strings.TrimSpace(string(item.LastAgentTask.TaskStatus))
		item.Metadata["last_agent_task_id"] = strings.TrimSpace(item.LastAgentTask.TaskID)
		item.Metadata["last_agent_task_message"] = strings.TrimSpace(item.LastAgentTask.Message)
		if !item.LastAgentTask.FinishedAt.IsZero() {
			item.Metadata["last_agent_task_at"] = item.LastAgentTask.FinishedAt.UTC().Format(time.RFC3339)
		}
	}
}

func parseJSONStringSlice(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return cloneStringSlice(out)
}

func parseRuntimeInstanceAgentTaskSummary(raw string) *model.RuntimeInstanceAgentTaskSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	var out model.RuntimeInstanceAgentTaskSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if strings.TrimSpace(out.TaskID) == "" && strings.TrimSpace(string(out.TaskType)) == "" {
		return nil
	}
	return &out
}

func encodeRuntimeInstanceAgentTaskSummary(summary *model.RuntimeInstanceAgentTaskSummary) string {
	if summary == nil {
		return "{}"
	}
	return mustJSON(summary, "{}")
}

func mustJSON(v interface{}, fallback string) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	out := strings.TrimSpace(string(raw))
	if out == "" {
		return fallback
	}
	return out
}

func readStringSliceFromObjectMap(in map[string]interface{}, key string) []string {
	if len(in) == 0 {
		return nil
	}
	raw, ok := in[key]
	if !ok || raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return cloneStringSlice(value)
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" || text == "<nil>" {
				continue
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func inferDriftReasonFromInstance(item model.RuntimeInstance) string {
	desired := strings.TrimSpace(strings.ToLower(item.DesiredState))
	observed := strings.TrimSpace(strings.ToLower(item.ObservedState))
	if desired == "" || observed == "" || desired == observed {
		return ""
	}
	return fmt.Sprintf("desired=%s observed=%s", item.DesiredState, item.ObservedState)
}

func (s *RuntimeObjectService) GetTemplateManifest(ctx context.Context, templateID string) (model.RuntimeBundleManifest, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return model.RuntimeBundleManifest{}, ErrRuntimeManifestNotFound
	}
	if s.store != nil {
		if item, ok, err := s.store.GetRuntimeBundleManifestByTemplateID(ctx, templateID); err == nil && ok {
			s.mu.Lock()
			s.manifests[item.ID] = item
			s.mu.Unlock()
			return item, nil
		}
	}
	tpl, ok := s.templates.GetTemplate(ctx, templateID)
	if !ok {
		return model.RuntimeBundleManifest{}, ErrRuntimeTemplateNotFound
	}
	var manifest model.RuntimeBundleManifest
	if tpl.Manifest != nil {
		manifest = *tpl.Manifest
	} else {
		manifest = manifestFromTemplate(tpl)
	}
	if strings.TrimSpace(manifest.ID) == "" {
		manifest.ID = tpl.ID
	}
	manifest.TemplateID = firstNonEmpty(strings.TrimSpace(manifest.TemplateID), tpl.ID)
	normalized, errs := normalizeAndValidateManifest(manifest)
	if len(errs) > 0 {
		return model.RuntimeBundleManifest{}, fmt.Errorf("manifest invalid: %s", strings.Join(errs, "; "))
	}
	if err := s.upsertManifest(ctx, normalized); err != nil {
		return model.RuntimeBundleManifest{}, err
	}
	return normalized, nil
}

func (s *RuntimeObjectService) SyncTemplateManifests(ctx context.Context) error {
	if s.templates == nil {
		return nil
	}
	templates := s.templates.ListTemplates(ctx)
	var errs []error
	for _, tpl := range templates {
		if _, err := s.GetTemplateManifest(ctx, tpl.ID); err != nil {
			errs = append(errs, fmt.Errorf("template=%s: %w", tpl.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *RuntimeObjectService) SyncModelRuntimeObjects(ctx context.Context, item model.Model) error {
	if strings.TrimSpace(item.ID) == "" {
		return nil
	}
	if item.BackendType == model.RuntimeTypeLMStudio {
		return nil
	}
	binding, err := s.ensureBindingForModel(ctx, item)
	if err != nil {
		return err
	}
	_, err = s.upsertRuntimeInstanceFromBinding(ctx, binding, item)
	return err
}

func (s *RuntimeObjectService) ensureBindingForModel(ctx context.Context, item model.Model) (model.RuntimeBinding, error) {
	existing, ok := s.findBindingByModel(item.ID)
	if ok {
		normalized, err := s.normalizeAndValidateBinding(ctx, existing)
		if err != nil {
			return existing, nil
		}
		if err := s.upsertBinding(ctx, normalized); err != nil {
			return model.RuntimeBinding{}, err
		}
		return normalized, nil
	}
	templateID := strings.TrimSpace(readMetadataValue(item.Metadata, "runtime_template_id"))
	if templateID == "" {
		if item.ModelType == model.ModelKindEmbedding {
			templateID = DefaultEmbeddingTemplateID
		} else {
			templateID = DefaultDockerTemplateID
		}
	}
	tpl, tplOK := s.templates.GetTemplate(ctx, templateID)
	if !tplOK {
		return model.RuntimeBinding{}, fmt.Errorf("%w: template=%s", ErrRuntimeTemplateNotFound, templateID)
	}
	manifest, _ := s.GetTemplateManifest(ctx, templateID)
	mode := model.RuntimeBindingModeGenericInjected
	if tpl.Dedicated {
		mode = model.RuntimeBindingModeDedicated
	}
	binding := model.RuntimeBinding{
		ID:            "rb-" + safeIDSegment(item.ID) + "-" + safeIDSegment(templateID),
		ModelID:       item.ID,
		TemplateID:    templateID,
		BindingMode:   mode,
		PreferredNode: strings.TrimSpace(item.HostNodeID),
		NodeSelector: map[string]string{
			"node_id": strings.TrimSpace(item.HostNodeID),
		},
		MountRules:   mergePreferredStringList(nil, tpl.InjectableMounts, tpl.Volumes),
		EnvOverrides: map[string]string{},
		Enabled:      true,
		ManifestID:   strings.TrimSpace(manifest.ID),
		Metadata: map[string]string{
			"generated_from": "model-sync",
			"phase":          "stage-0",
		},
	}
	if scriptRef := firstNonEmpty(strings.TrimSpace(item.ScriptRef), readMetadataValue(item.Metadata, "script_ref")); scriptRef != "" {
		binding.ScriptRef = scriptRef
	}
	normalized, err := s.normalizeAndValidateBinding(ctx, binding)
	if err != nil {
		return model.RuntimeBinding{}, err
	}
	if err := s.upsertBinding(ctx, normalized); err != nil {
		return model.RuntimeBinding{}, err
	}
	return normalized, nil
}

func (s *RuntimeObjectService) upsertRuntimeInstanceFromBinding(ctx context.Context, binding model.RuntimeBinding, item model.Model) (model.RuntimeInstance, error) {
	existing, ok := s.findInstanceByModel(item.ID)
	id := "ri-" + safeIDSegment(item.ID)
	createdAt := time.Now().UTC()
	if ok {
		id = existing.ID
		createdAt = existing.CreatedAt
	}
	tpl, _ := s.templates.GetTemplate(ctx, binding.TemplateID)
	desiredState := firstNonEmpty(strings.TrimSpace(item.DesiredState), string(item.State), string(model.ModelStateUnknown))
	observedState := firstNonEmpty(strings.TrimSpace(item.ObservedState), string(item.State), string(model.ModelStateUnknown))
	endpoint := firstNonEmpty(strings.TrimSpace(item.Endpoint), readMetadataValue(item.Metadata, "runtime_service_endpoint"))
	injectedEnv := cloneStringMap(tpl.Env)
	for k, v := range binding.EnvOverrides {
		injectedEnv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	resolvedPorts := normalizeStringList(tpl.ExposedPorts)
	if len(resolvedPorts) == 0 {
		if manifestID := strings.TrimSpace(binding.ManifestID); manifestID != "" {
			if manifest, getErr := s.GetManifestByID(ctx, manifestID); getErr == nil {
				resolvedPorts = normalizeStringList(manifest.ExposedPorts)
			}
		} else if manifest, getErr := s.GetTemplateManifest(ctx, binding.TemplateID); getErr == nil {
			resolvedPorts = normalizeStringList(manifest.ExposedPorts)
		}
	}
	instance := model.RuntimeInstance{
		ID:               id,
		ModelID:          item.ID,
		TemplateID:       binding.TemplateID,
		BindingID:        binding.ID,
		BindingMode:      binding.BindingMode,
		ManifestID:       strings.TrimSpace(binding.ManifestID),
		NodeID:           firstNonEmpty(strings.TrimSpace(binding.PreferredNode), strings.TrimSpace(item.HostNodeID)),
		DesiredState:     desiredState,
		ObservedState:    observedState,
		Readiness:        item.Readiness,
		HealthMessage:    strings.TrimSpace(item.HealthMessage),
		DriftReason:      inferDriftReason(item),
		Endpoint:         endpoint,
		LaunchedCommand:  mergePreferredStringList(nil, binding.CommandOverride, tpl.CommandTemplate, tpl.Command),
		MountedPaths:     mergePreferredStringList(nil, binding.MountRules, tpl.InjectableMounts, tpl.Volumes),
		InjectedEnv:      injectedEnv,
		ScriptUsed:       firstNonEmpty(strings.TrimSpace(binding.ScriptRef), strings.TrimSpace(item.ScriptRef)),
		ResolvedMounts:   mergePreferredStringList(nil, binding.MountRules, tpl.InjectableMounts, tpl.Volumes),
		ResolvedScript:   firstNonEmpty(strings.TrimSpace(binding.ScriptRef), strings.TrimSpace(item.ScriptRef)),
		ResolvedPorts:    cloneStringSlice(resolvedPorts),
		LastReconciledAt: item.LastReconciledAt,
		Metadata: map[string]string{
			"binding_mode": string(binding.BindingMode),
			"manifest_id":  strings.TrimSpace(binding.ManifestID),
			"phase":        "stage-a",
		},
		PrecheckStatus: model.PrecheckStatusUnknown,
		CreatedAt:      createdAt,
		UpdatedAt:      time.Now().UTC(),
	}
	if instance.NodeID == "" {
		nodes := s.nodeRegistry.List()
		if len(nodes) > 0 {
			instance.NodeID = nodes[0].ID
		}
	}
	if strings.TrimSpace(instance.ManifestID) == "" {
		if manifest, getErr := s.GetTemplateManifest(ctx, binding.TemplateID); getErr == nil {
			instance.ManifestID = strings.TrimSpace(manifest.ID)
			instance.Metadata["manifest_id"] = strings.TrimSpace(manifest.ID)
			if len(instance.ResolvedPorts) == 0 {
				instance.ResolvedPorts = normalizeStringList(manifest.ExposedPorts)
			}
		}
	}
	if !ok && item.LastReconciledAt.IsZero() {
		instance.LastReconciledAt = time.Now().UTC()
	}
	if instance.Readiness == "" {
		instance.Readiness = model.ReadinessUnknown
	}
	if strings.TrimSpace(string(instance.PrecheckStatus)) == "" {
		instance.PrecheckStatus = model.PrecheckStatusUnknown
	}
	if err := s.upsertRuntimeInstance(ctx, instance); err != nil {
		return model.RuntimeInstance{}, err
	}
	return instance, nil
}

func (s *RuntimeObjectService) normalizeAndValidateBinding(ctx context.Context, input model.RuntimeBinding) (model.RuntimeBinding, error) {
	out := input
	out.ID = strings.TrimSpace(out.ID)
	out.ModelID = strings.TrimSpace(out.ModelID)
	out.TemplateID = strings.TrimSpace(out.TemplateID)
	out.BindingMode = model.RuntimeBindingMode(strings.ToLower(strings.TrimSpace(string(out.BindingMode))))
	out.PreferredNode = strings.TrimSpace(out.PreferredNode)
	out.ScriptRef = strings.TrimSpace(out.ScriptRef)
	out.ManifestID = strings.TrimSpace(out.ManifestID)
	out.CompatibilityMessage = strings.TrimSpace(out.CompatibilityMessage)
	out.NodeSelector = cloneStringMap(out.NodeSelector)
	out.MountRules = normalizeStringList(out.MountRules)
	out.EnvOverrides = cloneStringMap(out.EnvOverrides)
	out.CommandOverride = normalizeStringList(out.CommandOverride)
	out.Metadata = cloneStringMap(out.Metadata)
	if out.ID == "" {
		out.ID = "rb-" + safeIDSegment(out.ModelID) + "-" + safeIDSegment(out.TemplateID)
	}
	if out.Enabled != false {
		out.Enabled = true
	}
	if out.ModelID == "" || out.TemplateID == "" {
		return model.RuntimeBinding{}, fmt.Errorf("%w: model_id/template_id 不能为空", ErrRuntimeBindingValidation)
	}
	if _, ok := s.modelRegistry.Get(out.ModelID); !ok {
		return model.RuntimeBinding{}, fmt.Errorf("%w: model_id 不存在: %s", ErrRuntimeBindingValidation, out.ModelID)
	}
	tpl, ok := s.templates.GetTemplate(ctx, out.TemplateID)
	if !ok {
		return model.RuntimeBinding{}, fmt.Errorf("%w: template_id 不存在: %s", ErrRuntimeBindingValidation, out.TemplateID)
	}
	switch out.BindingMode {
	case model.RuntimeBindingModeDedicated,
		model.RuntimeBindingModeGenericInjected,
		model.RuntimeBindingModeGenericWithScript,
		model.RuntimeBindingModeCustomBundle:
	default:
		return model.RuntimeBinding{}, fmt.Errorf("%w: binding_mode 非法", ErrRuntimeBindingValidation)
	}
	if out.BindingMode == model.RuntimeBindingModeGenericWithScript && out.ScriptRef == "" {
		return model.RuntimeBinding{}, fmt.Errorf("%w: generic_with_script 需要 script_ref", ErrRuntimeBindingValidation)
	}
	if out.BindingMode == model.RuntimeBindingModeCustomBundle && out.ManifestID == "" {
		return model.RuntimeBinding{}, fmt.Errorf("%w: custom_bundle 需要 manifest_id", ErrRuntimeBindingValidation)
	}
	if out.BindingMode == model.RuntimeBindingModeCustomBundle {
		out.Metadata["custom_bundle_phase0"] = "placeholder-path"
	}
	if out.PreferredNode == "" {
		if fromSelector := strings.TrimSpace(out.NodeSelector["node_id"]); fromSelector != "" {
			out.PreferredNode = fromSelector
		}
	}
	if out.PreferredNode != "" {
		out.NodeSelector["node_id"] = out.PreferredNode
	}
	status, message := s.evaluateBindingCompatibility(out, tpl)
	out.CompatibilityStatus = status
	out.CompatibilityMessage = message
	now := time.Now().UTC()
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now
	}
	out.UpdatedAt = now
	return out, nil
}

func (s *RuntimeObjectService) evaluateBindingCompatibility(binding model.RuntimeBinding, tpl model.RuntimeTemplate) (model.CompatibilityStatus, string) {
	m, ok := s.modelRegistry.Get(binding.ModelID)
	if !ok {
		return model.CompatibilityIncompatible, "model not found"
	}
	effectiveModelType := m.ModelType
	if strings.TrimSpace(string(effectiveModelType)) == "" || effectiveModelType == model.ModelKindUnknown {
		effectiveModelType = inferModelTypeByName(m.Name, m.ID)
		if strings.TrimSpace(readMetadataValue(m.Metadata, "category")) == "embedding" {
			effectiveModelType = model.ModelKindEmbedding
		}
	}
	if binding.BindingMode == model.RuntimeBindingModeDedicated && !tpl.Dedicated {
		return model.CompatibilityWarning, "模板未标记 dedicated，当前以 dedicated 模式绑定"
	}
	if binding.BindingMode == model.RuntimeBindingModeGenericWithScript && !tpl.ScriptMountAllowed {
		return model.CompatibilityIncompatible, "模板不允许 script mount"
	}
	if len(tpl.SupportedModelTypes) > 0 && !containsModelKind(tpl.SupportedModelTypes, effectiveModelType) && !containsModelKind(tpl.SupportedModelTypes, model.ModelKindUnknown) {
		return model.CompatibilityWarning, fmt.Sprintf("model_type=%s 不在模板支持列表", effectiveModelType)
	}
	if len(tpl.SupportedFormats) > 0 && !containsModelFormat(tpl.SupportedFormats, m.Format) && !containsModelFormat(tpl.SupportedFormats, model.ModelFormatUnknown) {
		return model.CompatibilityWarning, fmt.Sprintf("format=%s 不在模板支持列表", m.Format)
	}
	switch binding.BindingMode {
	case model.RuntimeBindingModeDedicated, model.RuntimeBindingModeGenericInjected:
		return model.CompatibilityCompatible, "phase-0 supported path"
	case model.RuntimeBindingModeGenericWithScript, model.RuntimeBindingModeCustomBundle:
		return model.CompatibilityWarning, "phase-0 modeled and validated; execution path reserved for later stages"
	default:
		return model.CompatibilityUnknown, "unknown compatibility"
	}
}

func (s *RuntimeObjectService) findBindingByModel(modelID string) (model.RuntimeBinding, bool) {
	items, err := s.ListBindings(context.Background())
	if err != nil {
		return model.RuntimeBinding{}, false
	}
	for _, item := range items {
		if strings.TrimSpace(item.ModelID) == strings.TrimSpace(modelID) {
			return item, true
		}
	}
	return model.RuntimeBinding{}, false
}

func (s *RuntimeObjectService) findInstanceByModel(modelID string) (model.RuntimeInstance, bool) {
	items, err := s.ListRuntimeInstances(context.Background())
	if err != nil {
		return model.RuntimeInstance{}, false
	}
	for _, item := range items {
		if strings.TrimSpace(item.ModelID) == strings.TrimSpace(modelID) {
			return item, true
		}
	}
	return model.RuntimeInstance{}, false
}

func (s *RuntimeObjectService) upsertBinding(ctx context.Context, item model.RuntimeBinding) error {
	if s.store != nil {
		if err := s.store.UpsertRuntimeBinding(ctx, item); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.bindings[item.ID] = item
	s.mu.Unlock()
	return nil
}

func (s *RuntimeObjectService) upsertRuntimeInstance(ctx context.Context, item model.RuntimeInstance) error {
	s.persistRuntimeInstanceDerivedFields(&item)
	if s.store != nil {
		if err := s.store.UpsertRuntimeInstance(ctx, item); err != nil {
			return err
		}
	}
	s.hydrateRuntimeInstanceDerivedFields(&item)
	s.mu.Lock()
	s.instances[item.ID] = item
	s.mu.Unlock()
	return nil
}

func (s *RuntimeObjectService) upsertManifest(ctx context.Context, item model.RuntimeBundleManifest) error {
	if s.store != nil {
		if err := s.store.UpsertRuntimeBundleManifest(ctx, item); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.manifests[item.ID] = item
	s.mu.Unlock()
	return nil
}

func (s *RuntimeObjectService) reloadBindingsFromStore(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	items, err := s.store.ListRuntimeBindings(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings = make(map[string]model.RuntimeBinding, len(items))
	for _, item := range items {
		s.bindings[item.ID] = item
	}
	return nil
}

func (s *RuntimeObjectService) reloadInstancesFromStore(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	items, err := s.store.ListRuntimeInstances(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances = make(map[string]model.RuntimeInstance, len(items))
	for _, item := range items {
		s.hydrateRuntimeInstanceDerivedFields(&item)
		s.instances[item.ID] = item
	}
	return nil
}

func (s *RuntimeObjectService) reloadManifestsFromStore(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	items, err := s.store.ListRuntimeBundleManifests(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests = make(map[string]model.RuntimeBundleManifest, len(items))
	for _, item := range items {
		s.manifests[item.ID] = item
	}
	return nil
}

func inferDriftReason(item model.Model) string {
	if strings.TrimSpace(item.DesiredState) == "" || strings.TrimSpace(item.ObservedState) == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(item.DesiredState), strings.TrimSpace(item.ObservedState)) {
		return ""
	}
	if msg := strings.TrimSpace(item.HealthMessage); msg != "" && strings.Contains(strings.ToLower(msg), "drift") {
		return msg
	}
	return fmt.Sprintf("desired=%s observed=%s", item.DesiredState, item.ObservedState)
}

func safeIDSegment(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range input {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '-' || r == '_' {
			b.WriteRune('-')
			continue
		}
		b.WriteRune('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	return out
}

func mergePreferredStringList(seed []string, lists ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	add := func(values []string) {
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
	add(seed)
	for _, values := range lists {
		add(values)
	}
	return out
}

func containsModelKind(list []model.ModelKind, target model.ModelKind) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(string(item)), strings.TrimSpace(string(target))) {
			return true
		}
	}
	return false
}

func containsModelFormat(list []model.ModelFormat, target model.ModelFormat) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(string(item)), strings.TrimSpace(string(target))) {
			return true
		}
	}
	return false
}
