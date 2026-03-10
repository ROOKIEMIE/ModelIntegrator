package lmstudio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"model-control-plane/src/pkg/model"
)

type Adapter struct {
	client               *Client
	cacheEnabled         bool
	cacheRefreshInterval time.Duration
	logger               *slog.Logger

	cacheMu        sync.RWMutex
	cachedModels   []model.Model
	cacheUpdatedAt time.Time
	cacheLoopOnce  sync.Once
}

func NewAdapter(endpoint, token string, timeout time.Duration, cacheEnabled bool, cacheRefreshInterval time.Duration) *Adapter {
	if cacheRefreshInterval <= 0 {
		cacheRefreshInterval = 30 * time.Second
	}
	return &Adapter{
		client:               NewClient(endpoint, token, timeout),
		cacheEnabled:         cacheEnabled,
		cacheRefreshInterval: cacheRefreshInterval,
		logger:               slog.Default(),
	}
}

func (a *Adapter) Name() string {
	return "lmstudio"
}

func (a *Adapter) StartCacheSync() {
	if !a.cacheEnabled {
		return
	}
	a.startCacheLoop()
}

func (a *Adapter) HealthCheck(ctx context.Context) (model.ActionResult, error) {
	statusCode, body, err := a.client.Call(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return actionResult(false, "LM Studio 健康检查失败", map[string]interface{}{"error": err.Error()}), nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return actionResult(false, "LM Studio 健康检查返回非 2xx", map[string]interface{}{"status_code": statusCode, "body": string(body)}), nil
	}
	return actionResult(true, "LM Studio 健康检查通过", map[string]interface{}{"status_code": statusCode}), nil
}

func (a *Adapter) ListModels(ctx context.Context) ([]model.Model, error) {
	if !a.cacheEnabled {
		return a.listModelsRemote(ctx)
	}
	a.startCacheLoop()
	if err := a.refreshCache(ctx); err != nil {
		if cached := a.snapshotCachedModels(); len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	return a.snapshotCachedModels(), nil
}

func (a *Adapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	resolvedModelName, matchedModel, err := a.resolveModelName(ctx, m)
	if err != nil {
		return actionResult(false, "加载前模型名称校验失败", map[string]interface{}{
			"error":          err.Error(),
			"requested_id":   m.ID,
			"requested_name": m.Name,
		}), nil
	}

	statusCode, _, usedPath, err := a.postModelAction(ctx, "load", resolvedModelName)
	if err != nil {
		return actionResult(false, "加载模型失败", map[string]interface{}{"error": err.Error()}), nil
	}
	a.refreshCacheAsync(5 * time.Second)
	return actionResult(true, "模型加载请求已发送到 LM Studio", map[string]interface{}{
		"status_code":      statusCode,
		"path":             usedPath,
		"requested_id":     m.ID,
		"requested_name":   m.Name,
		"resolved_model":   resolvedModelName,
		"matched_model_id": matchedModel.ID,
		"matched_name":     matchedModel.Name,
	}), nil
}

func (a *Adapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	resolvedModelName, matchedModel, err := a.resolveModelName(ctx, m)
	if err != nil {
		return actionResult(false, "卸载前模型名称校验失败", map[string]interface{}{
			"error":          err.Error(),
			"requested_id":   m.ID,
			"requested_name": m.Name,
		}), nil
	}

	statusCode, _, usedPath, err := a.postUnloadAction(ctx, resolvedModelName, matchedModel)
	if err != nil {
		return actionResult(false, "卸载模型失败", map[string]interface{}{"error": err.Error()}), nil
	}
	a.refreshCacheAsync(5 * time.Second)
	return actionResult(true, "模型卸载请求已发送到 LM Studio", map[string]interface{}{
		"status_code":      statusCode,
		"path":             usedPath,
		"requested_id":     m.ID,
		"requested_name":   m.Name,
		"resolved_model":   resolvedModelName,
		"matched_model_id": matchedModel.ID,
		"matched_name":     matchedModel.Name,
	}), nil
}

func (a *Adapter) StartModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	result, err := a.LoadModel(ctx, m)
	if err != nil {
		return result, err
	}
	if result.Message != "" {
		result.Message = "LM Studio 场景下 Start 映射为 Load: " + result.Message
	}
	return result, nil
}

