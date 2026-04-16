const endpointInput = document.querySelector("#endpoint");
const threadInput = document.querySelector("#threadId");
const promptInput = document.querySelector("#promptInput");
const sendButton = document.querySelector("#sendButton");
const requestStatus = document.querySelector("#requestStatus");
const aguiLog = document.querySelector("#aguiLog");
const a2uiLog = document.querySelector("#a2uiLog");
const aguiLogPanel = document.querySelector("#aguiLogPanel");
const a2uiLogPanel = document.querySelector("#a2uiLogPanel");
const logSwitchButtons = document.querySelectorAll("[data-log-stream]");
const a2uiCanvas = document.querySelector("#a2uiCanvas");

const FORMATS = {
  endpointDefault: "http://127.0.0.1:8080/a2ui",
  statusReady: "Ready",
  statusBusy: "Waiting for stream",
  statusDone: "Stream finished",
  statusNoAction: "No action configured",
  statusError: "Request failed",
  jsonSpacer: 2,
};

const surfaceStates = new Map();
const surfaceElements = new Map();

function createId(prefix) {
  const random = (typeof crypto !== "undefined" && crypto.randomUUID)
    ? crypto.randomUUID().replace(/-/g, "")
    : Math.random().toString(36).slice(2);
  return `${prefix}-${Date.now().toString(36)}-${random.slice(0, 6)}`;
}

function randomAlphabetic(length) {
  const chars = "abcdefghijklmnopqrstuvwxyz";
  const max = chars.length;
  let value = "";
  if (typeof crypto !== "undefined" && crypto.getRandomValues) {
    const bytes = new Uint8Array(length);
    crypto.getRandomValues(bytes);
    for (let i = 0; i < length; i += 1) {
      value += chars[bytes[i] % max];
    }
    return value;
  }
  for (let i = 0; i < length; i += 1) {
    value += chars[Math.floor(Math.random() * max)];
  }
  return value;
}

function createThreadId() {
  return randomAlphabetic(7);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function normalizePath(path) {
  if (typeof path !== "string") {
    return "";
  }
  const trimmed = path.trim();
  if (!trimmed) {
    return "";
  }
  return trimmed;
}

function getSurfaceState(surfaceId) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  let state = surfaceStates.get(normalizedSurfaceId);
  if (!state) {
    state = {
      rootId: null,
      styles: {},
      componentMap: new Map(),
      dataModel: {},
      inputBindings: new Map(),
      surfaceId: normalizedSurfaceId,
    };
    surfaceStates.set(normalizedSurfaceId, state);
  }
  if (!state.inputBindings) {
    state.inputBindings = new Map();
  }
  if (!state.dataModel) {
    state.dataModel = {};
  }
  return state;
}

function splitPath(path) {
  const normalized = normalizePath(path);
  if (!normalized || normalized === "/") {
    return [];
  }
  const normalizedPath = normalized[0] === "/" ? normalized.slice(1) : normalized;
  return normalizedPath.split("/").filter((part) => part.trim().length > 0);
}

function resolveDataPath(path, dataContextPath = "/") {
  const normalized = normalizePath(path);
  if (!normalized) {
    return "";
  }
  if (normalized === ".") {
    return normalizePath(dataContextPath) || "/";
  }
  if (normalized.startsWith("/")) {
    return normalized;
  }
  const normalizedContext = normalizePath(dataContextPath) || "/";
  const relativePath = normalized.replace(/^\.\/?/, "").replace(/^\/+/, "");
  if (!relativePath) {
    return normalizedContext;
  }
  if (normalizedContext === "/") {
    return `/${relativePath}`;
  }
  return `${normalizedContext.replace(/\/$/, "")}/${relativePath}`;
}

function getDataModelValueByPath(state, path) {
  const segments = splitPath(path);
  if (!Array.isArray(segments)) {
    return undefined;
  }
  let current = isObject(state) ? state.dataModel : undefined;
  for (const segment of segments) {
    if (current === undefined || current === null) {
      return undefined;
    }
    if (Array.isArray(current) && /^\d+$/.test(segment)) {
      const index = Number(segment);
      current = current[index];
      continue;
    }
    if (!isObject(current)) {
      return undefined;
    }
    current = current[segment];
  }
  if (current !== undefined) {
    return current;
  }
  return findDataModelValueByKey(state, segments[segments.length - 1]);
}

function getDataModelValueInContext(state, path, dataContextPath = "/") {
  const resolvedPath = resolveDataPath(path, dataContextPath);
  if (!resolvedPath) {
    return undefined;
  }
  return getDataModelValueByPath(state, resolvedPath);
}

function setDataModelValueInContext(state, path, value, dataContextPath = "/") {
  const resolvedPath = resolveDataPath(path, dataContextPath);
  if (!resolvedPath) {
    return;
  }
  setDataModelValueByPath(state, resolvedPath, value);
}

function resolveTemplateInstances(state, templateConfig, dataContextPath = "/") {
  if (!isObject(templateConfig) || typeof templateConfig.componentId !== "string" || !templateConfig.componentId.trim()) {
    return [];
  }
  const bindingPath = resolveDataPath(templateConfig.dataBinding, dataContextPath);
  if (!bindingPath) {
    return [];
  }
  const collection = getDataModelValueByPath(state, bindingPath);
  if (Array.isArray(collection)) {
    return collection.map((_, index) => ({
      componentId: templateConfig.componentId.trim(),
      dataContextPath: `${bindingPath}/${index}`,
      idSuffix: `:${index}`,
    }));
  }
  if (isObject(collection)) {
    return Object.keys(collection).map((key) => ({
      componentId: templateConfig.componentId.trim(),
      dataContextPath: `${bindingPath}/${key}`,
      idSuffix: `:${key}`,
    }));
  }
  return [];
}

function findDataModelValueByKey(state, key) {
  if (typeof key !== "string" || !key.trim()) {
    return undefined;
  }
  const target = isObject(state) ? state.dataModel : undefined;
  if (!isObject(target) && !Array.isArray(target)) {
    return undefined;
  }
  const normalizedKey = key.trim();
  const queue = [target];
  while (queue.length > 0) {
    const current = queue.shift();
    if (isObject(current)) {
      if (Object.prototype.hasOwnProperty.call(current, normalizedKey)) {
        return current[normalizedKey];
      }
      Object.keys(current).forEach((itemKey) => {
        const value = current[itemKey];
        if (value !== undefined && value !== null && (isObject(value) || Array.isArray(value))) {
          queue.push(value);
        }
      });
    } else if (Array.isArray(current)) {
      current.forEach((item) => {
        if (item !== undefined && item !== null && (isObject(item) || Array.isArray(item))) {
          queue.push(item);
        }
      });
    }
  }
  return undefined;
}

