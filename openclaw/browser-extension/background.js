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
    case "wait":
      return await executeInTab(tabId, command.action, command.args);
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

function relayExecutor(command) {
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

  function snapshot() {
    const items = Array.from(document.querySelectorAll(interactiveSelector))
      .filter(visible)
      .slice(0, 200)
      .map(describe);
    const lines = [
      `Page: ${document.title || ""}`,
      `URL: ${window.location.href}`
    ];
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
    return document.querySelector(`[${refAttr}="${ref}"]`);
  }

  function setValue(node, value) {
    node.focus();
    if ("value" in node) {
      node.value = value;
    }
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }

  const { action, args = {} } = command;
  switch (action) {
    case "snapshot":
      return snapshot();
    case "click":
      byRef(args.ref)?.click();
      return textResult(`Clicked ${args.ref}.`);
    case "hover":
      byRef(args.ref)?.dispatchEvent(new MouseEvent("mouseover", {
        bubbles: true
      }));
      return textResult(`Hovered ${args.ref}.`);
    case "type":
      setValue(byRef(args.ref), `${args.text || ""}`);
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
      (document.activeElement || document.body).dispatchEvent(
        new KeyboardEvent("keydown", {
          key: `${args.key || ""}`,
          bubbles: true
        })
      );
      return textResult(`Pressed ${args.key}.`);
    case "wait":
      return textResult("Wait completed.");
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
