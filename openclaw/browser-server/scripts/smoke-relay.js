import assert from "node:assert/strict";
import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { chromium } from "playwright";
import { startServer } from "../src/server.js";
import {
  authHeaders,
  closeServer,
  extractText,
  fetchJSON,
  findBrowserExecutable
} from "./common.js";

async function main() {
  const executablePath = await findBrowserExecutable();
  const env = {
    ...process.env,
    OPENCLAW_BROWSER_SERVER_ADDR:
      process.env.OPENCLAW_BROWSER_SERVER_ADDR || "127.0.0.1:19792",
    OPENCLAW_BROWSER_SERVER_TOKEN:
      process.env.OPENCLAW_BROWSER_SERVER_TOKEN || "",
    OPENCLAW_BROWSER_EXECUTABLE_PATH: executablePath,
    OPENCLAW_BROWSER_HEADLESS:
      process.env.OPENCLAW_BROWSER_HEADLESS || "true"
  };
  const { server, config } = await startServer(env);
  const baseURL = `http://${config.host}:${config.port}`;
  const relayURL = config.token
    ? `${baseURL}?token=${encodeURIComponent(config.token)}`
    : baseURL;
  const headers = {
    "content-type": "application/json",
    ...authHeaders(config.token)
  };
  const extensionPath = path.resolve("../browser-extension");
  const userDataDir = await fs.mkdtemp(
    path.join(os.tmpdir(), "openclaw-relay-")
  );
  let context;
  try {
    context = await chromium.launchPersistentContext(userDataDir, {
      executablePath,
      headless: env.OPENCLAW_BROWSER_HEADLESS !== "false",
      args: [
        `--disable-extensions-except=${extensionPath}`,
        `--load-extension=${extensionPath}`
      ]
    });

    const page = await context.newPage();
    await page.goto("https://example.com", {
      waitUntil: "domcontentloaded"
    });
    await page.bringToFront();

    let [worker] = context.serviceWorkers();
    if (!worker) {
      worker = await context.waitForEvent("serviceworker", {
        timeout: 10000
      });
    }

    const attached = await worker.evaluate(async (serverURL) => {
      await globalThis.__openclawRelayTestHooks.setServerURL(serverURL);
      await globalThis.__openclawRelayTestHooks.connectSocket();
      return globalThis.__openclawRelayTestHooks.attachCurrentTab();
    }, relayURL);
    assert.ok(attached.targetId);

    await page.waitForTimeout(1000);

    const relayStatus = await fetchJSON(`${baseURL}/extension/status`, {
      headers: authHeaders(config.token)
    });
    assert.equal((relayStatus.clients || []).length, 1);

    const tabs = await fetchJSON(`${baseURL}/tabs?profile=chrome`, {
      headers: authHeaders(config.token)
    });
    assert.equal((tabs.tabs || []).length, 1);
    assert.match(tabs.tabs[0].url || "", /example\.com/);

    const snapshot = await fetchJSON(`${baseURL}/snapshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({ profile: "chrome" })
    });
    const text = extractText(snapshot);
    assert.match(text, /Example Domain/);

    const screenshot = await fetchJSON(`${baseURL}/screenshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({ profile: "chrome" })
    });
    assert.ok((screenshot.content || []).length > 0);

    console.log(JSON.stringify({
      ok: true,
      baseURL,
      executablePath,
      targetId: attached.targetId,
      relayClients: relayStatus.clients,
      attachedTabs: tabs.tabs,
      snapshotPreview: text.split("\n").slice(0, 4),
      screenshotBytes: (screenshot.content?.[0]?.data || "").length
    }));
  } finally {
    if (context) {
      await context.close();
    }
    await fs.rm(userDataDir, { recursive: true, force: true });
    await closeServer(server);
  }
}

main().catch((error) => {
  console.error(JSON.stringify({
    ok: false,
    error: `${error.message || error}`
  }));
  process.exitCode = 1;
});
