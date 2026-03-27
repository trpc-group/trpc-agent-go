import assert from "node:assert/strict";
import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { chromium } from "playwright";
import { startServer } from "../src/server.js";
import {
  authHeaders,
  buildRelayLaunchOptions,
  closeServer,
  extractFirstRef,
  extractText,
  fetchJSON,
  relayChromiumChannel,
  resolveHeadlessMode
} from "./common.js";

async function main() {
  const executablePath =
    `${process.env.OPENCLAW_BROWSER_EXECUTABLE_PATH || ""}`.trim();
  const headless = resolveHeadlessMode();
  const env = {
    ...process.env,
    OPENCLAW_BROWSER_SERVER_ADDR:
      process.env.OPENCLAW_BROWSER_SERVER_ADDR || "127.0.0.1:19792",
    OPENCLAW_BROWSER_SERVER_TOKEN:
      process.env.OPENCLAW_BROWSER_SERVER_TOKEN || "",
    OPENCLAW_BROWSER_EXECUTABLE_PATH: executablePath,
    OPENCLAW_BROWSER_HEADLESS: headless ? "true" : "false"
  };
  const { server, runtime, config } = await startServer(env);
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
  const launchOptions = buildRelayLaunchOptions({
    extensionPath,
    executablePath,
    headless
  });
  let context;
  try {
    context = await chromium.launchPersistentContext(
      userDataDir,
      launchOptions
    );

    const page = await context.newPage();
    await page.goto("https://example.com", {
      waitUntil: "domcontentloaded"
    });
    await page.bringToFront();

    let [worker] = context.serviceWorkers();
    if (!worker) {
      worker = await context.waitForEvent("serviceworker", {
        timeout: 30000
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
    const ref = extractFirstRef(text);
    assert.ok(ref);

    const scrolled = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        request: {
          kind: "scrollIntoView",
          ref
        }
      })
    });
    assert.match(extractText(scrolled), /Scrolled/);

    const waited = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        request: {
          kind: "wait",
          text: "Example Domain",
          timeoutMs: 5000
        }
      })
    });
    assert.match(extractText(waited), /Text appeared/);

    const waitedByFn = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        request: {
          kind: "wait",
          fn: "() => document.title",
          timeoutMs: 5000
        }
      })
    });
    assert.match(extractText(waitedByFn), /Wait predicate matched/);

    const cookieSet = await fetchJSON(`${baseURL}/cookies/set`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        cookie: {
          name: "sid",
          value: "abc"
        }
      })
    });
    assert.match(extractText(cookieSet), /Set cookie sid/);

    const cookies = await fetchJSON(`${baseURL}/cookies?profile=chrome`, {
      headers: authHeaders(config.token)
    });
    assert.match(extractText(cookies), /sid=abc/);

    const storageSet = await fetchJSON(`${baseURL}/storage/local/set`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        key: "token",
        value: "abc"
      })
    });
    assert.match(extractText(storageSet), /Set localStorage token/);

    const storage = await fetchJSON(
      `${baseURL}/storage/local?profile=chrome&key=token`,
      {
        headers: authHeaders(config.token)
      }
    );
    assert.match(extractText(storage), /token=abc/);

    const evaluated = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        request: {
          kind: "evaluate",
          fn: "() => document.title"
        }
      })
    });
    assert.match(extractText(evaluated), /Example Domain/);

    const screenshot = await fetchJSON(`${baseURL}/screenshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        type: "jpeg"
      })
    });
    assert.ok((screenshot.content || []).length > 0);
    assert.equal(screenshot.content?.[0]?.mimeType, "image/jpeg");

    const refShot = await fetchJSON(`${baseURL}/screenshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "chrome",
        ref,
        type: "jpeg"
      })
    });
    assert.ok((refShot.content || []).length > 0);
    assert.equal(refShot.content?.[0]?.mimeType, "image/jpeg");
    assert.ok(
      (refShot.content?.[0]?.data || "").length <
        (screenshot.content?.[0]?.data || "").length
    );

    console.log(JSON.stringify({
      ok: true,
      baseURL,
      browser:
        launchOptions.executablePath || launchOptions.channel ||
        relayChromiumChannel,
      headless,
      targetId: attached.targetId,
      relayClients: relayStatus.clients,
      attachedTabs: tabs.tabs,
      snapshotPreview: text.split("\n").slice(0, 4),
      screenshotBytes: (screenshot.content?.[0]?.data || "").length,
      refScreenshotBytes: (refShot.content?.[0]?.data || "").length
    }));
  } finally {
    if (context) {
      await context.close();
    }
    await fs.rm(userDataDir, { recursive: true, force: true });
    await closeServer(server, runtime);
  }
}

main().catch((error) => {
  console.error(JSON.stringify({
    ok: false,
    error: `${error.message || error}`
  }));
  process.exitCode = 1;
});
