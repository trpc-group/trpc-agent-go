import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";
import { snapshotDOM } from "./dom-tools.js";
import { validateNavigationURL } from "./ssrf.js";

const defaultWaitTimeoutMs = 30000;
const defaultWaitDurationMs = 1000;
const defaultSlowTypeDelayMs = 50;

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
    const snapshot = await page.evaluate(snapshotDOM, {
      limit: Number(options.limit) || 0,
      selector: `${options.selector || ""}`.trim()
    });
    return textContent(snapshot.text, {
      targetId: this.pageIds.get(page),
      snapshot
    });
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

  async savePDF(targetId, filename) {
    const page = this.requirePageOrCurrent(targetId);
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "openclaw-pdf-"));
    const file = path.join(dir, safeFilename(filename, "page.pdf"));
    await page.pdf({ path: file });
    return textContent(`Saved PDF to ${file}`, {
      media_files: [file]
    });
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