func (a *Adapter) StopModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	result, err := a.UnloadModel(ctx, m)
	if err != nil {
		return result, err
	}
	if result.Message != "" {
		result.Message = "LM Studio 场景下 Stop 映射为 Unload: " + result.Message
	}
	return result, nil
}

func (a *Adapter) GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error) {
	path := "/v1/models/" + url.PathEscape(m.ID)
	statusCode, body, err := a.client.Call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return actionResult(false, "获取模型状态失败", map[string]interface{}{"error": err.Error()}), nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return actionResult(false, "获取模型状态失败，LM Studio 返回非 2xx", map[string]interface{}{"status_code": statusCode, "body": string(body)}), nil
	}

	return actionResult(true, "获取模型状态成功", map[string]interface{}{"status_code": statusCode, "body": string(body)}), nil
}

func actionResult(success bool, message string, detail map[string]interface{}) model.ActionResult {
	return model.ActionResult{
		Success:   success,
		Message:   message,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}

func (a *Adapter) resolveModelName(ctx context.Context, m model.Model) (string, model.Model, error) {
	remoteModels, err := a.listModelsForAction(ctx)
	if err != nil {
		return "", model.Model{}, err
	}
	if len(remoteModels) == 0 {
		return "", model.Model{}, fmt.Errorf("LM Studio 当前未返回可用模型")
	}

	equals := func(a, b string) bool {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}

	// 优先按配置中的模型名称匹配，其次按模型 ID 匹配。
	for _, item := range remoteModels {
		if m.Name != "" && (equals(item.Name, m.Name) || equals(item.ID, m.Name)) {
			if item.ID != "" {
				return item.ID, item, nil
			}
			return item.Name, item, nil
		}
	}
	for _, item := range remoteModels {
		if m.ID != "" && (equals(item.ID, m.ID) || equals(item.Name, m.ID)) {
			if item.ID != "" {
				return item.ID, item, nil
			}
			return item.Name, item, nil
		}
	}

	available := make([]string, 0, len(remoteModels))
	for _, item := range remoteModels {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.ID)
		}
		if name != "" {
			available = append(available, name)
		}
	}

	return "", model.Model{}, fmt.Errorf("未在 LM Studio 中找到目标模型（id=%s, name=%s），可用模型: %s", m.ID, m.Name, strings.Join(available, ", "))
}

