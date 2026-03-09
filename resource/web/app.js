const nodesEl = document.getElementById("nodes");
const modelsEl = document.getElementById("models");

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

function renderNodes(nodes) {
  if (!Array.isArray(nodes) || nodes.length === 0) {
    nodesEl.textContent = "暂无节点";
    return;
  }

  nodesEl.innerHTML = "";
  nodes.forEach((node) => {
    const item = document.createElement("div");
    item.className = "list-item";
    item.innerHTML = `
      <div class="item-title">${node.name} (${node.id})</div>
      <div class="meta">类型: ${node.type} | 主机: ${node.host} | 状态: ${node.status}</div>
      <div class="meta">Runtime 数量: ${(node.runtimes || []).length}</div>
    `;
    nodesEl.appendChild(item);
  });
}

function buildActionButton(modelId, action) {
  const button = document.createElement("button");
  button.textContent = action.toUpperCase();
  button.addEventListener("click", async () => {
    try {
      const data = await requestJSON(`/api/v1/models/${encodeURIComponent(modelId)}/${action}`, {
        method: "POST",
      });
      showToast(`${action} -> ${data.message || "ok"}`);
      await loadModels();
    } catch (err) {
      showToast(`${action} 失败: ${err.message}`);
    }
  });
  return button;
}

function renderModels(models) {
  if (!Array.isArray(models) || models.length === 0) {
    modelsEl.textContent = "暂无模型";
    return;
  }

  modelsEl.innerHTML = "";
  models.forEach((m) => {
    const item = document.createElement("div");
    item.className = "list-item";

    const title = document.createElement("div");
    title.className = "item-title";
    title.textContent = `${m.name} (${m.id})`;

    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = `后端: ${m.backend_type} | 节点: ${m.host_node_id} | 状态: ${m.state}`;

    const actions = document.createElement("div");
    actions.className = "actions";
    ["load", "unload", "start", "stop"].forEach((action) => {
      actions.appendChild(buildActionButton(m.id, action));
    });

    item.appendChild(title);
    item.appendChild(meta);
    item.appendChild(actions);
    modelsEl.appendChild(item);
  });
}

async function loadNodes() {
  try {
    const nodes = await requestJSON("/api/v1/nodes");
    renderNodes(nodes);
  } catch (err) {
    nodesEl.textContent = `节点加载失败: ${err.message}`;
  }
}

async function loadModels() {
  try {
    const models = await requestJSON("/api/v1/models");
    renderModels(models);
  } catch (err) {
    modelsEl.textContent = `模型加载失败: ${err.message}`;
  }
}

(async function init() {
  await Promise.all([loadNodes(), loadModels()]);
})();
