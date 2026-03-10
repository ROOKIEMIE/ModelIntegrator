package dockerctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	client, err := newDockerHTTPClient(a.name, a.endpoint, a.token, 10*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	if err := client.ping(ctx); err != nil {
		return result(false, "容器编排服务不可达", map[string]interface{}{"error": err.Error(), "endpoint": a.endpoint}), nil
	}
	return result(true, "容器编排服务可用", map[string]interface{}{"endpoint": a.endpoint}), nil
}

func (a *Adapter) ListModels(ctx context.Context) ([]model.Model, error) {
	client, err := newDockerHTTPClient(a.name, a.endpoint, a.token, 12*time.Second)
	if err != nil {
		return nil, err
	}
	containers, err := client.listContainers(ctx, true, map[string][]string{
		"label": {"com.modelintegrator.managed=true"},
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Model, 0, len(containers))
	for _, c := range containers {
		state := model.ModelStateStopped
		if strings.EqualFold(c.State, "running") {
			state = model.ModelStateRunning
		}
		m := model.Model{
			ID:          firstNonEmpty(c.Labels["com.modelintegrator.model_id"], strings.TrimPrefix(strings.TrimSpace(firstContainerName(c.Names)), "/")),
			Name:        firstNonEmpty(c.Labels["com.modelintegrator.model_name"], c.Image),
			Provider:    "docker",
			BackendType: model.RuntimeTypeDocker,
			State:       state,
			Metadata: map[string]string{
				"source":               "docker-list",
				"runtime_container_id": c.ID,
				"runtime_container":    strings.TrimPrefix(strings.TrimSpace(firstContainerName(c.Names)), "/"),
				"runtime_image":        c.Image,
			},
		}
		out = append(out, m)
	}
	return out, nil
}

func (a *Adapter) LoadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	client, err := a.newClientForModel(m, 25*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	tpl, err := parseRuntimeTemplateFromModel(m)
	if err != nil {
		return result(false, "运行时模板解析失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	tpl = materializeRuntimeTemplate(tpl, m)
	containerID, created, err := client.ensureContainer(ctx, m, tpl)
	if err != nil {
		return result(false, "容器创建失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	info, err := client.inspectContainer(ctx, containerID)
	if err != nil {
		return result(false, "容器状态读取失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	running := info.State != nil && info.State.Running
	if running {
		if err := client.stopContainer(ctx, containerID, 10); err != nil {
			return result(false, "容器装载后停止失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
		}
		running = false
	}
	return result(true, "容器模板已装载", map[string]interface{}{
		"model_id":             m.ID,
		"runtime_container_id": containerID,
		"runtime_container":    firstNonEmpty(strings.TrimPrefix(info.Name, "/"), containerNameForModel(m)),
		"runtime_created":      created,
		"runtime_running":      running,
		"runtime_image":        tpl.Image,
		"backend":              a.name,
	}), nil
}

func (a *Adapter) UnloadModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	client, err := a.newClientForModel(m, 25*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	containerID, exists, err := client.resolveContainerID(ctx, m)
	if err != nil {
		return result(false, "查询容器失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	if !exists {
		return result(true, "容器不存在，无需卸载", map[string]interface{}{"model_id": m.ID, "backend": a.name}), nil
	}
	_ = client.stopContainer(ctx, containerID, 10)
	if err := client.removeContainer(ctx, containerID, true); err != nil {
		return result(false, "卸载容器失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	return result(true, "容器已卸载", map[string]interface{}{
		"model_id":             m.ID,
		"runtime_container_id": containerID,
		"runtime_removed":      true,
		"backend":              a.name,
	}), nil
}

func (a *Adapter) StartModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	client, err := a.newClientForModel(m, 25*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	tpl, err := parseRuntimeTemplateFromModel(m)
	if err != nil {
		return result(false, "运行时模板解析失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	tpl = materializeRuntimeTemplate(tpl, m)
	containerID, _, err := client.ensureContainer(ctx, m, tpl)
	if err != nil {
		return result(false, "容器准备失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	info, err := client.inspectContainer(ctx, containerID)
	if err != nil {
		return result(false, "容器状态读取失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	if info.State != nil && info.State.Running {
		return result(true, "容器已在运行", map[string]interface{}{
			"runtime_container_id": containerID,
			"runtime_running":      true,
			"backend":              a.name,
		}), nil
	}
	if err := client.startContainer(ctx, containerID); err != nil {
		return result(false, "启动容器失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	finalStatus, waitErr := client.waitUntilContainerRunning(ctx, containerID, 8*time.Second)
	if waitErr != nil {
		return result(false, "容器启动后未保持运行", map[string]interface{}{
			"error":                waitErr.Error(),
			"runtime_status":       firstNonEmpty(finalStatus, "unknown"),
			"runtime_container_id": containerID,
			"backend":              a.name,
		}), nil
	}
	return result(true, "容器已启动", map[string]interface{}{
		"runtime_container_id": containerID,
		"runtime_running":      true,
		"backend":              a.name,
	}), nil
}

func (a *Adapter) StopModel(ctx context.Context, m model.Model) (model.ActionResult, error) {
	client, err := a.newClientForModel(m, 25*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	containerID, exists, err := client.resolveContainerID(ctx, m)
	if err != nil {
		return result(false, "查询容器失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	if !exists {
		return result(true, "容器不存在，无需停止", map[string]interface{}{"model_id": m.ID, "backend": a.name}), nil
	}
	info, err := client.inspectContainer(ctx, containerID)
	if err == nil && info.State != nil && !info.State.Running {
		return result(true, "容器已停止", map[string]interface{}{
			"runtime_container_id": containerID,
			"runtime_running":      false,
			"backend":              a.name,
		}), nil
	}
	if err := client.stopContainer(ctx, containerID, 10); err != nil {
		return result(false, "停止容器失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	return result(true, "容器已停止", map[string]interface{}{
		"runtime_container_id": containerID,
		"runtime_running":      false,
		"backend":              a.name,
	}), nil
}

func (a *Adapter) GetStatus(ctx context.Context, m model.Model) (model.ActionResult, error) {
	client, err := a.newClientForModel(m, 15*time.Second)
	if err != nil {
		return result(false, "初始化容器编排客户端失败", map[string]interface{}{"error": err.Error()}), nil
	}
	containerID, exists, err := client.resolveContainerID(ctx, m)
	if err != nil {
		return result(false, "查询容器失败", map[string]interface{}{"error": err.Error(), "model_id": m.ID}), nil
	}
	if !exists {
		return result(true, "容器未创建", map[string]interface{}{
			"runtime_exists": false,
			"backend":        a.name,
		}), nil
	}
	info, err := client.inspectContainer(ctx, containerID)
	if err != nil {
		return result(false, "容器状态读取失败", map[string]interface{}{"error": err.Error(), "container_id": containerID}), nil
	}
	return result(true, "容器状态查询成功", map[string]interface{}{
		"runtime_exists":       true,
		"runtime_container_id": containerID,
		"runtime_running":      info.State != nil && info.State.Running,
		"runtime_status":       firstNonEmpty(info.State.Status, "unknown"),
		"runtime_image":        info.Config.Image,
		"backend":              a.name,
	}), nil
}

func (a *Adapter) newClientForModel(m model.Model, timeout time.Duration) (*dockerHTTPClient, error) {
	endpoint, token := a.resolveConnectionForModel(m)
	return newDockerHTTPClient(a.name, endpoint, token, timeout)
}

func (a *Adapter) resolveConnectionForModel(m model.Model) (endpoint string, token string) {
	endpoint = firstNonEmpty(
		readMetadata(m.Metadata, "runtime_endpoint"),
		m.Endpoint,
		a.endpoint,
	)
	token = firstNonEmpty(
		readMetadata(m.Metadata, "runtime_token"),
		readMetadata(m.Metadata, "runtime_api_key"),
		readMetadata(m.Metadata, "runtime_bearer_token"),
		a.token,
	)
	return endpoint, token
}

func parseRuntimeTemplateFromModel(m model.Model) (model.RuntimeTemplate, error) {
	if m.Metadata == nil {
		return model.RuntimeTemplate{}, errors.New("model metadata 为空，缺少 runtime_template_payload")
	}
	raw := strings.TrimSpace(m.Metadata["runtime_template_payload"])
	if raw == "" {
		return model.RuntimeTemplate{}, errors.New("缺少 runtime_template_payload")
	}
	var tpl model.RuntimeTemplate
	if err := json.Unmarshal([]byte(raw), &tpl); err != nil {
		return model.RuntimeTemplate{}, fmt.Errorf("runtime_template_payload JSON 解析失败: %w", err)
	}
	if tpl.Image == "" {
		return model.RuntimeTemplate{}, errors.New("模板 image 为空")
	}
	return tpl, nil
}

func materializeRuntimeTemplate(tpl model.RuntimeTemplate, m model.Model) model.RuntimeTemplate {
	vars := runtimeTemplateVars(m)

	out := tpl
	out.Image = replaceTemplateVars(out.Image, vars)
	out.Command = replaceTemplateVarList(out.Command, vars)
	out.Volumes = normalizeVolumeBindings(replaceTemplateVarList(out.Volumes, vars))
	out.Ports = replaceTemplateVarList(out.Ports, vars)
	if len(out.Env) > 0 {
		env := make(map[string]string, len(out.Env))
		for k, v := range out.Env {
			env[k] = replaceTemplateVars(v, vars)
		}
		out.Env = env
	}
	return out
}

func runtimeTemplateVars(m model.Model) map[string]string {
	modelPath := strings.TrimSpace(readMetadata(m.Metadata, "path"))
	modelBase := strings.TrimSpace(filepath.Base(modelPath))
	if modelBase == "." || modelBase == "/" {
		modelBase = ""
	}
	if modelBase == "" {
		modelBase = strings.TrimSpace(m.ID)
	}
	if modelBase == "" {
		modelBase = strings.TrimSpace(m.Name)
	}
	if modelBase == "" {
		modelBase = "model"
	}

	modelPathInContainer := path.Join("/models", modelBase)
	return map[string]string{
		"{{MODEL_ID}}":             strings.TrimSpace(m.ID),
		"{{MODEL_NAME}}":           strings.TrimSpace(m.Name),
		"{{MODEL_PATH}}":           modelPath,
		"{{MODEL_BASENAME}}":       modelBase,
		"{{MODEL_PATH_CONTAINER}}": modelPathInContainer,
	}
}

func replaceTemplateVarList(items []string, vars map[string]string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, replaceTemplateVars(item, vars))
	}
	return out
}

func normalizeVolumeBindings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, normalizeVolumeBinding(item))
	}
	return out
}

func normalizeVolumeBinding(binding string) string {
	parts := strings.Split(binding, ":")
	if len(parts) < 2 {
		return binding
	}

	mode := ""
	if last := strings.TrimSpace(parts[len(parts)-1]); last == "ro" || last == "rw" {
		mode = last
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return binding
	}

	containerPath := strings.TrimSpace(parts[len(parts)-1])
	hostPath := strings.TrimSpace(strings.Join(parts[:len(parts)-1], ":"))
	if hostPath == "" || containerPath == "" {
		return binding
	}
	if !filepath.IsAbs(hostPath) {
		if abs, err := filepath.Abs(hostPath); err == nil {
			hostPath = abs
		}
	}

	if mode != "" {
		return hostPath + ":" + containerPath + ":" + mode
	}
	return hostPath + ":" + containerPath
}

func replaceTemplateVars(input string, vars map[string]string) string {
	out := input
	for key, value := range vars {
		out = strings.ReplaceAll(out, key, value)
	}
	return out
}

func result(success bool, message string, detail map[string]interface{}) model.ActionResult {
	return model.ActionResult{
		Success:   success,
		Message:   message,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}

type dockerHTTPClient struct {
	baseURL        string
	apiPrefix      string
	httpClient     *http.Client
	defaultHeaders map[string]string

	selfMountOnce sync.Once
	selfMounts    []containerMount
}

type containerMount struct {
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

func newDockerHTTPClient(adapterName, endpoint, token string, timeout time.Duration) (*dockerHTTPClient, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("endpoint 不能为空")
	}

	adapterName = strings.ToLower(strings.TrimSpace(adapterName))
	endpoint = strings.TrimSpace(endpoint)

	if strings.HasPrefix(endpoint, "unix://") {
		socketPath := strings.TrimPrefix(endpoint, "unix://")
		if socketPath == "" {
			return nil, fmt.Errorf("unix endpoint 缺少 socket path")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}
		return &dockerHTTPClient{
			baseURL:    "http://docker",
			httpClient: &http.Client{Transport: transport, Timeout: timeout},
		}, nil
	}

	if strings.HasPrefix(endpoint, "tcp://") {
		endpoint = "http://" + strings.TrimPrefix(endpoint, "tcp://")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("解析 endpoint 失败: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	if u.Host == "" {
		return nil, fmt.Errorf("endpoint 缺少 host")
	}

	client := &dockerHTTPClient{
		baseURL:        fmt.Sprintf("%s://%s", u.Scheme, u.Host),
		apiPrefix:      strings.TrimSuffix(strings.TrimSpace(u.Path), "/"),
		httpClient:     &http.Client{Timeout: timeout},
		defaultHeaders: map[string]string{},
	}

	if token != "" {
		client.defaultHeaders["Authorization"] = "Bearer " + token
		client.defaultHeaders["X-API-Key"] = token
	}

	shouldPortainer := adapterName == "portainer" || strings.Contains(strings.ToLower(u.Host), "portainer")
	if shouldPortainer {
		if !strings.Contains(client.apiPrefix, "/api/endpoints/") || !strings.Contains(client.apiPrefix, "/docker") {
			if token == "" {
				return nil, fmt.Errorf("portainer endpoint 需提供 token，或直接使用 /api/endpoints/{id}/docker 代理路径")
			}
			proxyPath, discoverErr := discoverPortainerProxyPath(context.Background(), client.baseURL, token, timeout)
			if discoverErr != nil {
				return nil, discoverErr
			}
			client.apiPrefix = proxyPath
		}
	}

	return client, nil
}

func discoverPortainerProxyPath(ctx context.Context, baseURL, token string, timeout time.Duration) (string, error) {
	httpClient := &http.Client{Timeout: timeout}
	endpointsURL := strings.TrimSuffix(baseURL, "/") + "/api/endpoints"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpointsURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-API-Key", token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询 portainer endpoints 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("查询 portainer endpoints 失败: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(body, &items); err != nil {
		return "", fmt.Errorf("解析 portainer endpoints 响应失败: %w", err)
	}
	if len(items) == 0 {
		return "", fmt.Errorf("portainer 中无可用 endpoint")
	}
	idValue := items[0]["Id"]
	id := toIntString(idValue)
	if id == "" {
		id = toIntString(items[0]["ID"])
	}
	if id == "" {
		return "", fmt.Errorf("portainer endpoint id 解析失败")
	}
	return "/api/endpoints/" + id + "/docker", nil
}

func (c *dockerHTTPClient) ping(ctx context.Context) error {
	status, body, err := c.request(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("ping failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *dockerHTTPClient) ensureContainer(ctx context.Context, m model.Model, tpl model.RuntimeTemplate) (string, bool, error) {
	if id := strings.TrimSpace(m.Metadata["runtime_container_id"]); id != "" {
		if info, err := c.inspectContainer(ctx, id); err == nil {
			if !isContainerOwnedByModel(info, m) {
				return "", false, fmt.Errorf("容器 %s 非当前模型受管容器，拒绝接管", id)
			}
			recreate, checkErr := c.containerNeedsRecreate(ctx, id, m, tpl)
			if checkErr != nil {
				return "", false, checkErr
			}
			if !recreate {
				return id, false, nil
			}
			_ = c.stopContainer(ctx, id, 5)
			_ = c.removeContainer(ctx, id, true)
		}
	}

	containerName := containerNameForModel(m)
	list, err := c.listContainers(ctx, true, map[string][]string{
		"name": {"^/" + containerName + "$"},
	})
	if err != nil {
		return "", false, err
	}
	if len(list) > 0 {
		existingID := list[0].ID
		info, inspectErr := c.inspectContainer(ctx, existingID)
		if inspectErr != nil {
			return "", false, inspectErr
		}
		if !isModelIntegratorManagedContainer(info) {
			return "", false, fmt.Errorf("存在同名非受管容器 (%s)，拒绝覆盖", existingID)
		}
		if !isContainerOwnedByModel(info, m) {
			return "", false, fmt.Errorf("容器名称冲突：%s 属于其他模型 (%s)", existingID, strings.TrimSpace(info.Config.Labels["com.modelintegrator.model_id"]))
		}
		recreate, checkErr := c.containerNeedsRecreate(ctx, existingID, m, tpl)
		if checkErr != nil {
			return "", false, checkErr
		}
		if !recreate {
			return existingID, false, nil
		}
		_ = c.stopContainer(ctx, existingID, 5)
		_ = c.removeContainer(ctx, existingID, true)
	}

	id, err := c.createContainer(ctx, m, tpl, containerName)
	if err != nil {
		if strings.Contains(err.Error(), "No such image") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			if pullErr := c.pullImage(ctx, tpl.Image); pullErr == nil {
				id, err = c.createContainer(ctx, m, tpl, containerName)
			}
		}
		if err != nil {
			return "", false, err
		}
	}
	return id, true, nil
}

func (c *dockerHTTPClient) containerNeedsRecreate(ctx context.Context, containerID string, m model.Model, tpl model.RuntimeTemplate) (bool, error) {
	info, err := c.inspectContainer(ctx, containerID)
	if err != nil {
		return true, nil
	}

	expectedTemplateID := firstNonEmpty(strings.TrimSpace(tpl.ID), strings.TrimSpace(m.Metadata["runtime_template_id"]))
	actualTemplateID := strings.TrimSpace(info.Config.Labels["com.modelintegrator.template_id"])
	if expectedTemplateID != "" && actualTemplateID != "" && expectedTemplateID != actualTemplateID {
		return true, nil
	}
	if strings.TrimSpace(info.Config.Image) != strings.TrimSpace(tpl.Image) {
		return true, nil
	}
	if !stringSlicesEqual(info.Config.Cmd, tpl.Command) {
		return true, nil
	}

	expectedBinds := c.normalizeBindsForDockerHost(ctx, tpl.Volumes)
	if !stringSlicesAsSetEqual(info.HostConfig.Binds, expectedBinds) {
		return true, nil
	}
	return false, nil
}

func (c *dockerHTTPClient) resolveContainerID(ctx context.Context, m model.Model) (string, bool, error) {
	if m.Metadata != nil {
		if id := strings.TrimSpace(m.Metadata["runtime_container_id"]); id != "" {
			if info, err := c.inspectContainer(ctx, id); err == nil {
				if isContainerOwnedByModel(info, m) {
					return id, true, nil
				}
				return "", false, nil
			}
		}
	}
	containerName := containerNameForModel(m)
	list, err := c.listContainers(ctx, true, map[string][]string{
		"name": {"^/" + containerName + "$"},
	})
	if err != nil {
		return "", false, err
	}
	if len(list) == 0 {
		return "", false, nil
	}
	info, inspectErr := c.inspectContainer(ctx, list[0].ID)
	if inspectErr != nil {
		return "", false, nil
	}
	if !isContainerOwnedByModel(info, m) {
		return "", false, nil
	}
	return list[0].ID, true, nil
}

func isModelIntegratorManagedContainer(info containerInspect) bool {
	labels := info.Config.Labels
	if labels == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(labels["com.modelintegrator.managed"]), "true")
}

func isContainerOwnedByModel(info containerInspect, m model.Model) bool {
	if !isModelIntegratorManagedContainer(info) {
		return false
	}
	labels := info.Config.Labels
	if labels == nil {
		return false
	}
	modelID := strings.TrimSpace(labels["com.modelintegrator.model_id"])
	expectedID := strings.TrimSpace(m.ID)
	return modelID != "" && expectedID != "" && modelID == expectedID
}

type containerListItem struct {
	ID     string            `json:"Id"`
	Image  string            `json:"Image"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

func (c *dockerHTTPClient) listContainers(ctx context.Context, all bool, filters map[string][]string) ([]containerListItem, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	if len(filters) > 0 {
		raw, _ := json.Marshal(filters)
		q.Set("filters", string(raw))
	}
	status, body, err := c.request(ctx, http.MethodGet, "/containers/json", q, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("list containers failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	var items []containerListItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	return items, nil
}

type containerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Cmd    []string          `json:"Cmd"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Binds []string `json:"Binds"`
	} `json:"HostConfig"`
	State *struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
}

func (c *dockerHTTPClient) inspectContainer(ctx context.Context, containerID string) (containerInspect, error) {
	status, body, err := c.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(containerID)+"/json", nil, nil)
	if err != nil {
		return containerInspect{}, err
	}
	if status == http.StatusNotFound {
		return containerInspect{}, fmt.Errorf("container %s not found", containerID)
	}
	if status < 200 || status >= 300 {
		return containerInspect{}, fmt.Errorf("inspect container failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	var out containerInspect
	if err := json.Unmarshal(body, &out); err != nil {
		return containerInspect{}, err
	}
	return out, nil
}

type portBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort,omitempty"`
}

type deviceRequest struct {
	Driver       string     `json:"Driver,omitempty"`
	Count        int        `json:"Count,omitempty"`
	Capabilities [][]string `json:"Capabilities,omitempty"`
}

type hostConfig struct {
	Binds          []string                 `json:"Binds,omitempty"`
	PortBindings   map[string][]portBinding `json:"PortBindings,omitempty"`
	DeviceRequests []deviceRequest          `json:"DeviceRequests,omitempty"`
	IpcMode        string                   `json:"IpcMode,omitempty"`
}

type containerCreatePayload struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   hostConfig          `json:"HostConfig,omitempty"`
}

type containerCreateResp struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings,omitempty"`
}

func (c *dockerHTTPClient) createContainer(ctx context.Context, m model.Model, tpl model.RuntimeTemplate, containerName string) (string, error) {
	envItems := make([]string, 0, len(tpl.Env))
	for k, v := range tpl.Env {
		envItems = append(envItems, fmt.Sprintf("%s=%s", k, v))
	}

	portBindings := map[string][]portBinding{}
	exposedPorts := map[string]struct{}{}
	for _, item := range tpl.Ports {
		containerPortKey, binding, err := parsePortMapping(item)
		if err != nil {
			return "", err
		}
		exposedPorts[containerPortKey] = struct{}{}
		portBindings[containerPortKey] = append(portBindings[containerPortKey], binding)
	}

	payload := containerCreatePayload{
		Image: tpl.Image,
		Cmd:   tpl.Command,
		Env:   envItems,
		Labels: map[string]string{
			"com.modelintegrator.managed":       "true",
			"com.modelintegrator.model_id":      m.ID,
			"com.modelintegrator.model_name":    m.Name,
			"com.modelintegrator.runtime_id":    m.RuntimeID,
			"com.modelintegrator.template_id":   firstNonEmpty(tpl.ID, m.Metadata["runtime_template_id"]),
			"com.modelintegrator.template_name": tpl.Name,
		},
		ExposedPorts: exposedPorts,
		HostConfig: hostConfig{
			Binds:        c.normalizeBindsForDockerHost(ctx, tpl.Volumes),
			PortBindings: portBindings,
		},
	}

	if len(payload.ExposedPorts) == 0 {
		payload.ExposedPorts = nil
	}
	if len(payload.HostConfig.PortBindings) == 0 {
		payload.HostConfig.PortBindings = nil
	}
	if tpl.NeedsGPU {
		payload.HostConfig.IpcMode = "host"
		payload.HostConfig.DeviceRequests = []deviceRequest{
			{
				Driver:       "nvidia",
				Count:        -1,
				Capabilities: [][]string{{"gpu"}},
			},
		}
	}

	q := url.Values{}
	q.Set("name", containerName)
	status, body, err := c.request(ctx, http.MethodPost, "/containers/create", q, payload)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("create container failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	var resp containerCreateResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.ID == "" {
		return "", fmt.Errorf("create container response missing id")
	}
	return resp.ID, nil
}

func (c *dockerHTTPClient) normalizeBindsForDockerHost(ctx context.Context, binds []string) []string {
	if len(binds) == 0 {
		return nil
	}
	out := make([]string, 0, len(binds))
	for _, bind := range binds {
		out = append(out, c.normalizeBindForDockerHost(ctx, bind))
	}
	return out
}

func (c *dockerHTTPClient) normalizeBindForDockerHost(ctx context.Context, bind string) string {
	hostPath, containerPath, mode, ok := splitVolumeBinding(bind)
	if !ok {
		return bind
	}
	hostPath = translateHostPathForDockerDaemon(ctx, c, hostPath)
	return joinVolumeBinding(hostPath, containerPath, mode)
}

func translateHostPathForDockerDaemon(ctx context.Context, c *dockerHTTPClient, hostPath string) string {
	normalized := strings.TrimSpace(hostPath)
	if normalized == "" {
		return hostPath
	}
	if !filepath.IsAbs(normalized) {
		if abs, err := filepath.Abs(normalized); err == nil {
			normalized = abs
		}
	}
	if c == nil {
		return normalized
	}
	translated, ok := c.translateContainerPathToHost(ctx, normalized)
	if !ok {
		return normalized
	}
	return translated
}

func (c *dockerHTTPClient) translateContainerPathToHost(ctx context.Context, containerPath string) (string, bool) {
	mounts := c.loadSelfMounts(ctx)
	if len(mounts) == 0 {
		return "", false
	}

	in := filepath.Clean(strings.TrimSpace(containerPath))
	bestDest := ""
	bestSource := ""
	for _, m := range mounts {
		dest := filepath.Clean(strings.TrimSpace(m.Destination))
		source := filepath.Clean(strings.TrimSpace(m.Source))
		if dest == "" || source == "" {
			continue
		}
		if in != dest && !strings.HasPrefix(in, dest+string(filepath.Separator)) {
			continue
		}
		if len(dest) > len(bestDest) {
			bestDest = dest
			bestSource = source
		}
	}
	if bestDest == "" || bestSource == "" {
		return "", false
	}

	rel := strings.TrimPrefix(in, bestDest)
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	if rel == "" {
		return bestSource, true
	}
	return filepath.Join(bestSource, rel), true
}

func (c *dockerHTTPClient) loadSelfMounts(ctx context.Context) []containerMount {
	c.selfMountOnce.Do(func() {
		containerID := strings.TrimSpace(selfContainerID())
		if containerID == "" {
			return
		}
		status, body, err := c.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(containerID)+"/json", nil, nil)
		if err != nil || status < 200 || status >= 300 {
			return
		}
		var inspect struct {
			Mounts []containerMount `json:"Mounts"`
		}
		if err := json.Unmarshal(body, &inspect); err != nil {
			return
		}
		c.selfMounts = inspect.Mounts
	})
	return c.selfMounts
}

func selfContainerID() string {
	if v := strings.TrimSpace(os.Getenv("HOSTNAME")); v != "" {
		return v
	}
	v, _ := os.Hostname()
	return strings.TrimSpace(v)
}

func splitVolumeBinding(binding string) (hostPath, containerPath, mode string, ok bool) {
	parts := strings.Split(binding, ":")
	if len(parts) < 2 {
		return "", "", "", false
	}

	if last := strings.TrimSpace(parts[len(parts)-1]); last == "ro" || last == "rw" {
		mode = last
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return "", "", "", false
	}

	containerPath = strings.TrimSpace(parts[len(parts)-1])
	hostPath = strings.TrimSpace(strings.Join(parts[:len(parts)-1], ":"))
	if hostPath == "" || containerPath == "" {
		return "", "", "", false
	}
	return hostPath, containerPath, mode, true
}

func joinVolumeBinding(hostPath, containerPath, mode string) string {
	if strings.TrimSpace(mode) == "" {
		return strings.TrimSpace(hostPath) + ":" + strings.TrimSpace(containerPath)
	}
	return strings.TrimSpace(hostPath) + ":" + strings.TrimSpace(containerPath) + ":" + strings.TrimSpace(mode)
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func stringSlicesAsSetEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	countA := map[string]int{}
	countB := map[string]int{}
	for _, item := range a {
		countA[strings.TrimSpace(item)]++
	}
	for _, item := range b {
		countB[strings.TrimSpace(item)]++
	}
	if len(countA) != len(countB) {
		return false
	}
	for k, v := range countA {
		if countB[k] != v {
			return false
		}
	}
	return true
}

func (c *dockerHTTPClient) startContainer(ctx context.Context, containerID string) error {
	status, body, err := c.request(ctx, http.MethodPost, "/containers/"+url.PathEscape(containerID)+"/start", nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotModified || (status >= 200 && status < 300) {
		return nil
	}
	return fmt.Errorf("start container failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
}

func (c *dockerHTTPClient) waitUntilContainerRunning(ctx context.Context, containerID string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	lastStatus := "unknown"

	for {
		info, err := c.inspectContainer(ctx, containerID)
		if err != nil {
			return lastStatus, err
		}
		if info.State != nil {
			lastStatus = firstNonEmpty(info.State.Status, lastStatus)
			if info.State.Running {
				return lastStatus, nil
			}
			if lastStatus == "exited" || lastStatus == "dead" {
				return lastStatus, fmt.Errorf("容器状态=%s", lastStatus)
			}
		}
		if time.Now().After(deadline) {
			return lastStatus, fmt.Errorf("等待超时，当前状态=%s", lastStatus)
		}
		select {
		case <-ctx.Done():
			return lastStatus, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (c *dockerHTTPClient) stopContainer(ctx context.Context, containerID string, timeoutSeconds int) error {
	q := url.Values{}
	if timeoutSeconds > 0 {
		q.Set("t", strconv.Itoa(timeoutSeconds))
	}
	status, body, err := c.request(ctx, http.MethodPost, "/containers/"+url.PathEscape(containerID)+"/stop", q, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotModified || status == http.StatusNotFound || (status >= 200 && status < 300) {
		return nil
	}
	return fmt.Errorf("stop container failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
}

func (c *dockerHTTPClient) removeContainer(ctx context.Context, containerID string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	status, body, err := c.request(ctx, http.MethodDelete, "/containers/"+url.PathEscape(containerID), q, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || (status >= 200 && status < 300) {
		return nil
	}
	return fmt.Errorf("remove container failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
}

func (c *dockerHTTPClient) pullImage(ctx context.Context, image string) error {
	repo, tag := splitImageReference(image)
	q := url.Values{}
	q.Set("fromImage", repo)
	if tag != "" {
		q.Set("tag", tag)
	}
	status, body, err := c.request(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("pull image failed: status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func splitImageReference(image string) (repo string, tag string) {
	ref := strings.TrimSpace(image)
	if ref == "" {
		return "", ""
	}
	if strings.Contains(ref, "@") {
		return ref, ""
	}

	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref[:lastColon], ref[lastColon+1:]
	}
	return ref, ""
}

func (c *dockerHTTPClient) request(ctx context.Context, method, apiPath string, q url.Values, payload interface{}) (int, []byte, error) {
	target := strings.TrimSuffix(c.baseURL, "/")
	fullPath := joinURLPath(c.apiPrefix, apiPath)
	target += fullPath
	if len(q) > 0 {
		target += "?" + q.Encode()
	}

	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.defaultHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, nil
}

func joinURLPath(prefix, suffix string) string {
	left := strings.TrimSpace(prefix)
	right := strings.TrimSpace(suffix)
	if left == "" {
		if strings.HasPrefix(right, "/") {
			return right
		}
		return "/" + right
	}
	if !strings.HasPrefix(left, "/") {
		left = "/" + left
	}
	left = strings.TrimSuffix(left, "/")
	if !strings.HasPrefix(right, "/") {
		right = "/" + right
	}
	return path.Clean(left + right)
}

func parsePortMapping(item string) (string, portBinding, error) {
	s := strings.TrimSpace(item)
	if s == "" {
		return "", portBinding{}, fmt.Errorf("port mapping 不能为空")
	}
	proto := "tcp"
	main := s
	if strings.Contains(s, "/") {
		parts := strings.Split(s, "/")
		if len(parts) != 2 {
			return "", portBinding{}, fmt.Errorf("port mapping 协议格式非法: %s", s)
		}
		main = strings.TrimSpace(parts[0])
		proto = strings.ToLower(strings.TrimSpace(parts[1]))
		if proto != "tcp" && proto != "udp" {
			return "", portBinding{}, fmt.Errorf("port mapping 协议仅支持 tcp/udp: %s", s)
		}
	}
	parts := strings.Split(main, ":")
	if len(parts) != 2 {
		return "", portBinding{}, fmt.Errorf("port mapping 格式应为 host:container[/proto]: %s", s)
	}
	hostPort := strings.TrimSpace(parts[0])
	containerPort := strings.TrimSpace(parts[1])
	if !isValidPort(hostPort) || !isValidPort(containerPort) {
		return "", portBinding{}, fmt.Errorf("port mapping 端口非法: %s", s)
	}
	key := containerPort + "/" + proto
	return key, portBinding{HostPort: hostPort}, nil
}

func isValidPort(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n > 0 && n <= 65535
}

var nonAlphaNumRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func containerNameForModel(m model.Model) string {
	base := strings.TrimSpace(m.ID)
	if base == "" {
		base = strings.TrimSpace(m.Name)
	}
	if base == "" {
		base = "model"
	}
	base = strings.ToLower(base)
	base = nonAlphaNumRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "model"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	return "mcp-model-" + base
}

func firstContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func readMetadata(metadata map[string]string, key string) string {
	if metadata == nil {
		return ""
	}
	return metadata[key]
}

func toIntString(v interface{}) string {
	switch x := v.(type) {
	case float64:
		return strconv.Itoa(int(x))
	case float32:
		return strconv.Itoa(int(x))
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.Itoa(int(x))
	case int64:
		return strconv.Itoa(int(x))
	case string:
		return strings.TrimSpace(x)
	default:
		return ""
	}
}
