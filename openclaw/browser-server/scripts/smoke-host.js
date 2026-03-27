import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
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
  const uploadDir = await fs.mkdtemp(
    path.join(os.tmpdir(), "openclaw-host-upload-")
  );
  const uploadPath = path.join(uploadDir, "fixture.txt");
  await fs.writeFile(uploadPath, "fixture\n", "utf8");
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

    const labeledSnapshot = await fetchJSON(`${baseURL}/snapshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        mode: "efficient",
        labels: true
      })
    });
    assert.equal((labeledSnapshot.content || []).length, 2);
    assert.equal(labeledSnapshot.content?.[1]?.type, "image");

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

    const cookieSet = await fetchJSON(`${baseURL}/cookies/set`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        cookie: {
          name: "sid",
          value: "abc"
        }
      })
    });
    assert.match(extractText(cookieSet), /Set cookie sid/);

    const cookies = await fetchJSON(`${baseURL}/cookies?profile=openclaw`, {
      headers: authHeaders(config.token)
    });
    assert.match(extractText(cookies), /sid=abc/);

    const storageSet = await fetchJSON(`${baseURL}/storage/local/set`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        key: "token",
        value: "abc"
      })
    });
    assert.match(extractText(storageSet), /Set localStorage token/);

    const storage = await fetchJSON(
      `${baseURL}/storage/local?profile=openclaw&key=token`,
      {
        headers: authHeaders(config.token)
      }
    );
    assert.match(extractText(storage), /token=abc/);

    const offlineOn = await fetchJSON(`${baseURL}/set/offline`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        offline: true
      })
    });
    assert.match(extractText(offlineOn), /enabled/);

    const offlineOff = await fetchJSON(`${baseURL}/set/offline`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        offline: false
      })
    });
    assert.match(extractText(offlineOff), /disabled/);

    const timezone = await fetchJSON(`${baseURL}/set/timezone`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        timezoneId: "Asia/Shanghai"
      })
    });
    assert.match(extractText(timezone), /Asia\/Shanghai/);

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
      body: JSON.stringify({
        profile: "openclaw",
        type: "jpeg"
      })
    });
    assert.ok((screenshot.content || []).length > 0);
    assert.equal(screenshot.content?.[0]?.mimeType, "image/jpeg");

    const elementShot = await fetchJSON(`${baseURL}/screenshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        element: "h1",
        type: "jpeg"
      })
    });
    assert.ok((elementShot.content || []).length > 0);
    assert.equal(elementShot.content?.[0]?.mimeType, "image/jpeg");
    assert.ok(
      (elementShot.content?.[0]?.data || "").length <
        (screenshot.content?.[0]?.data || "").length
    );

    const prepared = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        request: {
          kind: "evaluate",
          fn: `() => {
            document.body.innerHTML = "";
            const button = document.createElement("button");
            button.id = "upload-button";
            button.textContent = "Choose file";
            const input = document.createElement("input");
            input.id = "upload-input";
            input.type = "file";
            const blob = new Blob(["browser report"], {
              type: "text/plain"
            });
            const link = document.createElement("a");
            link.id = "download-link";
            link.textContent = "Download report";
            link.href = URL.createObjectURL(blob);
            link.download = "report.txt";
            button.addEventListener("click", () => input.click());
            document.body.append(button, input, link);
            return document.body.innerText;
          }`
        }
      })
    });
    assert.match(extractText(prepared), /Choose file/);
    assert.match(extractText(prepared), /Download report/);

    const uploadSnapshot = await fetchJSON(`${baseURL}/snapshot`, {
      method: "POST",
      headers,
      body: JSON.stringify({ profile: "openclaw" })
    });
    const uploadSnapshotText = extractText(uploadSnapshot);
    const uploadButtonRef = uploadSnapshotText
      .split("\n")
      .find((line) => {
        return line.includes("Choose file");
      })
      ?.match(/\[([^\]]+)\]/)?.[1] || "";
    const downloadRef = uploadSnapshotText
      .split("\n")
      .find((line) => {
        return line.includes("Download report");
      })
      ?.match(/\[([^\]]+)\]/)?.[1] || "";
    assert.ok(uploadButtonRef);
    assert.ok(downloadRef);

    const elementUpload = await fetchJSON(`${baseURL}/upload`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        element: "#upload-input",
        paths: [uploadPath],
        timeoutMs: 5000
      })
    });
    assert.match(extractText(elementUpload), /Uploaded 1 file/);

    const uploadedByElement = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        request: {
          kind: "evaluate",
          fn: `() => document.getElementById("upload-input").files.length`
        }
      })
    });
    assert.match(extractText(uploadedByElement), /1/);

    const cleared = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        request: {
          kind: "evaluate",
          fn: `() => {
            const input = document.getElementById("upload-input");
            input.value = "";
            return input.files.length;
          }`
        }
      })
    });
    assert.match(extractText(cleared), /0/);

    const refUpload = await fetchJSON(`${baseURL}/upload`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        ref: uploadButtonRef,
        paths: [uploadPath],
        timeoutMs: 5000
      })
    });
    assert.match(extractText(refUpload), /Uploaded 1 file/);

    const uploadedByRef = await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        request: {
          kind: "evaluate",
          fn: `() => document.getElementById("upload-input").files.length`
        }
      })
    });
    assert.match(extractText(uploadedByRef), /1/);

    const downloaded = await fetchJSON(`${baseURL}/download`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        ref: downloadRef,
        path: "report.txt",
        timeoutMs: 5000
      })
    });
    assert.match(extractText(downloaded), /Saved download/);
    const downloadedText = await fs.readFile(
      downloaded.download.path,
      "utf8"
    );
    assert.equal(downloadedText, "browser report");

    await fetchJSON(`${baseURL}/act`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        request: {
          kind: "evaluate",
          fn: `() => {
            setTimeout(() => {
              document.getElementById("download-link").click();
            }, 100);
            return true;
          }`
        }
      })
    });

    const waitedDownload = await fetchJSON(`${baseURL}/wait/download`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        profile: "openclaw",
        path: "wait-report.txt",
        timeoutMs: 5000
      })
    });
    assert.match(extractText(waitedDownload), /Saved download/);
    const waitedDownloadText = await fs.readFile(
      waitedDownload.download.path,
      "utf8"
    );
    assert.equal(waitedDownloadText, "browser report");

    console.log(JSON.stringify({
      ok: true,
      baseURL,
      headless,
      executablePath,
      targetId: snapshot.targetId,
      snapshotPreview: text.split("\n").slice(0, 4),
      uploadButtonRef,
      downloadRef,
      screenshotBytes: (screenshot.content?.[0]?.data || "").length,
      elementScreenshotBytes: (elementShot.content?.[0]?.data || "").length,
      labeledSnapshotBytes:
        (labeledSnapshot.content?.[1]?.data || "").length
    }));
  } finally {
    await fs.rm(uploadDir, { recursive: true, force: true });
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
