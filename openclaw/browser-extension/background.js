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
      return await executeInTab(tabId, "snapshot", command.args);
    case "screenshot":
      return await captureTab(tabId);
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

async function captureTab(tabId) {
  const tab = await chrome.tabs.get(tabId);
  const data = await chrome.tabs.captureVisibleTab(tab.windowId, {
    format: "png"
  });
  return {
    targetId: relayTargetId(tabId),
    content: [{
      type: "image",
      mimeType: "image/png",
      data: data.replace(/^data:image\/png;base64,/, "")
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

  function describe(node) {
    const text = (
      node.getAttribute("aria-label") ||
      node.getAttribute("placeholder") ||
      node.innerText ||
      node.textContent ||
      node.value ||
      ""
    ).trim().replace(/\s+/g, " ");
    return {
      ref: ensureRef(node),
      role: node.getAttribute("role") || node.tagName.toLowerCase(),
      text
    };
  }

  function snapshot(args) {
    const selector = `${args.selector || ""}`.trim();
    const root = selector
      ? document.querySelector(selector)
      : document;
    if (!root) {
      throw new Error(`Snapshot selector not found: ${selector}`);
    }
    const limit = Math.max(0, Number(args.limit) || 0);
    const discovered = Array.from(root.querySelectorAll(interactiveSelector));
    if (
      root !== document &&
      typeof root.matches === "function" &&
      root.matches(interactiveSelector)
    ) {
      discovered.unshift(root);
    }
    const items = Array.from(new Set(discovered))
      .filter(visible)
      .slice(0, limit || 200)
      .map(describe);
    const lines = [
      `Page: ${document.title || ""}`,
      `URL: ${window.location.href}`
    ];
    if (selector) {
      lines.push(`Selector: ${selector}`);
    }
    for (const item of items) {
      const label = item.text ? ` "${item.text}"` : "";
      lines.push(`[${item.ref}] ${item.role}${label}`);
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

  const { action, args = {} } = command;
  switch (action) {
    case "snapshot":
      return snapshot(args);
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
