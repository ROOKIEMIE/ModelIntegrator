package service

import (
	"context"
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
	return item, nil
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
	instance := model.RuntimeInstance{
		ID:               id,
		ModelID:          item.ID,
		TemplateID:       binding.TemplateID,
		BindingID:        binding.ID,
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
		LastReconciledAt: item.LastReconciledAt,
		Metadata: map[string]string{
			"binding_mode": string(binding.BindingMode),
			"phase":        "stage-0",
		},
		CreatedAt: createdAt,
		UpdatedAt: time.Now().UTC(),
	}
	if instance.NodeID == "" {
		nodes := s.nodeRegistry.List()
		if len(nodes) > 0 {
			instance.NodeID = nodes[0].ID
		}
	}
	if !ok && item.LastReconciledAt.IsZero() {
		instance.LastReconciledAt = time.Now().UTC()
	}
	if instance.Readiness == "" {
		instance.Readiness = model.ReadinessUnknown
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
	if s.store != nil {
		if err := s.store.UpsertRuntimeInstance(ctx, item); err != nil {
			return err
		}
	}
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
