const nodesEl = document.getElementById("nodes");
const modelsEl = document.getElementById("models");
const modelTabsEl = document.getElementById("model-tabs");

const state = {
  nodes: [],
  models: [],
  activeNodeId: "",
  nodeActionLocks: {},
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
  const resp = await fetch(url, options);
  const payload = await resp.json();
  if (!resp.ok || payload.success === false) {
    const msg = payload.message || `请求失败: ${resp.status}`;
    throw new Error(msg);
  }
  return payload.data;
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

function buildActionButton(modelId, action, nodeId, modelState) {
  const button = document.createElement("button");
  button.textContent = action.toUpperCase();
  button.dataset.nodeId = nodeId;
  const allowed = actionAllowedByState(action, modelState);
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
      await loadModels();
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
    meta.textContent = `后端: ${m.backend_type} | 节点: ${m.host_node_id} | 状态: ${m.state} | 装载: ${isLoadedState(m.state) ? "已装载" : "未装载"}`;

    const actions = document.createElement("div");
    actions.className = "actions";
    actionsForModelBackend(m.backend_type).forEach((action) => {
      actions.appendChild(buildActionButton(m.id, action, m.host_node_id || "unknown", m.state));
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

async function loadModels() {
  const models = await requestJSON("/api/v1/models");
  state.models = Array.isArray(models) ? models : [];
}

(async function init() {
  try {
    await Promise.all([loadNodes(), loadModels()]);
    renderNodes();
    renderModelTabs();
    renderModels();
  } catch (err) {
    nodesEl.textContent = `加载失败: ${err.message}`;
    modelsEl.textContent = `加载失败: ${err.message}`;
  }
})();
