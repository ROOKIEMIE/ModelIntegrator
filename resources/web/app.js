const nodesEl = document.getElementById("nodes");
const modelsEl = document.getElementById("models");
const modelTabsEl = document.getElementById("model-tabs");
const pageTabsEl = document.getElementById("page-tabs");
const runtimePageEl = document.getElementById("runtime-page");
const downloadPageEl = document.getElementById("download-page");
const runtimeSubTabsEl = document.getElementById("runtime-sub-tabs");
const runtimeListViewEl = document.getElementById("runtime-list-view");
const runtimeTemplateViewEl = document.getElementById("runtime-template-view");
const runtimeTemplatesEl = document.getElementById("runtime-templates");
const runtimeTemplateResultEl = document.getElementById("runtime-template-result");
const runtimeTemplateFormEl = document.getElementById("runtime-template-form");
const templateValidateBtn = document.getElementById("tpl-validate-btn");
const templateRegisterBtn = document.getElementById("tpl-register-btn");
const tasksEl = document.getElementById("tasks");
const testRunsEl = document.getElementById("test-runs");
const runE5TestBtn = document.getElementById("run-e5-test-btn");
const refreshRuntimeTaskBtn = document.getElementById("refresh-runtime-task-btn");

const state = {
  nodes: [],
  models: [],
  runtimeTemplates: [],
  tasks: [],
  testRuns: [],
  activeNodeId: "",
  activePage: "runtime",
  activeRuntimeTab: "list",
  nodeActionLocks: {},
  apiToken: "",
};

function showToast(text) {
  let toast = document.getElementById("toast");
  if (!toast) {
    toast = document.createElement("div");
    toast.id = "toast";
    document.body.appendChild(toast);
  }
  toast.textContent = text;
  toast.style.display = "block";
  setTimeout(() => {
    toast.style.display = "none";
  }, 2500);
}

async function requestJSON(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.apiToken) {
    headers.set("Authorization", `Bearer ${state.apiToken}`);
  }

  const resp = await fetch(url, {
    ...options,
    headers,
  });

  let payload = {};
  try {
    payload = await resp.json();
  } catch (err) {
    payload = {};
  }

  if (!resp.ok || payload.success === false) {
    if (resp.status === 401) {
      throw new Error("未授权，请在 URL 添加 ?token=<api-token> 或设置 localStorage.lcp_api_token");
    }
    const msg = payload.message || `请求失败: ${resp.status}`;
    throw new Error(msg);
  }
  return payload.data;
}

async function waitTaskDone(taskId, timeoutMs = 120000) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const task = await requestJSON(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
    const status = String(task.status || "").toLowerCase();
    if (["success", "failed", "timeout", "canceled"].includes(status)) {
      if (status !== "success") {
        throw new Error(`任务失败: ${task.message || status}${task.error ? ` (${task.error})` : ""}`);
      }
      return task;
    }
    await new Promise((resolve) => setTimeout(resolve, 1200));
  }
  throw new Error("等待任务超时");
}

function resolveAPIToken() {
  const fromQuery = String(new URLSearchParams(window.location.search).get("token") || "").trim();
  if (fromQuery) {
    localStorage.setItem("lcp_api_token", fromQuery);
    // 兼容历史本地存储键。
    localStorage.setItem("mi_api_token", fromQuery);
    return fromQuery;
  }
  const token = String(localStorage.getItem("lcp_api_token") || "").trim();
  if (token) {
    return token;
  }
  return String(localStorage.getItem("mi_api_token") || "").trim();
}

