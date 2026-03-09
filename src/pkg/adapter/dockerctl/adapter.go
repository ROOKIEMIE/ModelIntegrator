package dockerctl

import (
	"context"
	"time"

	"ModelIntegrator/src/pkg/model"
)

type Adapter struct {
	name     string
	endpoint string
	token    string
}

func NewAdapter(name, endpoint, token string) *Adapter {
	if name == "" {
		name = "dockerctl"
	}
	return &Adapter{name: name, endpoint: endpoint, token: token}
}

func (a *Adapter) Name() string {
	return a.name
}

func (a *Adapter) HealthCheck(ctx context.Context) (model.ActionResult, error) {
	_ = ctx
	if a.endpoint == "" {
		return result(false, "未配置 endpoint，当前为占位适配器", nil), nil
	}
	return result(true, "占位健康检查成功（待接入真实 Docker/Portainer API）", map[string]interface{}{"endpoint": a.endpoint}), nil
}

func (a *Adapter) ListModels(ctx context.Context) ([]model.Model, error) {
	_ = ctx
	return []model.Model{}, nil
}

func (a *Adapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	return result(true, "占位操作：Load 已接收，待实现真实调用", map[string]interface{}{"model_id": m.ID, "endpoint": a.endpoint}), nil
}

func (a *Adapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	return result(true, "占位操作：Unload 已接收，待实现真实调用", map[string]interface{}{"model_id": m.ID, "endpoint": a.endpoint}), nil
}

func (a *Adapter) StartModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	return result(true, "占位操作：Start 已接收，待实现真实调用", map[string]interface{}{"model_id": m.ID, "endpoint": a.endpoint}), nil
}

func (a *Adapter) StopModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	return result(true, "占位操作：Stop 已接收，待实现真实调用", map[string]interface{}{"model_id": m.ID, "endpoint": a.endpoint}), nil
}

func (a *Adapter) GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error) {
	_ = ctx
	return result(true, "占位操作：GetStatus 已接收，待实现真实调用", map[string]interface{}{"model_id": m.ID}), nil
}

func result(success bool, message string, detail map[string]interface{}) model.ActionResult {
	return model.ActionResult{
		Success:   success,
		Message:   message,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}
