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
const runtimeBindingsEl = document.getElementById("runtime-bindings");
const runtimeInstancesEl = document.getElementById("runtime-instances");
const tasksEl = document.getElementById("tasks");
const testRunsEl = document.getElementById("test-runs");
const runE5TestBtn = document.getElementById("run-e5-test-btn");
const refreshRuntimeTaskBtn = document.getElementById("refresh-runtime-task-btn");

const state = {
  nodes: [],
  models: [],
  runtimeTemplates: [],
  runtimeBindings: [],
  runtimeInstances: [],
  tasks: [],
  testRuns: [],
  activeNodeId: "",
  activePage: "runtime",
  activeRuntimeTab: "list",
  nodeActionLocks: {},
  apiToken: "",
  nodeIdAliases: {},
};

function normalizeNodeRole(rawRole, metadata = {}) {
  const role = String(rawRole || "").trim().toLowerCase();
  if (role === "controller" || role === "managed") {
    return role;
  }
  if (String(metadata.controller_node || "").toLowerCase() === "true") {
    return "controller";
  }
  return "managed";
}

function normalizeNodeSuffix(rawID) {
  const id = String(rawID || "").trim();
  if (!id) {
    return "local";
  }
  const plain = id.startsWith("node-") ? id.slice("node-".length) : id;
  const suffix = plain
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  if (!suffix || suffix === "controller" || suffix === "managed") {
    return "local";
  }
  if (suffix === "sub") {
    return "local";
  }
  if (suffix.startsWith("sub-")) {
    return suffix.slice("sub-".length) || "local";
  }
  if (suffix.startsWith("managed-")) {
    return suffix.slice("managed-".length) || "local";
  }
  if (suffix.startsWith("controller-")) {
    return suffix.slice("controller-".length) || "local";
  }
  return suffix;
}

function normalizeNodeID(rawID, normalizedRole) {
  if (normalizedRole === "controller") {
    return "node-controller";
  }
  const id = String(rawID || "").trim();
  if (id.startsWith("node-managed-")) {
    return id;
  }
  const suffix = normalizeNodeSuffix(id);
  return `node-managed-${suffix}`;
}

function normalizeNodeReference(rawID) {
  const id = String(rawID || "").trim();
  if (!id) {
    return "";
  }
  if (state.nodeIdAliases[id]) {
    return state.nodeIdAliases[id];
  }
  if (id === "node-controller" || id.startsWith("node-managed-")) {
    return id;
  }
  return id;
}

function normalizeNodeText(rawText) {
  const text = String(rawText || "");
  if (!text) {
    return "";
  }
  let normalized = text;
  Object.keys(state.nodeIdAliases).forEach((legacyID) => {
    const canonicalID = state.nodeIdAliases[legacyID];
    if (!legacyID || legacyID === canonicalID) {
      return;
    }
    normalized = normalized.split(legacyID).join(canonicalID);
  });
  return normalized;
}

function normalizeNodeName(rawName, normalizedID, normalizedRole) {
  const name = normalizeNodeText(rawName).trim();
  if (normalizedRole === "controller" || normalizedID === "node-controller") {
    return "Controller Node";
  }
  if (normalizedID.startsWith("node-managed-")) {
    const suffix = normalizedID.slice("node-managed-".length);
    return suffix ? `Managed Node ${suffix}` : "Managed Node";
  }
  return normalizeNodeText(name) || normalizedID || "-";
}

function normalizeNodeRecord(node) {
  if (!node || typeof node !== "object") {
    return node;
  }
  const metadata = node.metadata && typeof node.metadata === "object" ? node.metadata : {};
  const rawID = String(node.id || "").trim();
  const normalizedRole = normalizeNodeRole(node.role, metadata);
  const normalizedID = normalizeNodeID(rawID, normalizedRole);
  return {
    ...node,
    __raw_id: rawID,
    id: normalizedID,
    role: normalizedRole,
    name: normalizeNodeName(node.name, normalizedID, normalizedRole),
  };
}