function renderNodes() {
  if (!Array.isArray(state.nodes) || state.nodes.length === 0) {
    nodesEl.textContent = "暂无节点";
    return;
  }

  nodesEl.innerHTML = "";
  state.nodes.forEach((node) => {
    const nodeLoadedCount = countLoadedModelsByNode(node.id);
    const runtimeSummary = summarizeRuntimeLoad(node);
    const agentStatus = node.agent_status || (node.agent && node.agent.status) || "none";
    const heartbeatAt = (node.agent && node.agent.last_heartbeat_at) || node.last_seen_at;
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(node.name)} (${escapeHTML(node.id)})</div>
      <div class="meta">描述: ${escapeHTML(node.description || "-")}</div>
      <div class="meta">分类: ${escapeHTML(node.classification || "-")} | 状态: ${escapeHTML(node.status || "unknown")} | 类型: ${escapeHTML(node.type || "-")} | 主机: ${escapeHTML(node.host || "-")}</div>
      <div class="meta">能力分级: ${escapeHTML(node.capability_tier || "unknown")} | 能力来源: ${escapeHTML(node.capability_source || "unknown")} | 操作级别: ${escapeHTML(node.operation_level || "-")}</div>
      <div class="meta">Agent 状态: ${escapeHTML(agentStatus)} | 最近心跳: ${escapeHTML(formatTime(heartbeatAt))}</div>
      <div class="meta">能力说明: ${escapeHTML(node.capability_note || "-")}</div>
      <div class="meta">平台: ${(node.platform && node.platform.accelerator) || "unknown"} | GPU: ${(node.platform && node.platform.gpu) || "unknown"} | CUDA: ${(node.platform && node.platform.cuda_version) || "unknown"} | Driver: ${(node.platform && node.platform.driver) || "unknown"}</div>
      <div class="meta">Runtime 数量: ${(node.runtimes || []).length} | 已装载模型: ${nodeLoadedCount}</div>
      <div class="meta">Runtime 状态: ${runtimeSummary || "-"}</div>
      ${buildRuntimeCapabilityBlock(node)}
    `;
    nodesEl.appendChild(item);
  });
}

function buildRuntimeCapabilityBlock(node) {
  const runtimes = Array.isArray(node.runtimes) ? node.runtimes : [];
  if (runtimes.length === 0) {
    return `<div class="meta">Runtime 明细: -</div>`;
  }
  const parts = runtimes.map((rt) => {
    const capabilities = Array.isArray(rt.capabilities) && rt.capabilities.length > 0 ? rt.capabilities.join(", ") : "-";
    const actions = Array.isArray(rt.actions) && rt.actions.length > 0 ? rt.actions.join(", ") : "-";
    const lastSeen = rt.last_seen_at ? formatTime(rt.last_seen_at) : "-";
    return `<div class="runtime-capability-item">
      <div class="meta"><strong>${escapeHTML(rt.type || "unknown")}</strong> (${escapeHTML(rt.id || "-")}) | endpoint: ${escapeHTML(rt.endpoint || "-")}</div>
      <div class="meta">状态: ${escapeHTML(rt.status || "unknown")} | 最近可见: ${escapeHTML(lastSeen)}</div>
      <div class="meta">来源: ${escapeHTML(rt.capability_source || "-")} | 说明: ${escapeHTML(rt.capability_note || "-")}</div>
      <div class="meta">能力: ${escapeHTML(capabilities)}</div>
      <div class="meta">可操作: ${escapeHTML(actions)}</div>
    </div>`;
  });
  return `<div class="runtime-capability-list">${parts.join("")}</div>`;
}

function renderPageTabs() {
  if (!pageTabsEl) {
    return;
  }
  const buttons = pageTabsEl.querySelectorAll("[data-page]");
  buttons.forEach((btn) => {
    const pageName = String(btn.dataset.page || "").trim();
    btn.classList.toggle("active", pageName === state.activePage);
    btn.onclick = () => {
      state.activePage = pageName;
      renderPageTabs();
      renderActivePage();
    };
  });
}

function renderActivePage() {
  if (runtimePageEl) {
    runtimePageEl.classList.toggle("hidden", state.activePage !== "runtime");
  }
  if (downloadPageEl) {
    downloadPageEl.classList.toggle("hidden", state.activePage !== "download");
  }
  renderActiveRuntimeTab();
}

function renderRuntimeSubTabs() {
  if (!runtimeSubTabsEl) {
    return;
  }
  const buttons = runtimeSubTabsEl.querySelectorAll("[data-runtime-tab]");
  buttons.forEach((btn) => {
    const tabName = String(btn.dataset.runtimeTab || "").trim();
    btn.classList.toggle("active", tabName === state.activeRuntimeTab);
    btn.onclick = () => {
      state.activeRuntimeTab = tabName;
      renderRuntimeSubTabs();
      renderActiveRuntimeTab();
    };
  });
}

function renderActiveRuntimeTab() {
  const inRuntimePage = state.activePage === "runtime";
  if (runtimeListViewEl) {
    runtimeListViewEl.classList.toggle("hidden", !inRuntimePage || state.activeRuntimeTab !== "list");
  }
  if (runtimeTemplateViewEl) {
    runtimeTemplateViewEl.classList.toggle("hidden", !inRuntimePage || state.activeRuntimeTab !== "template");
  }
}

function renderRuntimeTemplates() {
  if (!runtimeTemplatesEl) {
    return;
  }
  if (!Array.isArray(state.runtimeTemplates) || state.runtimeTemplates.length === 0) {
    runtimeTemplatesEl.textContent = "暂无模板";
    return;
  }
  runtimeTemplatesEl.innerHTML = "";
  state.runtimeTemplates.forEach((tpl) => {
    const item = document.createElement("div");
    item.className = "template-item";

    const title = document.createElement("div");
    title.className = "item-title";
    title.textContent = `${tpl.name || tpl.id} (${tpl.id})`;

    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = `runtime: ${tpl.runtime_type || "-"} | image: ${tpl.image || "-"} | source: ${tpl.source || "unknown"} | GPU: ${tpl.needs_gpu ? "yes" : "no"}`;

    const command = Array.isArray(tpl.command) ? tpl.command.join(" ") : "";
    const preview = document.createElement("div");
    preview.className = "template-code";
    preview.textContent = command || "(no command)";

    item.appendChild(title);
    item.appendChild(meta);
    item.appendChild(preview);
    runtimeTemplatesEl.appendChild(item);
  });
}

function statusPill(status) {
  const value = String(status || "unknown").trim() || "unknown";
  const cls = value.toLowerCase().replace(/[^a-z0-9_]+/g, "_");
  return `<span class="status-pill status-${escapeHTML(cls)}">${escapeHTML(value)}</span>`;
}

function renderTasks() {
  if (!tasksEl) {
    return;
  }
  if (!Array.isArray(state.tasks) || state.tasks.length === 0) {
    tasksEl.textContent = "暂无任务";
    return;
  }
  tasksEl.innerHTML = "";
  state.tasks.slice(0, 10).forEach((task) => {
    const item = document.createElement("div");
    item.className = "list-item";
    const detail = task.detail && typeof task.detail === "object" ? JSON.stringify(task.detail) : "";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(task.type)} (${escapeHTML(task.id)})</div>
      <div class="meta">状态: ${statusPill(task.status)} | 进度: ${escapeHTML(String(task.progress || 0))}%</div>
      <div class="meta">目标: ${escapeHTML(task.target_type || "-")} / ${escapeHTML(task.target_id || "-")}</div>
      <div class="meta">执行者: ${escapeHTML(task.worker_id || task.assigned_agent_id || "-")} | 开始: ${escapeHTML(formatTime(task.started_at))} | 结束: ${escapeHTML(formatTime(task.finished_at))}</div>
      <div class="meta">消息: ${escapeHTML(task.message || "-")}</div>
      <div class="meta">错误: ${escapeHTML(task.error || "-")}</div>
      <div class="meta">明细: ${escapeHTML(detail || "-")}</div>
    `;
    tasksEl.appendChild(item);
  });
}

