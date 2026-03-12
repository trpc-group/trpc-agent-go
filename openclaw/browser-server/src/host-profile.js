import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
  chromium,
  devices as playwrightDevices
} from "playwright";
import { snapshotDOM } from "./dom-tools.js";
import { validateNavigationURL } from "./ssrf.js";

const defaultWaitTimeoutMs = 30000;
const defaultWaitDurationMs = 1000;
const defaultSlowTypeDelayMs = 50;
const snapshotEfficientDepth = 6;
const snapshotLabelAttr = "data-openclaw-labels";

function textContent(text, extra = {}) {
  return {
    ...extra,
    content: [{ type: "text", text }]
  };
}

function normalizeModifiers(modifiers) {
  const allowed = new Set(["Alt", "Control", "Meta", "Shift"]);
  return (modifiers || [])
    .map((value) => `${value || ""}`.trim())
    .filter((value) => allowed.has(value));
}

function waitTimeoutMs(request) {
  const timeoutMs = Number(request.timeoutMs);
  if (Number.isFinite(timeoutMs) && timeoutMs > 0) {
    return timeoutMs;
  }
  return defaultWaitTimeoutMs;
}

function waitDurationMs(request) {
  const timeMs = Number(request.timeMs);
  if (Number.isFinite(timeMs) && timeMs > 0) {
    return timeMs;
  }
  const timeSeconds = Number(request.time);
  if (Number.isFinite(timeSeconds) && timeSeconds > 0) {
    return timeSeconds * 1000;
  }
  return defaultWaitDurationMs;
}

function evaluateSource(request) {
  return `${request.fn || request.function || ""}`.trim();
}

function formatEvaluatedValue(value) {
  const text = JSON.stringify(value, null, 2);
  if (text !== undefined) {
    return text;
  }
  return String(value);
}

function formatTabs(tabs) {
  if (tabs.length === 0) {
    return "No browser tabs are open.";
  }
  return tabs
    .map((tab) => {
      const marker = tab.active ? ">" : " ";
      return `${marker} ${tab.index} ${tab.title} - ${tab.url}`.trim();
    })
    .join("\n");
}

function safeFilename(value, fallback) {
  const cleaned = `${value || ""}`.trim().replace(/[^a-zA-Z0-9._-]+/g, "-");
  return cleaned || fallback;
}

function snapshotMode(value) {
  return `${value || ""}`.trim();
}

function snapshotDepth(options = {}) {
  const depth = Number(options.depth);
  if (Number.isFinite(depth) && depth >= 0) {
    return depth;
  }
  if (snapshotMode(options.mode) === "efficient") {
    return snapshotEfficientDepth;
  }
  return undefined;
}

function normalizeSnapshotOptions(options = {}) {
  const mode = snapshotMode(options.mode);
  return {
    limit: Number(options.limit) || 0,
    selector: `${options.selector || ""}`.trim(),
    interactive: options.interactive !== false,
    compact: options.compact === true || mode === "efficient",
    depth: snapshotDepth(options)
  };
}

function downloadFilename(options = {}, fallback) {
  const candidate = `${options.path || options.filename || ""}`.trim();
  return safeFilename(candidate, fallback);
}

function cookiesText(cookies = []) {
  if (!Array.isArray(cookies) || cookies.length === 0) {
    return "No cookies.";
  }
  return cookies
    .map((cookie) => {
      const parts = [`${cookie.name || ""}=${cookie.value || ""}`];
      if (cookie.domain) {
        parts.push(`domain=${cookie.domain}`);
      }
      if (cookie.path) {
        parts.push(`path=${cookie.path}`);
      }
      return parts.join("; ");
    })
    .join("\n");
}