function setDataModelValueByPath(state, path, value) {
  if (!isObject(state)) {
    return;
  }
  const segments = splitPath(path);
  if (!segments.length) {
    state.dataModel = value;
    return;
  }
  let current = state.dataModel;
  if (!isObject(current) && !Array.isArray(current)) {
    current = {};
    state.dataModel = current;
  }
  for (let i = 0; i < segments.length; i += 1) {
    const segment = segments[i];
    const last = i === segments.length - 1;
    if (last) {
      current[segment] = value;
      return;
    }
    if (current[segment] === undefined || current[segment] === null) {
      current[segment] = {};
    }
    if (!isObject(current[segment])) {
      current[segment] = {};
    }
    current = current[segment];
  }
}

function parseDataModelValue(rawValue) {
  if (!isObject(rawValue)) {
    return rawValue;
  }
  if (typeof rawValue.valueString === "string") {
    return rawValue.valueString;
  }
  if (typeof rawValue.valueNumber === "number") {
    return rawValue.valueNumber;
  }
  if (typeof rawValue.valueBoolean === "boolean") {
    return rawValue.valueBoolean;
  }
  if (Array.isArray(rawValue.valueMap)) {
    const result = {};
    rawValue.valueMap.forEach((entry) => {
      if (!isObject(entry) || typeof entry.key !== "string") {
        return;
      }
      result[entry.key] = parseDataModelValue(entry.value !== undefined ? entry.value : entry);
    });
    return result;
  }
  if (typeof rawValue.path === "string") {
    return { path: rawValue.path };
  }
  return rawValue;
}

function mergeDataModels(target, updates) {
  if (Array.isArray(target) || Array.isArray(updates)) {
    return updates;
  }
  if (!isObject(target) || !isObject(updates)) {
    return updates;
  }
  const merged = Object.assign({}, target);
  Object.keys(updates).forEach((key) => {
    merged[key] = isObject(merged[key]) && isObject(updates[key])
      ? mergeDataModels(merged[key], updates[key])
      : updates[key];
  });
  return merged;
}

function normalizeDataModelLiteral(value) {
  if (value === undefined || value === null) {
    return null;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  if (Array.isArray(value) || isObject(value)) {
    return JSON.stringify(value);
  }
  return String(value);
}

function setStatus(message, level) {
  if (!requestStatus) {
    return;
  }
  requestStatus.textContent = message;
  requestStatus.className = "request-status";
  if (level === "busy") {
    requestStatus.classList.add("request-status-running");
  } else if (level === "ok") {
    requestStatus.classList.add("request-status-done");
  } else if (level === "error") {
    requestStatus.classList.add("request-status-error");
  } else {
    requestStatus.classList.add("request-status-ready");
  }
}

function formatTime() {
  return new Date().toLocaleTimeString("en-US", { hour12: false });
}

function safeJSONStringify(value) {
  try {
    return JSON.stringify(value, null, FORMATS.jsonSpacer);
  } catch (error) {
    return String(value);
  }
}

function appendLogItem(target, title, detail, payload, isMuted) {
  const item = document.createElement("li");
  item.className = "log-item";
  const titleRow = document.createElement("div");
  titleRow.className = "title";
  const titleText = document.createElement("span");
  titleText.textContent = title;
  const metaText = document.createElement("span");
  metaText.className = "meta";
  metaText.textContent = formatTime();
  titleRow.appendChild(titleText);
  titleRow.appendChild(metaText);
  item.appendChild(titleRow);
  if (detail) {
    const detailNode = document.createElement("div");
    detailNode.textContent = detail;
    if (isMuted) {
      detailNode.className = "muted";
    }
    item.appendChild(detailNode);
  }
  if (payload !== undefined) {
    const code = document.createElement("pre");
    code.className = "code";
    code.textContent = safeJSONStringify(payload);
    item.appendChild(code);
  }
  target.appendChild(item);
  target.parentElement.scrollTop = target.parentElement.scrollHeight;
}

function parseSseFrame(frameText) {
  const lines = frameText.split(/\r?\n/);
  const eventLine = lines.find((line) => line.startsWith("event:"));
  const dataLines = lines
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trim());
  const dataText = dataLines.join("\n").trim();
  if (!dataText) {
    return null;
  }
  try {
    return {
      eventType: eventLine ? eventLine.slice(6).trim() : "",
      data: JSON.parse(dataText),
    };
  } catch {
    return {
      eventType: eventLine ? eventLine.slice(6).trim() : "",
      data: dataText,
    };
  }
}

function parseAguiEvent(rawEvent) {
  if (rawEvent === undefined || rawEvent === null || rawEvent instanceof ArrayBuffer) {
    return null;
  }
  if (typeof rawEvent !== "object") {
    return {
      type: "RAW_STRING",
      payload: { value: String(rawEvent) },
    };
  }
  if (typeof rawEvent.type === "string") {
    return rawEvent;
  }
  if (typeof rawEvent.Type === "string") {
    return { ...rawEvent, type: rawEvent.Type };
  }
  return {
    type: "RAW_OBJECT",
    payload: rawEvent,
  };
}

function extractRawPayload(rawEvent) {
  if (typeof rawEvent === "string") {
    const trimmed = rawEvent.trim();
    if (trimmed) {
      try {
        return extractRawPayload(JSON.parse(trimmed));
      } catch {
        return rawEvent;
      }
    }
    return rawEvent;
  }
  if (!isObject(rawEvent)) {
    return rawEvent;
  }
  if (rawEvent.event !== undefined) {
    return rawEvent.event;
  }
  if (rawEvent.payload !== undefined) {
    return rawEvent.payload;
  }
  if (rawEvent.data !== undefined) {
    return rawEvent.data;
  }
  return rawEvent;
}

function extractA2UIPayload(rawEvent) {
  if (!isObject(rawEvent)) {
    return null;
  }
  const visited = new Set();
  let current = rawEvent;
  while (isObject(current) && !visited.has(current)) {
    if (current.surfaceUpdate || current.beginRendering || current.deleteSurface || current.dataModelUpdate || current.endRendering) {
      return current;
    }
    visited.add(current);
    if (isObject(current.event)) {
      current = current.event;
      continue;
    }
    if (isObject(current.payload)) {
      current = current.payload;
      continue;
    }
    if (isObject(current.data)) {
      current = current.data;
      continue;
    }
    break;
  }
  return null;
}

function resolveSurfaceIdFromPayload(payload) {
  if (!isObject(payload)) {
    return "";
  }
  const candidates = [
    payload.surfaceId,
    isObject(payload.surfaceUpdate) ? payload.surfaceUpdate.surfaceId : undefined,
    isObject(payload.beginRendering) ? payload.beginRendering.surfaceId : undefined,
    isObject(payload.dataModelUpdate) ? payload.dataModelUpdate.surfaceId : undefined,
    isObject(payload.deleteSurface) ? payload.deleteSurface.surfaceId : undefined,
  ];
  const normalized = candidates.find((value) => typeof value === "string" && value.trim().length > 0);
  return normalized || "";
}