function renderTestRuns() {
  if (!testRunsEl) {
    return;
  }
  if (!Array.isArray(state.testRuns) || state.testRuns.length === 0) {
    testRunsEl.textContent = "暂无测试运行记录";
    return;
  }
  testRunsEl.innerHTML = "";
  state.testRuns.slice(0, 10).forEach((run) => {
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(run.scenario || "-")} (${escapeHTML(run.test_run_id || "-")})</div>
      <div class="meta">状态: ${statusPill(run.status)} | 触发人: ${escapeHTML(run.triggered_by || "-")}</div>
      <div class="meta">开始: ${escapeHTML(formatTime(run.started_at))} | 结束: ${escapeHTML(formatTime(run.finished_at))}</div>
      <div class="meta">摘要: ${escapeHTML(run.summary || "-")}</div>
      <div class="meta">日志: ${escapeHTML(run.log_path || "-")}</div>
      <div class="meta">错误: ${escapeHTML(run.error || "-")}</div>
    `;
    testRunsEl.appendChild(item);
  });
}

function renderModelTabs() {
  if (!Array.isArray(state.nodes) || state.nodes.length === 0) {
    modelTabsEl.textContent = "";
    return;
  }

  modelTabsEl.innerHTML = "";
  state.nodes.forEach((node) => {
    const button = document.createElement("button");
    button.className = "tab-btn";
    if (node.id === state.activeNodeId) {
      button.classList.add("active");
    }
    button.textContent = `${node.name} (${countLoadedModelsByNode(node.id)})`;
    button.addEventListener("click", () => {
      state.activeNodeId = node.id;
      renderModelTabs();
      renderModels();
    });
    modelTabsEl.appendChild(button);
  });
}

function setNodeLock(nodeId, locked) {
  state.nodeActionLocks[nodeId] = locked;
  const selector = `[data-node-id=\"${nodeId}\"]`;
  document.querySelectorAll(selector).forEach((btn) => {
    const allowed = btn.dataset.allowed !== "false";
    btn.disabled = locked || !allowed;
    btn.style.opacity = btn.disabled ? "0.5" : "1";
  });
}

function normalizeModelState(raw) {
  return String(raw || "").toLowerCase().trim();
}

function normalizeBackendType(raw) {
  return String(raw || "").toLowerCase().trim();
}

function displayModelState(rawState, backendType) {
  const stateValue = normalizeModelState(rawState) || "unknown";
  const backend = normalizeBackendType(backendType);
  if ((backend === "docker" || backend === "portainer") && stateValue === "stopped") {
    return "unload";
  }
  return stateValue;
}

function isLoadedState(raw) {
  const stateValue = normalizeModelState(raw);
  return stateValue === "loaded" || stateValue === "running" || stateValue === "busy";
}

function countLoadedModelsByNode(nodeId) {
  if (!Array.isArray(state.models)) {
    return 0;
  }
  return state.models.filter((m) => m.host_node_id === nodeId && isLoadedState(m.state)).length;
}

function countLoadedModelsByNodeAndBackend(nodeId, backendType) {
  if (!Array.isArray(state.models)) {
    return 0;
  }
  const normalizedBackend = normalizeBackendType(backendType);
  return state.models.filter((m) => {
    return m.host_node_id === nodeId && normalizeBackendType(m.backend_type) === normalizedBackend && isLoadedState(m.state);
  }).length;
}

function summarizeRuntimeLoad(node) {
  const runtimes = Array.isArray(node.runtimes) ? node.runtimes : [];
  const parts = [];
  runtimes.forEach((rt) => {
    const loaded = countLoadedModelsByNodeAndBackend(node.id, rt.type);
    const rtType = normalizeBackendType(rt.type) || "unknown";
    const enabled = rt.enabled === false ? "disabled" : "enabled";
    const status = rt.status || "unknown";
    parts.push(`${rtType}:${enabled}/${status}, loaded=${loaded}`);
  });
  return parts.join(" | ");
}

function actionsForModelBackend(backendType) {
  const normalized = normalizeBackendType(backendType);
  if (normalized === "lmstudio") {
    return ["load", "unload"];
  }
  return ["load", "unload", "start", "stop", "refresh"];
}

function actionAllowedByState(action, rawState) {
  const stateValue = normalizeModelState(rawState);
  if (action === "load" || action === "start") {
    return !isLoadedState(stateValue);
  }
  if (action === "unload" || action === "stop") {
    return isLoadedState(stateValue);
  }
  if (action === "refresh") {
    return true;
  }
  return true;
}

function actionAllowedByBackendAndState(action, backendType, rawState) {
  const backend = normalizeBackendType(backendType);
  const stateValue = normalizeModelState(rawState);

  if (backend === "lmstudio") {
    return actionAllowedByState(action, stateValue);
  }

  if (action === "load") {
    return stateValue === "stopped" || stateValue === "unknown" || stateValue === "error";
  }
  if (action === "unload") {
    return stateValue === "loaded" || stateValue === "running" || stateValue === "busy";
  }
  if (action === "start") {
    return stateValue === "loaded";
  }
  if (action === "stop") {
    return stateValue === "running" || stateValue === "busy";
  }
  if (action === "refresh") {
    return true;
  }
  return true;
}

function buildActionButton(modelId, action, nodeId, modelState, backendType) {
  const button = document.createElement("button");
  button.textContent = action.toUpperCase();
  button.dataset.nodeId = nodeId;
  const allowed = actionAllowedByBackendAndState(action, backendType, modelState);
  button.dataset.allowed = String(allowed);
  const locked = Boolean(state.nodeActionLocks[nodeId]);
  button.disabled = locked || !allowed;
  button.style.opacity = button.disabled ? "0.5" : "1";

  button.addEventListener("click", async () => {
    if (button.dataset.allowed === "false") {
      showToast(`${action} 当前状态下不可执行`);
      return;
    }
    if (state.nodeActionLocks[nodeId]) {
      showToast(`节点 ${nodeId} 正在执行其他操作，请稍后重试`);
      return;
    }
    setNodeLock(nodeId, true);
    try {
      let message = "ok";
      if (action === "start" || action === "stop" || action === "refresh") {
        const task = await requestJSON(`/api/v1/tasks/runtime/${action}`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            model_id: modelId,
            triggered_by: "web-console",
          }),
        });
        const finalTask = await waitTaskDone(task.id);
        message = `${action} task=${task.id} ${finalTask.status}`;
      } else {
        const data = await requestJSON(`/api/v1/models/${encodeURIComponent(modelId)}/${action}`, {
          method: "POST",
        });
        message = `${action} -> ${data.message || "ok"}`;
      }
      showToast(message);
      await loadModels({ refresh: true });
      await Promise.all([loadTasks(), loadTestRuns()]);
      renderNodes();
      renderModelTabs();
      renderModels();
      renderTasks();
      renderTestRuns();
    } catch (err) {
      showToast(`${action} 失败: ${err.message}`);
    } finally {
      setNodeLock(nodeId, false);
    }
  });
  return button;
}