function nodeRecordScore(node) {
  if (!node || typeof node !== "object") {
    return 0;
  }
  let score = 0;
  if (String(node.__raw_id || "") === String(node.id || "")) {
    score += 20;
  }
  if (String(node.status || "").trim() && String(node.status || "").toLowerCase() !== "unknown") {
    score += 5;
  }
  if (String(node.agent_status || "").trim() && String(node.agent_status || "").toLowerCase() !== "none") {
    score += 5;
  }
  if (String(node.host || "").trim()) {
    score += 4;
  }
  if (String(node.name || "").trim()) {
    score += 3;
  }
  if (Array.isArray(node.runtimes)) {
    score += Math.min(node.runtimes.length, 5);
  }
  if (String(node.last_seen_at || "").trim()) {
    score += 2;
  }
  return score;
}

function mergeNodeRecords(current, candidate) {
  const currentScore = nodeRecordScore(current);
  const candidateScore = nodeRecordScore(candidate);
  const preferred = candidateScore >= currentScore ? candidate : current;
  const secondary = preferred === candidate ? current : candidate;

  const preferredMetadata = preferred.metadata && typeof preferred.metadata === "object" ? preferred.metadata : {};
  const secondaryMetadata = secondary.metadata && typeof secondary.metadata === "object" ? secondary.metadata : {};

  return {
    ...secondary,
    ...preferred,
    metadata: {
      ...secondaryMetadata,
      ...preferredMetadata,
    },
    platform: preferred.platform || secondary.platform,
    agent: preferred.agent || secondary.agent,
    runtimes: Array.isArray(preferred.runtimes) && preferred.runtimes.length > 0 ? preferred.runtimes : secondary.runtimes,
  };
}

function buildNodeIdentityKey(node) {
  if (!node || typeof node !== "object") {
    return "";
  }
  const metadata = node.metadata && typeof node.metadata === "object" ? node.metadata : {};
  const host = String(node.host || metadata.hostname || metadata.host || "").trim().toLowerCase();
  if (!host) {
    return "";
  }
  return host;
}

function normalizeModelRecord(model) {
  if (!model || typeof model !== "object") {
    return model;
  }
  return {
    ...model,
    host_node_id: normalizeNodeReference(model.host_node_id),
  };
}

