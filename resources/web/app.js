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

const state = {
  nodes: [],
  models: [],
  runtimeTemplates: [],
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
      throw new Error("未授权，请在 URL 添加 ?token=<api-token> 或设置 localStorage.mi_api_token");
    }
    const msg = payload.message || `请求失败: ${resp.status}`;
    throw new Error(msg);
  }
  return payload.data;
}

function resolveAPIToken() {
  const fromQuery = String(new URLSearchParams(window.location.search).get("token") || "").trim();
  if (fromQuery) {
    localStorage.setItem("mi_api_token", fromQuery);
    return fromQuery;
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
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${node.name} (${node.id})</div>
      <div class="meta">描述: ${node.description || "-"}</div>
      <div class="meta">类型: ${node.type} | 主机: ${node.host} | 状态: ${node.status}</div>
      <div class="meta">平台: ${(node.platform && node.platform.accelerator) || "unknown"} | GPU: ${(node.platform && node.platform.gpu) || "unknown"} | CUDA: ${(node.platform && node.platform.cuda_version) || "unknown"} | Driver: ${(node.platform && node.platform.driver) || "unknown"}</div>
      <div class="meta">Runtime 数量: ${(node.runtimes || []).length} | 已装载模型: ${nodeLoadedCount}</div>
      <div class="meta">Runtime 状态: ${runtimeSummary || "-"}</div>
    `;
    nodesEl.appendChild(item);
  });
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
    parts.push(`${rtType}:${enabled}, loaded=${loaded}`);
  });
  return parts.join(" | ");
}

function actionsForModelBackend(backendType) {
  const normalized = normalizeBackendType(backendType);
  if (normalized === "lmstudio") {
    return ["load", "unload"];
  }
  return ["load", "unload", "start", "stop"];
}

function actionAllowedByState(action, rawState) {
  const stateValue = normalizeModelState(rawState);
  if (action === "load" || action === "start") {
    return !isLoadedState(stateValue);
  }
  if (action === "unload" || action === "stop") {
    return isLoadedState(stateValue);
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
      const data = await requestJSON(`/api/v1/models/${encodeURIComponent(modelId)}/${action}`, {
        method: "POST",
      });
      showToast(`${action} -> ${data.message || "ok"}`);
      await loadModels({ refresh: true });
      renderNodes();
      renderModelTabs();
      renderModels();
    } catch (err) {
      showToast(`${action} 失败: ${err.message}`);
    } finally {
      setNodeLock(nodeId, false);
    }
  });
  return button;
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
      `装载: ${isLoadedState(m.state) ? "已装载" : "未装载"}`,
    ];
    if (normalizeBackendType(m.backend_type) !== "lmstudio") {
      metaParts.push(`模板: ${templateID}`);
    }
    meta.textContent = metaParts.join(" | ");

    const actions = document.createElement("div");
    actions.className = "actions";
    actionsForModelBackend(m.backend_type).forEach((action) => {
      actions.appendChild(buildActionButton(m.id, action, m.host_node_id || "unknown", m.state, m.backend_type));
    });

    item.appendChild(title);
    item.appendChild(meta);
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

(async function init() {
  try {
    state.apiToken = resolveAPIToken();

    let runtimeTemplatesLoadFailed = false;
    await Promise.all([loadNodes(), loadModels()]);
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
    renderPageTabs();
    renderRuntimeSubTabs();
    renderActivePage();
    renderNodes();
    renderModelTabs();
    renderModels();
    if (!runtimeTemplatesLoadFailed) {
      renderRuntimeTemplates();
    }
  } catch (err) {
    nodesEl.textContent = `加载失败: ${err.message}`;
    modelsEl.textContent = `加载失败: ${err.message}`;
    if (runtimeTemplatesEl) {
      runtimeTemplatesEl.textContent = `加载失败: ${err.message}`;
    }
  }
})();
