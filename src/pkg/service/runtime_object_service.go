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

type RuntimeInstanceProjectionSink interface {
	ApplyRuntimeInstanceProjection(ctx context.Context, instance model.RuntimeInstance) error
}

type RuntimeObjectService struct {
	modelRegistry *registry.ModelRegistry
	nodeRegistry  *registry.NodeRegistry
	templates     *RuntimeTemplateService
	store         *sqlitestore.Store
	logger        *slog.Logger
	agentSvc      *AgentService
	projection    RuntimeInstanceProjectionSink

	mu                    sync.RWMutex
	bindings              map[string]model.RuntimeBinding
	instances             map[string]model.RuntimeInstance
	manifests             map[string]model.RuntimeBundleManifest
	observationStaleAfter time.Duration
	reconcileMu           sync.Mutex
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
		modelRegistry:         modelRegistry,
		nodeRegistry:          nodeRegistry,
		templates:             templates,
		logger:                logger,
		bindings:              make(map[string]model.RuntimeBinding),
		instances:             make(map[string]model.RuntimeInstance),
		manifests:             make(map[string]model.RuntimeBundleManifest),
		observationStaleAfter: 2 * time.Minute,
	}
}

func (s *RuntimeObjectService) SetAgentService(agentSvc *AgentService) {
	s.agentSvc = agentSvc
}

func (s *RuntimeObjectService) SetRuntimeInstanceProjectionSink(sink RuntimeInstanceProjectionSink) {
	s.projection = sink
}

func (s *RuntimeObjectService) SetObservationStaleAfter(window time.Duration) {
	if window <= 0 {
		return
	}
	s.observationStaleAfter = window
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

// StartInstanceReconcileLoop starts a lightweight instance-first reconcile loop.
// It continuously explains desired/observed/readiness/precheck/drift on RuntimeInstance.
func (s *RuntimeObjectService) StartInstanceReconcileLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		_ = s.RunInstanceReconcileOnce(ctx, "bootstrap")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.RunInstanceReconcileOnce(ctx, "periodic")
			}
		}
	}()
}

func (s *RuntimeObjectService) RunInstanceReconcileOnce(ctx context.Context, trigger string) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	items, err := s.ListRuntimeInstances(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var errs []error
	for i := range items {
		item := items[i]
		updated, summary, recErr := s.reconcileRuntimeInstanceInternal(ctx, item, now, trigger)
		if recErr != nil {
			errs = append(errs, fmt.Errorf("runtime_instance=%s: %w", item.ID, recErr))
			continue
		}
		if upsertErr := s.upsertRuntimeInstance(ctx, updated); upsertErr != nil {
			errs = append(errs, fmt.Errorf("runtime_instance=%s upsert failed: %w", item.ID, upsertErr))
			continue
		}
		if s.projection != nil {
			if projErr := s.projection.ApplyRuntimeInstanceProjection(ctx, updated); projErr != nil {
				s.logger.Warn("runtime instance projection to model failed", "runtime_instance_id", updated.ID, "model_id", updated.ModelID, "error", projErr)
			}
		}
		s.logger.Debug("runtime instance reconciled",
			"runtime_instance_id", updated.ID,
			"model_id", updated.ModelID,
			"desired_state", updated.DesiredState,
			"observed_state", updated.ObservedState,
			"readiness", updated.Readiness,
			"drift_reason", updated.DriftReason,
			"precheck_status", updated.PrecheckStatus,
			"precheck_gating", updated.PrecheckGating,
			"conflict_status", updated.ConflictStatus,
			"conflict_blocking", updated.ConflictBlocking,
			"gating_status", updated.GatingStatus,
			"gating_allowed", updated.GatingAllowed,
			"plan_action", updated.LastPlanAction,
			"plan_status", updated.LastPlanStatus,
			"agent_status", summary.AgentStatus,
			"observation_stale", summary.ObservationStale,
			"trigger", trigger,
		)
	}
	return errors.Join(errs...)
}

func (s *RuntimeObjectService) ReconcileRuntimeInstance(ctx context.Context, runtimeInstanceID, trigger string) (model.RuntimeInstanceReconcileSummary, error) {
	runtimeInstanceID = strings.TrimSpace(runtimeInstanceID)
	if runtimeInstanceID == "" {
		return model.RuntimeInstanceReconcileSummary{}, ErrRuntimeInstanceNotFound
	}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	item, err := s.GetRuntimeInstance(ctx, runtimeInstanceID)
	if err != nil {
		return model.RuntimeInstanceReconcileSummary{}, err
	}
	updated, summary, err := s.reconcileRuntimeInstanceInternal(ctx, item, time.Now().UTC(), trigger)
	if err != nil {
		return model.RuntimeInstanceReconcileSummary{}, err
	}
	if err := s.upsertRuntimeInstance(ctx, updated); err != nil {
		return model.RuntimeInstanceReconcileSummary{}, err
	}
	if s.projection != nil {
		if projErr := s.projection.ApplyRuntimeInstanceProjection(ctx, updated); projErr != nil {
			s.logger.Warn("runtime instance projection to model failed", "runtime_instance_id", updated.ID, "model_id", updated.ModelID, "error", projErr)
		}
	}
	return summary, nil
}

func (s *RuntimeObjectService) GetRuntimeInstanceReconcileSummary(ctx context.Context, runtimeInstanceID string) (model.RuntimeInstanceReconcileSummary, error) {
	item, err := s.GetRuntimeInstance(ctx, runtimeInstanceID)
	if err != nil {
		return model.RuntimeInstanceReconcileSummary{}, err
	}
	_, summary, err := s.reconcileRuntimeInstanceInternal(ctx, item, time.Now().UTC(), "summary_query")
	if err != nil {
		return model.RuntimeInstanceReconcileSummary{}, err
	}
	return summary, nil
}