function normalizeTaskRecord(task) {
  if (!task || typeof task !== "object") {
    return task;
  }
  const detail = task.detail && typeof task.detail === "object" ? { ...task.detail } : task.detail;
  if (detail && typeof detail === "object") {
    if (typeof detail.execution_worker === "string") {
      detail.execution_worker = normalizeNodeText(detail.execution_worker);
    }
    if (typeof detail.node_id === "string") {
      detail.node_id = normalizeNodeReference(detail.node_id);
    }
  }
  return {
    ...task,
    worker_id: normalizeNodeText(task.worker_id),
    assigned_agent_id: normalizeNodeText(task.assigned_agent_id),
    target_id: normalizeNodeReference(task.target_id) || normalizeNodeText(task.target_id),
    message: normalizeNodeText(task.message),
    error: normalizeNodeText(task.error),
    detail,
  };
}

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
    const role = String(node.role || "").trim().toLowerCase();
    const metadata = node.metadata && typeof node.metadata === "object" ? node.metadata : {};
    const isControllerNode = role === "controller" || String(metadata.controller_node || "").toLowerCase() === "true";
    const managedNodeRaw = String(metadata.managed_node || "").toLowerCase();
    const isManagedNode = managedNodeRaw === "" || managedNodeRaw === "true";
    const localAgentExpected = String(metadata.local_agent_expected || "").toLowerCase();
    const localAgentHint = localAgentExpected === "true" ? "应运行本机 agent" : localAgentExpected === "false" ? "本机 agent 非强制" : "-";
    const architectureRole = isControllerNode && isManagedNode ? "controller + managed node" : isControllerNode ? "controller" : isManagedNode ? "managed node" : "unknown";
    const runtimeCount = Array.isArray(node.runtimes) ? node.runtimes.length : 0;
    const summaryParts = [
      `架构: ${architectureRole}`,
      `状态: ${node.status || "unknown"}`,
      `Agent: ${agentStatus}`,
      `Runtime: ${runtimeCount}`,
      `已装载模型: ${nodeLoadedCount}`,
    ];
    const summaryText = summaryParts.join(" | ");
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <details class="node-card">
        <summary class="node-summary">
          <div class="node-summary-title">${escapeHTML(node.name)} (${escapeHTML(node.id)})</div>
          <div class="node-summary-meta">${escapeHTML(summaryText)}</div>
          <div class="node-summary-toggle">
            <span class="node-summary-divider" aria-hidden="true"></span>
            <span class="node-summary-hint">详情</span>
          </div>
        </summary>
        <div class="node-detail">
          <div class="meta">描述: ${escapeHTML(node.description || "-")}</div>
          <div class="meta">架构角色: ${escapeHTML(architectureRole)} | 分类: ${escapeHTML(node.classification || "-")} | 状态: ${escapeHTML(node.status || "unknown")} | 类型: ${escapeHTML(node.type || "-")} | 主机: ${escapeHTML(node.host || "-")}</div>
          <div class="meta">角色字段: ${escapeHTML(node.role || "-")} | Managed Node: ${isManagedNode ? "yes" : "no"} | 本机 Agent 期望: ${escapeHTML(localAgentHint)}</div>
          <div class="meta">能力分级: ${escapeHTML(node.capability_tier || "unknown")} | 能力来源: ${escapeHTML(node.capability_source || "unknown")} | 操作级别: ${escapeHTML(node.operation_level || "-")}</div>
          <div class="meta">Agent 状态: ${escapeHTML(agentStatus)} | 最近心跳: ${escapeHTML(formatTime(heartbeatAt))}</div>
          <div class="meta">能力说明: ${escapeHTML(node.capability_note || "-")}</div>
          <div class="meta">平台: ${(node.platform && node.platform.accelerator) || "unknown"} | GPU: ${(node.platform && node.platform.gpu) || "unknown"} | CUDA: ${(node.platform && node.platform.cuda_version) || "unknown"} | Driver: ${(node.platform && node.platform.driver) || "unknown"}</div>
          <div class="meta">Runtime 数量: ${runtimeCount} | 已装载模型: ${nodeLoadedCount}</div>
          <div class="meta">Runtime 状态: ${runtimeSummary || "-"}</div>
          ${buildRuntimeCapabilityBlock(node)}
        </div>
      </details>
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
    const relatedBindings = Array.isArray(state.runtimeBindings)
      ? state.runtimeBindings.filter((binding) => String(binding.template_id || "").trim() === String(tpl.id || "").trim())
      : [];
    const manifest = tpl.manifest && typeof tpl.manifest === "object" ? tpl.manifest : null;

    const title = document.createElement("div");
    title.className = "item-title";
    title.textContent = `${tpl.name || tpl.id} (${tpl.id})`;

    const summaryMeta = document.createElement("div");
    summaryMeta.className = "meta";
    summaryMeta.textContent = `runtime: ${tpl.runtime_type || "-"} | runtime_kind: ${tpl.runtime_kind || "-"} | image: ${tpl.image || "-"} | source: ${tpl.source || "unknown"} | GPU: ${tpl.needs_gpu ? "yes" : "no"} | bindings: ${relatedBindings.length}`;

    const manifestMeta = document.createElement("div");
    manifestMeta.className = "meta";
    manifestMeta.textContent = `manifest: ${manifest ? manifest.manifest_version || "-" : "-"} | model_injection_mode: ${manifest ? manifest.model_injection_mode || "-" : "-"}`;

    const command = Array.isArray(tpl.command) ? tpl.command.join(" ") : "";
    const preview = document.createElement("div");
    preview.className = "template-code";
    preview.textContent = command || "(no command)";

    item.appendChild(title);
    item.appendChild(summaryMeta);
    item.appendChild(manifestMeta);
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
    const detailRaw = task.detail && typeof task.detail === "object" ? JSON.stringify(task.detail) : "";
    const detail = normalizeNodeText(detailRaw);
    const executionPath = task.detail && typeof task.detail === "object" ? String(task.detail.execution_path || "").trim() : "";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(task.type)} (${escapeHTML(task.id)})</div>
      <div class="meta">状态: ${statusPill(task.status)} | 进度: ${escapeHTML(String(task.progress || 0))}%</div>
      <div class="meta">目标: ${escapeHTML(task.target_type || "-")} / ${escapeHTML(task.target_id || "-")}</div>
      <div class="meta">执行者: ${escapeHTML(task.worker_id || task.assigned_agent_id || "-")} | 执行路径: ${escapeHTML(executionPath || "controller-direct")} | 开始: ${escapeHTML(formatTime(task.started_at))} | 结束: ${escapeHTML(formatTime(task.finished_at))}</div>
      <div class="meta">消息: ${escapeHTML(task.message || "-")}</div>
      <div class="meta">错误: ${escapeHTML(task.error || "-")}</div>
      <div class="meta">明细: ${escapeHTML(detail || "-")}</div>
    `;
    tasksEl.appendChild(item);
  });
}

function renderRuntimeBindings() {
  if (!runtimeBindingsEl) {
    return;
  }
  if (!Array.isArray(state.runtimeBindings) || state.runtimeBindings.length === 0) {
    runtimeBindingsEl.textContent = "暂无 runtime bindings";
    return;
  }
  runtimeBindingsEl.innerHTML = "";
  state.runtimeBindings.slice(0, 20).forEach((binding) => {
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(binding.id || "-")}</div>
      <div class="meta">model: ${escapeHTML(binding.model_id || "-")} | template: ${escapeHTML(binding.template_id || "-")} | mode: ${escapeHTML(binding.binding_mode || "-")}</div>
      <div class="meta">compatibility: ${escapeHTML(binding.compatibility_status || "unknown")} | enabled: ${binding.enabled === false ? "no" : "yes"}</div>
      <div class="meta">message: ${escapeHTML(binding.compatibility_message || "-")}</div>
    `;
    runtimeBindingsEl.appendChild(item);
  });
}