function normalizeSurfaceId(surfaceId) {
  if (typeof surfaceId !== "string" || !surfaceId.trim()) {
    return "default";
  }
  return surfaceId.trim();
}

function ensureSurfaceElement(surfaceId) {
  const existing = surfaceElements.get(surfaceId);
  if (existing) {
    return existing;
  }
  const section = document.createElement("div");
  section.className = "surface-card";
  section.dataset.surfaceId = surfaceId;
  const header = document.createElement("div");
  header.className = "surface-header";
  const title = document.createElement("span");
  title.textContent = `surfaceId: ${surfaceId}`;
  const badge = document.createElement("span");
  badge.className = "a2ui-badge";
  badge.textContent = "A2UI";
  header.appendChild(title);
  header.appendChild(badge);
  const body = document.createElement("div");
  body.className = "surface-body";
  section.appendChild(header);
  section.appendChild(body);
  a2uiCanvas.appendChild(section);
  const holder = { section, header, body };
  surfaceElements.set(surfaceId, holder);
  return holder;
}

function clearRenderedSurfaces() {
  a2uiCanvas.replaceChildren();
  surfaceStates.clear();
  surfaceElements.clear();
}

function mapAlignment(value) {
  switch ((value || "start").toString().toLowerCase()) {
    case "start":
      return "flex-start";
    case "center":
      return "center";
    case "end":
      return "flex-end";
    case "stretch":
      return "stretch";
    default:
      return "flex-start";
  }
}

function mapDistribution(value) {
  switch ((value || "start").toString().toLowerCase()) {
    case "start":
      return "flex-start";
    case "center":
      return "center";
    case "end":
      return "flex-end";
    case "space-between":
      return "space-between";
    case "space-around":
      return "space-around";
    case "space-evenly":
      return "space-evenly";
    default:
      return "flex-start";
  }
}

function normalizeImageUsageHint(value) {
  const normalized = (value || "").toString().trim().toLowerCase().replace(/[^a-z0-9]/g, "");
  switch (normalized) {
    case "icon":
      return "icon";
    case "avatar":
      return "avatar";
    case "smallfeature":
      return "smallfeature";
    case "mediumfeature":
      return "mediumfeature";
    case "largefeature":
      return "largefeature";
    case "header":
      return "header";
    default:
      return "";
  }
}

