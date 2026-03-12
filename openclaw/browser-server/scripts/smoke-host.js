import assert from "node:assert/strict";
import { startServer } from "../src/server.js";
import {
  authHeaders,
  closeServer,
  extractFirstRef,
  extractText,
  fetchJSON,
  findBrowserExecutable,
  resolveHeadlessMode
} from "./common.js";

async function main() {
  const executablePath = await findBrowserExecutable();
  const headless = resolveHeadlessMode();
  const env = {
    ...process.env,
    OPENCLAW_BROWSER_SERVER_ADDR:
      process.env.OPENCLAW_BROWSER_SERVER_ADDR || "127.0.0.1:19791",
    OPENCLAW_BROWSER_SERVER_TOKEN:
      process.env.OPENCLAW_BROWSER_SERVER_TOKEN || "",
    OPENCLAW_BROWSER_EXECUTABLE_PATH: executablePath,
    OPENCLAW_BROWSER_HEADLESS: headless ? "true" : "false"
  };
  const { server, runtime, config } = await startServer(env);
  const baseURL = `http://${config.host}:${config.port}`;
  const headers = {
    "content-type": "application/json",
    ...authHeaders(config.token)
  };
  try {
    await fetchJSON(`${baseURL}/start`, {
      method: "POST",
      headers,
      body: JSON.stringify({ profile: "openclaw" })
    });

    const open = await fetchJSON(`${baseURL}/tabs/open`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        url: "https://example.com"
      })
    });
    assert.equal(open.targetId, "tab-1");

    const snapshot = await fetchJSON(`${baseURL}/snapshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({ profile: "openclaw" })
    });
    const text = extractText(snapshot);
    assert.match(text, /Example Domain/);
    const ref = extractFirstRef(text);
    assert.ok(ref);

    const scrolled = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
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
        profile: "openclaw",
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
        profile: "openclaw",
        request: {
          kind: "wait",
          fn: "() => document.title",
          timeoutMs: 5000
        }
      })
    });
    assert.match(extractText(waitedByFn), /Wait predicate matched/);

    const evaluated = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
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
      body: JSON.stringify({ profile: "openclaw" })
    });
    assert.ok((screenshot.content || []).length > 0);

    console.log(JSON.stringify({
      ok: true,
      baseURL,
      headless,
      executablePath,
      targetId: snapshot.targetId,
      snapshotPreview: text.split("\n").slice(0, 4),
      screenshotBytes: (screenshot.content?.[0]?.data || "").length
    }));
  } finally {
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
