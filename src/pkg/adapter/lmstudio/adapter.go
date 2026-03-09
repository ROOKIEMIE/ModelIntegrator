package lmstudio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"ModelIntegrator/src/pkg/model"
)

type Adapter struct {
	client *Client
}

func NewAdapter(endpoint, token string, timeout time.Duration) *Adapter {
	return &Adapter{client: NewClient(endpoint, token, timeout)}
}

func (a *Adapter) Name() string {
	return "lmstudio"
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
	statusCode, body, err := a.client.Call(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("LM Studio /v1/models 返回状态码 %d: %s", statusCode, string(body))
	}

	models := make([]model.Model, 0)

	type modelEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	var listResp struct {
		Data []modelEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &listResp); err == nil && len(listResp.Data) > 0 {
		for _, item := range listResp.Data {
			id := item.ID
			if id == "" {
				id = item.Name
			}
			models = append(models, model.Model{
				ID:          id,
				Name:        item.Name,
				Provider:    "lmstudio",
				BackendType: model.RuntimeTypeLMStudio,
				RuntimeID:   "lmstudio-default",
				State:       model.ModelStateUnknown,
			})
		}
		return models, nil
	}

	var directList []modelEntry
	if err := json.Unmarshal(body, &directList); err == nil {
		for _, item := range directList {
			id := item.ID
			if id == "" {
				id = item.Name
			}
			models = append(models, model.Model{
				ID:          id,
				Name:        item.Name,
				Provider:    "lmstudio",
				BackendType: model.RuntimeTypeLMStudio,
				RuntimeID:   "lmstudio-default",
				State:       model.ModelStateUnknown,
			})
		}
		return models, nil
	}

	return []model.Model{}, nil
}

func (a *Adapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	statusCode, body, err := a.client.Call(ctx, http.MethodPost, "/v1/models/load", map[string]interface{}{"model": m.ID})
	if err != nil {
		return actionResult(false, "加载模型失败", map[string]interface{}{"error": err.Error()}), nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return actionResult(false, "加载模型失败，LM Studio 返回非 2xx", map[string]interface{}{"status_code": statusCode, "body": string(body)}), nil
	}
	return actionResult(true, "模型加载请求已发送到 LM Studio", map[string]interface{}{"status_code": statusCode, "model_id": m.ID}), nil
}

func (a *Adapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	statusCode, body, err := a.client.Call(ctx, http.MethodPost, "/v1/models/unload", map[string]interface{}{"model": m.ID})
	if err != nil {
		return actionResult(false, "卸载模型失败", map[string]interface{}{"error": err.Error()}), nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return actionResult(false, "卸载模型失败，LM Studio 返回非 2xx", map[string]interface{}{"status_code": statusCode, "body": string(body)}), nil
	}
	return actionResult(true, "模型卸载请求已发送到 LM Studio", map[string]interface{}{"status_code": statusCode, "model_id": m.ID}), nil
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
