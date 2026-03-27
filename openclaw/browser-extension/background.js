const defaultServerURL = "http://127.0.0.1:19790";
const clientIdKey = "openclawClientId";
const serverURLKey = "openclawServerURL";

let socket = null;
let clientId = "";
let serverURL = defaultServerURL;
let attachedTabs = new Map();

function sortAttachedTabs() {
  return Array.from(attachedTabs.values()).sort((left, right) => {
    return left.tabId - right.tabId;
  });
}

function relayTargetId(tabId) {
  return `tab-${tabId}`;
}

async function ensureSettings() {
  const stored = await chrome.storage.local.get([
    clientIdKey,
    serverURLKey
  ]);
  clientId = stored[clientIdKey] || crypto.randomUUID();
  serverURL = stored[serverURLKey] || defaultServerURL;
  await chrome.storage.local.set({
    [clientIdKey]: clientId,
    [serverURLKey]: serverURL
  });
}

function websocketURL() {
  const url = new URL(serverURL);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = "/extension/ws";
  url.searchParams.set("client_id", clientId);
  return url.toString();
}

async function connectSocket() {
  await ensureSettings();
  if (socket && socket.readyState === WebSocket.OPEN) {
    return;
  }
  socket = new WebSocket(websocketURL());
  socket.addEventListener("open", async () => {
    socket.send(JSON.stringify({
      type: "hello",
      clientId
    }));
    await publishTabs();
  });
  socket.addEventListener("message", async (event) => {
    const message = JSON.parse(event.data);
    if (message.type !== "command") {
      return;
    }
    try {
      const data = await executeCommand(message);
      socket.send(JSON.stringify({
        type: "result",
        id: message.id,
        ok: true,
        data
      }));
    } catch (error) {
      socket.send(JSON.stringify({
        type: "result",
        id: message.id,
        ok: false,
        error: `${error.message || error}`
      }));
    }
  });
  socket.addEventListener("close", () => {
    socket = null;
  });
}

async function publishTabs() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const tabs = [];
  for (const tab of attachedTabs.values()) {
    tabs.push(tab);
  }
  socket.send(JSON.stringify({
    type: "tabs",
    tabs
  }));
}

async function activeTab() {
  const [tab] = await chrome.tabs.query({
    active: true,
    currentWindow: true
  });
  if (!tab || !tab.id) {
    throw new Error("No active tab");
  }
  return tab;
}

async function attachTabByID(tabId) {
  await connectSocket();
  const tab = await chrome.tabs.get(tabId);
  if (!tab.id) {
    throw new Error("No tab id");
  }
  for (const [targetID, attached] of attachedTabs.entries()) {
    attached.active = targetID === relayTargetId(tab.id);
    attachedTabs.set(targetID, attached);
  }
  const payload = {
    clientId,
    targetId: relayTargetId(tab.id),
    tabId: tab.id,
    windowId: tab.windowId,
    title: tab.title || "",
    url: tab.url || "",
    active: true
  };
  attachedTabs.set(payload.targetId, payload);
  await publishTabs();
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({
      type: "attached",
      ...payload
    }));
  }
  return payload;
}

async function attachCurrentTab() {
  const tab = await activeTab();
  return attachTabByID(tab.id);
}

async function detachCurrentTab() {
  const tab = await activeTab();
  const targetId = relayTargetId(tab.id);
  attachedTabs.delete(targetId);
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({
      type: "detached",
      targetId
    }));
  }
  await publishTabs();
}

async function setServerURL(value) {
  serverURL = `${value || ""}`.trim() || defaultServerURL;
  await chrome.storage.local.set({
    [serverURLKey]: serverURL
  });
}

async function statusPayload() {
  await ensureSettings();
  return {
    ok: true,
    clientId,
    serverURL,
    connected: Boolean(socket && socket.readyState === WebSocket.OPEN),
    attachedTabs: sortAttachedTabs()
  };
}

function installTestHooks() {
  globalThis.__openclawRelayTestHooks = {
    attachCurrentTab,
    attachTabByID,
    detachCurrentTab,
    setServerURL,
    connectSocket,
    statusPayload
  };
}