func (a *Adapter) listModelsRemote(ctx context.Context) ([]model.Model, error) {
	paths := []string{"/api/v1/models", "/v1/models"}
	var lastErr error
	for _, path := range paths {
		models, err := a.listModelsByPath(ctx, path)
		if err == nil {
			return models, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return []model.Model{}, nil
}

func (a *Adapter) listModelsByPath(ctx context.Context, path string) ([]model.Model, error) {
	statusCode, body, err := a.client.Call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("LM Studio %s 返回状态码 %d: %s", path, statusCode, string(body))
	}
	return parseModelListBody(body), nil
}

func (a *Adapter) postModelAction(ctx context.Context, action, modelName string) (statusCode int, respBody []byte, usedPath string, err error) {
	paths := []string{"/api/v1/models/" + action, "/v1/models/" + action}
	requestBody := map[string]interface{}{"model": modelName}

	var lastErr error
	for _, path := range paths {
		statusCode, respBody, callErr := a.client.Call(ctx, http.MethodPost, path, requestBody)
		if callErr != nil {
			lastErr = fmt.Errorf("%s 调用失败: %w", path, callErr)
			continue
		}
		if statusCode >= 200 && statusCode < 300 {
			return statusCode, respBody, path, nil
		}
		lastErr = fmt.Errorf("%s 返回状态码 %d: %s", path, statusCode, strings.TrimSpace(string(respBody)))
	}

	if lastErr != nil {
		return 0, nil, "", lastErr
	}
	return 0, nil, "", fmt.Errorf("LM Studio %s 请求失败", action)
}

func (a *Adapter) postUnloadAction(ctx context.Context, modelName string, matched model.Model) (statusCode int, respBody []byte, usedPath string, err error) {
	type unloadAttempt struct {
		path string
		body map[string]interface{}
	}

	attempts := make([]unloadAttempt, 0, 3)
	if matched.Metadata != nil {
		if instanceID := strings.TrimSpace(matched.Metadata["loaded_instance_id"]); instanceID != "" {
			attempts = append(attempts, unloadAttempt{
				path: "/api/v1/models/unload",
				body: map[string]interface{}{"instance_id": instanceID},
			})
		}
	}
	attempts = append(attempts,
		unloadAttempt{
			path: "/api/v1/models/unload",
			body: map[string]interface{}{"model": modelName},
		},
		unloadAttempt{
			path: "/v1/models/unload",
			body: map[string]interface{}{"model": modelName},
		},
	)

	var lastErr error
	for _, attempt := range attempts {
		statusCode, respBody, callErr := a.client.Call(ctx, http.MethodPost, attempt.path, attempt.body)
		if callErr != nil {
			lastErr = fmt.Errorf("%s 调用失败: %w", attempt.path, callErr)
			continue
		}
		if statusCode >= 200 && statusCode < 300 {
			return statusCode, respBody, attempt.path, nil
		}
		lastErr = fmt.Errorf("%s 返回状态码 %d: %s", attempt.path, statusCode, strings.TrimSpace(string(respBody)))
	}
	if lastErr != nil {
		return 0, nil, "", lastErr
	}
	return 0, nil, "", fmt.Errorf("LM Studio unload 请求失败")
}

func parseModelListBody(body []byte) []model.Model {
	parseEntries := func(entries []map[string]interface{}) []model.Model {
		models := make([]model.Model, 0, len(entries))
		for _, entry := range entries {
			id := toString(entry["id"])
			if id == "" {
				id = toString(entry["key"])
			}
			name := toString(entry["name"])
			if name == "" {
				name = toString(entry["display_name"])
			}
			if id == "" {
				id = name
			}
			if name == "" {
				name = id
			}
			if id == "" {
				continue
			}

			models = append(models, model.Model{
				ID:          id,
				Name:        name,
				Provider:    "lmstudio",
				BackendType: model.RuntimeTypeLMStudio,
				RuntimeID:   "lmstudio-default",
				State:       parseModelState(entry),
				Metadata:    parseModelMetadata(entry),
			})
		}
		return models
	}

	var dataWrapper struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &dataWrapper); err == nil && len(dataWrapper.Data) > 0 {
		return parseEntries(dataWrapper.Data)
	}

	var modelsWrapper struct {
		Models []map[string]interface{} `json:"models"`
	}
	if err := json.Unmarshal(body, &modelsWrapper); err == nil && len(modelsWrapper.Models) > 0 {
		return parseEntries(modelsWrapper.Models)
	}

	var directList []map[string]interface{}
	if err := json.Unmarshal(body, &directList); err == nil {
		return parseEntries(directList)
	}

	return []model.Model{}
}

func (a *Adapter) listModelsForAction(ctx context.Context) ([]model.Model, error) {
	if !a.cacheEnabled {
		return a.listModelsRemote(ctx)
	}

	a.startCacheLoop()
	if err := a.refreshCache(ctx); err != nil {
		if cached := a.snapshotCachedModels(); len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	cached := a.snapshotCachedModels()
	if len(cached) == 0 {
		return nil, fmt.Errorf("LM Studio 缓存为空")
	}
	return cached, nil
}

func (a *Adapter) startCacheLoop() {
	a.cacheLoopOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(a.cacheRefreshInterval)
			defer ticker.Stop()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := a.refreshCache(ctx); err != nil {
				a.logger.Warn("LM Studio 初始缓存刷新失败", "error", err)
			}
			cancel()

			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := a.refreshCache(ctx); err != nil {
					a.logger.Warn("LM Studio 周期缓存刷新失败", "error", err)
				}
				cancel()
			}
		}()
	})
}