function readLiteralOrPath(value, surfaceId, fallback = "", dataContextPath = "/") {
  if (value === undefined || value === null) {
    return fallback;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (isObject(value)) {
    if (typeof value.literalString === "string") {
      return value.literalString;
    }
    if (typeof value.path === "string") {
      const state = getSurfaceState(surfaceId || "");
      const resolved = getDataModelValueInContext(state, value.path, dataContextPath);
      if (resolved === undefined) {
        return fallback;
      }
      if (resolved === null) {
        return "";
      }
      if (typeof resolved === "string" || typeof resolved === "number" || typeof resolved === "boolean") {
        return String(resolved);
      }
      return JSON.stringify(resolved);
    }
  }
  return fallback;
}

function readBooleanValue(value, defaultValue, surfaceId, dataContextPath = "/") {
  if (typeof value === "boolean") {
    return value;
  }
  if (isObject(value)) {
    if (typeof value.literalBoolean === "boolean") {
      return value.literalBoolean;
    }
    if (typeof value.value === "boolean") {
      return value.value;
    }
    if (typeof value.path === "string" && value.path.trim()) {
      const state = getSurfaceState(surfaceId || "");
      const resolved = getDataModelValueInContext(state, value.path, dataContextPath);
      if (typeof resolved === "boolean") {
        return resolved;
      }
      return defaultValue;
    }
  }
  return defaultValue;
}

function readSelectionValues(selection, surfaceId, dataContextPath = "/") {
  if (!isObject(selection) && !Array.isArray(selection)) {
    if (typeof selection === "string") {
      return [selection];
    }
    if (typeof selection === "number" || typeof selection === "boolean") {
      return [String(selection)];
    }
    return [];
  }
  if (Array.isArray(selection)) {
    return selection.map((item) => String(item)).filter((item) => item.trim() !== "");
  }
  if (Array.isArray(selection.literalArray)) {
    return selection.literalArray.map((item) => String(item)).filter((item) => item.trim() !== "");
  }
  if (typeof selection.path === "string" && selection.path.trim()) {
    const state = getSurfaceState(surfaceId || "");
    const resolved = getDataModelValueInContext(state, selection.path, dataContextPath);
    if (Array.isArray(resolved)) {
      return resolved.map((item) => String(item)).filter((item) => item.trim() !== "");
    }
    if (typeof resolved === "string" || typeof resolved === "number" || typeof resolved === "boolean") {
      return [String(resolved)];
    }
    return [];
  }
  return [];
}

function collectChildIdsFromPayload(payload) {
  const ids = [];
  if (payload === null || typeof payload !== "object") {
    return ids;
  }
  const pushIfString = (id) => {
    if (typeof id === "string" && id.trim()) {
      ids.push(id.trim());
    }
  };
  pushIfString(payload.child);
  if (payload.children && Array.isArray(payload.children.explicitList)) {
    payload.children.explicitList.forEach(pushIfString);
  }
  if (payload.children && isObject(payload.children.template)) {
    pushIfString(payload.children.template.componentId);
  }
  if (Array.isArray(payload.items)) {
    payload.items.forEach((item) => {
      if (typeof item === "string") {
        pushIfString(item);
      } else if (item && typeof item.id === "string") {
        pushIfString(item.id);
      } else if (isObject(item) && item.tab && typeof item.tab.id === "string") {
        pushIfString(item.tab.id);
      }
    });
  }
  if (payload.primary !== undefined) {
    pushIfString(payload.primary);
  }
  if (payload.secondary !== undefined) {
    pushIfString(payload.secondary);
  }
  if (payload.icon && typeof payload.icon === "string") {
    pushIfString(payload.icon);
  }
  return ids;
}

function renderReferencedChildren(payload, componentMap, surfaceId, visited, dataContextPath = "/", idSuffix = "") {
  const nodes = [];
  if (!isObject(payload)) {
    return nodes;
  }
  if (typeof payload.child === "string" && payload.child.trim()) {
    nodes.push(renderA2UINode(payload.child.trim(), componentMap, surfaceId, new Set(visited), dataContextPath, idSuffix));
  }
  if (isObject(payload.children)) {
    if (Array.isArray(payload.children.explicitList)) {
      payload.children.explicitList.forEach((id) => {
        if (typeof id !== "string" || !id.trim()) {
          return;
        }
        nodes.push(renderA2UINode(id.trim(), componentMap, surfaceId, new Set(visited), dataContextPath, idSuffix));
      });
    }
    if (isObject(payload.children.template)) {
      const state = getSurfaceState(surfaceId);
      const instances = resolveTemplateInstances(state, payload.children.template, dataContextPath);
      instances.forEach((instance) => {
        nodes.push(renderA2UINode(instance.componentId, componentMap, surfaceId, new Set(visited), instance.dataContextPath, instance.idSuffix));
      });
    }
  }
  return nodes;
}

function resolveRootComponentId(surfaceId, components) {
  const state = surfaceStates.get(surfaceId) || {};
  if (typeof state.rootId === "string" && components.has(state.rootId)) {
    return state.rootId;
  }
  if (components.has("root")) {
    return "root";
  }
  const referenced = new Set();
  components.forEach((item) => {
    const wrapper = item && item.component;
    if (!isObject(wrapper)) {
      return;
    }
    const type = Object.keys(wrapper)[0];
    if (!type || !isObject(wrapper[type])) {
      return;
    }
    collectChildIdsFromPayload(wrapper[type]).forEach((id) => referenced.add(id));
  });
  const firstRoot = [...components.values()].find((item) => item.id && !referenced.has(item.id));
  return firstRoot ? firstRoot.id : [...components.keys()][0];
}

function applyFontToSurface(element, styles) {
  if (!isObject(styles)) {
    return;
  }
  if (typeof styles.font === "string" && styles.font.trim()) {
    element.style.fontFamily = styles.font.trim();
  }
  if (typeof styles.primaryColor === "string" && styles.primaryColor.trim()) {
    element.style.color = styles.primaryColor.trim();
  }
}

function updateSurfaceState(surfaceId, payload) {
  const state = surfaceStates.get(surfaceId) || { rootId: null, styles: {}, componentMap: new Map() };
  if (payload && isObject(payload)) {
    if (typeof payload.root === "string" && payload.root.trim()) {
      state.rootId = payload.root.trim();
    }
    if (isObject(payload.styles)) {
      state.styles = payload.styles;
    }
  }
  state.lastUpdated = Date.now();
  surfaceStates.set(surfaceId, state);
}

function renderA2UINode(componentId, componentMap, surfaceId, visited, dataContextPath = "/", idSuffix = "") {
  if (!visited) {
    visited = new Set();
  }
  const instanceId = `${componentId}${idSuffix}`;
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const state = getSurfaceState(normalizedSurfaceId);
  if (!isObject(componentMap) || !componentMap.has(componentId) || visited.has(instanceId)) {
    const empty = document.createElement("div");
    empty.className = "a2ui-unknown";
    empty.textContent = `Missing or cyclic component: ${instanceId}`;
    return empty;
  }
  const component = componentMap.get(componentId);
  if (!isObject(component)) {
    const empty = document.createElement("div");
    empty.className = "a2ui-unknown";
    empty.textContent = `Invalid component node: ${instanceId}`;
    return empty;
  }
  const wrapper = component.component;
  if (!isObject(wrapper)) {
    const empty = document.createElement("div");
    empty.className = "a2ui-unknown";
    empty.textContent = `Invalid component wrapper: ${instanceId}`;
    return empty;
  }
  const type = Object.keys(wrapper)[0];
  const config = isObject(wrapper[type]) ? wrapper[type] : {};
  visited.add(instanceId);
  let node;
  switch (type) {
    case "Text": {
      const hint = typeof config.usageHint === "string" ? config.usageHint.toLowerCase() : "body";
      const tag = hint === "h1" ? "h1" : hint === "h2" ? "h2" : hint === "h3" ? "h3" : hint === "h4" ? "h4" : hint === "h5" ? "h5" : "p";
      const textNode = document.createElement(tag);
      textNode.className = `a2ui-node-root a2ui-text a2ui-text-${hint}`;
      textNode.textContent = readLiteralOrPath(config.text, normalizedSurfaceId, "", dataContextPath);
      node = textNode;
      break;
    }
    case "Column":
    case "Row": {
      node = document.createElement("div");
      node.className = `a2ui-node-root ${type.toLowerCase()}`;
      node.style.alignItems = mapAlignment(config.alignment);
      node.style.justifyContent = mapDistribution(config.distribution);
      node.style.flexDirection = type === "Column" ? "column" : "row";
      if (type === "Row") {
        const explicitAlign = mapAlignment(config.alignment);
        node.style.alignItems = explicitAlign === "stretch" ? "stretch" : explicitAlign;
      }
      const childNodes = renderReferencedChildren(config, componentMap, normalizedSurfaceId, visited, dataContextPath, idSuffix);
      childNodes.forEach((childNode) => {
        if (childNode) {
          node.appendChild(childNode);
        }
      });
      if (!node.hasChildNodes()) {
        const placeholder = document.createElement("div");
        placeholder.className = "muted";
        placeholder.textContent = "Empty container";
        node.appendChild(placeholder);
      }
      break;
    }
    case "Card": {
      node = document.createElement("div");
      node.className = "a2ui-node-root a2ui-card";
      const childNodes = renderReferencedChildren(config, componentMap, normalizedSurfaceId, visited, dataContextPath, idSuffix);
      childNodes.forEach((childNode) => {
        if (childNode) {
          node.appendChild(childNode);
        }
      });
      break;
    }
    case "Divider": {
      node = document.createElement("hr");
      node.className = "a2ui-divider";
      break;
    }
    case "Button": {
      node = document.createElement("button");
      node.className = "a2ui-node-root a2ui-button";
      const buttonLabel = readLiteralOrPath(config.label, normalizedSurfaceId, "", dataContextPath)
        || readLiteralOrPath(config.text, normalizedSurfaceId, "", dataContextPath)
        || readLiteralOrPath(config.children, normalizedSurfaceId, "", dataContextPath);
      const childNodes = renderReferencedChildren(config, componentMap, normalizedSurfaceId, visited, dataContextPath, idSuffix);
      childNodes.forEach((childNode) => {
        if (childNode) {
          node.appendChild(childNode);
        }
      });
      if (!node.hasChildNodes()) {
        node.textContent = buttonLabel || "Button";
      }
      bindButtonAction(node, normalizedSurfaceId, instanceId, config.action, dataContextPath);
      node.disabled = !!config.disabled;
      break;
    }
    case "Image": {
      const imageUrl = readLiteralOrPath(config.url, normalizedSurfaceId, "", dataContextPath);
      const usageHint = normalizeImageUsageHint(config.usageHint);
      const fitHint = (typeof config.fit === "string" ? config.fit.toLowerCase() : "").trim();
      if (imageUrl) {
        node = document.createElement("img");
        node.className = "a2ui-image";
        node.src = imageUrl;
        node.alt = readLiteralOrPath(config.alt, normalizedSurfaceId, "", dataContextPath) || "A2UI Image";
        if (fitHint) {
          node.classList.add(`a2ui-image--${fitHint.replace(/[^a-z-]/g, "")}`);
        }
        if (usageHint) {
          node.classList.add(`a2ui-image--${usageHint}`);
        }
      } else {
        node = document.createElement("div");
        node.className = "a2ui-unknown";
        node.textContent = "Image: no url";
      }
      break;
    }
    case "Icon": {
      node = document.createElement("span");
      node.className = "a2ui-node-root a2ui-text";
      node.textContent = `[icon] ${readLiteralOrPath(config.name, normalizedSurfaceId, "", dataContextPath) || "icon"}`;
      break;
    }
    case "Video":
    case "AudioPlayer": {
      const mediaUrl = readLiteralOrPath(config.url || config.src, normalizedSurfaceId, "", dataContextPath);
      node = document.createElement(type === "Video" ? "video" : "audio");
      node.className = "a2ui-media";
      node.controls = true;
      node.autoplay = false;
      node.loop = false;
      if (mediaUrl) {
        node.src = mediaUrl;
      } else {
        node.textContent = `${type}: no media source`;
      }
      break;
    }
    case "List": {
      node = document.createElement("div");
      node.className = "a2ui-node-root a2ui-column";
      const childNodes = renderReferencedChildren(config, componentMap, normalizedSurfaceId, visited, dataContextPath, idSuffix);
      if (childNodes.length === 0 && Array.isArray(config.items)) {
        config.items.forEach((item) => {
          const entry = document.createElement("div");
          entry.className = "a2ui-text";
          entry.textContent = isObject(item)
            ? readLiteralOrPath(item.text || item.label || item.value, normalizedSurfaceId, "", dataContextPath)
            : String(item);
          node.appendChild(entry);
        });
      } else {
        childNodes.forEach((childNode) => {
          if (childNode) {
            node.appendChild(childNode);
          }
        });
      }
      break;
    }
    case "TextField": {
      const fieldType = typeof config.textFieldType === "string" ? config.textFieldType.toLowerCase() : "shorttext";
      const label = readLiteralOrPath(config.label, normalizedSurfaceId, "", dataContextPath);
      const valueSource = isObject(config.value)
        ? config.value
        : isObject(config.text)
          ? config.text
          : isObject(config.initialValue)
            ? config.initialValue
            : "";
      const valuePath = isObject(valueSource) && typeof valueSource.path === "string" && valueSource.path.trim()
        ? resolveDataPath(valueSource.path.trim(), dataContextPath)
        : "";
      const inputNode = fieldType === "longtext"
        ? document.createElement("textarea")
        : (() => {
          const input = document.createElement("input");
          input.type = fieldType === "number" ? "number" : fieldType === "date" ? "date" : fieldType === "obscured" ? "password" : "text";
          return input;
        })();
      inputNode.className = "a2ui-node-root";
      if (fieldType === "longtext") {
        inputNode.rows = 4;
        inputNode.className = "a2ui-node-root";
      }
      inputNode.value = readLiteralOrPath(valueSource, normalizedSurfaceId, "", dataContextPath);
      inputNode.placeholder = readLiteralOrPath(config.placeholder, normalizedSurfaceId, "", dataContextPath);
      if (valuePath) {
        state.inputBindings.set(valuePath, inputNode);
        setDataModelValueByPath(state, valuePath, inputNode.value);
        const updateTextModel = () => setDataModelValueByPath(state, valuePath, inputNode.value);
        inputNode.addEventListener("input", updateTextModel);
        inputNode.addEventListener("change", updateTextModel);
      }
      inputNode.disabled = !!config.readOnly;
      if (label) {
        const fieldContainer = document.createElement("div");
        const labelNode = document.createElement("label");
        fieldContainer.className = "a2ui-node-root a2ui-form-field";
        labelNode.className = "a2ui-text a2ui-form-field-label";
        labelNode.textContent = label;
        const fieldId = `textfield-${instanceId.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
        inputNode.id = fieldId;
        labelNode.htmlFor = fieldId;
        fieldContainer.appendChild(labelNode);
        fieldContainer.appendChild(inputNode);
        node = fieldContainer;
      } else {
        node = inputNode;
      }
      break;
    }
    case "DateTimeInput": {
      const label = readLiteralOrPath(config.label, normalizedSurfaceId, "", dataContextPath);
      const inputNode = document.createElement("input");
      inputNode.className = "a2ui-node-root";
      inputNode.type = "datetime-local";
      const valuePath = isObject(config.value) && typeof config.value.path === "string" && config.value.path.trim()
        ? resolveDataPath(config.value.path.trim(), dataContextPath)
        : "";
      inputNode.value = readLiteralOrPath(config.value, normalizedSurfaceId, "", dataContextPath) || "";
      if (valuePath) {
        state.inputBindings.set(valuePath, inputNode);
        setDataModelValueByPath(state, valuePath, inputNode.value);
        const updateDateModel = () => setDataModelValueByPath(state, valuePath, inputNode.value);
        inputNode.addEventListener("change", updateDateModel);
      }
      if (label) {
        const fieldContainer = document.createElement("div");
        const labelNode = document.createElement("label");
        fieldContainer.className = "a2ui-node-root a2ui-form-field";
        labelNode.className = "a2ui-text a2ui-form-field-label";
        labelNode.textContent = label;
        const fieldId = `datetime-${instanceId.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
        inputNode.id = fieldId;
        labelNode.htmlFor = fieldId;
        fieldContainer.appendChild(labelNode);
        fieldContainer.appendChild(inputNode);
        node = fieldContainer;
      } else {
        node = inputNode;
      }
      break;
    }
    case "CheckBox": {
      node = document.createElement("label");
      node.className = "a2ui-node-root a2ui-checkbox";
      const valuePath = isObject(config.value) && typeof config.value.path === "string" && config.value.path.trim()
        ? resolveDataPath(config.value.path.trim(), dataContextPath)
        : "";
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.checked = readBooleanValue(config.value, readBooleanValue(config.checked, false, normalizedSurfaceId, dataContextPath), normalizedSurfaceId, dataContextPath);
      checkbox.disabled = !!config.disabled;
      checkbox.className = "a2ui-input";
      if (valuePath) {
        setDataModelValueByPath(state, valuePath, checkbox.checked);
        const updateCheckboxModel = () => setDataModelValueByPath(state, valuePath, checkbox.checked);
        checkbox.addEventListener("change", updateCheckboxModel);
      }
      node.appendChild(checkbox);
      const text = document.createElement("span");
      text.className = "a2ui-text";
      text.textContent = readLiteralOrPath(config.label, normalizedSurfaceId, "", dataContextPath) || "";
      node.appendChild(text);
      break;
    }
    case "MultipleChoice": {
      node = document.createElement("div");
      node.className = "a2ui-node-root a2ui-multichoice";
      const options = Array.isArray(config.options) ? config.options : [];
      if (options.length === 0) {
        node.textContent = "No options";
        break;
      }
      const selectionsPath = isObject(config.selections) && typeof config.selections.path === "string" && config.selections.path.trim()
        ? resolveDataPath(config.selections.path.trim(), dataContextPath)
        : "";
      const selectedValues = readSelectionValues(config.selections, normalizedSurfaceId, dataContextPath);
      const selectedSet = new Set(selectedValues);
      const isRadio = typeof config.maxAllowedSelections === "number" ? config.maxAllowedSelections <= 1 : true;
      const inputType = isRadio ? "radio" : "checkbox";
      const optionName = `choice_${instanceId.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
      options.forEach((option) => {
        if (!isObject(option)) {
          return;
        }
        const optionNode = document.createElement("label");
        optionNode.className = "a2ui-choice-item";
        const input = document.createElement("input");
        input.type = inputType;
        input.value = String(option.value || "");
        input.disabled = !!option.disabled;
        if (isRadio) {
          input.name = optionName;
        }
        if (selectedSet.has(String(option.value || ""))) {
          input.checked = true;
        }
        if (selectionsPath) {
          const syncSelections = () => {
            const checked = [...node.querySelectorAll(`input[type='${inputType}']`)]
              .filter((el) => (el instanceof HTMLInputElement && el.checked))
              .map((el) => el.value);
            const nextValue = isRadio ? checked.slice(0, 1) : checked;
            setDataModelValueByPath(state, selectionsPath, nextValue);
          };
          input.addEventListener("change", syncSelections);
        }
        const optionText = document.createElement("span");
        optionText.className = "a2ui-text";
        optionText.textContent = readLiteralOrPath(option.label, normalizedSurfaceId, "", dataContextPath) || "";
        optionNode.appendChild(input);
        optionNode.appendChild(optionText);
        node.appendChild(optionNode);
      });
      if (selectionsPath) {
        setDataModelValueByPath(state, selectionsPath, isRadio ? selectedValues.slice(0, 1) : selectedValues);
      }
      break;
    }
    case "Slider": {
      node = document.createElement("input");
      node.className = "a2ui-node-root";
      node.type = "range";
      if (config.min !== undefined) node.min = String(config.min);
      if (config.max !== undefined) node.max = String(config.max);
      if (config.step !== undefined) node.step = String(config.step);
      if (config.value !== undefined) node.value = readLiteralOrPath(config.value, normalizedSurfaceId, "", dataContextPath);
      break;
    }
    default: {
      node = document.createElement("div");
      node.className = "a2ui-node-root a2ui-unknown";
      const title = document.createElement("div");
      title.innerHTML = `<span class="a2ui-badge">${type || "Unknown"}</span>`;
      const detail = document.createElement("pre");
      detail.textContent = safeJSONStringify(config);
      node.appendChild(title);
      node.appendChild(detail);
      break;
    }
  }
  if (node && component.weight !== undefined) {
    const ratio = Number(component.weight);
    if (!Number.isNaN(ratio) && ratio > 0) {
      node.style.flexGrow = String(ratio);
      node.style.flex = node.style.flexGrow;
    }
  }
  return node;
}

function renderSurfaceUpdateMessage(surfaceId, surfaceUpdate) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const components = Array.isArray(surfaceUpdate.components) ? surfaceUpdate.components : [];
  const componentMap = new Map();
  components.forEach((item) => {
    if (!isObject(item)) {
      return;
    }
    if (typeof item.id === "string" && item.id.trim()) {
      componentMap.set(item.id.trim(), item);
    }
  });
  const holder = ensureSurfaceElement(normalizedSurfaceId);
  holder.body.replaceChildren();
  applyFontToSurface(holder.body, surfaceUpdate.styles);
  const state = getSurfaceState(normalizedSurfaceId);
  state.componentMap = componentMap;
  state.lastUpdated = Date.now();
  const rootId = resolveRootComponentId(normalizedSurfaceId, componentMap);
  state.rootId = rootId;
  if (typeof state.surfaceId !== "string") {
    state.surfaceId = normalizedSurfaceId;
  }
  surfaceStates.set(normalizedSurfaceId, state);
  if (!rootId) {
    const empty = document.createElement("div");
    empty.className = "surface-empty";
    empty.textContent = "Surface has no renderable components.";
    holder.body.appendChild(empty);
    return;
  }
  const rootNode = renderA2UINode(rootId, componentMap, normalizedSurfaceId, new Set(), "/", "");
  if (rootNode) {
    rootNode.classList.add("a2ui-node-root");
    holder.body.appendChild(rootNode);
  } else {
    const empty = document.createElement("div");
    empty.className = "surface-empty";
    empty.textContent = `Failed to render component: ${rootId}`;
    holder.body.appendChild(empty);
  }
}

function applyBeginRenderingMessage(surfaceId, begin) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const state = getSurfaceState(normalizedSurfaceId);
  state.rootId = typeof begin.root === "string" && begin.root.trim() ? begin.root.trim() : state.rootId || null;
  state.styles = isObject(begin.styles) ? begin.styles : {};
  state.isRenderingStarted = true;
  state.lastUpdated = Date.now();
  surfaceStates.set(normalizedSurfaceId, state);
  const holder = ensureSurfaceElement(normalizedSurfaceId);
  holder.body.classList.remove("surface-empty");
  applyFontToSurface(holder.body, state.styles);
}

function applyDeleteSurface(surfaceId) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const holder = surfaceElements.get(normalizedSurfaceId);
  if (holder) {
    holder.section.remove();
    surfaceElements.delete(normalizedSurfaceId);
  }
  surfaceStates.delete(normalizedSurfaceId);
}

function resolveDeleteSurfaceId(deleteSurface) {
  if (typeof deleteSurface === "string") {
    return normalizeSurfaceId(deleteSurface);
  }
  if (isObject(deleteSurface) && typeof deleteSurface.surfaceId === "string") {
    return normalizeSurfaceId(deleteSurface.surfaceId);
  }
  if (isObject(deleteSurface) && typeof deleteSurface.id === "string") {
    return normalizeSurfaceId(deleteSurface.id);
  }
  return "";
}

function renderDataModelUpdate(surfaceId) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const holder = ensureSurfaceElement(normalizedSurfaceId);
  const state = getSurfaceState(normalizedSurfaceId);
  const components = state.componentMap instanceof Map ? [...state.componentMap.values()] : [];
  if (components.length === 0) {
    return;
  }
  const rootId = state.rootId || resolveRootComponentId(normalizedSurfaceId, state.componentMap);
  if (!rootId) {
    return;
  }
  const rootNode = renderA2UINode(rootId, state.componentMap, normalizedSurfaceId, new Set(), "/", "");
  holder.body.replaceChildren();
  if (rootNode) {
    rootNode.classList.add("a2ui-node-root");
    holder.body.appendChild(rootNode);
  } else {
    const fallback = document.createElement("div");
    fallback.className = "surface-empty";
    fallback.textContent = `Failed to re-render component: ${rootId}`;
    holder.body.appendChild(fallback);
  }
}

function applyDataModelUpdate(surfaceId, dataModelUpdate) {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const state = getSurfaceState(normalizedSurfaceId);
  const normalizedPath = normalizePath(isObject(dataModelUpdate) ? dataModelUpdate.path : "");
  const contents = isObject(dataModelUpdate) && Array.isArray(dataModelUpdate.contents) ? dataModelUpdate.contents : [];
  if (!contents.length) {
    return;
  }
  const updates = {};
  contents.forEach((entry) => {
    if (!isObject(entry) || typeof entry.key !== "string" || !entry.key.trim()) {
      return;
    }
    updates[entry.key.trim()] = parseDataModelValue(entry);
  });
  if (normalizedPath && normalizedPath !== "/") {
    const currentValue = getDataModelValueByPath(state, normalizedPath);
    const merged = mergeDataModels(
      isObject(currentValue) ? currentValue : {},
      updates,
    );
    setDataModelValueByPath(state, normalizedPath, merged);
  } else {
    state.dataModel = mergeDataModels(state.dataModel || {}, updates);
  }
  renderDataModelUpdate(normalizedSurfaceId);
}

function renderA2UIMessage(a2uiPayload) {
  if (!isObject(a2uiPayload)) {
    return false;
  }
  const envelope = extractA2UIPayload(a2uiPayload);
  if (!envelope) {
    return false;
  }
  if (isObject(envelope.beginRendering)) {
    applyBeginRenderingMessage(envelope.beginRendering.surfaceId, envelope.beginRendering);
    return true;
  }
  if (isObject(envelope.surfaceUpdate)) {
    renderSurfaceUpdateMessage(envelope.surfaceUpdate.surfaceId, envelope.surfaceUpdate);
    return true;
  }
  if (envelope.deleteSurface !== undefined) {
    const deleteSurfaceId = resolveDeleteSurfaceId(envelope.deleteSurface);
    if (deleteSurfaceId) {
      applyDeleteSurface(deleteSurfaceId);
    }
    return true;
  }
  if (isObject(envelope.dataModelUpdate)) {
    const dataModelSurfaceId = resolveSurfaceIdFromPayload(envelope)
      || resolveSurfaceIdFromPayload(envelope.dataModelUpdate);
    applyDataModelUpdate(dataModelSurfaceId, envelope.dataModelUpdate);
    return true;
  }
  return false;
}

function summarizeA2UIMessage(eventPayload) {
  if (!isObject(eventPayload)) {
    return {
      summary: "A2UI text",
      detail: "RAW event includes non-object data.",
      data: eventPayload,
    };
  }
  const surfaceId = resolveSurfaceIdFromPayload(eventPayload);
  if (surfaceId) {
    return {
      summary: "A2UI payload",
      detail: `Surface: ${surfaceId}`,
      data: eventPayload,
    };
  }
  const keys = Object.keys(eventPayload);
  if (!keys.length) {
    return {
      summary: "A2UI empty payload",
      detail: "No keys in A2UI payload.",
      data: eventPayload,
    };
  }
  return {
    summary: `A2UI ${keys[0]}`,
    detail: `Object payload keys: ${keys.join(", ")}`,
    data: eventPayload,
  };
}

function renderAguiEvent(event) {
  const aguiType = (event.type || "").toString();
  const normalized = parseAguiEvent(event);
  if (!normalized) {
    return;
  }
  const eventType = normalized.type || aguiType || "unknown";
  const detail = `Event type: ${eventType}`;
  appendLogItem(aguiLog, `[${normalized.type || aguiType || "unknown"}]`, detail, normalized, false);
  if (eventType.toLowerCase() === "raw") {
    const rawPayload = extractRawPayload(normalized);
    const message = extractRawPayload(rawPayload);
    const source = isObject(rawPayload) && typeof rawPayload.source === "string"
      ? rawPayload.source
      : "";
    const summary = summarizeA2UIMessage(message || rawPayload);
    const sourceSuffix = source ? `  source: ${source}` : "";
    appendLogItem(a2uiLog, `${summary.summary}${sourceSuffix}`, summary.detail, summary.data, true);
    renderA2UIMessage(message || rawPayload);
  }
}

function normalizeContextValue(surfaceId, rawValue, dataContextPath = "/") {
  if (rawValue === null || rawValue === undefined) {
    return null;
  }
  if (typeof rawValue === "string" || typeof rawValue === "number" || typeof rawValue === "boolean") {
    return rawValue;
  }
  if (Array.isArray(rawValue)) {
    return rawValue.map((item) => normalizeContextValue(surfaceId, item, dataContextPath));
  }
  if (isObject(rawValue)) {
    if (typeof rawValue.path === "string" && rawValue.path.trim()) {
      const state = getSurfaceState(surfaceId || "");
      const resolved = getDataModelValueInContext(state, rawValue.path, dataContextPath);
      if (resolved === undefined) {
        return null;
      }
      return normalizeContextValue(surfaceId, resolved, dataContextPath);
    }
    if (typeof rawValue.literalString === "string") {
      return rawValue.literalString;
    }
    if (typeof rawValue.literalNumber === "number") {
      return rawValue.literalNumber;
    }
    if (typeof rawValue.literalBoolean === "boolean") {
      return rawValue.literalBoolean;
    }
    const normalized = {};
    Object.keys(rawValue).forEach((key) => {
      normalized[key] = normalizeContextValue(surfaceId, rawValue[key], dataContextPath);
    });
    return normalized;
  }
  return String(rawValue);
}

function projectActionContextValue(value) {
  if (value === null || value === undefined) {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => projectActionContextValue(item));
  }
  if (!isObject(value)) {
    return value;
  }
  if (Object.prototype.hasOwnProperty.call(value, "selected")) {
    return { selected: projectActionContextValue(value.selected) };
  }
  if (Object.prototype.hasOwnProperty.call(value, "value")) {
    return { value: projectActionContextValue(value.value) };
  }
  if (Object.prototype.hasOwnProperty.call(value, "checked")) {
    return { checked: projectActionContextValue(value.checked) };
  }
  const projected = {};
  Object.keys(value).forEach((key) => {
    projected[key] = projectActionContextValue(value[key]);
  });
  return projected;
}

function clearLogs() {
  aguiLog.replaceChildren();
  a2uiLog.replaceChildren();
  clearRenderedSurfaces();
}

async function streamEvents(url, payload, signal) {
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      accept: "text/event-stream",
    },
    body: JSON.stringify(payload),
    signal,
  });
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`HTTP ${response.status}: ${text || response.statusText}`);
  }
  if (!response.body) {
    throw new Error("Response body is empty");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let buffer = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
    while (true) {
      const boundary = buffer.indexOf("\n\n");
      if (boundary < 0) {
        break;
      }
      const frameText = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const parsed = parseSseFrame(frameText);
      if (!parsed) {
        continue;
      }
      const event = parseAguiEvent(parsed.data);
      if (!event) {
        continue;
      }
      if (event.type && event.type.toLowerCase() === "raw") {
        event.payload = extractRawPayload(parsed.data);
      }
      renderAguiEvent(event);
    }
  }
  const tail = buffer.trim();
  if (tail) {
    const parsed = parseSseFrame(tail);
    if (parsed) {
      const event = parseAguiEvent(parsed.data);
      if (event) {
        if (event.type && event.type.toLowerCase() === "raw") {
          event.payload = extractRawPayload(parsed.data);
        }
        renderAguiEvent(event);
      }
    }
  }
}

function normalizeEndpoint(value) {
  const trimmed = value.trim();
  return trimmed || FORMATS.endpointDefault;
}

let controller = null;
let activeLogStream = "agui";

function normalizeA2uiValue(value) {
  if (value === null || value === undefined) {
    return null;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => normalizeA2uiValue(item));
  }
  if (isObject(value)) {
    if (typeof value.path === "string") {
      return { path: value.path };
    }
    if (typeof value.literalString === "string") {
      return { literalString: value.literalString };
    }
    if (typeof value.literalNumber === "number") {
      return { literalNumber: value.literalNumber };
    }
    if (typeof value.literalBoolean === "boolean") {
      return { literalBoolean: value.literalBoolean };
    }
    const normalized = {};
    Object.keys(value).forEach((key) => {
      normalized[key] = normalizeA2uiValue(value[key]);
    });
    return normalized;
  }
  return String(value);
}

function extractActionContext(surfaceId, rawContext, dataContextPath = "/") {
  const context = {};
  if (!Array.isArray(rawContext)) {
    return context;
  }
  rawContext.forEach((entry) => {
    if (!isObject(entry)) {
      return;
    }
    if (typeof entry.key !== "string" || !entry.key.trim()) {
      return;
    }
    context[entry.key.trim()] = normalizeContextValue(surfaceId, entry.value, dataContextPath);
  });
  return context;
}

function buildUserActionPayload(surfaceId, sourceComponentId, action, dataContextPath = "/") {
  const normalizedSurfaceId = normalizeSurfaceId(surfaceId);
  const actionName = isObject(action) && typeof action.name === "string" && action.name.trim()
    ? action.name.trim()
    : "unknown";
  const context = extractActionContext(normalizedSurfaceId, isObject(action) ? action.context : undefined, dataContextPath);
  if (actionName === "submit_test") {
    if (Object.prototype.hasOwnProperty.call(context, "questions")) {
      context.questions = projectActionContextValue(context.questions);
    }
    if (Object.prototype.hasOwnProperty.call(context, "special")) {
      context.special = projectActionContextValue(context.special);
    }
  }
  return {
    userAction: {
      name: actionName,
      surfaceId: normalizedSurfaceId,
      sourceComponentId: isObject(action) && typeof action.sourceComponentId === "string" && action.sourceComponentId.trim()
        ? action.sourceComponentId.trim()
        : sourceComponentId,
      timestamp: new Date().toISOString(),
      context,
    },
  };
}

function isUserActionButtonActionSupported(actionConfig) {
  if (!isObject(actionConfig)) {
    return false;
  }
  return typeof actionConfig.name === "string" && actionConfig.name.trim().length > 0;
}

function bindButtonAction(node, surfaceId, componentId, actionConfig, dataContextPath = "/") {
  if (!isUserActionButtonActionSupported(actionConfig)) {
    return;
  }
  node.type = "button";
  node.addEventListener("click", async (event) => {
    event.preventDefault();
    const actionPayload = buildUserActionPayload(surfaceId, componentId, actionConfig, dataContextPath);
    await sendUserAction(surfaceId, componentId, actionPayload.userAction);
  });
}

function readThreadIdOrCreate() {
  const threadId = threadInput.value.trim();
  if (threadId) {
    return threadId;
  }
  const next = createThreadId();
  threadInput.value = next;
  localStorage.setItem("a2ui-demo-thread-id", next);
  return next;
}

async function sendRun(payload, title, detail) {
  const endpoint = normalizeEndpoint(endpointInput.value);
  const threadId = readThreadIdOrCreate();
  const runId = payload.runId || createId("run");
  payload.threadId = threadId;
  payload.runId = runId;
  appendLogItem(aguiLog, title, detail || `RunID: ${runId}`, payload, false);
  sendButton.disabled = true;
  setStatus(FORMATS.statusBusy, "busy");
  controller = new AbortController();
  try {
    await streamEvents(endpoint, payload, controller.signal);
    setStatus(FORMATS.statusDone, "ok");
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    appendLogItem(aguiLog, "ERROR", message);
    setStatus(FORMATS.statusError, "error");
  } finally {
    sendButton.disabled = false;
    controller = null;
  }
}

async function sendPrompt(event) {
  event.preventDefault();
  if (controller) {
    return;
  }
  const prompt = promptInput.value.trim();
  if (!prompt) {
    return;
  }
  const payload = {
    messages: [{ role: "user", content: prompt }],
  };
  promptInput.value = "";
  await sendRun(payload, "USER", `Prompt: ${prompt}`);
}

async function sendUserAction(surfaceId, sourceComponentId, userActionPayload) {
  if (!isObject(userActionPayload)) {
    appendLogItem(aguiLog, "USER_ACTION", FORMATS.statusNoAction);
    return;
  }
  if (controller) {
    appendLogItem(aguiLog, "USER_ACTION", FORMATS.statusBusy);
    return;
  }
  const eventPayload = {
    userAction: {
      name: userActionPayload.name,
      surfaceId: normalizeSurfaceId(surfaceId),
      sourceComponentId,
      timestamp: userActionPayload.timestamp || new Date().toISOString(),
      context: userActionPayload.context || {},
    },
  };
  const payload = {
    messages: [{ role: "user", content: JSON.stringify(eventPayload) }],
  };
  await sendRun(payload, "USER_ACTION", `Action: ${userActionPayload.name}`);
}

function initThreadId() {
  const threadId = createThreadId();
  threadInput.value = threadId;
}

function setLogStream(target) {
  if (target !== "agui" && target !== "a2ui") {
    return;
  }
  activeLogStream = target;
  if (aguiLogPanel) {
    aguiLogPanel.classList.toggle("is-hidden", activeLogStream !== "agui");
  }
  if (a2uiLogPanel) {
    a2uiLogPanel.classList.toggle("is-hidden", activeLogStream !== "a2ui");
  }
  if (logSwitchButtons) {
    logSwitchButtons.forEach((button) => {
      const isCurrent = button.dataset.logStream === activeLogStream;
      button.classList.toggle("active", isCurrent);
      button.setAttribute("aria-pressed", isCurrent ? "true" : "false");
    });
  }
}

document.querySelector("#chatForm").addEventListener("submit", sendPrompt);
window.addEventListener("load", () => {
  initThreadId();
  setStatus(FORMATS.statusReady);
  endpointInput.value = FORMATS.endpointDefault;
  if (logSwitchButtons.length) {
    logSwitchButtons.forEach((button) => {
      button.addEventListener("click", () => {
        setLogStream(button.dataset.logStream);
      });
    });
  }
  setLogStream(activeLogStream);
});