async function executeCommand(command) {
  const tabId = command.tabId;
  switch (command.action) {
    case "focus":
      await chrome.tabs.update(tabId, { active: true });
      return textResult(`Focused tab-${tabId}.`);
    case "close":
      await chrome.tabs.remove(tabId);
      attachedTabs.delete(relayTargetId(tabId));
      return textResult(`Closed tab-${tabId}.`);
    case "navigate":
      await chrome.tabs.update(tabId, { url: command.args.url });
      return textResult(`Navigated to ${command.args.url}`);
    case "snapshot":
      return await captureSnapshotResult(tabId, command.args);
    case "screenshot":
      return await captureTab(tabId, command.args);
    case "cookies_get":
    case "cookies_set":
    case "cookies_clear":
    case "storage_get":
    case "storage_set":
    case "storage_clear":
    case "click":
    case "hover":
    case "type":
    case "select":
    case "fill":
    case "press":
    case "scrollIntoView":
    case "drag":
    case "wait":
    case "evaluate":
      return await executeInTab(tabId, command.action, command.args);
    case "resize": {
      const tab = await chrome.tabs.get(tabId);
      await chrome.windows.update(tab.windowId, {
        width: Number(command.args.width) || 1280,
        height: Number(command.args.height) || 720
      });
      return textResult("Resized window.");
    }
    default:
      throw new Error(`Unsupported relay action: ${command.action}`);
  }
}

async function executeInTab(tabId, action, args) {
  const results = await chrome.scripting.executeScript({
    target: { tabId },
    func: relayExecutor,
    args: [{ action, args }]
  });
  return results[0]?.result;
}

async function captureSnapshotResult(tabId, args = {}) {
  if (`${args.frame || ""}`.trim()) {
    throw new Error("Snapshot frame is not supported by chrome relay");
  }
  if (!args.labels) {
    return await executeInTab(tabId, "snapshot", args);
  }
  const snapshot = await executeInTab(tabId, "snapshot_prepare_labels", args);
  try {
    const image = await captureTab(tabId, { type: "png" });
    return {
      ...snapshot,
      content: [
        ...(snapshot.content || []),
        ...(image.content || [])
      ]
    };
  } finally {
    await executeInTab(tabId, "snapshot_cleanup_labels", {});
  }
}

function screenshotFormat(args = {}) {
  return `${args.type || "png"}`.trim() === "jpeg"
    ? "jpeg"
    : "png";
}

function screenshotMimeType(format) {
  return format === "jpeg" ? "image/jpeg" : "image/png";
}

function base64Payload(dataURL) {
  return dataURL.replace(/^data:image\/[a-zA-Z0-9.+-]+;base64,/, "");
}

