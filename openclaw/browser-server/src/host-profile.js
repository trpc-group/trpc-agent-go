import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";
import { snapshotDOM } from "./dom-tools.js";
import { validateNavigationURL } from "./ssrf.js";

function textContent(text, extra = {}) {
  return {
    ...extra,
    content: [{ type: "text", text }]
  };
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

  async snapshot(targetId) {
    const page = this.requirePageOrCurrent(targetId);
    const snapshot = await page.evaluate(snapshotDOM);
    return textContent(snapshot.text, {
      targetId: this.pageIds.get(page),
      snapshot
    });
  }

  async screenshot(targetId, options = {}) {
    const page = this.requirePageOrCurrent(targetId);
    let buffer;
    if (options.ref) {
      const locator = page.locator(`[data-openclaw-ref="${options.ref}"]`);
      buffer = await locator.screenshot({
        type: options.type === "jpeg" ? "jpeg" : "png"
      });
    } else {
      buffer = await page.screenshot({
        fullPage: Boolean(options.fullPage),
        type: options.type === "jpeg" ? "jpeg" : "png"
      });
    }
    const mimeType =
      options.type === "jpeg" ? "image/jpeg" : "image/png";
    return {
      targetId: this.pageIds.get(page),
      content: [{
        type: "image",
        mimeType,
        data: buffer.toString("base64")
      }]
    };
  }

  async consoleMessages(targetId) {
    this.requirePageOrCurrent(targetId);
    const lines = this.consoleEntries.slice(-50).map((entry) => {
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

  async uploadFiles(targetId, inputRef, paths) {
    const page = this.requirePageOrCurrent(targetId);
    const ref = `${inputRef || ""}`.trim();
    if (!ref) {
      throw new Error("upload requires inputRef");
    }
    const locator = page.locator(`[data-openclaw-ref="${ref}"]`);
    await locator.setInputFiles(paths);
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
        await page.locator(`[data-openclaw-ref="${ref}"]`).click({
          button: request.button || "left"
        });
        return textContent(`Clicked ${ref}.`);
      case "type":
        await page.locator(`[data-openclaw-ref="${ref}"]`).fill(
          `${request.text || ""}`
        );
        return textContent(`Typed into ${ref}.`);
      case "hover":
        await page.locator(`[data-openclaw-ref="${ref}"]`).hover();
        return textContent(`Hovered ${ref}.`);
      case "select":
        await page.locator(`[data-openclaw-ref="${ref}"]`).selectOption(
          request.values || []
        );
        return textContent(`Selected ${ref}.`);
      case "fill":
        for (const field of request.fields || []) {
          await page.locator(`[data-openclaw-ref="${field.ref}"]`).fill(
            `${field.text || ""}`
          );
        }
        return textContent("Filled form fields.");
      case "press":
        await page.keyboard.press(`${request.key || ""}`);
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
    if (request.selector) {
      await page.waitForSelector(request.selector, {
        timeout: Number(request.timeoutMs) || 30000
      });
      return textContent(`Selector appeared: ${request.selector}`);
    }
    if (request.loadState) {
      await page.waitForLoadState(request.loadState, {
        timeout: Number(request.timeoutMs) || 30000
      });
      return textContent(`Load state reached: ${request.loadState}`);
    }
    if (request.text) {
      await page.getByText(request.text).waitFor({
        timeout: Number(request.timeoutMs) || 30000
      });
      return textContent(`Text appeared: ${request.text}`);
    }
    if (request.textGone) {
      await page.waitForFunction((value) => {
        return !document.body.innerText.includes(value);
      }, request.textGone, {
        timeout: Number(request.timeoutMs) || 30000
      });
      return textContent(`Text disappeared: ${request.textGone}`);
    }
    await page.waitForTimeout(Number(request.timeMs) || 1000);
    return textContent("Wait completed.");
  }

  async evaluate(page, request) {
    const fnText = `${request.fn || ""}`.trim();
    if (!fnText) {
      throw new Error("evaluate requires fn");
    }
    const value = await page.evaluate(`(${fnText})()`);
    return textContent(JSON.stringify(value, null, 2), { value });
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
