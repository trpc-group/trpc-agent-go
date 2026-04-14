import { randomUUID } from "node:crypto";

function createDeferred(timeoutMs = 30000) {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  const timer = setTimeout(() => {
    reject(new Error("Chrome relay command timed out"));
  }, timeoutMs);
  return {
    promise,
    resolve(value) {
      clearTimeout(timer);
      resolve(value);
    },
    reject(error) {
      clearTimeout(timer);
      reject(error);
    }
  };
}

function textContent(text) {
  return {
    content: [{ type: "text", text }]
  };
}

export class ChromeRelay {
  constructor() {
    this.clients = new Map();
    this.tabs = new Map();
    this.pending = new Map();
  }

  registerSocket(clientId, socket) {
    this.clients.set(clientId, {
      socket,
      connectedAt: Date.now()
    });
  }

  unregisterSocket(clientId) {
    this.clients.delete(clientId);
    for (const [targetId, tab] of this.tabs.entries()) {
      if (tab.clientId === clientId) {
        this.tabs.delete(targetId);
      }
    }
  }

  updateAttachedTab(payload) {
    const targetId = payload.targetId || `tab-${payload.tabId}`;
    this.tabs.set(targetId, {
      targetId,
      clientId: payload.clientId,
      tabId: payload.tabId,
      title: payload.title || "",
      url: payload.url || "",
      windowId: payload.windowId || 0,
      active: Boolean(payload.active)
    });
    return this.tabs.get(targetId);
  }

  detachTab(targetId) {
    this.tabs.delete(targetId);
  }

  listTabs() {
    return Array.from(this.tabs.values()).sort((a, b) => {
      return a.tabId - b.tabId;
    });
  }

  listClients() {
    return Array.from(this.clients.entries())
      .map(([clientId, client]) => {
        return {
          clientId,
          connectedAt: client.connectedAt,
          tabCount: this.listTabs().filter((tab) => {
            return tab.clientId === clientId;
          }).length
        };
      })
      .sort((a, b) => {
        return a.clientId.localeCompare(b.clientId);
      });
  }

  formatTabsText() {
    const tabs = this.listTabs();
    if (tabs.length === 0) {
      return "No Chrome relay tabs are attached.";
    }
    return tabs
      .map((tab) => {
        const marker = tab.active ? ">" : " ";
        const title = tab.title || "(untitled)";
        const url = tab.url || "";
        return `${marker} ${tab.tabId} ${title} - ${url}`.trim();
      })
      .join("\n");
  }

  async execute(targetId, action, args) {
    const tab = this.resolveTab(targetId);
    const client = this.clients.get(tab.clientId);
    if (!client || client.socket.readyState !== 1) {
      throw new Error("Chrome relay client is not connected");
    }

    const id = randomUUID();
    const deferred = createDeferred();
    this.pending.set(id, deferred);
    client.socket.send(JSON.stringify({
      type: "command",
      id,
      targetId: tab.targetId,
      tabId: tab.tabId,
      action,
      args
    }));
    try {
      return await deferred.promise;
    } finally {
      this.pending.delete(id);
    }
  }

  handleMessage(clientId, message) {
    switch (message.type) {
      case "hello":
        return;
      case "attached":
        this.updateAttachedTab({
          ...message,
          clientId
        });
        return;
      case "detached":
        this.detachTab(message.targetId);
        return;
      case "tabs":
        for (const tab of message.tabs || []) {
          this.updateAttachedTab({
            ...tab,
            clientId
          });
        }
        return;
      case "result": {
        const deferred = this.pending.get(message.id);
        if (!deferred) {
          return;
        }
        if (message.ok) {
          deferred.resolve(message.data);
        } else {
          deferred.reject(new Error(message.error || "Chrome relay failed"));
        }
        return;
      }
      default:
        return;
    }
  }

  resolveTab(targetId) {
    if (targetId) {
      const tab = this.tabs.get(targetId);
      if (!tab) {
        throw new Error(`Chrome relay target is not attached: ${targetId}`);
      }
      return tab;
    }
    const tabs = this.listTabs();
    const active = tabs.find((tab) => tab.active);
    if (active) {
      return active;
    }
    if (tabs.length === 1) {
      return tabs[0];
    }
    throw new Error("Chrome relay target is ambiguous; provide targetId");
  }

  tabsResult() {
    return {
      tabs: this.listTabs(),
      ...textContent(this.formatTabsText())
    };
  }

  status() {
    return {
      clients: this.listClients(),
      tabs: this.listTabs()
    };
  }
}
