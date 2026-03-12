import test from "node:test";
import assert from "node:assert/strict";
import { HostProfile } from "./host-profile.js";

function profileForPage(page) {
  const profile = new HostProfile({
    headless: true,
    policy: {}
  });
  profile.requirePageOrCurrent = () => page;
  profile.pageIds.set(page, "tab-1");
  return profile;
}

test("HostProfile screenshot supports CSS element selectors", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async screenshot(options) {
      calls.push(options);
      return Buffer.from("fake-image");
    }
  };
  const page = {
    locator(selector) {
      calls.push(selector);
      return locator;
    },
    async screenshot() {
      throw new Error("page screenshot should not run");
    }
  };

  const profile = profileForPage(page);
  const result = await profile.screenshot("tab-1", {
    element: "#hero",
    type: "jpeg"
  });

  assert.equal(calls[0], "#hero");
  assert.equal(calls[1].type, "jpeg");
  assert.equal(result.targetId, "tab-1");
  assert.equal(result.content[0].mimeType, "image/jpeg");
});

test("HostProfile upload supports CSS selectors", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async setInputFiles(paths, options) {
      calls.push({ paths, options });
    }
  };
  const page = {
    locator(selector) {
      calls.push({ selector });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.uploadFiles("tab-1", {
    element: "#upload-input",
    paths: ["/tmp/a.txt"],
    timeoutMs: 1234
  });

  assert.equal(calls[0].selector, "#upload-input");
  assert.deepEqual(calls[1].paths, ["/tmp/a.txt"]);
  assert.equal(calls[1].options.timeout, 1234);
  assert.equal(result.content[0].text, "Uploaded 1 file(s).");
});

test("HostProfile rejects full-page element screenshots", async () => {
  const page = {
    locator() {
      throw new Error("locator should not run");
    },
    async screenshot() {
      throw new Error("page screenshot should not run");
    }
  };

  const profile = profileForPage(page);
  await assert.rejects(async () => {
    await profile.screenshot("tab-1", {
      element: "#hero",
      fullPage: true
    });
  }, /fullPage is not supported/);
});

test("HostProfile upload supports file chooser refs", async () => {
  const calls = [];
  const chooser = {
    async setFiles(paths) {
      calls.push({ setFiles: paths });
    }
  };
  const locator = {
    async click(options) {
      calls.push({ click: options });
    }
  };
  const page = {
    async waitForEvent(name, options) {
      calls.push({ waitForEvent: name, options });
      return chooser;
    },
    locator(selector) {
      calls.push({ selector });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.uploadFiles("tab-1", {
    ref: "e1",
    paths: ["/tmp/a.txt"],
    timeoutMs: 2222
  });

  assert.equal(calls[0].waitForEvent, "filechooser");
  assert.equal(calls[0].options.timeout, 2222);
  assert.equal(calls[1].selector, '[data-openclaw-ref="e1"]');
  assert.equal(calls[2].click.timeout, 2222);
  assert.deepEqual(calls[3].setFiles, ["/tmp/a.txt"]);
  assert.equal(result.content[0].text, "Uploaded 1 file(s).");
});

test("HostProfile consoleMessages filters by level", async () => {
  const page = {};
  const profile = profileForPage(page);
  profile.consoleEntries = [
    { type: "log", text: "hello" },
    { type: "error", text: "boom" }
  ];

  const result = await profile.consoleMessages("tab-1", {
    level: "error"
  });

  assert.equal(result.content[0].text, "error: boom");
});