function storageText(kind, values = {}) {
  const entries = Object.entries(values);
  if (entries.length === 0) {
    return `No ${kind}Storage values.`;
  }
  return entries
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function cookieInput(options = {}) {
  return options.cookie && typeof options.cookie === "object"
    ? options.cookie
    : options;
}

function currentOrigin(page, fallback = "") {
  const candidate = `${fallback || ""}`.trim();
  if (candidate) {
    return candidate;
  }
  try {
    return new URL(page.url()).origin;
  } catch {
    return "";
  }
}

async function withCDPSession(page, fn) {
  const session = await page.context().newCDPSession(page);
  try {
    return await fn(session);
  } finally {
    await session.detach().catch(() => {});
  }
}

export class HostProfile {
  constructor(config) {
    this.config = config;
    this.browser = null;
    this.context = null;
    this.pages = [];
    this.pageIds = new Map();
    this.activeTargetId = "";
    this.consoleEntries = [];
    this.pendingDialog = null;
    this.nextTabIndex = 1;
  }

  async start() {
    if (this.browser) {
      return this.status();
    }
    const launchOptions = {
      headless: this.config.headless
    };
    if (this.config.executablePath) {
      launchOptions.executablePath = this.config.executablePath;
    }
    this.browser = await chromium.launch(launchOptions);
    this.context = await this.browser.newContext();
    await this.context.route("**/*", async (route) => {
      try {
        await validateNavigationURL(route.request().url(), this.config.policy);
        await route.continue();
      } catch (error) {
        await route.abort();
      }
    });
    return this.status();
  }

  async stop() {
    if (this.context) {
      await this.context.close();
    }
    if (this.browser) {
      await this.browser.close();
    }
    this.browser = null;
    this.context = null;
    this.pages = [];
    this.pageIds.clear();
    this.activeTargetId = "";
    this.consoleEntries = [];
    this.pendingDialog = null;
    this.nextTabIndex = 1;
    return this.status();
  }

  status() {
    return {
      state: this.browser ? "ready" : "stopped",
      tabs: this.listTabs()
    };
  }

  async openTab(url) {
    await this.start();
    const page = await this.context.newPage();
    const targetId = this.trackPage(page);
    if (url) {
      await validateNavigationURL(url, this.config.policy);
      await page.goto(url, { waitUntil: "domcontentloaded" });
    }
    this.activeTargetId = targetId;
    return {
      targetId,
      tabs: this.listTabs()
    };
  }

  async focusTab(targetId) {
    const page = this.requirePage(targetId);
    await page.bringToFront();
    this.activeTargetId = targetId;
    return this.listTabs();
  }

  async closeTab(targetId) {
    if (!targetId) {
      const page = this.currentPage();
      if (!page) {
        return this.listTabs();
      }
      await page.close();
      return this.listTabs();
    }
    const page = this.requirePage(targetId);
    await page.close();
    return this.listTabs();
  }

  listTabs() {
    return this.pages
      .filter((page) => !page.isClosed())
      .map((page) => {
        const targetId = this.pageIds.get(page) || "";
        return {
          targetId,
          index: Number(targetId.replace("tab-", "")) || 0,
          title: "",
          active: targetId === this.activeTargetId,
          url: page.url()
        };
      });
  }

  async tabsResult() {
    const tabs = [];
    for (const page of this.pages) {
      if (page.isClosed()) {
        continue;
      }
      const targetId = this.pageIds.get(page);
      tabs.push({
        targetId,
        index: Number(targetId.replace("tab-", "")) || 0,
        title: await page.title(),
        url: page.url(),
        active: targetId === this.activeTargetId
      });
    }
    return textContent(formatTabs(tabs), { tabs });
  }

  async navigate(targetId, url) {
    await validateNavigationURL(url, this.config.policy);
    const page = this.requirePageOrCurrent(targetId);
    await page.goto(url, { waitUntil: "domcontentloaded" });
    this.activeTargetId = this.pageIds.get(page);
    return textContent(`Navigated to ${url}`, {
      targetId: this.activeTargetId
    });
  }

  async snapshot(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const frameSelector = `${options.frame || ""}`.trim();
    const snapshotFormat = `${options.snapshotFormat || ""}`.trim();
    const refsMode = `${options.refs || ""}`.trim();
    if (
      (snapshotFormat && snapshotFormat !== "role") ||
      (refsMode && refsMode !== "role")
    ) {
      throw new Error(
        "browser-server snapshots only support role refs"
      );
    }
    const evaluationTarget = await this.snapshotTarget(page, frameSelector);
    const snapshot = await evaluationTarget.evaluate(
      snapshotDOM,
      normalizeSnapshotOptions(options)
    );
    const result = {
      targetId: this.pageIds.get(page),
      snapshot,
      content: [{
        type: "text",
        text: snapshot.text
      }]
    };
    if (!options.labels) {
      return result;
    }

    const refs = (snapshot.items || [])
      .map((item) => item.ref)
      .filter(Boolean);
    const labels = await this.addSnapshotLabels(evaluationTarget, refs);
    try {
      const buffer = await page.screenshot({
        type: "png"
      });
      result.labels = true;
      result.labelsCount = labels.labels;
      result.labelsSkipped = labels.skipped;
      result.content.push({
        type: "image",
        mimeType: "image/png",
        data: buffer.toString("base64")
      });
      return result;
    } finally {
      await this.clearSnapshotLabels(evaluationTarget);
    }
  }

  async screenshot(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const imageType = options.type === "jpeg" ? "jpeg" : "png";
    const selector = `${options.element || ""}`.trim();
    if ((options.ref || selector) && options.fullPage) {
      throw new Error(
        "fullPage is not supported for element screenshots"
      );
    }
    let buffer;
    if (options.ref) {
      const locator = page.locator(`[data-openclaw-ref="${options.ref}"]`);
      buffer = await locator.screenshot({
        type: imageType
      });
    } else if (selector) {
      buffer = await page.locator(selector).first().screenshot({
        type: imageType
      });
    } else {
      buffer = await page.screenshot({
        fullPage: Boolean(options.fullPage),
        type: imageType
      });
    }
    const mimeType = imageType === "jpeg"
      ? "image/jpeg"
      : "image/png";
    return {
      targetId: this.pageIds.get(page),
      content: [{
        type: "image",
        mimeType,
        data: buffer.toString("base64")
      }]
    };
  }

  async consoleMessages(targetId, options = {}) {
    this.requirePageOrCurrent(targetId);
    const level = `${options.level || ""}`.trim();
    const lines = this.consoleEntries
      .slice(-50)
      .filter((entry) => {
        if (!level) {
          return true;
        }
        return entry.type === level;
      })
      .map((entry) => {
        return `${entry.type}: ${entry.text}`;
      });
    return textContent(lines.join("\n") || "No console messages.");
  }

  async cookies(targetId) {
    const page = this.requirePageOrCurrent(targetId);
    const cookies = await page.context().cookies();
    return textContent(cookiesText(cookies), {
      targetId: this.pageIds.get(page),
      cookies
    });
  }

  async setCookie(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const cookie = cookieInput(options);
    const name = `${cookie.name || ""}`.trim();
    if (!name) {
      throw new Error("cookie name is required");
    }
    if (cookie.value === undefined) {
      throw new Error("cookie value is required");
    }
    const hasURL = `${cookie.url || ""}`.trim() !== "";
    const hasDomainPath = `${cookie.domain || ""}`.trim() !== "" &&
      `${cookie.path || ""}`.trim() !== "";
    if (!hasURL && !hasDomainPath) {
      cookie.url = currentOrigin(page);
    }
    if (!`${cookie.url || ""}`.trim() && !hasDomainPath) {
      throw new Error("cookie requires url, or domain+path");
    }
    await page.context().addCookies([cookie]);
    return textContent(`Set cookie ${name}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async clearCookies(targetId) {
    const page = this.requirePageOrCurrent(targetId);
    await page.context().clearCookies();
    return textContent("Cleared cookies.", {
      targetId: this.pageIds.get(page)
    });
  }

  async storageGet(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const kind = `${options.kind || "local"}`.trim() === "session"
      ? "session"
      : "local";
    const key = `${options.key || ""}`.trim();
    const values = await page.evaluate(
      ({ storageKind, storageKey }) => {
        const store = storageKind === "session"
          ? window.sessionStorage
          : window.localStorage;
        if (storageKey) {
          const value = store.getItem(storageKey);
          return value === null
            ? {}
            : { [storageKey]: value };
        }
        const out = {};
        for (let index = 0; index < store.length; index += 1) {
          const itemKey = store.key(index);
          if (!itemKey) {
            continue;
          }
          const value = store.getItem(itemKey);
          if (value !== null) {
            out[itemKey] = value;
          }
        }
        return out;
      },
      {
        storageKind: kind,
        storageKey: key
      }
    );
    return textContent(storageText(kind, values), {
      targetId: this.pageIds.get(page),
      values
    });
  }

  async storageSet(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const kind = `${options.kind || "local"}`.trim() === "session"
      ? "session"
      : "local";
    const key = `${options.key || ""}`.trim();
    if (!key) {
      throw new Error("storage key is required");
    }
    const value = `${options.value || ""}`;
    await page.evaluate(
      ({ storageKind, storageKey, storageValue }) => {
        const store = storageKind === "session"
          ? window.sessionStorage
          : window.localStorage;
        store.setItem(storageKey, storageValue);
      },
      {
        storageKind: kind,
        storageKey: key,
        storageValue: value
      }
    );
    return textContent(`Set ${kind}Storage ${key}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async storageClear(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const kind = `${options.kind || "local"}`.trim() === "session"
      ? "session"
      : "local";
    await page.evaluate((storageKind) => {
      const store = storageKind === "session"
        ? window.sessionStorage
        : window.localStorage;
      store.clear();
    }, kind);
    return textContent(`Cleared ${kind}Storage.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async setOffline(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const offline = Boolean(options.offline);
    await page.context().setOffline(offline);
    return textContent(`Offline mode ${offline ? "enabled" : "disabled"}.`, {
      targetId: this.pageIds.get(page),
      offline
    });
  }

  async setHeaders(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const headers = options.headers &&
      typeof options.headers === "object"
      ? Object.fromEntries(
          Object.entries(options.headers).map(([key, value]) => {
            return [key, `${value || ""}`];
          })
        )
      : {};
    await page.context().setExtraHTTPHeaders(headers);
    return textContent(`Set ${Object.keys(headers).length} header(s).`, {
      targetId: this.pageIds.get(page),
      headers
    });
  }

  async setCredentials(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    if (options.clear) {
      await page.context().setHTTPCredentials(null);
      return textContent("Cleared HTTP credentials.", {
        targetId: this.pageIds.get(page)
      });
    }
    const username = `${options.username || ""}`.trim();
    if (!username) {
      throw new Error("username is required");
    }
    await page.context().setHTTPCredentials({
      username,
      password: `${options.password || ""}`
    });
    return textContent(`Set HTTP credentials for ${username}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async setGeolocation(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const context = page.context();
    if (options.clear) {
      await context.setGeolocation(null);
      await context.clearPermissions().catch(() => {});
      return textContent("Cleared geolocation override.", {
        targetId: this.pageIds.get(page)
      });
    }
    const latitude = Number(options.latitude);
    const longitude = Number(options.longitude);
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) {
      throw new Error("latitude and longitude are required");
    }
    const accuracy = Number(options.accuracy);
    await context.setGeolocation({
      latitude,
      longitude,
      accuracy: Number.isFinite(accuracy) ? accuracy : undefined
    });
    const origin = currentOrigin(page, options.origin);
    if (origin) {
      await context.grantPermissions(["geolocation"], {
        origin
      }).catch(() => {});
    }
    return textContent(
      `Set geolocation to ${latitude}, ${longitude}.`,
      {
        targetId: this.pageIds.get(page)
      }
    );
  }

  async setMedia(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const colorScheme = `${options.colorScheme || ""}`.trim();
    await page.emulateMedia({
      colorScheme: colorScheme === "none"
        ? null
        : colorScheme || null
    });
    return textContent(
      `Set color scheme to ${colorScheme || "default"}.`,
      {
        targetId: this.pageIds.get(page)
      }
    );
  }

  async setTimezone(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const timezoneID = `${options.timezoneId || ""}`.trim();
    if (!timezoneID) {
      throw new Error("timezoneId is required");
    }
    await withCDPSession(page, async (session) => {
      await session.send("Emulation.setTimezoneOverride", {
        timezoneId: timezoneID
      });
    });
    return textContent(`Set timezone to ${timezoneID}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async setLocale(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const locale = `${options.locale || ""}`.trim();
    if (!locale) {
      throw new Error("locale is required");
    }
    await withCDPSession(page, async (session) => {
      await session.send("Emulation.setLocaleOverride", {
        locale
      });
    });
    return textContent(`Set locale to ${locale}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async setDevice(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const name = `${options.name || ""}`.trim();
    if (!name) {
      throw new Error("device name is required");
    }
    const descriptor = playwrightDevices[name];
    if (!descriptor) {
      throw new Error(`Unknown device "${name}".`);
    }
    if (descriptor.viewport) {
      await page.setViewportSize({
        width: descriptor.viewport.width,
        height: descriptor.viewport.height
      });
    }
    await withCDPSession(page, async (session) => {
      if (descriptor.userAgent || descriptor.locale) {
        await session.send("Emulation.setUserAgentOverride", {
          userAgent: descriptor.userAgent || "",
          acceptLanguage: descriptor.locale || undefined
        });
      }
      if (descriptor.viewport) {
        await session.send("Emulation.setDeviceMetricsOverride", {
          mobile: Boolean(descriptor.isMobile),
          width: descriptor.viewport.width,
          height: descriptor.viewport.height,
          deviceScaleFactor: descriptor.deviceScaleFactor || 1,
          screenWidth: descriptor.viewport.width,
          screenHeight: descriptor.viewport.height
        });
      }
      if (descriptor.hasTouch) {
        await session.send("Emulation.setTouchEmulationEnabled", {
          enabled: true
        });
      }
    });
    return textContent(`Emulated device ${name}.`, {
      targetId: this.pageIds.get(page)
    });
  }

  async savePDF(targetId, filename) {
    const page = this.requirePageOrCurrent(targetId);
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "openclaw-pdf-"));
    const file = path.join(dir, safeFilename(filename, "page.pdf"));
    await page.pdf({ path: file });
    return textContent(`Saved PDF to ${file}`, {
      media_files: [file]
    });
  }

  async downloadFile(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const ref = `${options.ref || ""}`.trim();
    if (!ref) {
      throw new Error("download requires ref");
    }
    const timeout = waitTimeoutMs(options);
    const downloadPromise = page.waitForEvent("download", {
      timeout
    });
    await page.locator(`[data-openclaw-ref="${ref}"]`).click({
      timeout
    });
    const download = await downloadPromise;
    return this.saveDownload(download, options);
  }

  async waitForDownload(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const download = await page.waitForEvent("download", {
      timeout: waitTimeoutMs(options)
    });
    return this.saveDownload(download, options);
  }

  async uploadFiles(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    const paths = Array.isArray(options.paths) ? options.paths : [];
    if (paths.length === 0) {
      throw new Error("upload requires paths");
    }
    const inputRef = `${options.inputRef || ""}`.trim();
    const element = `${options.element || ""}`.trim();
    const ref = `${options.ref || ""}`.trim();
    const timeoutMs = waitTimeoutMs(options);
    if (ref && (inputRef || element)) {
      throw new Error("ref cannot be combined with inputRef/element");
    }
    if (inputRef && element) {
      throw new Error("inputRef and element are mutually exclusive");
    }
    if (inputRef || element) {
      const locator = inputRef
        ? page.locator(`[data-openclaw-ref="${inputRef}"]`)
        : page.locator(element).first();
      await locator.setInputFiles(paths, {
        timeout: timeoutMs
      });
      return textContent(`Uploaded ${paths.length} file(s).`);
    }
    if (!ref) {
      throw new Error("upload requires ref, inputRef, or element");
    }
    const chooserPromise = page.waitForEvent("filechooser", {
      timeout: timeoutMs
    });
    await page.locator(`[data-openclaw-ref="${ref}"]`).click({
      timeout: timeoutMs
    });
    const chooser = await chooserPromise;
    await chooser.setFiles(paths);
    return textContent(`Uploaded ${paths.length} file(s).`);
  }

  async handleDialog(accept, promptText) {
    if (!this.pendingDialog) {
      return textContent("No pending dialog.");
    }
    if (accept) {
      await this.pendingDialog.accept(promptText || "");
    } else {
      await this.pendingDialog.dismiss();
    }
    this.pendingDialog = null;
    return textContent("Handled dialog.");
  }

  async act(targetId, request) {
    const page = this.requirePageOrCurrent(targetId);
    const ref = `${request.ref || ""}`.trim();
    switch (`${request.kind || ""}`.trim()) {
      case "click":
        await this.click(page, request, ref);
        return textContent(`Clicked ${ref}.`);
      case "type":
        await this.type(page, request, ref);
        return textContent(`Typed into ${ref}.`);
      case "hover":
        await page.locator(`[data-openclaw-ref="${ref}"]`).hover({
          timeout: waitTimeoutMs(request)
        });
        return textContent(`Hovered ${ref}.`);
      case "scrollIntoView":
        await this.scrollIntoView(page, request, ref);
        return textContent(`Scrolled ${ref} into view.`);
      case "drag":
        await this.drag(page, request);
        return textContent(
          `Dragged ${request.startRef} -> ${request.endRef}.`
        );
      case "select":
        await page.locator(`[data-openclaw-ref="${ref}"]`).selectOption(
          request.values || [],
          {
            timeout: waitTimeoutMs(request)
          }
        );
        return textContent(`Selected ${ref}.`);
      case "fill":
        {
          const timeoutMs = waitTimeoutMs(request);
          for (const field of request.fields || []) {
            await page.locator(`[data-openclaw-ref="${field.ref}"]`).fill(
              `${field.text || ""}`,
              {
                timeout: timeoutMs
              }
            );
          }
          return textContent("Filled form fields.");
        }
      case "press":
        await page.keyboard.press(`${request.key || ""}`, {
          delay: Number(request.delayMs) || 0
        });
        return textContent(`Pressed ${request.key}.`);
      case "resize":
        await page.setViewportSize({
          width: Number(request.width) || 1280,
          height: Number(request.height) || 720
        });
        return textContent("Resized viewport.");
      case "wait":
        return this.waitFor(page, request);
      case "evaluate":
        return this.evaluate(page, request);
      case "close":
        await page.close();
        return textContent("Closed tab.");
      default:
        throw new Error(`Unsupported act kind: ${request.kind}`);
    }
  }

  async waitFor(page, request) {
    const timeoutMs = waitTimeoutMs(request);
    if (request.selector) {
      await page.waitForSelector(request.selector, {
        timeout: timeoutMs
      });
      return textContent(`Selector appeared: ${request.selector}`);
    }
    if (request.url) {
      await page.waitForURL(request.url, {
        timeout: timeoutMs
      });
      return textContent(`URL matched: ${request.url}`);
    }
    if (request.loadState) {
      await page.waitForLoadState(request.loadState, {
        timeout: timeoutMs
      });
      return textContent(`Load state reached: ${request.loadState}`);
    }
    if (request.text) {
      await page.getByText(request.text).waitFor({
        timeout: timeoutMs
      });
      return textContent(`Text appeared: ${request.text}`);
    }
    if (request.textGone) {
      await page.waitForFunction((value) => {
        return !document.body.innerText.includes(value);
      }, request.textGone, {
        timeout: timeoutMs
      });
      return textContent(`Text disappeared: ${request.textGone}`);
    }
    const fnText = evaluateSource(request);
    if (fnText) {
      await page.waitForFunction((source) => {
        const fn = (0, eval)(`(${source})`);
        return Boolean(fn());
      }, fnText, {
        timeout: timeoutMs
      });
      return textContent("Wait predicate matched.");
    }
    await page.waitForTimeout(waitDurationMs(request));
    return textContent("Wait completed.");
  }

  async evaluate(page, request) {
    const fnText = evaluateSource(request);
    if (!fnText) {
      throw new Error("evaluate requires fn");
    }
    let value;
    if (`${request.ref || ""}`.trim()) {
      value = await page.locator(
        `[data-openclaw-ref="${request.ref}"]`
      ).evaluate((element, source) => {
        const fn = (0, eval)(`(${source})`);
        return fn(element);
      }, fnText);
    } else {
      value = await page.evaluate((source) => {
        const fn = (0, eval)(`(${source})`);
        return fn();
      }, fnText);
    }
    return textContent(formatEvaluatedValue(value), { value });
  }

  async click(page, request, ref) {
    const locator = page.locator(`[data-openclaw-ref="${ref}"]`);
    const options = {
      button: request.button || "left",
      modifiers: normalizeModifiers(request.modifiers),
      timeout: waitTimeoutMs(request)
    };
    if (request.doubleClick) {
      await locator.dblclick(options);
      return;
    }
    await locator.click(options);
  }

  async scrollIntoView(page, request, ref) {
    await page.locator(
      `[data-openclaw-ref="${ref}"]`
    ).scrollIntoViewIfNeeded({
      timeout: waitTimeoutMs(request)
    });
  }

  async type(page, request, ref) {
    const locator = page.locator(`[data-openclaw-ref="${ref}"]`);
    const text = `${request.text || ""}`;
    const timeoutMs = waitTimeoutMs(request);
    if (request.slowly) {
      await locator.click({ timeout: timeoutMs });
      await locator.fill("", { timeout: timeoutMs });
      await page.keyboard.type(text, {
        delay: defaultSlowTypeDelayMs
      });
    } else {
      await locator.fill(text, { timeout: timeoutMs });
    }
    if (request.submit) {
      await page.keyboard.press("Enter");
    }
  }

  async drag(page, request) {
    const startRef = `${request.startRef || ""}`.trim();
    const endRef = `${request.endRef || ""}`.trim();
    if (!startRef || !endRef) {
      throw new Error("drag requires startRef and endRef");
    }
    await page.locator(`[data-openclaw-ref="${startRef}"]`).dragTo(
      page.locator(`[data-openclaw-ref="${endRef}"]`),
      {
        timeout: waitTimeoutMs(request)
      }
    );
  }

  async snapshotTarget(page, frameSelector) {
    if (!frameSelector) {
      return page;
    }
    const handle = await page.locator(frameSelector).first().elementHandle();
    if (!handle) {
      throw new Error(`Snapshot frame not found: ${frameSelector}`);
    }
    const frame = await handle.contentFrame();
    if (!frame) {
      throw new Error(`Snapshot frame is not available: ${frameSelector}`);
    }
    return frame;
  }

  async addSnapshotLabels(target, refs) {
    return target.evaluate((snapshotRefs) => {
      const labelAttr = "data-openclaw-labels";
      const refAttr = "data-openclaw-ref";
      const clear = () => {
        document.querySelectorAll(`[${labelAttr}]`).forEach((node) => {
          node.remove();
        });
      };
      clear();
      let labels = 0;
      let skipped = 0;
      for (const ref of snapshotRefs) {
        const node = document.querySelector(
          `[${refAttr}="${ref}"]`
        );
        if (!node) {
          skipped += 1;
          continue;
        }
        const rect = node.getBoundingClientRect();
        if (rect.width <= 0 || rect.height <= 0) {
          skipped += 1;
          continue;
        }
        const box = document.createElement("div");
        box.setAttribute(labelAttr, "1");
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
        tag.setAttribute(labelAttr, "1");
        tag.textContent = ref;
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
    }, refs);
  }

  async clearSnapshotLabels(target) {
    await target.evaluate((labelAttr) => {
      document.querySelectorAll(`[${labelAttr}]`).forEach((node) => {
        node.remove();
      });
    }, snapshotLabelAttr);
  }

  async saveDownload(download, options = {}) {
    const suggested = typeof download.suggestedFilename === "function"
      ? download.suggestedFilename()
      : "download.bin";
    const dir = await fs.mkdtemp(
      path.join(os.tmpdir(), "openclaw-download-")
    );
    const file = path.join(
      dir,
      downloadFilename(options, safeFilename(suggested, "download.bin"))
    );
    await download.saveAs(file);
    const url = typeof download.url === "function"
      ? download.url()
      : "";
    return textContent(`Saved download to ${file}`, {
      media_files: [file],
      download: {
        path: file,
        suggestedFilename: suggested,
        url
      }
    });
  }

  trackPage(page) {
    const targetId = `tab-${this.nextTabIndex++}`;
    this.pages.push(page);
    this.pageIds.set(page, targetId);
    page.on("console", (message) => {
      this.consoleEntries.push({
        type: message.type(),
        text: message.text()
      });
      if (this.consoleEntries.length > 200) {
        this.consoleEntries.shift();
      }
    });
    page.on("dialog", async (dialog) => {
      this.pendingDialog = dialog;
    });
    page.on("close", () => {
      this.pageIds.delete(page);
      this.pages = this.pages.filter((item) => item !== page);
      if (this.activeTargetId === targetId) {
        this.activeTargetId = this.pages.length > 0
          ? this.pageIds.get(this.pages[this.pages.length - 1]) || ""
          : "";
      }
    });
    return targetId;
  }

  currentPage() {
    if (this.activeTargetId) {
      return this.pages.find((page) => {
        return this.pageIds.get(page) === this.activeTargetId;
      }) || null;
    }
    return this.pages.find((page) => !page.isClosed()) || null;
  }

  requirePage(targetId) {
    const page = this.pages.find((item) => {
      return this.pageIds.get(item) === targetId && !item.isClosed();
    });
    if (!page) {
      throw new Error(`Unknown targetId: ${targetId}`);
    }
    this.activeTargetId = targetId;
    return page;
  }

  requirePageOrCurrent(targetId) {
    if (targetId) {
      return this.requirePage(targetId);
    }
    const page = this.currentPage();
    if (page) {
      return page;
    }
    throw new Error("No browser tab is available");
  }
}