function escapeHTML(raw) {
  return String(raw || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function formatTime(raw) {
  const text = String(raw || "").trim();
  if (!text) {
    return "-";
  }
  const ts = Date.parse(text);
  if (Number.isNaN(ts)) {
    return text;
  }
  return new Date(ts).toLocaleString();
}

function parseLineList(text) {
  return String(text || "")
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
}

function parseEnvMap(text) {
  const out = {};
  parseLineList(text).forEach((line) => {
    const index = line.indexOf("=");
    if (index <= 0) {
      return;
    }
    const key = line.slice(0, index).trim();
    const value = line.slice(index + 1).trim();
    if (key) {
      out[key] = value;
    }
  });
  return out;
}

function collectTemplatePayload() {
  return {
    id: document.getElementById("tpl-id")?.value.trim() || "",
    name: document.getElementById("tpl-name")?.value.trim() || "",
    runtime_type: document.getElementById("tpl-runtime-type")?.value.trim() || "docker",
    image: document.getElementById("tpl-image")?.value.trim() || "",
    description: document.getElementById("tpl-description")?.value.trim() || "",
    command: parseLineList(document.getElementById("tpl-command")?.value),
    volumes: parseLineList(document.getElementById("tpl-volumes")?.value),
    ports: parseLineList(document.getElementById("tpl-ports")?.value),
    env: parseEnvMap(document.getElementById("tpl-env")?.value),
    needs_gpu: Boolean(document.getElementById("tpl-needs-gpu")?.checked),
  };
}

function setTemplateResult(text) {
  if (!runtimeTemplateResultEl) {
    return;
  }
  runtimeTemplateResultEl.textContent = text;
}

async function validateTemplate() {
  const payload = collectTemplatePayload();
  const data = await requestJSON("/api/v1/runtime-templates/validate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const errors = Array.isArray(data.errors) ? data.errors : [];
  const warnings = Array.isArray(data.warnings) ? data.warnings : [];
  if (data.valid) {
    setTemplateResult(`校验通过${warnings.length > 0 ? `，warnings: ${warnings.join("; ")}` : ""}`);
  } else {
    setTemplateResult(`校验失败: ${errors.join("; ") || "unknown error"}`);
  }
  return data;
}

async function registerTemplate() {
  const payload = collectTemplatePayload();
  const data = await requestJSON("/api/v1/runtime-templates", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  setTemplateResult(`注册成功: ${data.normalized?.id || payload.id}`);
  await loadRuntimeTemplates();
  renderRuntimeTemplates();
}

function bindDownloadActions() {
  if (!runtimeTemplateFormEl) {
    return;
  }
  templateValidateBtn?.addEventListener("click", async () => {
    try {
      await validateTemplate();
    } catch (err) {
      setTemplateResult(`校验失败: ${err.message}`);
      showToast(`校验失败: ${err.message}`);
    }
  });
  templateRegisterBtn?.addEventListener("click", async () => {
    try {
      const validateResult = await validateTemplate();
      if (!validateResult.valid) {
        showToast("模板校验未通过，无法注册");
        return;
      }
      await registerTemplate();
      showToast("模板注册成功");
    } catch (err) {
      setTemplateResult(`注册失败: ${err.message}`);
      showToast(`注册失败: ${err.message}`);
    }
  });
}

function renderModels() {
  if (!Array.isArray(state.models) || state.models.length === 0) {
    modelsEl.textContent = "暂无模型";
    return;
  }

  const visibleModels = state.models.filter((m) => m.host_node_id === state.activeNodeId);
  if (visibleModels.length === 0) {
    modelsEl.textContent = "当前节点暂无模型";
    return;
  }

  modelsEl.innerHTML = "";
  visibleModels.forEach((m) => {
    const item = document.createElement("div");
    item.className = "list-item";

    const title = document.createElement("div");
    title.className = "item-title";
    title.textContent = `${m.name} (${m.id})`;

    const meta = document.createElement("div");
    meta.className = "meta";
    const templateID = m.metadata && m.metadata.runtime_template_id ? m.metadata.runtime_template_id : "-";
    const metaParts = [
      `后端: ${m.backend_type}`,
      `节点: ${m.host_node_id}`,
      `状态: ${displayModelState(m.state, m.backend_type)}`,
      `期望: ${m.desired_state || "-"}`,
      `实际: ${m.observed_state || "-"}`,
      `Readiness: ${m.readiness || "unknown"}`,
      `装载: ${isLoadedState(m.state) ? "已装载" : "未装载"}`,
    ];
    if (normalizeBackendType(m.backend_type) !== "lmstudio") {
      metaParts.push(`模板: ${templateID}`);
    }
    meta.textContent = metaParts.join(" | ");
    const health = document.createElement("div");
    health.className = "meta";
    health.innerHTML = `健康信息: ${statusPill(m.readiness || "unknown")} ${escapeHTML(m.health_message || "-")} | endpoint: ${escapeHTML(m.endpoint || "-")} | 最近协调: ${escapeHTML(formatTime(m.last_reconciled_at))}`;

    const actions = document.createElement("div");
    actions.className = "actions";
    actionsForModelBackend(m.backend_type).forEach((action) => {
      actions.appendChild(buildActionButton(m.id, action, m.host_node_id || "unknown", m.state, m.backend_type));
    });

    item.appendChild(title);
    item.appendChild(meta);
    item.appendChild(health);
    item.appendChild(actions);
    modelsEl.appendChild(item);
  });
}

async function loadNodes() {
  const nodes = await requestJSON("/api/v1/nodes");
  state.nodes = Array.isArray(nodes) ? nodes : [];
  if (!state.activeNodeId && state.nodes.length > 0) {
    state.activeNodeId = state.nodes[0].id;
  }
}

async function loadModels(options = {}) {
  const refresh = Boolean(options.refresh);
  const path = refresh ? "/api/v1/models?refresh=true" : "/api/v1/models";
  const models = await requestJSON(path);
  state.models = Array.isArray(models) ? models : [];
}

async function loadRuntimeTemplates() {
  const templates = await requestJSON("/api/v1/runtime-templates");
  state.runtimeTemplates = Array.isArray(templates) ? templates : [];
}

async function loadTasks() {
  const tasks = await requestJSON("/api/v1/tasks?limit=20");
  state.tasks = Array.isArray(tasks) ? tasks : [];
}

async function loadTestRuns() {
  const runs = await requestJSON("/api/v1/test-runs?limit=20");
  state.testRuns = Array.isArray(runs) ? runs : [];
}

function bindTestActions() {
  runE5TestBtn?.addEventListener("click", async () => {
    try {
      const run = await requestJSON("/api/v1/test-runs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          scenario: "e5_embedding_smoke",
          triggered_by: "web-console",
        }),
      });
      showToast(`已创建测试运行: ${run.test_run_id}`);
      await Promise.all([loadTasks(), loadTestRuns()]);
      renderTasks();
      renderTestRuns();
    } catch (err) {
      showToast(`创建测试运行失败: ${err.message}`);
    }
  });

  refreshRuntimeTaskBtn?.addEventListener("click", async () => {
    try {
      const model = Array.isArray(state.models) ? state.models.find((item) => item.id === "local-multilingual-e5-base") || state.models[0] : null;
      if (!model || !model.id) {
        throw new Error("未找到可刷新的模型");
      }
      const task = await requestJSON("/api/v1/tasks/runtime/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          model_id: model.id,
          triggered_by: "web-console",
        }),
      });
      await waitTaskDone(task.id, 90000);
      await Promise.all([loadModels({ refresh: true }), loadTasks()]);
      renderModels();
      renderTasks();
      showToast(`刷新完成: ${task.id}`);
    } catch (err) {
      showToast(`刷新失败: ${err.message}`);
    }
  });
}