function bytesToBase64(bytes) {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(bytes).toString("base64");
  }
  let binary = "";
  const chunkSize = 0x8000;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const chunk = bytes.subarray(index, index + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

async function captureClip(tabId, args = {}) {
  const ref = `${args.ref || ""}`.trim();
  const element = `${args.element || ""}`.trim();
  if (!ref && !element) {
    return null;
  }
  const results = await chrome.scripting.executeScript({
    target: { tabId },
    func: (options) => {
      const refAttr = "data-openclaw-ref";
      const ref = `${options.ref || ""}`.trim();
      const element = `${options.element || ""}`.trim();
      const node = ref
        ? document.querySelector(`[${refAttr}="${ref}"]`)
        : document.querySelector(element);
      if (!node) {
        throw new Error(
          ref ? `Unknown ref: ${ref}` : `Unknown element: ${element}`
        );
      }
      const rect = node.getBoundingClientRect();
      return {
        x: rect.left,
        y: rect.top,
        width: rect.width,
        height: rect.height,
        devicePixelRatio: window.devicePixelRatio || 1
      };
    },
    args: [{
      ref,
      element
    }]
  });
  return results[0]?.result || null;
}

async function cropCapturedImage(dataURL, clip, format) {
  if (!clip) {
    return base64Payload(dataURL);
  }
  if (
    typeof OffscreenCanvas !== "function" ||
    typeof createImageBitmap !== "function" ||
    typeof fetch !== "function"
  ) {
    return base64Payload(dataURL);
  }
  const response = await fetch(dataURL);
  const blob = await response.blob();
  const bitmap = await createImageBitmap(blob);
  try {
    const scale = Number(clip.devicePixelRatio) > 0
      ? Number(clip.devicePixelRatio)
      : 1;
    const x = Math.max(0, Math.floor(Number(clip.x) * scale));
    const y = Math.max(0, Math.floor(Number(clip.y) * scale));
    if (x >= bitmap.width || y >= bitmap.height) {
      throw new Error("Screenshot target is outside the viewport");
    }
    const width = Math.max(
      1,
      Math.min(
        bitmap.width - x,
        Math.ceil(Math.max(0, Number(clip.width)) * scale)
      )
    );
    const height = Math.max(
      1,
      Math.min(
        bitmap.height - y,
        Math.ceil(Math.max(0, Number(clip.height)) * scale)
      )
    );
    const canvas = new OffscreenCanvas(width, height);
    const context = canvas.getContext("2d");
    if (!context) {
      throw new Error("Failed to create screenshot canvas");
    }
    context.drawImage(bitmap, x, y, width, height, 0, 0, width, height);
    const output = await canvas.convertToBlob({
      type: screenshotMimeType(format)
    });
    return bytesToBase64(new Uint8Array(await output.arrayBuffer()));
  } finally {
    if (typeof bitmap.close === "function") {
      bitmap.close();
    }
  }
}

async function captureTab(tabId, args = {}) {
  const tab = await chrome.tabs.get(tabId);
  const format = screenshotFormat(args);
  const data = await chrome.tabs.captureVisibleTab(tab.windowId, {
    format
  });
  const clip = await captureClip(tabId, args);
  return {
    targetId: relayTargetId(tabId),
    content: [{
      type: "image",
      mimeType: screenshotMimeType(format),
      data: await cropCapturedImage(data, clip, format)
    }]
  };
}

function textResult(text) {
  return {
    content: [{ type: "text", text }]
  };
}

async function relayExecutor(command) {
  const refAttr = "data-openclaw-ref";
  const snapshotLabelAttr = "data-openclaw-labels";
  const interactiveSelector = [
    "a[href]",
    "button",
    "input",
    "select",
    "textarea",
    "[role='button']",
    "[role='link']",
    "[contenteditable='true']",
    "[tabindex]"
  ].join(",");
  const meaningfulSelector = [
    "main",
    "section",
    "article",
    "nav",
    "form",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "p",
    "ul",
    "ol",
    "li",
    "label",
    "a[href]",
    "button",
    "input",
    "select",
    "textarea",
    "[role]",
    "[contenteditable='true']"
  ].join(",");

  function textResult(text) {
    return {
      content: [{ type: "text", text }]
    };
  }

  function sleep(ms) {
    return new Promise((resolve) => {
      setTimeout(resolve, ms);
    });
  }

  function visible(node) {
    const style = window.getComputedStyle(node);
    if (style.visibility === "hidden" || style.display === "none") {
      return false;
    }
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }

  function ensureRef(node) {
    let ref = node.getAttribute(refAttr);
    if (!ref) {
      ref = `e${Math.random().toString(36).slice(2, 8)}`;
      node.setAttribute(refAttr, ref);
    }
    return ref;
  }

  function textFor(node) {
    return (
      node.getAttribute("aria-label") ||
      node.getAttribute("placeholder") ||
      node.innerText ||
      node.textContent ||
      node.value ||
      ""
    ).trim().replace(/\s+/g, " ");
  }

  function matchesSelector(node, selector) {
    return typeof node.matches === "function" && node.matches(selector);
  }

  function interactiveNode(node) {
    return matchesSelector(node, interactiveSelector);
  }

  function meaningfulNode(node, interactiveOnly) {
    if (interactiveNode(node)) {
      return true;
    }
    if (interactiveOnly) {
      return false;
    }
    return matchesSelector(node, meaningfulSelector) || textFor(node) !== "";
  }

  function snapshotLine(item, compact) {
    const indent = "  ".repeat(item.depth || 0);
    const ref = item.ref ? `[${item.ref}] ` : "";
    const label = item.text ? ` "${item.text}"` : "";
    if (compact) {
      return `${indent}${ref}${item.role}${label}`;
    }
    const tag = item.tag && item.tag !== item.role
      ? ` tag=${item.tag}`
      : "";
    const kind = item.type ? ` type=${item.type}` : "";
    const disabled = item.disabled ? " disabled" : "";
    return `${indent}${ref}${item.role}${tag}${kind}${disabled}${label}`;
  }

  function snapshotOptions(args) {
    const frame = `${args.frame || ""}`.trim();
    if (frame) {
      throw new Error("Snapshot frame is not supported by chrome relay");
    }
    const snapshotFormat = `${args.snapshotFormat || ""}`.trim();
    const refs = `${args.refs || ""}`.trim();
    if (
      (snapshotFormat && snapshotFormat !== "role") ||
      (refs && refs !== "role")
    ) {
      throw new Error("chrome relay snapshots only support role refs");
    }
    const mode = `${args.mode || ""}`.trim();
    const depth = Number(args.depth);
    return {
      selector: `${args.selector || ""}`.trim(),
      limit: Math.max(0, Number(args.limit) || 0) || 200,
      interactive: args.interactive !== false,
      compact: args.compact === true || mode === "efficient",
      depth: Number.isFinite(depth) && depth >= 0
        ? depth
        : mode === "efficient"
          ? 6
          : undefined
    };
  }

  function snapshot(args) {
    const options = snapshotOptions(args);
    const selector = options.selector;
    const root = selector
      ? document.querySelector(selector)
      : document.body || document.documentElement;
    if (!root) {
      throw new Error(`Snapshot selector not found: ${selector}`);
    }
    const items = [];
    const stack = [{
      node: root,
      depth: 0
    }];
    while (stack.length > 0 && items.length < options.limit) {
      const current = stack.pop();
      const node = current?.node;
      if (!node || !visible(node)) {
        continue;
      }
      const include = meaningfulNode(node, options.interactive);
      if (include) {
        if (options.depth === undefined || current.depth <= options.depth) {
          items.push({
            ref: interactiveNode(node) ? ensureRef(node) : "",
            role: node.getAttribute("role") || node.tagName.toLowerCase(),
            text: textFor(node),
            tag: node.tagName.toLowerCase(),
            type: node.getAttribute("type") || "",
            disabled: Boolean(node.disabled),
            depth: current.depth
          });
        }
      }
      const nextDepth = include ? current.depth + 1 : current.depth;
      if (options.depth !== undefined && nextDepth > options.depth) {
        continue;
      }
      const children = Array.from(node.children || []);
      for (let index = children.length - 1; index >= 0; index -= 1) {
        stack.push({
          node: children[index],
          depth: nextDepth
        });
      }
    }
    const lines = [
      `Page: ${document.title || ""}`,
      `URL: ${window.location.href}`
    ];
    if (selector) {
      lines.push(`Selector: ${selector}`);
    }
    for (const item of items) {
      lines.push(snapshotLine(item, options.compact));
    }
    return {
      snapshot: {
        title: document.title || "",
        url: window.location.href,
        items,
        text: lines.join("\n")
      },
      content: [{ type: "text", text: lines.join("\n") }]
    };
  }

  function clearSnapshotLabels() {
    document.querySelectorAll(`[${snapshotLabelAttr}]`).forEach((node) => {
      node.remove();
    });
  }

  function applySnapshotLabels(items) {
    clearSnapshotLabels();
    let labels = 0;
    let skipped = 0;
    for (const item of items) {
      if (!item.ref) {
        continue;
      }
      const node = byRef(item.ref);
      const rect = node.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) {
        skipped += 1;
        continue;
      }
      const box = document.createElement("div");
      box.setAttribute(snapshotLabelAttr, "1");
      box.style.position = "fixed";
      box.style.left = `${rect.left}px`;
      box.style.top = `${rect.top}px`;
      box.style.width = `${rect.width}px`;
      box.style.height = `${rect.height}px`;
      box.style.border = "2px solid #d83b01";
      box.style.background = "rgba(216, 59, 1, 0.08)";
      box.style.pointerEvents = "none";
      box.style.zIndex = "2147483647";

      const tag = document.createElement("div");
      tag.setAttribute(snapshotLabelAttr, "1");
      tag.textContent = item.ref;
      tag.style.position = "fixed";
      tag.style.left = `${Math.max(0, rect.left)}px`;
      tag.style.top = `${Math.max(0, rect.top - 20)}px`;
      tag.style.padding = "1px 4px";
      tag.style.background = "#d83b01";
      tag.style.color = "#fff";
      tag.style.font = "12px monospace";
      tag.style.borderRadius = "4px";
      tag.style.pointerEvents = "none";
      tag.style.zIndex = "2147483647";

      document.body.append(box, tag);
      labels += 1;
    }
    return { labels, skipped };
  }

  function byRef(ref) {
    const node = document.querySelector(`[${refAttr}="${ref}"]`);
    if (!node) {
      throw new Error(`Unknown ref: ${ref}`);
    }
    return node;
  }

  function setValue(node, value) {
    node.focus();
    if ("value" in node) {
      node.value = value;
    }
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }

  function modifierState(modifiers) {
    const state = {
      altKey: false,
      ctrlKey: false,
      metaKey: false,
      shiftKey: false
    };
    for (const modifier of modifiers || []) {
      switch (`${modifier || ""}`.trim().toLowerCase()) {
        case "alt":
          state.altKey = true;
          break;
        case "control":
        case "ctrl":
          state.ctrlKey = true;
          break;
        case "meta":
        case "cmd":
        case "command":
          state.metaKey = true;
          break;
        case "shift":
          state.shiftKey = true;
          break;
        default:
          break;
      }
    }
    return state;
  }

  function buttonCode(button) {
    switch (`${button || "left"}`.trim().toLowerCase()) {
      case "middle":
        return 1;
      case "right":
        return 2;
      default:
        return 0;
    }
  }

  function mouseOptions(node, args, detail) {
    const rect = node.getBoundingClientRect();
    return {
      bubbles: true,
      cancelable: true,
      button: buttonCode(args.button),
      buttons: 1,
      detail,
      clientX: rect.left + rect.width / 2,
      clientY: rect.top + rect.height / 2,
      ...modifierState(args.modifiers)
    };
  }

  function clickNode(node, args) {
    const hasModifiers = (args.modifiers || []).length > 0;
    if (!args.doubleClick &&
      `${args.button || "left"}`.trim().toLowerCase() === "left" &&
      !hasModifiers &&
      typeof node.click === "function") {
      node.click();
      return;
    }
    const options = mouseOptions(node, args, 1);
    node.dispatchEvent(new MouseEvent("mousedown", options));
    node.dispatchEvent(new MouseEvent("mouseup", options));
    node.dispatchEvent(new MouseEvent("click", options));
    if (args.doubleClick) {
      const repeat = mouseOptions(node, args, 2);
      node.dispatchEvent(new MouseEvent("mousedown", repeat));
      node.dispatchEvent(new MouseEvent("mouseup", repeat));
      node.dispatchEvent(new MouseEvent("click", repeat));
      node.dispatchEvent(new MouseEvent("dblclick", repeat));
    }
  }

  async function typeNode(node, args) {
    const text = `${args.text || ""}`;
    if (!args.slowly) {
      setValue(node, text);
    } else {
      setValue(node, "");
      let current = "";
      for (const char of text) {
        current += char;
        setValue(node, current);
        await sleep(50);
      }
    }
    if (args.submit) {
      node.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true
      }));
      if (node.form && typeof node.form.requestSubmit === "function") {
        node.form.requestSubmit();
      }
    }
  }

  async function pressKey(args) {
    const node = document.activeElement || document.body;
    const init = {
      key: `${args.key || ""}`,
      bubbles: true,
      ...modifierState(args.modifiers)
    };
    node.dispatchEvent(new KeyboardEvent("keydown", init));
    await sleep(Math.max(0, Number(args.delayMs) || 0));
    node.dispatchEvent(new KeyboardEvent("keyup", init));
  }

  function scrollNode(node) {
    if (typeof node.scrollIntoView === "function") {
      node.scrollIntoView({
        block: "center",
        inline: "nearest"
      });
    }
  }

  async function waitFor(predicate, timeoutMs) {
    const startedAt = Date.now();
    while (!predicate()) {
      if (Date.now() - startedAt >= timeoutMs) {
        throw new Error("Wait timed out");
      }
      await sleep(50);
    }
  }

  function loadStateReached(state) {
    switch (`${state || ""}`.trim()) {
      case "domcontentloaded":
        return document.readyState !== "loading";
      case "load":
      case "networkidle":
      default:
        return document.readyState === "complete";
    }
  }

  async function waitForArgs(args) {
    const timeoutMs = Math.max(1, Number(args.timeoutMs) || 30000);
    if (args.selector) {
      await waitFor(() => Boolean(document.querySelector(args.selector)),
        timeoutMs);
      return textResult(`Selector appeared: ${args.selector}`);
    }
    if (args.url) {
      await waitFor(() => window.location.href === args.url, timeoutMs);
      return textResult(`URL matched: ${args.url}`);
    }
    if (args.loadState) {
      await waitFor(() => loadStateReached(args.loadState), timeoutMs);
      return textResult(`Load state reached: ${args.loadState}`);
    }
    if (args.text) {
      await waitFor(() => {
        return document.body?.innerText?.includes(args.text);
      }, timeoutMs);
      return textResult(`Text appeared: ${args.text}`);
    }
    if (args.textGone) {
      await waitFor(() => {
        return !document.body?.innerText?.includes(args.textGone);
      }, timeoutMs);
      return textResult(`Text disappeared: ${args.textGone}`);
    }
    const fnText = `${args.fn || args.function || ""}`.trim();
    if (fnText) {
      const parsed = parseEvaluateExpression(fnText);
      if (!parsed) {
        throw new Error("relay wait requires an arrow function");
      }
      await waitFor(() => {
        return Boolean(evaluateParsedArgs(args, parsed));
      }, timeoutMs);
      return textResult("Wait predicate matched.");
    }
    const waitMs = Math.max(0, Number(args.timeMs) || 0) ||
      Math.max(0, Number(args.time) || 1) * 1000;
    await sleep(waitMs);
    return textResult("Wait completed.");
  }

  async function dragBetween(args) {
    const start = byRef(args.startRef);
    const end = byRef(args.endRef);
    const DragEventType = typeof DragEvent === "function"
      ? DragEvent
      : MouseEvent;
    const data = typeof DataTransfer === "function"
      ? new DataTransfer()
      : undefined;
    const init = {
      bubbles: true,
      cancelable: true,
      dataTransfer: data
    };
    start.dispatchEvent(new DragEventType("dragstart", init));
    end.dispatchEvent(new DragEventType("dragenter", init));
    end.dispatchEvent(new DragEventType("dragover", init));
    end.dispatchEvent(new DragEventType("drop", init));
    start.dispatchEvent(new DragEventType("dragend", init));
    return textResult(`Dragged ${args.startRef} -> ${args.endRef}.`);
  }

  function escapePattern(text) {
    return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  function parseEvaluateExpression(fnText) {
    const block = fnText.match(
      /^\s*\(?\s*([A-Za-z_$][\w$]*)?\s*\)?\s*=>\s*\{\s*return\s+([\s\S]+?);\s*\}\s*$/
    );
    if (block) {
      return {
        parameter: block[1] || "",
        expression: block[2].trim()
      };
    }
    const inline = fnText.match(
      /^\s*\(?\s*([A-Za-z_$][\w$]*)?\s*\)?\s*=>\s*([\s\S]+)$/
    );
    if (!inline) {
      return null;
    }
    return {
      parameter: inline[1] || "",
      expression: inline[2].trim()
    };
  }

  function evaluatePageExpression(expression) {
    const normalized = expression.replace(/\s+/g, "");
    switch (normalized) {
      case "document.title":
        return document.title || "";
      case "document.URL":
      case "window.location.href":
      case "location.href":
        return window.location.href;
      case "document.body.innerText":
        return document.body?.innerText || "";
      case "document.body.textContent":
        return document.body?.textContent || "";
      default:
        throw new Error(
          `Unsupported relay evaluate expression: ${expression}`
        );
    }
  }

  function evaluateElementExpression(node, expression, parameter) {
    const alias = parameter ? parameter : "element";
    const normalized = expression
      .replace(new RegExp(`\\b${escapePattern(alias)}\\b`, "g"), "element")
      .replace(/\s+/g, "");
    switch (normalized) {
      case "element.textContent":
        return node.textContent || "";
      case "element.innerText":
        return node.innerText || "";
      case "element.value":
        return node.value || "";
      case "element.outerHTML":
        return node.outerHTML || "";
      case "element.innerHTML":
        return node.innerHTML || "";
      case "element.href":
        return node.href || "";
      default: {
        const attrMatch = normalized.match(
          /^element\.getAttribute\((['"])([^'"]+)\1\)$/
        );
        if (attrMatch) {
          return node.getAttribute(attrMatch[2]);
        }
        throw new Error(
          `Unsupported relay evaluate expression: ${expression}`
        );
      }
    }
  }

  function evaluateParsedArgs(args, parsed) {
    return args.ref
      ? evaluateElementExpression(
          byRef(args.ref),
          parsed.expression,
          parsed.parameter
        )
      : evaluatePageExpression(parsed.expression);
  }

  function evaluateArgs(args) {
    const fnText = `${args.fn || args.function || ""}`.trim();
    if (!fnText) {
      throw new Error("evaluate requires fn");
    }
    const parsed = parseEvaluateExpression(fnText);
    if (!parsed) {
      throw new Error("relay evaluate requires an arrow function");
    }
    const value = evaluateParsedArgs(args, parsed);
    const text = JSON.stringify(value, null, 2);
    return {
      value,
      content: [{
        type: "text",
        text: text === undefined ? String(value) : text
      }]
    };
  }

  function cookiePairs() {
    const raw = `${document.cookie || ""}`.trim();
    if (!raw) {
      return [];
    }
    return raw
      .split(/;\s*/)
      .filter(Boolean)
      .map((entry) => {
        const [name, ...valueParts] = entry.split("=");
        return {
          name: decodeURIComponent(name || ""),
          value: decodeURIComponent(valueParts.join("=") || "")
        };
      });
  }

  function cookiesGet() {
    const cookies = cookiePairs();
    const text = cookies.length === 0
      ? "No cookies."
      : cookies.map((cookie) => {
          return `${cookie.name}=${cookie.value}`;
        }).join("\n");
    return {
      cookies,
      content: [{
        type: "text",
        text
      }]
    };
  }

  function cookiesSet(args) {
    const cookie = args.cookie && typeof args.cookie === "object"
      ? args.cookie
      : args;
    const name = `${cookie.name || ""}`.trim();
    if (!name) {
      throw new Error("cookie name is required");
    }
    if (cookie.value === undefined) {
      throw new Error("cookie value is required");
    }
    const parts = [
      `${encodeURIComponent(name)}=` +
        `${encodeURIComponent(`${cookie.value || ""}`)}`
    ];
    const cookiePath = `${cookie.path || "/"}`.trim() || "/";
    parts.push(`path=${cookiePath}`);
    const domain = `${cookie.domain || ""}`.trim();
    if (domain) {
      parts.push(`domain=${domain}`);
    }
    const sameSite = `${cookie.sameSite || ""}`.trim();
    if (sameSite) {
      parts.push(`SameSite=${sameSite}`);
    }
    if (cookie.secure) {
      parts.push("Secure");
    }
    document.cookie = parts.join("; ");
    return textResult(`Set cookie ${name}.`);
  }

  function cookiesClear() {
    for (const cookie of cookiePairs()) {
      document.cookie = [
        `${encodeURIComponent(cookie.name)}=`,
        "expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "path=/"
      ].join("; ");
    }
    return textResult("Cleared visible page cookies.");
  }

  function storageKind(kind) {
    return `${kind || "local"}`.trim() === "session"
      ? window.sessionStorage
      : window.localStorage;
  }

  function storageGet(args) {
    const store = storageKind(args.kind);
    const key = `${args.key || ""}`.trim();
    const values = {};
    if (key) {
      const value = store.getItem(key);
      if (value !== null) {
        values[key] = value;
      }
    } else {
      for (let index = 0; index < store.length; index += 1) {
        const itemKey = store.key(index);
        if (!itemKey) {
          continue;
        }
        const value = store.getItem(itemKey);
        if (value !== null) {
          values[itemKey] = value;
        }
      }
    }
    const entries = Object.entries(values);
    return {
      values,
      content: [{
        type: "text",
        text: entries.length === 0
          ? `No ${args.kind || "local"}Storage values.`
          : entries.map(([entryKey, entryValue]) => {
              return `${entryKey}=${entryValue}`;
            }).join("\n")
      }]
    };
  }

  function storageSet(args) {
    const key = `${args.key || ""}`.trim();
    if (!key) {
      throw new Error("storage key is required");
    }
    storageKind(args.kind).setItem(key, `${args.value || ""}`);
    return textResult(
      `Set ${args.kind || "local"}Storage ${key}.`
    );
  }

  function storageClear(args) {
    storageKind(args.kind).clear();
    return textResult(
      `Cleared ${args.kind || "local"}Storage.`
    );
  }

  const { action, args = {} } = command;
  switch (action) {
    case "snapshot":
      return snapshot(args);
    case "snapshot_prepare_labels": {
      const result = snapshot(args);
      const labels = applySnapshotLabels(result.snapshot.items || []);
      return {
        ...result,
        labels: true,
        labelsCount: labels.labels,
        labelsSkipped: labels.skipped
      };
    }
    case "snapshot_cleanup_labels":
      clearSnapshotLabels();
      return { ok: true };
    case "click":
      clickNode(byRef(args.ref), args);
      return textResult(`Clicked ${args.ref}.`);
    case "hover":
      byRef(args.ref).dispatchEvent(new MouseEvent("mouseover", {
        bubbles: true
      }));
      return textResult(`Hovered ${args.ref}.`);
    case "type":
      await typeNode(byRef(args.ref), args);
      return textResult(`Typed into ${args.ref}.`);
    case "select": {
      const node = byRef(args.ref);
      const values = new Set(args.values || []);
      for (const option of Array.from(node.options || [])) {
        option.selected = values.has(option.value);
      }
      node.dispatchEvent(new Event("input", { bubbles: true }));
      node.dispatchEvent(new Event("change", { bubbles: true }));
      return textResult(`Selected ${args.ref}.`);
    }
    case "fill":
      for (const field of args.fields || []) {
        setValue(byRef(field.ref), `${field.text || ""}`);
      }
      return textResult("Filled form fields.");
    case "press":
      await pressKey(args);
      return textResult(`Pressed ${args.key}.`);
    case "scrollIntoView":
      scrollNode(byRef(args.ref));
      return textResult(`Scrolled ${args.ref} into view.`);
    case "drag":
      return await dragBetween(args);
    case "evaluate":
      return evaluateArgs(args);
    case "cookies_get":
      return cookiesGet();
    case "cookies_set":
      return cookiesSet(args);
    case "cookies_clear":
      return cookiesClear();
    case "storage_get":
      return storageGet(args);
    case "storage_set":
      return storageSet(args);
    case "storage_clear":
      return storageClear(args);
    case "wait":
      return await waitForArgs(args);
    default:
      throw new Error(`Unsupported action: ${action}`);
  }
}

chrome.runtime.onInstalled.addListener(() => {
  connectSocket().catch(() => {});
});

chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  for (const [targetID, attached] of attachedTabs.entries()) {
    attached.active = targetID === relayTargetId(tabId);
    attachedTabs.set(targetID, attached);
  }
  if (attachedTabs.size > 0) {
    await publishTabs();
  }
});

chrome.tabs.onUpdated.addListener(async (tabId, changeInfo, tab) => {
  const targetId = relayTargetId(tabId);
  const attached = attachedTabs.get(targetId);
  if (!attached) {
    return;
  }
  attached.title = changeInfo.title || tab.title || attached.title;
  attached.url = changeInfo.url || tab.url || attached.url;
  attached.active = tab.active ?? attached.active;
  attachedTabs.set(targetId, attached);
  await publishTabs();
});

chrome.tabs.onRemoved.addListener(async (tabId) => {
  const targetId = relayTargetId(tabId);
  if (!attachedTabs.delete(targetId)) {
    return;
  }
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({
      type: "detached",
      targetId
    }));
  }
  await publishTabs();
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  (async () => {
    switch (message.type) {
      case "status":
        sendResponse(await statusPayload());
        return;
      case "set_server_url":
        await setServerURL(message.serverURL);
        await connectSocket();
        sendResponse({ ok: true });
        return;
      case "connect":
        await connectSocket();
        sendResponse(await statusPayload());
        return;
      case "attach_current_tab":
        sendResponse({ ok: true, tab: await attachCurrentTab() });
        return;
      case "attach_tab_by_id":
        sendResponse({
          ok: true,
          tab: await attachTabByID(Number(message.tabId))
        });
        return;
      case "detach_current_tab":
        await detachCurrentTab();
        sendResponse({ ok: true });
        return;
      default:
        sendResponse({ ok: false, error: "Unknown action" });
    }
  })().catch((error) => {
    sendResponse({
      ok: false,
      error: `${error.message || error}`
    });
  });
  return true;
});

installTestHooks();