func (s *RuntimeObjectService) reconcileRuntimeInstanceInternal(
	ctx context.Context,
	item model.RuntimeInstance,
	now time.Time,
	trigger string,
) (model.RuntimeInstance, model.RuntimeInstanceReconcileSummary, error) {
	s.hydrateRuntimeInstanceDerivedFields(&item)
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	desired := strings.TrimSpace(item.DesiredState)
	if desired == "" || strings.EqualFold(desired, string(model.ModelStateUnknown)) {
		if m, ok := s.modelRegistry.Get(strings.TrimSpace(item.ModelID)); ok {
			desired = firstNonEmpty(strings.TrimSpace(m.DesiredState), strings.TrimSpace(string(m.State)))
		}
	}
	if desired == "" {
		desired = string(model.ModelStateUnknown)
	}
	item.DesiredState = desired

	observed := strings.TrimSpace(item.ObservedState)
	if observed == "" {
		observed = string(model.ModelStateUnknown)
	}
	item.ObservedState = observed
	if strings.TrimSpace(string(item.Readiness)) == "" {
		item.Readiness = model.ReadinessUnknown
	}

	reconcileReasons := make([]string, 0, 8)
	appendReason := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}
		for _, existing := range reconcileReasons {
			if strings.EqualFold(existing, reason) {
				return
			}
		}
		reconcileReasons = append(reconcileReasons, reason)
	}

	var resolvedCtx *RuntimeInstanceResolvedContext
	if item.BindingID != "" && (item.BindingMode == "" || item.ManifestID == "" || len(item.ResolvedPorts) == 0) {
		if resolved, resolveErr := s.ResolveRuntimeInstanceContext(ctx, item.ID); resolveErr == nil {
			resolvedCtx = &resolved
			if item.BindingMode == "" {
				item.BindingMode = resolved.Binding.BindingMode
			}
			if item.ManifestID == "" {
				item.ManifestID = strings.TrimSpace(resolved.Manifest.ID)
			}
			if len(item.ResolvedPorts) == 0 {
				item.ResolvedPorts = normalizeStringList(resolved.Manifest.ExposedPorts)
			}
			if item.ResolvedScript == "" {
				item.ResolvedScript = strings.TrimSpace(resolved.Binding.ScriptRef)
			}
			if item.PrecheckResult != nil &&
				(!item.PrecheckResult.CompatibilityResult.ModelTypeMatched || !item.PrecheckResult.CompatibilityResult.ModelFormatMatched) {
				appendReason("manifest_model_compatibility_failed")
			}
		}
	}
	if resolvedCtx == nil && item.BindingID != "" {
		if resolved, resolveErr := s.ResolveRuntimeInstanceContext(ctx, item.ID); resolveErr == nil {
			resolvedCtx = &resolved
		}
	}

	targetAction := determineRuntimeReconcileTargetAction(item.DesiredState, item.ObservedState, trigger)

	agentStatus := "unknown"
	agentOnline := false
	agentSignalKnown := false
	if s.agentSvc != nil && strings.TrimSpace(item.NodeID) != "" {
		agentSignalKnown = true
		if agent, ok := s.agentSvc.GetByNodeID(item.NodeID); ok {
			agentStatus = strings.TrimSpace(string(agent.Status))
			agentOnline = agent.Status == model.AgentStatusOnline
		} else {
			agentStatus = "missing"
		}
	}

	lastObservationAt := time.Time{}
	if item.LastAgentTask != nil && !item.LastAgentTask.FinishedAt.IsZero() {
		lastObservationAt = item.LastAgentTask.FinishedAt.UTC()
	}
	if lastObservationAt.IsZero() && !item.LastPrecheckAt.IsZero() {
		lastObservationAt = item.LastPrecheckAt.UTC()
	}
	staleWindow := s.observationStaleAfter
	if staleWindow <= 0 {
		staleWindow = 2 * time.Minute
	}
	observationStale := false
	if agentSignalKnown {
		if !lastObservationAt.IsZero() {
			observationStale = now.Sub(lastObservationAt) > staleWindow
		} else if strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning)) {
			observationStale = true
		}
	}

	precheckSummary := buildRuntimePrecheckSummaryFromInstance(item, now)
	item.PrecheckSummary = precheckSummary
	item.PrecheckStatus = precheckSummary.Status
	item.PrecheckGating = precheckSummary.Gating
	item.PrecheckReasons = normalizeReasonCodesFromConditionReasons(precheckSummary.Reasons, item.PrecheckReasons)
	if precheckSummary.Gating {
		appendReason("precheck_blocked")
	}

	allInstances, listErr := s.ListRuntimeInstances(ctx)
	if listErr != nil {
		s.logger.Warn("list runtime instances for conflict evaluation failed", "runtime_instance_id", item.ID, "error", listErr)
	}
	conflictSummary := s.evaluateRuntimeConflictSummary(
		ctx,
		item,
		resolvedCtx,
		allInstances,
		now,
		targetAction,
		agentSignalKnown,
		agentOnline,
		observationStale,
	)
	item.ConflictSummary = &conflictSummary
	item.ConflictStatus = conflictSummary.Status
	item.ConflictBlocking = conflictSummary.Blocking
	item.ConflictReasons = normalizeReasonCodesFromConditionReasons(conflictSummary.Reasons, nil)
	item.ConflictSource = conflictSummary.Source
	item.ConflictGeneratedAt = conflictSummary.GeneratedAt
	if item.ConflictStatus == model.RuntimeConflictStatusBlocked || item.ConflictStatus == model.RuntimeConflictStatusWarning {
		appendReason("runtime_conflict")
	}

	gatingSummary := buildRuntimeGatingSummary(precheckSummary, conflictSummary, targetAction, now)
	item.GatingSummary = &gatingSummary
	item.GatingStatus = gatingSummary.Status
	item.GatingAllowed = gatingSummary.Allowed
	item.GatingReasons = normalizeReasonCodesFromConditionReasons(gatingSummary.Reasons, nil)
	item.GatingSource = gatingSummary.Source
	item.GatingGeneratedAt = gatingSummary.GeneratedAt
	if !item.GatingAllowed {
		appendReason("gating_blocked")
	}

	planSummary := buildRuntimeLifecyclePlanSummary(item, gatingSummary, conflictSummary, targetAction, trigger, now)
	item.LastLifecyclePlan = &planSummary
	item.LastPlanAction = planSummary.Action
	item.LastPlanStatus = planSummary.Status
	item.LastPlanReason = strings.TrimSpace(planSummary.Message)
	item.LastPlanGeneratedAt = chooseNewerTime(planSummary.GeneratedAt, planSummary.UpdatedAt)
	item.LastPlanDetail = map[string]interface{}{
		"plan_id":              strings.TrimSpace(planSummary.PlanID),
		"reason_codes":         cloneStringSlice(planSummary.ReasonCodes),
		"blocked_reason_codes": cloneStringSlice(planSummary.BlockedReasonCodes),
		"release_targets":      cloneStringSlice(planSummary.ReleaseTargets),
		"requested_task_type":  strings.TrimSpace(string(planSummary.RequestedTaskType)),
		"triggered_by":         strings.TrimSpace(planSummary.TriggeredBy),
		"source":               strings.TrimSpace(string(planSummary.Source)),
		"target_action":        strings.TrimSpace(string(targetAction)),
		"conflict_status":      strings.TrimSpace(string(conflictSummary.Status)),
		"gating_status":        strings.TrimSpace(string(gatingSummary.Status)),
		"gating_allowed":       gatingSummary.Allowed,
	}
	if planSummary.Action != model.RuntimeLifecycleActionNone {
		appendReason("lifecycle_plan:" + strings.TrimSpace(string(planSummary.Action)))
	}
	if len(planSummary.ReleaseTargets) > 0 {
		appendReason("release_targets_planned")
		_ = s.applyReleasePlanToTargets(ctx, item.ID, planSummary.ReleaseTargets, trigger, now)
	}

	precheckBlocked := item.PrecheckGating || item.PrecheckStatus == model.PrecheckStatusFailed
	if precheckBlocked && isRuntimeLoadLikeAction(targetAction) {
		appendReason("precheck_blocked")
		item.Readiness = model.ReadinessNotReady
		item.DriftReason = firstNonEmpty(
			strings.TrimSpace(item.DriftReason),
			"precheck_blocked",
		)
		precheckText := strings.Join(item.PrecheckReasons, ",")
		if precheckText == "" {
			precheckText = string(item.PrecheckStatus)
		}
		item.HealthMessage = firstNonEmpty(
			strings.TrimSpace(item.HealthMessage),
			"precheck gating blocked: "+precheckText,
		)
	} else if !item.GatingAllowed && isRuntimeLoadLikeAction(targetAction) {
		item.Readiness = model.ReadinessNotReady
		item.DriftReason = firstNonEmpty(strings.TrimSpace(item.DriftReason), "gating_blocked")
		item.HealthMessage = appendRuntimeMessage(item.HealthMessage, "lifecycle action gated by precheck/conflict")
	} else if strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning)) {
		if strings.EqualFold(strings.TrimSpace(item.ObservedState), string(model.ModelStateRunning)) {
			if item.Readiness != model.ReadinessReady {
				appendReason("runtime_running_not_ready")
				item.Readiness = model.ReadinessNotReady
				item.DriftReason = "runtime_running_not_ready"
				item.HealthMessage = firstNonEmpty(
					strings.TrimSpace(item.HealthMessage),
					"precheck passed but runtime readiness check is not ready",
				)
			} else {
				item.DriftReason = clearDesiredObservedDrift(item.DriftReason, item.DesiredState, item.ObservedState)
			}
		} else {
			appendReason("desired_observed_drift")
			item.Readiness = model.ReadinessNotReady
			item.DriftReason = fmt.Sprintf("desired=%s observed=%s", item.DesiredState, item.ObservedState)
			item.HealthMessage = firstNonEmpty(
				strings.TrimSpace(item.HealthMessage),
				fmt.Sprintf("runtime not in desired state: desired=%s observed=%s", item.DesiredState, item.ObservedState),
			)
		}
	} else if strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateStopped)) {
		if strings.EqualFold(strings.TrimSpace(item.ObservedState), string(model.ModelStateRunning)) {
			appendReason("desired_observed_drift")
			item.DriftReason = fmt.Sprintf("desired=%s observed=%s", item.DesiredState, item.ObservedState)
			item.HealthMessage = firstNonEmpty(
				strings.TrimSpace(item.HealthMessage),
				"instance expected stopped but still running",
			)
		}
		if item.Readiness == model.ReadinessReady {
			item.Readiness = model.ReadinessNotReady
		}
	}

	if agentSignalKnown && !agentOnline && isRuntimeLoadLikeAction(targetAction) {
		appendReason("agent_offline")
		if strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning)) && strings.TrimSpace(item.DriftReason) == "" {
			item.DriftReason = "agent_offline"
		}
		item.HealthMessage = appendRuntimeMessage(item.HealthMessage, "agent offline or unavailable")
	}
	if observationStale && strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning)) {
		appendReason("agent_observation_stale")
		if strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning)) && strings.TrimSpace(item.DriftReason) == "" {
			item.DriftReason = "agent_observation_stale"
		}
		item.HealthMessage = appendRuntimeMessage(item.HealthMessage, "agent observation is stale")
	}

	item.LastReconciledAt = now
	item.Metadata["phase"] = "stage-b"
	item.Metadata["reconcile_reason_codes"] = strings.Join(reconcileReasons, ",")
	item.Metadata["reconcile_agent_status"] = firstNonEmpty(agentStatus, "unknown")
	item.Metadata["reconcile_observation_stale"] = fmt.Sprintf("%t", observationStale)
	item.Metadata["conflict_status"] = strings.TrimSpace(string(item.ConflictStatus))
	item.Metadata["gating_status"] = strings.TrimSpace(string(item.GatingStatus))
	item.Metadata["gating_allowed"] = fmt.Sprintf("%t", item.GatingAllowed)
	item.Metadata["last_plan_action"] = strings.TrimSpace(string(item.LastPlanAction))
	item.Metadata["last_plan_status"] = strings.TrimSpace(string(item.LastPlanStatus))

	summary := model.RuntimeInstanceReconcileSummary{
		RuntimeInstanceID: item.ID,
		ModelID:           item.ModelID,
		NodeID:            item.NodeID,
		DesiredState:      item.DesiredState,
		ObservedState:     item.ObservedState,
		Readiness:         item.Readiness,
		HealthMessage:     strings.TrimSpace(item.HealthMessage),
		DriftReason:       strings.TrimSpace(item.DriftReason),
		PrecheckStatus:    item.PrecheckStatus,
		PrecheckGating:    item.PrecheckGating,
		PrecheckReasons:   cloneStringSlice(item.PrecheckReasons),
		ConflictStatus:    item.ConflictStatus,
		ConflictBlocking:  item.ConflictBlocking,
		ConflictReasons:   cloneStringSlice(item.ConflictReasons),
		GatingStatus:      item.GatingStatus,
		GatingAllowed:     item.GatingAllowed,
		GatingReasons:     cloneStringSlice(item.GatingReasons),
		PlannedAction:     planSummary.Action,
		AgentStatus:       firstNonEmpty(agentStatus, "unknown"),
		AgentOnline:       agentOnline,
		ObservationStale:  observationStale,
		LastObservationAt: lastObservationAt,
		LastReconciledAt:  now,
		Trigger:           strings.TrimSpace(trigger),
	}

	summary.DesiredState = item.DesiredState
	summary.ObservedState = item.ObservedState
	summary.Readiness = item.Readiness
	summary.HealthMessage = strings.TrimSpace(item.HealthMessage)
	summary.DriftReason = strings.TrimSpace(item.DriftReason)
	summary.ReconcileReasons = cloneStringSlice(reconcileReasons)
	summary.PrecheckStatus = item.PrecheckStatus
	summary.PrecheckGating = item.PrecheckGating
	summary.PrecheckReasons = cloneStringSlice(item.PrecheckReasons)
	summary.LastReconciledAt = item.LastReconciledAt
	summary.BindingMode = item.BindingMode
	summary.ManifestID = item.ManifestID
	summary.RuntimeTemplateID = item.TemplateID
	summary.LastAgentTask = item.LastAgentTask
	summary.LastLifecyclePlan = item.LastLifecyclePlan

	return item, summary, nil
}