function renderRuntimeInstances() {
  if (!runtimeInstancesEl) {
    return;
  }
  if (!Array.isArray(state.runtimeInstances) || state.runtimeInstances.length === 0) {
    runtimeInstancesEl.textContent = "暂无 runtime instances";
    return;
  }
  runtimeInstancesEl.innerHTML = "";
  state.runtimeInstances.slice(0, 20).forEach((instance) => {
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${escapeHTML(instance.id || "-")}</div>
      <div class="meta">model: ${escapeHTML(instance.model_id || "-")} | template: ${escapeHTML(instance.template_id || "-")} | binding: ${escapeHTML(instance.binding_id || "-")}</div>
      <div class="meta">node: ${escapeHTML(instance.node_id || "-")} | desired/observed: ${escapeHTML(instance.desired_state || "-")} / ${escapeHTML(instance.observed_state || "-")} | readiness: ${escapeHTML(instance.readiness || "unknown")}</div>
      <div class="meta">endpoint: ${escapeHTML(instance.endpoint || "-")} | drift: ${escapeHTML(instance.drift_reason || "-")}</div>
    `;
    runtimeInstancesEl.appendChild(item);
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

function findBindingByModelID(modelID) {
  if (!Array.isArray(state.runtimeBindings)) {
    return null;
  }
  return state.runtimeBindings.find((item) => String(item.model_id || "").trim() === String(modelID || "").trim()) || null;
}

function findInstanceByModelID(modelID) {
  if (!Array.isArray(state.runtimeInstances)) {
    return null;
  }
  return state.runtimeInstances.find((item) => String(item.model_id || "").trim() === String(modelID || "").trim()) || null;
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
      await Promise.all([loadTasks(), loadTestRuns(), loadRuntimeBindings(), loadRuntimeInstances()]);
      renderNodes();
      renderModelTabs();
      renderModels();
      renderRuntimeBindings();
      renderRuntimeInstances();
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
    const binding = findBindingByModelID(m.id);
    const runtimeInstance = findInstanceByModelID(m.id);
    const item = document.createElement("div");
    item.className = "list-item";

    const title = document.createElement("div");
    title.className = "item-title";
    title.textContent = `${m.name} (${m.id})`;

    const summaryMeta = document.createElement("div");
    summaryMeta.className = "meta";
    const templateID = m.metadata && m.metadata.runtime_template_id ? m.metadata.runtime_template_id : "-";
    const bindingTemplateID = binding && binding.template_id ? binding.template_id : templateID;
    const summaryParts = [
      `后端: ${m.backend_type}`,
      `节点: ${m.host_node_id}`,
      `状态: ${displayModelState(m.state, m.backend_type)}`,
      `模板: ${bindingTemplateID}`,
    ];
    summaryMeta.textContent = summaryParts.join(" | ");
    const health = document.createElement("div");
    health.className = "meta";
    health.innerHTML = `健康信息: ${statusPill(m.readiness || "unknown")} ${escapeHTML(m.health_message || "-")} | endpoint: ${escapeHTML(m.endpoint || "-")} | 最近协调: ${escapeHTML(formatTime(m.last_reconciled_at))}`;

    const detailToggle = document.createElement("div");
    detailToggle.className = "model-detail-toggle";
    detailToggle.setAttribute("role", "button");
    detailToggle.setAttribute("tabindex", "0");
    detailToggle.innerHTML = `
      <span class="model-detail-divider" aria-hidden="true"></span>
      <span class="model-detail-label">详情</span>
      <span class="model-detail-arrow" aria-hidden="true">▸</span>
    `;

    const detailPanel = document.createElement("div");
    detailPanel.className = "model-detail-panel hidden";
    const detailLines = [
      `Provider: ${m.provider || "-"}`,
      `Runtime ID: ${m.runtime_id || "-"}`,
      `Model ID: ${m.id || "-"}`,
      `期望/实际: ${m.desired_state || "-"} / ${m.observed_state || "-"}`,
      `Readiness: ${m.readiness || "unknown"} | 装载: ${isLoadedState(m.state) ? "已装载" : "未装载"}`,
      `Binding: ${binding ? binding.id || "-" : "-"} | mode: ${binding ? binding.binding_mode || "-" : "-"}`,
      `Binding compatibility: ${binding ? binding.compatibility_status || "unknown" : "unknown"} | message: ${binding ? binding.compatibility_message || "-" : "-"}`,
      `RuntimeInstance: ${runtimeInstance ? runtimeInstance.id || "-" : "-"} | node: ${runtimeInstance ? runtimeInstance.node_id || "-" : "-"}`,
      `RuntimeInstance desired/observed: ${runtimeInstance ? runtimeInstance.desired_state || "-" : "-"} / ${runtimeInstance ? runtimeInstance.observed_state || "-" : "-"}`,
      `RuntimeInstance readiness: ${runtimeInstance ? runtimeInstance.readiness || "unknown" : "unknown"} | drift: ${runtimeInstance ? runtimeInstance.drift_reason || "-" : "-"}`,
    ];
    detailLines.forEach((line) => {
      const detailRow = document.createElement("div");
      detailRow.className = "meta";
      detailRow.textContent = line;
      detailPanel.appendChild(detailRow);
    });

    const detailArrow = detailToggle.querySelector(".model-detail-arrow");
    const setExpanded = (expanded) => {
      detailPanel.classList.toggle("hidden", !expanded);
      if (detailArrow) {
        detailArrow.textContent = expanded ? "▾" : "▸";
      }
    };
    setExpanded(false);
    detailToggle.addEventListener("click", () => {
      const expanded = detailPanel.classList.contains("hidden");
      setExpanded(expanded);
    });
    detailToggle.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        const expanded = detailPanel.classList.contains("hidden");
        setExpanded(expanded);
      }
    });

    const actions = document.createElement("div");
    actions.className = "actions";
    actionsForModelBackend(m.backend_type).forEach((action) => {
      actions.appendChild(buildActionButton(m.id, action, m.host_node_id || "unknown", m.state, m.backend_type));
    });

    item.appendChild(title);
    item.appendChild(summaryMeta);
    item.appendChild(health);
    item.appendChild(detailToggle);
    item.appendChild(detailPanel);
    item.appendChild(actions);
    modelsEl.appendChild(item);
  });
}

async function loadNodes() {
  const nodes = await requestJSON("/api/v1/nodes");
  const normalizedNodes = Array.isArray(nodes) ? nodes.map((node) => normalizeNodeRecord(node)) : [];
  const aliases = {};
  const groupedByIdentity = new Map();
  const mergedByCanonicalID = new Map();

  normalizedNodes.forEach((node) => {
    const identityKey = buildNodeIdentityKey(node);
    const groupKey = identityKey || `id:${node.id}`;
    if (!groupedByIdentity.has(groupKey)) {
      groupedByIdentity.set(groupKey, []);
    }
    groupedByIdentity.get(groupKey).push(node);
  });

  groupedByIdentity.forEach((group) => {
    if (!Array.isArray(group) || group.length === 0) {
      return;
    }
    let merged = group[0];
    for (let i = 1; i < group.length; i += 1) {
      merged = mergeNodeRecords(merged, group[i]);
    }
    const canonicalID = merged.id;
    const existing = mergedByCanonicalID.get(canonicalID);
    mergedByCanonicalID.set(canonicalID, existing ? mergeNodeRecords(existing, merged) : merged);

    group.forEach((node) => {
      aliases[node.id] = canonicalID;
      if (node.__raw_id) {
        aliases[node.__raw_id] = canonicalID;
      }
    });
  });

  const dedupedNodes = Array.from(mergedByCanonicalID.values()).sort((a, b) => {
    if (a.id === "node-controller" && b.id !== "node-controller") {
      return -1;
    }
    if (b.id === "node-controller" && a.id !== "node-controller") {
      return 1;
    }
    return String(a.name || a.id).localeCompare(String(b.name || b.id));
  });
  dedupedNodes.forEach((node) => {
    aliases[node.id] = node.id;
  });
  state.nodeIdAliases = aliases;
  state.nodes = dedupedNodes.map((node) => {
    const out = { ...node };
    delete out.__raw_id;
    return out;
  });
  state.activeNodeId = normalizeNodeReference(state.activeNodeId);
  const activeNodeExists = state.nodes.some((node) => node.id === state.activeNodeId);
  if ((!state.activeNodeId || !activeNodeExists) && state.nodes.length > 0) {
    state.activeNodeId = state.nodes[0].id;
  }
}

async function loadModels(options = {}) {
  const refresh = Boolean(options.refresh);
  const path = refresh ? "/api/v1/models?refresh=true" : "/api/v1/models";
  const models = await requestJSON(path);
  state.models = Array.isArray(models) ? models.map((model) => normalizeModelRecord(model)) : [];
}

async function loadRuntimeTemplates() {
  const templates = await requestJSON("/api/v1/runtime-templates");
  state.runtimeTemplates = Array.isArray(templates) ? templates : [];
}

async function loadRuntimeBindings() {
  const bindings = await requestJSON("/api/v1/runtime-bindings");
  state.runtimeBindings = Array.isArray(bindings) ? bindings : [];
}

async function loadRuntimeInstances() {
  const instances = await requestJSON("/api/v1/runtime-instances");
  state.runtimeInstances = Array.isArray(instances) ? instances : [];
}

async function loadTasks() {
  const tasks = await requestJSON("/api/v1/tasks?limit=20");
  state.tasks = Array.isArray(tasks) ? tasks.map((task) => normalizeTaskRecord(task)) : [];
}

async function loadTestRuns() {
  const runs = await requestJSON("/api/v1/test-runs?limit=20");
  state.testRuns = Array.isArray(runs)
    ? runs.map((run) => ({
        ...run,
        summary: normalizeNodeText(run.summary),
        error: normalizeNodeText(run.error),
      }))
    : [];
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
      await Promise.all([loadModels({ refresh: true }), loadTasks(), loadRuntimeBindings(), loadRuntimeInstances()]);
      renderModels();
      renderRuntimeBindings();
      renderRuntimeInstances();
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
    await loadNodes();
    await Promise.all([loadModels(), loadTasks(), loadTestRuns(), loadRuntimeBindings(), loadRuntimeInstances()]);
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
    renderRuntimeBindings();
    renderRuntimeInstances();
    renderTasks();
    renderTestRuns();
    if (!runtimeTemplatesLoadFailed) {
      renderRuntimeTemplates();
    }

    setInterval(async () => {
      try {
        await Promise.all([loadTasks(), loadTestRuns(), loadRuntimeBindings(), loadRuntimeInstances()]);
        renderRuntimeBindings();
        renderRuntimeInstances();
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
    if (runtimeBindingsEl) {
      runtimeBindingsEl.textContent = `加载失败: ${err.message}`;
    }
    if (runtimeInstancesEl) {
      runtimeInstancesEl.textContent = `加载失败: ${err.message}`;
    }
    if (testRunsEl) {
      testRunsEl.textContent = `加载失败: ${err.message}`;
    }
    if (runtimeTemplatesEl) {
      runtimeTemplatesEl.textContent = `加载失败: ${err.message}`;
    }
  }
})();