func (a *Adapter) refreshCache(ctx context.Context) error {
	models, err := a.listModelsRemote(ctx)
	if err != nil {
		return err
	}

	a.cacheMu.Lock()
	a.cachedModels = cloneModels(models)
	a.cacheUpdatedAt = time.Now().UTC()
	a.cacheMu.Unlock()
	return nil
}

func (a *Adapter) snapshotCachedModels() []model.Model {
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	return cloneModels(a.cachedModels)
}

func (a *Adapter) refreshCacheAsync(timeout time.Duration) {
	if !a.cacheEnabled {
		return
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := a.refreshCache(ctx); err != nil {
			a.logger.Warn("LM Studio 异步缓存刷新失败", "error", err)
		}
	}()
}

func cloneModels(src []model.Model) []model.Model {
	dst := make([]model.Model, len(src))
	copy(dst, src)
	return dst
}

func parseModelState(entry map[string]interface{}) model.ModelState {
	if loadedInstances, ok := entry["loaded_instances"]; ok {
		if instances, ok := loadedInstances.([]interface{}); ok {
			if len(instances) > 0 {
				return model.ModelStateLoaded
			}
			return model.ModelStateStopped
		}
		if loadedInstances == nil {
			return model.ModelStateStopped
		}
	}

	for _, key := range []string{"loaded", "is_loaded", "isLoaded", "isLoadedModel"} {
		if loaded, ok := parseBoolAny(entry[key]); ok {
			if loaded {
				return model.ModelStateLoaded
			}
			return model.ModelStateStopped
		}
	}

	for _, key := range []string{"state", "status", "load_state", "loadState"} {
		raw := strings.ToLower(strings.TrimSpace(toString(entry[key])))
		switch raw {
		case "loaded", "ready", "active":
			return model.ModelStateLoaded
		case "running", "serving":
			return model.ModelStateRunning
		case "busy":
			return model.ModelStateBusy
		case "stopped", "unloaded", "idle", "not_loaded", "not-loaded":
			return model.ModelStateStopped
		case "error", "failed":
			return model.ModelStateError
		}
	}

	for _, nestedKey := range []string{"state", "status"} {
		if nested, ok := entry[nestedKey].(map[string]interface{}); ok {
			if state := parseModelState(nested); state != model.ModelStateUnknown {
				return state
			}
		}
	}
	return model.ModelStateUnknown
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return strings.TrimSpace(vv)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", vv))
	}
}

func parseBoolAny(v interface{}) (bool, bool) {
	switch vv := v.(type) {
	case bool:
		return vv, true
	case string:
		switch strings.ToLower(strings.TrimSpace(vv)) {
		case "true", "1", "yes", "on", "loaded":
			return true, true
		case "false", "0", "no", "off", "unloaded":
			return false, true
		default:
			return false, false
		}
	case float64:
		if vv == 1 {
			return true, true
		}
		if vv == 0 {
			return false, true
		}
		return false, false
	case int:
		if vv == 1 {
			return true, true
		}
		if vv == 0 {
			return false, true
		}
		return false, false
	default:
		return false, false
	}
}

func parseModelMetadata(entry map[string]interface{}) map[string]string {
	meta := map[string]string{}
	if loadedInstances, ok := entry["loaded_instances"].([]interface{}); ok {
		meta["loaded_instances_count"] = fmt.Sprintf("%d", len(loadedInstances))
		if len(loadedInstances) > 0 {
			if first, ok := loadedInstances[0].(map[string]interface{}); ok {
				if instanceID := strings.TrimSpace(toString(first["id"])); instanceID != "" {
					meta["loaded_instance_id"] = instanceID
				}
			}
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