func buildRuntimePrecheckSummaryFromInstance(item model.RuntimeInstance, now time.Time) *model.RuntimePrecheckSummary {
	if item.PrecheckSummary != nil {
		summary := *item.PrecheckSummary
		summary.Status = model.PrecheckOverallStatus(firstNonEmpty(strings.TrimSpace(string(summary.Status)), strings.TrimSpace(string(item.PrecheckStatus)), string(model.PrecheckStatusUnknown)))
		summary.Gating = summary.Gating || item.PrecheckGating
		if summary.Source == "" {
			if strings.TrimSpace(item.LastPrecheckTaskID) != "" {
				summary.Source = model.RuntimeSignalSourceAgent
			} else {
				summary.Source = model.RuntimeSignalSourceController
			}
		}
		if summary.GeneratedAt.IsZero() {
			summary.GeneratedAt = chooseNewerTime(item.LastPrecheckAt, now)
		}
		if len(summary.Reasons) == 0 {
			summary.Reasons = precheckReasonsToConditionReasons(item.PrecheckReasons, summary.Source, item.ID, item.NodeID, item.BindingID)
		}
		return &summary
	}

	source := model.RuntimeSignalSourceController
	if strings.TrimSpace(item.LastPrecheckTaskID) != "" {
		source = model.RuntimeSignalSourceAgent
	}
	reasons := precheckReasonsToConditionReasons(item.PrecheckReasons, source, item.ID, item.NodeID, item.BindingID)
	if item.PrecheckResult != nil && len(item.PrecheckResult.Reasons) > 0 {
		reasons = make([]model.RuntimeConditionReason, 0, len(item.PrecheckResult.Reasons))
		for _, reason := range item.PrecheckResult.Reasons {
			reasons = append(reasons, model.RuntimeConditionReason{
				Code:              strings.TrimSpace(string(reason.Code)),
				Message:           strings.TrimSpace(reason.Message),
				Blocking:          reason.Blocking,
				Source:            source,
				RelatedInstanceID: strings.TrimSpace(item.ID),
				RelatedNodeID:     strings.TrimSpace(item.NodeID),
				RelatedBindingID:  strings.TrimSpace(item.BindingID),
				Detail:            cloneObjectMap(reason.Detail),
			})
		}
	}
	status := model.PrecheckOverallStatus(firstNonEmpty(strings.TrimSpace(string(item.PrecheckStatus)), string(model.PrecheckStatusUnknown)))
	gating := item.PrecheckGating || status == model.PrecheckStatusFailed
	if status == model.PrecheckStatusUnknown && len(reasons) > 0 {
		status = model.PrecheckStatusWarning
	}
	return &model.RuntimePrecheckSummary{
		Status:      status,
		Gating:      gating,
		Reasons:     reasons,
		GeneratedAt: chooseNewerTime(item.LastPrecheckAt, now),
		Source:      source,
	}
}

func precheckReasonsToConditionReasons(codes []string, source model.RuntimeSignalSource, instanceID, nodeID, bindingID string) []model.RuntimeConditionReason {
	out := make([]model.RuntimeConditionReason, 0, len(codes))
	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		out = append(out, model.RuntimeConditionReason{
			Code:              code,
			Message:           code,
			Blocking:          true,
			Source:            source,
			RelatedInstanceID: strings.TrimSpace(instanceID),
			RelatedNodeID:     strings.TrimSpace(nodeID),
			RelatedBindingID:  strings.TrimSpace(bindingID),
		})
	}
	return out
}

