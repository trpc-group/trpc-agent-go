import { WebSocketServer } from "ws";
import { ChromeRelay } from "./chrome-relay.js";
import { HostProfile } from "./host-profile.js";

function textResult(text, extra = {}) {
  return {
    ...extra,
    content: [{ type: "text", text }]
  };
}

export class BrowserRuntime {
  constructor(config) {
    this.config = config;
    this.hostProfile = new HostProfile(config);
    this.chromeRelay = new ChromeRelay();
    this.wsServer = null;
  }

  attachWebSocketServer(server) {
    this.wsServer = new WebSocketServer({
      noServer: true
    });
    server.on("upgrade", (request, socket, head) => {
      if (!request.url.startsWith("/extension/ws")) {
        return;
      }
      if (this.config.token) {
        const url = new URL(request.url, "http://127.0.0.1");
        const token = `${url.searchParams.get("token") || ""}`.trim();
        if (token !== this.config.token) {
          socket.write(
            "HTTP/1.1 401 Unauthorized\r\n" +
              "Connection: close\r\n\r\n"
          );
          socket.destroy();
          return;
        }
      }
      this.wsServer.handleUpgrade(request, socket, head, (ws) => {
        const url = new URL(request.url, "http://127.0.0.1");
        const clientId = url.searchParams.get("client_id");
        if (!clientId) {
          ws.close();
          return;
        }
        this.chromeRelay.registerSocket(clientId, ws);
        ws.on("message", (raw) => {
          try {
            const message = JSON.parse(`${raw || ""}`);
            this.chromeRelay.handleMessage(clientId, message);
          } catch (_error) {
            // Ignore malformed relay messages.
          }
        });
        ws.on("close", () => {
          this.chromeRelay.unregisterSocket(clientId);
        });
      });
    });
  }

  async status(profile) {
    if (profile === "chrome") {
      return {
        state: "ready",
        tabs: this.chromeRelay.listTabs()
      };
    }
    return this.hostProfile.status();
  }

  async start(profile) {
    if (profile === "chrome") {
      return textResult("Chrome relay is ready when the extension connects.");
    }
    const status = await this.hostProfile.start();
    return textResult("Started browser profile.", status);
  }

  async stop(profile) {
    if (profile === "chrome") {
      return textResult("Chrome relay remains managed by the browser.");
    }
    const status = await this.hostProfile.stop();
    return textResult("Stopped browser profile.", status);
  }

  async profiles() {
    return {
      profiles: [
        {
          name: "openclaw",
          state: (await this.hostProfile.status()).state,
          driver: "playwright"
        },
        {
          name: "chrome",
          state: "ready",
          driver: "extension-relay",
          tabs: this.chromeRelay.listTabs().length
        }
      ]
    };
  }

  async extensionStatus() {
    return this.chromeRelay.status();
  }

  async tabs(profile) {
    if (profile === "chrome") {
      return this.chromeRelay.tabsResult();
    }
    return this.hostProfile.tabsResult();
  }

  async open(profile, url) {
    if (profile === "chrome") {
      const tab = this.chromeRelay.resolveTab();
      return this.chromeRelay.execute(tab.targetId, "navigate", { url });
    }
    const result = await this.hostProfile.openTab(url);
    return textResult(`Opened tab ${result.targetId}.`, result);
  }

  async focus(profile, targetId) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(targetId, "focus", {});
    }
    const tabs = await this.hostProfile.focusTab(targetId);
    return textResult(`Focused ${targetId}.`, { tabs });
  }

  async close(profile, targetId) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(targetId, "close", {});
    }
    const tabs = await this.hostProfile.closeTab(targetId);
    return textResult(`Closed ${targetId || "active tab"}.`, { tabs });
  }

  async snapshot(profile, payload) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(payload.targetId, "snapshot", payload);
    }
    return this.hostProfile.snapshot(payload.targetId, payload);
  }

  async screenshot(profile, payload) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(
        payload.targetId,
        "screenshot",
        payload
      );
    }
    return this.hostProfile.screenshot(payload.targetId, payload);
  }

  async navigate(profile, payload) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(
        payload.targetId,
        "navigate",
        payload
      );
    }
    return this.hostProfile.navigate(payload.targetId, payload.url);
  }

  async console(profile, payload) {
    if (profile === "chrome") {
      return textResult("Console capture is not available in chrome relay.");
    }
    return this.hostProfile.consoleMessages(payload.targetId, payload);
  }

  async pdf(profile, payload) {
    if (profile === "chrome") {
      return textResult("PDF export is not available in chrome relay.");
    }
    return this.hostProfile.savePDF(
      payload.targetId,
      payload.filename
    );
  }

  async upload(profile, payload) {
    if (profile === "chrome") {
      return textResult("File upload is not available in chrome relay.");
    }
    return this.hostProfile.uploadFiles(payload.targetId, payload);
  }

  async dialog(profile, payload) {
    if (profile === "chrome") {
      return textResult("Dialog control is not available in chrome relay.");
    }
    return this.hostProfile.handleDialog(
      Boolean(payload.accept),
      payload.promptText
    );
  }

  async act(profile, payload) {
    if (profile === "chrome") {
      return this.chromeRelay.execute(
        payload.targetId || payload.request?.targetId,
        payload.request?.kind || payload.kind,
        payload.request || payload
      );
    }
    return this.hostProfile.act(
      payload.targetId || payload.request?.targetId,
      payload.request || payload
    );
  }
}