(async function init() {
  try {
    state.apiToken = resolveAPIToken();

    let runtimeTemplatesLoadFailed = false;
    await Promise.all([loadNodes(), loadModels(), loadTasks(), loadTestRuns()]);
    try {
      await loadRuntimeTemplates();
    } catch (err) {
      runtimeTemplatesLoadFailed = true;
      state.runtimeTemplates = [];
      if (runtimeTemplatesEl) {
        runtimeTemplatesEl.textContent = `模板加载失败: ${err.message}`;
      }
    }
    bindDownloadActions();
    bindTestActions();
    renderPageTabs();
    renderRuntimeSubTabs();
    renderActivePage();
    renderNodes();
    renderModelTabs();
    renderModels();
    renderTasks();
    renderTestRuns();
    if (!runtimeTemplatesLoadFailed) {
      renderRuntimeTemplates();
    }

    setInterval(async () => {
      try {
        await Promise.all([loadTasks(), loadTestRuns()]);
        renderTasks();
        renderTestRuns();
      } catch (err) {
        // no-op
      }
    }, 5000);
  } catch (err) {
    nodesEl.textContent = `加载失败: ${err.message}`;
    modelsEl.textContent = `加载失败: ${err.message}`;
    if (tasksEl) {
      tasksEl.textContent = `加载失败: ${err.message}`;
    }
    if (testRunsEl) {
      testRunsEl.textContent = `加载失败: ${err.message}`;
    }
    if (runtimeTemplatesEl) {
      runtimeTemplatesEl.textContent = `加载失败: ${err.message}`;
    }
  }
})();