func normalizeReasonCodesFromConditionReasons(reasons []model.RuntimeConditionReason, fallback []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		code := strings.TrimSpace(reason.Code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	for _, raw := range fallback {
		code := strings.TrimSpace(raw)
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

func determineRuntimeReconcileTargetAction(desiredState, observedState, trigger string) model.RuntimeLifecycleAction {
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	if strings.Contains(trigger, "runtime_task_start") || strings.Contains(trigger, "runtime_task_restart") {
		return model.RuntimeLifecycleActionLoadStart
	}
	if strings.Contains(trigger, "runtime_task_stop") {
		return model.RuntimeLifecycleActionUnload
	}
	desired := strings.ToLower(strings.TrimSpace(desiredState))
	observed := strings.ToLower(strings.TrimSpace(observedState))
	if desired == string(model.ModelStateRunning) && observed != string(model.ModelStateRunning) {
		return model.RuntimeLifecycleActionLoad
	}
	if desired == string(model.ModelStateStopped) && observed == string(model.ModelStateRunning) {
		return model.RuntimeLifecycleActionUnload
	}
	return model.RuntimeLifecycleActionNone
}

func isRuntimeLoadLikeAction(action model.RuntimeLifecycleAction) bool {
	switch action {
	case model.RuntimeLifecycleActionLoad, model.RuntimeLifecycleActionLoadStart, model.RuntimeLifecycleActionStart, model.RuntimeLifecycleActionRefresh:
		return true
	default:
		return false
	}
}

func isRuntimeReleaseLikeAction(action model.RuntimeLifecycleAction) bool {
	switch action {
	case model.RuntimeLifecycleActionUnload, model.RuntimeLifecycleActionStop, model.RuntimeLifecycleActionRelease:
		return true
	default:
		return false
	}
}

func (s *RuntimeObjectService) evaluateRuntimeConflictSummary(
	ctx context.Context,
	item model.RuntimeInstance,
	resolvedCtx *RuntimeInstanceResolvedContext,
	allInstances []model.RuntimeInstance,
	now time.Time,
	targetAction model.RuntimeLifecycleAction,
	agentSignalKnown bool,
	agentOnline bool,
	observationStale bool,
) model.RuntimeConflictSummary {
	_ = ctx
	reasons := make([]model.RuntimeConditionReason, 0, 8)
	appendReason := func(reason model.RuntimeConditionReason) {
		code := strings.TrimSpace(reason.Code)
		if code == "" {
			return
		}
		for _, existing := range reasons {
			if strings.EqualFold(strings.TrimSpace(existing.Code), code) &&
				strings.EqualFold(strings.TrimSpace(existing.RelatedInstanceID), strings.TrimSpace(reason.RelatedInstanceID)) {
				return
			}
		}
		reason.Code = code
		if reason.Source == "" {
			reason.Source = model.RuntimeSignalSourceController
		}
		if strings.TrimSpace(reason.RelatedInstanceID) == "" {
			reason.RelatedInstanceID = strings.TrimSpace(item.ID)
		}
		if strings.TrimSpace(reason.RelatedNodeID) == "" {
			reason.RelatedNodeID = strings.TrimSpace(item.NodeID)
		}
		if strings.TrimSpace(reason.RelatedBindingID) == "" {
			reason.RelatedBindingID = strings.TrimSpace(item.BindingID)
		}
		reasons = append(reasons, reason)
	}

	loadLike := isRuntimeLoadLikeAction(targetAction)
	desiredRunning := strings.EqualFold(strings.TrimSpace(item.DesiredState), string(model.ModelStateRunning))
	observedRunning := strings.EqualFold(strings.TrimSpace(item.ObservedState), string(model.ModelStateRunning))
	if !loadLike {
		loadLike = desiredRunning && !observedRunning
	}

	if desired := strings.TrimSpace(strings.ToLower(item.DesiredState)); desired != "" {
		observed := strings.TrimSpace(strings.ToLower(item.ObservedState))
		if observed != "" && desired != observed {
			appendReason(model.RuntimeConditionReason{
				Code:     "desired_observed_conflict",
				Message:  fmt.Sprintf("desired=%s observed=%s", item.DesiredState, item.ObservedState),
				Blocking: false,
				Detail: map[string]interface{}{
					"desired_state":  strings.TrimSpace(item.DesiredState),
					"observed_state": strings.TrimSpace(item.ObservedState),
				},
			})
		}
	}

	ports := extractRuntimeHostPorts(item.ResolvedPorts)
	if len(ports) == 0 && resolvedCtx != nil {
		ports = extractRuntimeHostPorts(resolvedCtx.Manifest.ExposedPorts)
	}
	runtimeKind := strings.TrimSpace(string(resolveRuntimeKindForInstance(item, resolvedCtx)))
	for _, other := range allInstances {
		if strings.EqualFold(strings.TrimSpace(other.ID), strings.TrimSpace(item.ID)) {
			continue
		}
		if strings.TrimSpace(other.NodeID) == "" || !strings.EqualFold(strings.TrimSpace(other.NodeID), strings.TrimSpace(item.NodeID)) {
			continue
		}
		if !isRuntimeInstanceOccupyingSlots(other) {
			continue
		}
		otherPorts := extractRuntimeHostPorts(other.ResolvedPorts)
		if len(otherPorts) == 0 {
			if resolvedOther, resolveErr := s.ResolveRuntimeInstanceContext(ctx, other.ID); resolveErr == nil {
				otherPorts = extractRuntimeHostPorts(resolvedOther.Manifest.ExposedPorts)
			}
		}
		if len(ports) > 0 && len(otherPorts) > 0 {
			if conflictedPort := intersectFirstString(ports, otherPorts); conflictedPort != "" {
				appendReason(model.RuntimeConditionReason{
					Code:              "port_conflict",
					Message:           "runtime instance port conflict",
					Blocking:          loadLike,
					RelatedInstanceID: strings.TrimSpace(other.ID),
					Detail: map[string]interface{}{
						"host_port": conflictedPort,
					},
				})
			}
		}

		if runtimeKind != "" && runtimeKind != string(model.RuntimeKindUnknown) {
			otherKind := strings.TrimSpace(string(resolveRuntimeKindForInstance(other, nil)))
			if otherKind == "" || otherKind == string(model.RuntimeKindUnknown) {
				if resolvedOther, resolveErr := s.ResolveRuntimeInstanceContext(ctx, other.ID); resolveErr == nil {
					otherKind = strings.TrimSpace(string(resolvedOther.Manifest.RuntimeKind))
				}
			}
			if strings.EqualFold(runtimeKind, otherKind) && loadLike && !observedRunning {
				appendReason(model.RuntimeConditionReason{
					Code:              "runtime_kind_mutex_conflict",
					Message:           "runtime kind slot is occupied by another running instance",
					Blocking:          true,
					RelatedInstanceID: strings.TrimSpace(other.ID),
					Detail: map[string]interface{}{
						"runtime_kind": runtimeKind,
					},
				})
			}
		}

		if strings.EqualFold(strings.TrimSpace(other.ModelID), strings.TrimSpace(item.ModelID)) && loadLike && !observedRunning {
			appendReason(model.RuntimeConditionReason{
				Code:              "duplicate_model_instance",
				Message:           "same model has another active instance on this node",
				Blocking:          true,
				RelatedInstanceID: strings.TrimSpace(other.ID),
			})
		}
	}

	if resolvedCtx != nil {
		if !resolvedCtx.Manifest.CommandOverrideAllowed && len(resolvedCtx.Binding.CommandOverride) > 0 {
			appendReason(model.RuntimeConditionReason{
				Code:     "command_override_not_allowed",
				Message:  "binding command override is forbidden by manifest",
				Blocking: true,
			})
		}
		if !resolvedCtx.Manifest.ScriptMountAllowed &&
			(strings.TrimSpace(resolvedCtx.Binding.ScriptRef) != "" || resolvedCtx.Binding.BindingMode == model.RuntimeBindingModeGenericWithScript) {
			appendReason(model.RuntimeConditionReason{
				Code:     "script_mount_not_allowed",
				Message:  "binding script usage is forbidden by manifest",
				Blocking: true,
			})
		}
	}

	for _, code := range item.PrecheckReasons {
		normalized := strings.TrimSpace(strings.ToLower(code))
		if normalized == string(model.PrecheckReasonCommandOverrideNotAllowed) || normalized == string(model.PrecheckReasonScriptMountNotAllowed) {
			appendReason(model.RuntimeConditionReason{
				Code:     normalized,
				Message:  normalized,
				Blocking: true,
			})
		}
	}

	if agentSignalKnown && !agentOnline && loadLike {
		appendReason(model.RuntimeConditionReason{
			Code:     "local_agent_offline",
			Message:  "local-agent is offline or unavailable",
			Blocking: true,
		})
	}
	if observationStale && loadLike {
		appendReason(model.RuntimeConditionReason{
			Code:     "agent_observation_stale",
			Message:  "agent observation is stale",
			Blocking: true,
		})
	}

	blocking := false
	for _, reason := range reasons {
		if reason.Blocking {
			blocking = true
			break
		}
	}
	status := model.RuntimeConflictStatusClear
	if len(reasons) > 0 {
		if blocking {
			status = model.RuntimeConflictStatusBlocked
		} else {
			status = model.RuntimeConflictStatusWarning
		}
	}
	return model.RuntimeConflictSummary{
		Status:      status,
		Blocking:    blocking,
		Reasons:     reasons,
		GeneratedAt: now,
		Source:      model.RuntimeSignalSourceController,
	}
}

func buildRuntimeGatingSummary(
	precheck *model.RuntimePrecheckSummary,
	conflict model.RuntimeConflictSummary,
	targetAction model.RuntimeLifecycleAction,
	now time.Time,
) model.RuntimeGatingSummary {
	reasons := make([]model.RuntimeConditionReason, 0, 12)
	allowed := true
	status := model.RuntimeGatingStatusAllowed
	loadLike := isRuntimeLoadLikeAction(targetAction)

	appendReason := func(reason model.RuntimeConditionReason) {
		code := strings.TrimSpace(reason.Code)
		if code == "" {
			return
		}
		for _, existing := range reasons {
			if strings.EqualFold(strings.TrimSpace(existing.Code), code) &&
				strings.EqualFold(strings.TrimSpace(existing.RelatedInstanceID), strings.TrimSpace(reason.RelatedInstanceID)) {
				return
			}
		}
		reasons = append(reasons, reason)
		if reason.Blocking && loadLike {
			allowed = false
		}
	}

	if precheck != nil {
		for _, reason := range precheck.Reasons {
			appendReason(reason)
		}
		if precheck.Gating && loadLike {
			allowed = false
			status = model.RuntimeGatingStatusBlocked
		}
	}
	for _, reason := range conflict.Reasons {
		appendReason(reason)
	}
	if conflict.Blocking && loadLike {
		allowed = false
	}

	if !allowed {
		if hasReasonCode(reasons, "runtime_kind_mutex_conflict") {
			status = model.RuntimeGatingStatusDeferred
		} else {
			status = model.RuntimeGatingStatusBlocked
		}
	} else if !loadLike && !isRuntimeReleaseLikeAction(targetAction) && targetAction == model.RuntimeLifecycleActionNone {
		status = model.RuntimeGatingStatusAllowed
	}

	return model.RuntimeGatingSummary{
		Status:       status,
		Allowed:      allowed,
		Reasons:      reasons,
		GeneratedAt:  now,
		Source:       model.RuntimeSignalSourceController,
		TargetAction: targetAction,
	}
}

func buildRuntimeLifecyclePlanSummary(
	item model.RuntimeInstance,
	gating model.RuntimeGatingSummary,
	conflict model.RuntimeConflictSummary,
	targetAction model.RuntimeLifecycleAction,
	trigger string,
	now time.Time,
) model.RuntimeLifecyclePlanSummary {
	plan := model.RuntimeLifecyclePlanSummary{
		PlanID:      fmt.Sprintf("plan-%s-%d", safeIDSegment(item.ID), now.UnixNano()),
		Action:      model.RuntimeLifecycleActionNone,
		Status:      model.RuntimeLifecyclePlanStatusCompleted,
		Message:     "no lifecycle transition required",
		TriggeredBy: strings.TrimSpace(trigger),
		Source:      model.RuntimeSignalSourceController,
		GeneratedAt: now,
		UpdatedAt:   now,
	}
	plan.ReasonCodes = normalizeReasonCodesFromConditionReasons(gating.Reasons, nil)

	if targetAction == model.RuntimeLifecycleActionNone {
		return plan
	}

	if !gating.Allowed {
		plan.Action = normalizePlannedAction(targetAction)
		plan.Status = model.RuntimeLifecyclePlanStatusBlocked
		plan.Message = "lifecycle transition blocked by gating/conflict"
		plan.BlockedReasonCodes = collectBlockingReasonCodes(gating.Reasons)
		plan.ReleaseTargets = collectReleaseTargetsFromConflict(conflict.Reasons)
		if gating.Status == model.RuntimeGatingStatusDeferred && len(plan.ReleaseTargets) > 0 {
			plan.Status = model.RuntimeLifecyclePlanStatusDeferred
			plan.Message = "lifecycle transition deferred until release targets stop"
		}
		return plan
	}

	plan.Action = normalizePlannedAction(targetAction)
	plan.Status = model.RuntimeLifecyclePlanStatusPlanned
	switch plan.Action {
	case model.RuntimeLifecycleActionLoad, model.RuntimeLifecycleActionLoadStart, model.RuntimeLifecycleActionStart:
		plan.RequestedTaskType = model.TaskTypeRuntimeStart
		plan.Message = "instance planned to load/start"
	case model.RuntimeLifecycleActionUnload, model.RuntimeLifecycleActionStop:
		plan.RequestedTaskType = model.TaskTypeRuntimeStop
		plan.Message = "instance planned to stop/unload"
	default:
		plan.Message = "instance lifecycle action planned"
	}
	return plan
}

func normalizePlannedAction(action model.RuntimeLifecycleAction) model.RuntimeLifecycleAction {
	switch action {
	case model.RuntimeLifecycleActionLoadStart:
		return model.RuntimeLifecycleActionLoad
	case model.RuntimeLifecycleActionRelease:
		return model.RuntimeLifecycleActionUnload
	default:
		return action
	}
}

func collectBlockingReasonCodes(reasons []model.RuntimeConditionReason) []string {
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if !reason.Blocking {
			continue
		}
		code := strings.TrimSpace(reason.Code)
		if code == "" {
			continue
		}
		out = append(out, code)
	}
	return normalizeStringList(out)
}

func collectReleaseTargetsFromConflict(reasons []model.RuntimeConditionReason) []string {
	targets := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if strings.TrimSpace(reason.Code) != "runtime_kind_mutex_conflict" {
			continue
		}
		id := strings.TrimSpace(reason.RelatedInstanceID)
		if id == "" {
			continue
		}
		targets = append(targets, id)
	}
	return normalizeStringList(targets)
}

func hasReasonCode(reasons []model.RuntimeConditionReason, code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	for _, reason := range reasons {
		if strings.TrimSpace(strings.ToLower(reason.Code)) == code {
			return true
		}
	}
	return false
}

func resolveRuntimeKindForInstance(item model.RuntimeInstance, resolvedCtx *RuntimeInstanceResolvedContext) model.RuntimeKind {
	if resolvedCtx != nil {
		return resolvedCtx.Manifest.RuntimeKind
	}
	if item.Metadata != nil {
		if raw := strings.TrimSpace(item.Metadata["runtime_kind"]); raw != "" {
			return model.RuntimeKind(raw)
		}
	}
	return model.RuntimeKindUnknown
}

func extractRuntimeHostPorts(ports []string) []string {
	out := make([]string, 0, len(ports))
	for _, raw := range ports {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		main := value
		if idx := strings.Index(main, "/"); idx > 0 {
			main = main[:idx]
		}
		parts := strings.Split(main, ":")
		candidate := ""
		switch len(parts) {
		case 1:
			candidate = strings.TrimSpace(parts[0])
		case 2:
			candidate = strings.TrimSpace(parts[0])
		default:
			candidate = strings.TrimSpace(parts[len(parts)-2])
		}
		if candidate != "" {
			out = append(out, candidate)
		}
	}
	return normalizeStringList(out)
}

func intersectFirstString(a, b []string) string {
	if len(a) == 0 || len(b) == 0 {
		return ""
	}
	lookup := map[string]struct{}{}
	for _, item := range b {
		lookup[strings.TrimSpace(strings.ToLower(item))] = struct{}{}
	}
	for _, item := range a {
		key := strings.TrimSpace(strings.ToLower(item))
		if key == "" {
			continue
		}
		if _, ok := lookup[key]; ok {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func isRuntimeInstanceOccupyingSlots(item model.RuntimeInstance) bool {
	observed := strings.TrimSpace(strings.ToLower(item.ObservedState))
	desired := strings.TrimSpace(strings.ToLower(item.DesiredState))
	if observed == string(model.ModelStateRunning) {
		return true
	}
	if desired == string(model.ModelStateRunning) && observed != string(model.ModelStateStopped) {
		return true
	}
	if item.LastLifecyclePlan != nil {
		action := strings.TrimSpace(strings.ToLower(string(item.LastLifecyclePlan.Action)))
		status := strings.TrimSpace(strings.ToLower(string(item.LastLifecyclePlan.Status)))
		if (action == string(model.RuntimeLifecycleActionLoad) || action == string(model.RuntimeLifecycleActionLoadStart) || action == string(model.RuntimeLifecycleActionStart)) &&
			(status == string(model.RuntimeLifecyclePlanStatusPlanned) || status == string(model.RuntimeLifecyclePlanStatusExecuting) || status == string(model.RuntimeLifecyclePlanStatusDeferred)) {
			return true
		}
	}
	return false
}

func (s *RuntimeObjectService) applyReleasePlanToTargets(ctx context.Context, ownerInstanceID string, targetIDs []string, trigger string, now time.Time) error {
	targetIDs = normalizeStringList(targetIDs)
	if len(targetIDs) == 0 {
		return nil
	}
	var errs []error
	for _, targetID := range targetIDs {
		if strings.EqualFold(strings.TrimSpace(targetID), strings.TrimSpace(ownerInstanceID)) {
			continue
		}
		target, err := s.GetRuntimeInstance(ctx, targetID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		target.LastLifecyclePlan = &model.RuntimeLifecyclePlanSummary{
			PlanID:            fmt.Sprintf("plan-release-%s-%d", safeIDSegment(target.ID), now.UnixNano()),
			Action:            model.RuntimeLifecycleActionUnload,
			Status:            model.RuntimeLifecyclePlanStatusPlanned,
			Message:           fmt.Sprintf("planned unload to release runtime slot for %s", ownerInstanceID),
			ReasonCodes:       []string{"runtime_kind_mutex_release"},
			RequestedTaskType: model.TaskTypeRuntimeStop,
			TriggeredBy:       strings.TrimSpace(trigger),
			Source:            model.RuntimeSignalSourceController,
			GeneratedAt:       now,
			UpdatedAt:         now,
		}
		target.LastPlanAction = target.LastLifecyclePlan.Action
		target.LastPlanStatus = target.LastLifecyclePlan.Status
		target.LastPlanReason = strings.TrimSpace(target.LastLifecyclePlan.Message)
		target.LastPlanGeneratedAt = now
		target.LastPlanDetail = map[string]interface{}{
			"release_for_instance": strings.TrimSpace(ownerInstanceID),
			"reason_codes":         []string{"runtime_kind_mutex_release"},
		}
		if err := s.upsertRuntimeInstance(ctx, target); err != nil {
			errs = append(errs, err)
			continue
		}
		if s.projection != nil {
			if projErr := s.projection.ApplyRuntimeInstanceProjection(ctx, target); projErr != nil {
				s.logger.Warn("runtime instance projection for release plan target failed", "runtime_instance_id", target.ID, "error", projErr)
			}
		}
	}
	return errors.Join(errs...)
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
	detailView := flattenAgentTaskDetail(task.Detail)
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
		strings.TrimSpace(fmt.Sprint(detailView["runtime_service_endpoint"])),
		strings.TrimSpace(fmt.Sprint(detailView["endpoint"])),
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "endpoint")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "endpoint")),
	); endpoint != "" && endpoint != "<nil>" {
		item.Endpoint = endpoint
	}
	if containerID := firstNonEmpty(
		strings.TrimSpace(fmt.Sprint(detailView["runtime_container_id"])),
		strings.TrimSpace(readStringFromObjectMap(task.Payload, "runtime_container_id")),
		strings.TrimSpace(readStringFromNestedObjectMap(task.Payload, "resolved_context", "runtime_container_id")),
	); containerID != "" && containerID != "<nil>" {
		item.Metadata["runtime_container_id"] = containerID
	}
	if mounts := firstNonEmptyStringSlice(
		readStringSliceFromObjectMap(detailView, "resolved_mounts"),
		readStringSliceFromObjectMap(task.Payload, "binding_mount_rules"),
		readStringSliceFromObjectMap(task.Payload, "mount_points"),
	); len(mounts) > 0 {
		item.ResolvedMounts = cloneStringSlice(mounts)
		item.MountedPaths = cloneStringSlice(mounts)
	}
	if ports := firstNonEmptyStringSlice(
		readStringSliceFromObjectMap(detailView, "resolved_ports"),
		readStringSliceFromObjectMap(task.Payload, "exposed_ports"),
		readStringSliceFromObjectMap(task.Payload, "resolved_ports"),
	); len(ports) > 0 {
		item.ResolvedPorts = cloneStringSlice(ports)
	}
	if script := firstNonEmpty(
		strings.TrimSpace(fmt.Sprint(detailView["resolved_script"])),
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
			item.PrecheckReasons = cloneStringSlice(readStringSliceFromObjectMap(detailView, "precheck_failures"))
		}
		if precheck := decodeRuntimePrecheckResult(firstNonNil(detailView["precheck_result"], detailView["structured_result"])); precheck != nil {
			item.PrecheckResult = precheck
			item.PrecheckStatus = precheck.OverallStatus
			item.PrecheckGating = precheck.Gating
			item.PrecheckReasons = extractPrecheckReasonCodes(precheck.Reasons)
			reasons := make([]model.RuntimeConditionReason, 0, len(precheck.Reasons))
			for _, reason := range precheck.Reasons {
				reasons = append(reasons, model.RuntimeConditionReason{
					Code:              strings.TrimSpace(string(reason.Code)),
					Message:           strings.TrimSpace(reason.Message),
					Blocking:          reason.Blocking,
					Source:            model.RuntimeSignalSourceAgent,
					RelatedInstanceID: strings.TrimSpace(item.ID),
					RelatedNodeID:     strings.TrimSpace(item.NodeID),
					RelatedBindingID:  strings.TrimSpace(item.BindingID),
					Detail:            cloneObjectMap(reason.Detail),
				})
			}
			item.PrecheckSummary = &model.RuntimePrecheckSummary{
				Status:      precheck.OverallStatus,
				Gating:      precheck.Gating,
				Reasons:     reasons,
				GeneratedAt: chooseTaskEventTime(task, now),
				Source:      model.RuntimeSignalSourceAgent,
			}
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
		} else {
			item.PrecheckSummary = &model.RuntimePrecheckSummary{
				Status:      item.PrecheckStatus,
				Gating:      item.PrecheckGating,
				Reasons:     precheckReasonsToConditionReasons(item.PrecheckReasons, model.RuntimeSignalSourceAgent, item.ID, item.NodeID, item.BindingID),
				GeneratedAt: chooseTaskEventTime(task, now),
				Source:      model.RuntimeSignalSourceAgent,
			}
		}
		if item.PrecheckGating {
			item.Readiness = model.ReadinessNotReady
			item.DriftReason = firstNonEmpty(item.DriftReason, "precheck_gating=true")
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentRuntimeReadiness:
		ready, hasReady := boolFromTaskDetail(detailView, "runtime_ready")
		if !hasReady {
			ready, hasReady = boolFromTaskDetail(detailView, "ready")
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
		if hostPort := strings.TrimSpace(fmt.Sprint(detailView["host_port"])); hostPort != "" && hostPort != "<nil>" {
			item.ResolvedPorts = appendUniqueString(item.ResolvedPorts, hostPort)
		}
		if !success {
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentModelPathCheck:
		if absPath := strings.TrimSpace(fmt.Sprint(detailView["abs_path"])); absPath != "" && absPath != "<nil>" {
			item.ResolvedMounts = appendUniqueString(item.ResolvedMounts, absPath)
			item.MountedPaths = appendUniqueString(item.MountedPaths, absPath)
		}
		if exists, ok := boolFromTaskDetail(detailView, "exists"); ok && !exists {
			item.Readiness = model.ReadinessNotReady
		}
		if !success {
			item.Readiness = model.ReadinessNotReady
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	case model.TaskTypeAgentDockerInspect, model.TaskTypeAgentDockerStart, model.TaskTypeAgentDockerStop:
		exists, hasExists := boolFromTaskDetail(detailView, "runtime_exists")
		running, hasRunning := boolFromTaskDetail(detailView, "runtime_running")
		if hasExists && !exists {
			item.ObservedState = "stopped"
			item.Readiness = model.ReadinessNotReady
		} else if hasRunning && running {
			item.ObservedState = "running"
			if ready, ok := boolFromTaskDetail(detailView, "runtime_ready"); ok && !ready {
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
		if snapshot, ok := detailView["resource_snapshot"].(map[string]interface{}); ok {
			if hostname := strings.TrimSpace(fmt.Sprint(snapshot["hostname"])); hostname != "" && hostname != "<nil>" {
				item.Metadata["snapshot_hostname"] = hostname
			}
			if dockerRaw, ok := snapshot["docker_access"].(map[string]interface{}); ok {
				item.Metadata["snapshot_docker_accessible"] = strings.TrimSpace(fmt.Sprint(dockerRaw["api_reachable"]))
			}
		}
		item.HealthMessage = firstNonEmpty(msg, item.HealthMessage)
	}

	if observed := strings.TrimSpace(fmt.Sprint(detailView["observed_state"])); observed != "" && observed != "<nil>" {
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
	if err := s.upsertRuntimeInstance(ctx, item); err != nil {
		return err
	}
	_, err = s.ReconcileRuntimeInstance(ctx, item.ID, "agent_task_report")
	return err
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

func flattenAgentTaskDetail(detail map[string]interface{}) map[string]interface{} {
	out := cloneObjectMap(detail)
	if out == nil {
		out = map[string]interface{}{}
	}
	if nestedDetail, ok := detail["detail"].(map[string]interface{}); ok {
		for k, v := range nestedDetail {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			if _, exists := out[key]; !exists {
				out[key] = v
			}
		}
	}
	if structured, ok := detail["structured_result"].(map[string]interface{}); ok {
		for k, v := range structured {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			if _, exists := out[key]; !exists {
				out[key] = v
			}
		}
	}
	return out
}

func firstNonNil(values ...interface{}) interface{} {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
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

func appendRuntimeMessage(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return base
	}
	if base == "" {
		return extra
	}
	if strings.Contains(strings.ToLower(base), strings.ToLower(extra)) {
		return base
	}
	return base + "; " + extra
}

func clearDesiredObservedDrift(current, desired, observed string) string {
	normalized := strings.ToLower(strings.TrimSpace(current))
	if !strings.HasPrefix(normalized, "desired=") {
		return strings.TrimSpace(current)
	}
	if strings.EqualFold(strings.TrimSpace(desired), strings.TrimSpace(observed)) {
		return ""
	}
	return strings.TrimSpace(current)
}

func chooseReadinessState(primary, fallback model.ReadinessState) model.ReadinessState {
	if strings.TrimSpace(string(primary)) != "" && primary != model.ReadinessUnknown {
		return primary
	}
	if strings.TrimSpace(string(fallback)) != "" {
		return fallback
	}
	return model.ReadinessUnknown
}

func chooseNewerTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.After(b) {
		return a.UTC()
	}
	return b.UTC()
}

func firstNonEmptySignalSource(primary, fallback model.RuntimeSignalSource) model.RuntimeSignalSource {
	if strings.TrimSpace(string(primary)) != "" && primary != model.RuntimeSignalSourceUnknown {
		return primary
	}
	if strings.TrimSpace(string(fallback)) != "" {
		return fallback
	}
	return model.RuntimeSignalSourceUnknown
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
	if item.PrecheckSummary == nil {
		item.PrecheckSummary = parseRuntimePrecheckSummary(item.Metadata["precheck_summary_json"])
	}
	if item.ConflictSummary == nil {
		item.ConflictSummary = parseRuntimeConflictSummary(item.Metadata["conflict_summary_json"])
	}
	if item.GatingSummary == nil {
		item.GatingSummary = parseRuntimeGatingSummary(item.Metadata["gating_summary_json"])
	}
	if item.LastLifecyclePlan == nil {
		item.LastLifecyclePlan = parseRuntimeLifecyclePlanSummary(item.Metadata["last_lifecycle_plan_json"])
	}
	if item.PrecheckSummary != nil {
		item.PrecheckStatus = model.PrecheckOverallStatus(firstNonEmpty(strings.TrimSpace(string(item.PrecheckStatus)), strings.TrimSpace(string(item.PrecheckSummary.Status))))
		item.PrecheckGating = item.PrecheckGating || item.PrecheckSummary.Gating
		item.PrecheckReasons = normalizeReasonCodesFromConditionReasons(item.PrecheckSummary.Reasons, item.PrecheckReasons)
	}
	if item.ConflictSummary != nil {
		item.ConflictStatus = model.RuntimeConflictStatus(firstNonEmpty(strings.TrimSpace(string(item.ConflictStatus)), strings.TrimSpace(string(item.ConflictSummary.Status))))
		item.ConflictBlocking = item.ConflictBlocking || item.ConflictSummary.Blocking
		item.ConflictSource = model.RuntimeSignalSource(firstNonEmpty(strings.TrimSpace(string(item.ConflictSource)), strings.TrimSpace(string(item.ConflictSummary.Source))))
		item.ConflictGeneratedAt = chooseNewerTime(item.ConflictGeneratedAt, item.ConflictSummary.GeneratedAt)
		item.ConflictReasons = normalizeReasonCodesFromConditionReasons(item.ConflictSummary.Reasons, item.ConflictReasons)
	}
	if item.GatingSummary != nil {
		item.GatingStatus = model.RuntimeGatingStatus(firstNonEmpty(strings.TrimSpace(string(item.GatingStatus)), strings.TrimSpace(string(item.GatingSummary.Status))))
		item.GatingAllowed = item.GatingAllowed || item.GatingSummary.Allowed
		item.GatingSource = model.RuntimeSignalSource(firstNonEmpty(strings.TrimSpace(string(item.GatingSource)), strings.TrimSpace(string(item.GatingSummary.Source))))
		item.GatingGeneratedAt = chooseNewerTime(item.GatingGeneratedAt, item.GatingSummary.GeneratedAt)
		item.GatingReasons = normalizeReasonCodesFromConditionReasons(item.GatingSummary.Reasons, item.GatingReasons)
	}
	if item.LastLifecyclePlan != nil {
		item.LastPlanAction = model.RuntimeLifecycleAction(firstNonEmpty(strings.TrimSpace(string(item.LastPlanAction)), strings.TrimSpace(string(item.LastLifecyclePlan.Action))))
		item.LastPlanStatus = model.RuntimeLifecyclePlanStatus(firstNonEmpty(strings.TrimSpace(string(item.LastPlanStatus)), strings.TrimSpace(string(item.LastLifecyclePlan.Status))))
		item.LastPlanReason = firstNonEmpty(strings.TrimSpace(item.LastPlanReason), strings.TrimSpace(item.LastLifecyclePlan.Message))
		item.LastPlanGeneratedAt = chooseNewerTime(item.LastPlanGeneratedAt, chooseNewerTime(item.LastLifecyclePlan.GeneratedAt, item.LastLifecyclePlan.UpdatedAt))
		if item.LastPlanDetail == nil {
			item.LastPlanDetail = map[string]interface{}{}
		}
		if strings.TrimSpace(item.LastLifecyclePlan.PlanID) != "" {
			item.LastPlanDetail["plan_id"] = strings.TrimSpace(item.LastLifecyclePlan.PlanID)
		}
		if len(item.LastLifecyclePlan.ReasonCodes) > 0 {
			item.LastPlanDetail["reason_codes"] = cloneStringSlice(item.LastLifecyclePlan.ReasonCodes)
		}
		if len(item.LastLifecyclePlan.BlockedReasonCodes) > 0 {
			item.LastPlanDetail["blocked_reason_codes"] = cloneStringSlice(item.LastLifecyclePlan.BlockedReasonCodes)
		}
		if len(item.LastLifecyclePlan.ReleaseTargets) > 0 {
			item.LastPlanDetail["release_targets"] = cloneStringSlice(item.LastLifecyclePlan.ReleaseTargets)
		}
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
	if item.PrecheckSummary == nil && (strings.TrimSpace(string(item.PrecheckStatus)) != "" || item.PrecheckGating || len(item.PrecheckReasons) > 0) {
		item.PrecheckSummary = &model.RuntimePrecheckSummary{
			Status:      item.PrecheckStatus,
			Gating:      item.PrecheckGating,
			Reasons:     precheckReasonsToConditionReasons(item.PrecheckReasons, model.RuntimeSignalSourceController, item.ID, item.NodeID, item.BindingID),
			GeneratedAt: chooseNewerTime(item.LastPrecheckAt, item.LastReconciledAt),
			Source:      model.RuntimeSignalSourceController,
		}
	}
	if item.ConflictSummary == nil && (strings.TrimSpace(string(item.ConflictStatus)) != "" || len(item.ConflictReasons) > 0 || item.ConflictBlocking) {
		item.ConflictSummary = &model.RuntimeConflictSummary{
			Status:      item.ConflictStatus,
			Blocking:    item.ConflictBlocking,
			Reasons:     precheckReasonsToConditionReasons(item.ConflictReasons, firstNonEmptySignalSource(item.ConflictSource, model.RuntimeSignalSourceController), item.ID, item.NodeID, item.BindingID),
			GeneratedAt: chooseNewerTime(item.ConflictGeneratedAt, item.LastReconciledAt),
			Source:      firstNonEmptySignalSource(item.ConflictSource, model.RuntimeSignalSourceController),
		}
	}
	if item.GatingSummary == nil && (strings.TrimSpace(string(item.GatingStatus)) != "" || len(item.GatingReasons) > 0 || item.GatingAllowed) {
		item.GatingSummary = &model.RuntimeGatingSummary{
			Status:       item.GatingStatus,
			Allowed:      item.GatingAllowed,
			Reasons:      precheckReasonsToConditionReasons(item.GatingReasons, firstNonEmptySignalSource(item.GatingSource, model.RuntimeSignalSourceController), item.ID, item.NodeID, item.BindingID),
			GeneratedAt:  chooseNewerTime(item.GatingGeneratedAt, item.LastReconciledAt),
			Source:       firstNonEmptySignalSource(item.GatingSource, model.RuntimeSignalSourceController),
			TargetAction: item.LastPlanAction,
		}
	}
	if item.LastLifecyclePlan == nil && (strings.TrimSpace(string(item.LastPlanAction)) != "" || strings.TrimSpace(string(item.LastPlanStatus)) != "" || strings.TrimSpace(item.LastPlanReason) != "") {
		item.LastLifecyclePlan = &model.RuntimeLifecyclePlanSummary{
			PlanID:             strings.TrimSpace(fmt.Sprint(item.LastPlanDetail["plan_id"])),
			Action:             item.LastPlanAction,
			Status:             item.LastPlanStatus,
			Message:            strings.TrimSpace(item.LastPlanReason),
			ReasonCodes:        normalizeStringList(readStringSliceFromObjectMap(item.LastPlanDetail, "reason_codes")),
			BlockedReasonCodes: normalizeStringList(readStringSliceFromObjectMap(item.LastPlanDetail, "blocked_reason_codes")),
			ReleaseTargets:     normalizeStringList(readStringSliceFromObjectMap(item.LastPlanDetail, "release_targets")),
			TriggeredBy:        firstNonEmpty(strings.TrimSpace(fmt.Sprint(item.LastPlanDetail["triggered_by"])), "controller.reconcile"),
			Source:             model.RuntimeSignalSourceController,
			GeneratedAt:        item.LastPlanGeneratedAt,
			UpdatedAt:          item.LastPlanGeneratedAt,
		}
	}
	if item.PrecheckSummary != nil {
		item.Metadata["precheck_summary_json"] = mustJSON(item.PrecheckSummary, "{}")
	}
	if item.ConflictSummary != nil {
		item.Metadata["conflict_summary_json"] = mustJSON(item.ConflictSummary, "{}")
	}
	if item.GatingSummary != nil {
		item.Metadata["gating_summary_json"] = mustJSON(item.GatingSummary, "{}")
	}
	if item.LastLifecyclePlan != nil {
		item.Metadata["last_lifecycle_plan_json"] = mustJSON(item.LastLifecyclePlan, "{}")
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

func parseRuntimePrecheckSummary(raw string) *model.RuntimePrecheckSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out model.RuntimePrecheckSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return &out
}

func parseRuntimeConflictSummary(raw string) *model.RuntimeConflictSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out model.RuntimeConflictSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return &out
}

func parseRuntimeGatingSummary(raw string) *model.RuntimeGatingSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out model.RuntimeGatingSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return &out
}

func parseRuntimeLifecyclePlanSummary(raw string) *model.RuntimeLifecyclePlanSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out model.RuntimeLifecyclePlanSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
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
	if ok {
		instance.BindingMode = model.RuntimeBindingMode(firstNonEmpty(strings.TrimSpace(string(instance.BindingMode)), strings.TrimSpace(string(existing.BindingMode))))
		instance.ManifestID = firstNonEmpty(strings.TrimSpace(instance.ManifestID), strings.TrimSpace(existing.ManifestID))
		instance.NodeID = firstNonEmpty(strings.TrimSpace(instance.NodeID), strings.TrimSpace(existing.NodeID))
		instance.ObservedState = firstNonEmpty(strings.TrimSpace(existing.ObservedState), strings.TrimSpace(instance.ObservedState))
		instance.Readiness = chooseReadinessState(existing.Readiness, instance.Readiness)
		instance.HealthMessage = firstNonEmpty(strings.TrimSpace(existing.HealthMessage), strings.TrimSpace(instance.HealthMessage))
		instance.DriftReason = firstNonEmpty(strings.TrimSpace(existing.DriftReason), strings.TrimSpace(instance.DriftReason))
		instance.Endpoint = firstNonEmpty(strings.TrimSpace(existing.Endpoint), strings.TrimSpace(instance.Endpoint))
		instance.LastReconciledAt = chooseNewerTime(existing.LastReconciledAt, instance.LastReconciledAt)
		instance.LastPrecheckAt = chooseNewerTime(existing.LastPrecheckAt, instance.LastPrecheckAt)
		instance.LastPrecheckTaskID = firstNonEmpty(strings.TrimSpace(existing.LastPrecheckTaskID), strings.TrimSpace(instance.LastPrecheckTaskID))
		instance.PrecheckStatus = model.PrecheckOverallStatus(firstNonEmpty(strings.TrimSpace(string(existing.PrecheckStatus)), strings.TrimSpace(string(instance.PrecheckStatus))))
		instance.PrecheckGating = existing.PrecheckGating || instance.PrecheckGating
		if len(existing.PrecheckReasons) > 0 {
			instance.PrecheckReasons = cloneStringSlice(existing.PrecheckReasons)
		}
		if existing.PrecheckResult != nil {
			instance.PrecheckResult = existing.PrecheckResult
		}
		if existing.PrecheckSummary != nil {
			instance.PrecheckSummary = existing.PrecheckSummary
		}
		if existing.LastAgentTask != nil {
			instance.LastAgentTask = existing.LastAgentTask
		}
		instance.ConflictStatus = model.RuntimeConflictStatus(firstNonEmpty(strings.TrimSpace(string(existing.ConflictStatus)), strings.TrimSpace(string(instance.ConflictStatus))))
		instance.ConflictBlocking = existing.ConflictBlocking || instance.ConflictBlocking
		instance.ConflictReasons = normalizeReasonCodesFromConditionReasons(precheckReasonsToConditionReasons(existing.ConflictReasons, model.RuntimeSignalSourceController, existing.ID, existing.NodeID, existing.BindingID), instance.ConflictReasons)
		instance.ConflictSource = model.RuntimeSignalSource(firstNonEmpty(strings.TrimSpace(string(existing.ConflictSource)), strings.TrimSpace(string(instance.ConflictSource))))
		instance.ConflictGeneratedAt = chooseNewerTime(existing.ConflictGeneratedAt, instance.ConflictGeneratedAt)
		if existing.ConflictSummary != nil {
			instance.ConflictSummary = existing.ConflictSummary
		}
		instance.GatingStatus = model.RuntimeGatingStatus(firstNonEmpty(strings.TrimSpace(string(existing.GatingStatus)), strings.TrimSpace(string(instance.GatingStatus))))
		instance.GatingAllowed = existing.GatingAllowed || instance.GatingAllowed
		instance.GatingReasons = normalizeReasonCodesFromConditionReasons(precheckReasonsToConditionReasons(existing.GatingReasons, model.RuntimeSignalSourceController, existing.ID, existing.NodeID, existing.BindingID), instance.GatingReasons)
		instance.GatingSource = model.RuntimeSignalSource(firstNonEmpty(strings.TrimSpace(string(existing.GatingSource)), strings.TrimSpace(string(instance.GatingSource))))
		instance.GatingGeneratedAt = chooseNewerTime(existing.GatingGeneratedAt, instance.GatingGeneratedAt)
		if existing.GatingSummary != nil {
			instance.GatingSummary = existing.GatingSummary
		}
		instance.LastPlanAction = model.RuntimeLifecycleAction(firstNonEmpty(strings.TrimSpace(string(existing.LastPlanAction)), strings.TrimSpace(string(instance.LastPlanAction))))
		instance.LastPlanStatus = model.RuntimeLifecyclePlanStatus(firstNonEmpty(strings.TrimSpace(string(existing.LastPlanStatus)), strings.TrimSpace(string(instance.LastPlanStatus))))
		instance.LastPlanReason = firstNonEmpty(strings.TrimSpace(existing.LastPlanReason), strings.TrimSpace(instance.LastPlanReason))
		instance.LastPlanGeneratedAt = chooseNewerTime(existing.LastPlanGeneratedAt, instance.LastPlanGeneratedAt)
		if len(existing.LastPlanDetail) > 0 && len(instance.LastPlanDetail) == 0 {
			instance.LastPlanDetail = cloneObjectMap(existing.LastPlanDetail)
		}
		if existing.LastLifecyclePlan != nil {
			instance.LastLifecyclePlan = existing.LastLifecyclePlan
		}
		if len(existing.ResolvedMounts) > 0 {
			instance.ResolvedMounts = cloneStringSlice(existing.ResolvedMounts)
		}
		if len(existing.ResolvedPorts) > 0 {
			instance.ResolvedPorts = cloneStringSlice(existing.ResolvedPorts)
		}
		if strings.TrimSpace(existing.ResolvedScript) != "" {
			instance.ResolvedScript = strings.TrimSpace(existing.ResolvedScript)
		}
		if len(existing.MountedPaths) > 0 {
			instance.MountedPaths = cloneStringSlice(existing.MountedPaths)
		}
		if strings.TrimSpace(existing.ScriptUsed) != "" {
			instance.ScriptUsed = strings.TrimSpace(existing.ScriptUsed)
		}
		mergedMetadata := cloneStringMap(existing.Metadata)
		for k, v := range instance.Metadata {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			mergedMetadata[key] = strings.TrimSpace(v)
		}
		instance.Metadata = mergedMetadata
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
