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

test("HostProfile snapshot supports labels and advanced options", async () => {
  const evaluateCalls = [];
  const page = {
    async evaluate(fn, options) {
      evaluateCalls.push({ fn: `${fn.name || "anonymous"}`, options });
      if (evaluateCalls.length === 1) {
        return {
          title: "Example",
          url: "https://example.com",
          items: [{
            ref: "e1",
            role: "button",
            text: "Save"
          }],
          text: "Page: Example"
        };
      }
      if (evaluateCalls.length === 2) {
        return { labels: 1, skipped: 0 };
      }
      return { ok: true };
    },
    async screenshot(options) {
      assert.equal(options.type, "png");
      return Buffer.from("labeled");
    }
  };

  const profile = profileForPage(page);
  const result = await profile.snapshot("tab-1", {
    mode: "efficient",
    selector: "#main",
    labels: true
  });

  assert.equal(evaluateCalls[0].options.selector, "#main");
  assert.equal(evaluateCalls[0].options.compact, true);
  assert.equal(evaluateCalls[0].options.depth, 6);
  assert.equal(result.labels, true);
  assert.equal(result.labelsCount, 1);
  assert.equal(result.content.length, 2);
  assert.equal(result.content[1].mimeType, "image/png");
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

test("HostProfile download clicks a ref and saves the file", async () => {
  const calls = [];
  const download = {
    suggestedFilename() {
      return "report.txt";
    },
    url() {
      return "https://example.com/report.txt";
    },
    async saveAs(file) {
      calls.push({ saveAs: file });
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
      return download;
    },
    locator(selector) {
      calls.push({ selector });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.downloadFile("tab-1", {
    ref: "e1",
    path: "report.txt",
    timeoutMs: 2222
  });

  assert.equal(calls[0].waitForEvent, "download");
  assert.equal(calls[0].options.timeout, 2222);
  assert.equal(calls[1].selector, '[data-openclaw-ref="e1"]');
  assert.equal(calls[2].click.timeout, 2222);
  assert.match(calls[3].saveAs, /report\.txt$/);
  assert.match(result.content[0].text, /Saved download/);
});

test("HostProfile waits for a download without clicking", async () => {
  const calls = [];
  const download = {
    suggestedFilename() {
      return "report.txt";
    },
    url() {
      return "https://example.com/report.txt";
    },
    async saveAs(file) {
      calls.push({ saveAs: file });
    }
  };
  const page = {
    async waitForEvent(name, options) {
      calls.push({ waitForEvent: name, options });
      return download;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.waitForDownload("tab-1", {
    timeoutMs: 1111
  });

  assert.equal(calls[0].waitForEvent, "download");
  assert.equal(calls[0].options.timeout, 1111);
  assert.match(calls[1].saveAs, /report\.txt$/);
  assert.match(result.content[0].text, /Saved download/);
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

test("HostProfile cookies returns context cookies", async () => {
  const page = {
    context() {
      return {
        async cookies() {
          return [{
            name: "sid",
            value: "abc",
            domain: "example.com",
            path: "/"
          }];
        }
      };
    }
  };

  const profile = profileForPage(page);
  const result = await profile.cookies("tab-1");

  assert.equal(result.cookies[0].name, "sid");
  assert.match(result.content[0].text, /sid=abc/);
});

test("HostProfile storageSet and storageGet use the requested scope", async () => {
  const calls = [];
  const page = {
    async evaluate(fn, payload) {
      calls.push(payload);
      if (payload.storageValue !== undefined) {
        return null;
      }
      return {
        token: "abc"
      };
    }
  };

  const profile = profileForPage(page);
  await profile.storageSet("tab-1", {
    kind: "session",
    key: "token",
    value: "abc"
  });
  const result = await profile.storageGet("tab-1", {
    kind: "session",
    key: "token"
  });

  assert.equal(calls[0].storageKind, "session");
  assert.equal(calls[0].storageKey, "token");
  assert.equal(calls[0].storageValue, "abc");
  assert.equal(calls[1].storageKind, "session");
  assert.equal(result.values.token, "abc");
});

test("HostProfile setOffline delegates to the browser context", async () => {
  const calls = [];
  const page = {
    context() {
      return {
        async setOffline(value) {
          calls.push(value);
        }
      };
    }
  };

  const profile = profileForPage(page);
  const result = await profile.setOffline("tab-1", {
    offline: true
  });

  assert.deepEqual(calls, [true]);
  assert.equal(result.offline, true);
});

test("HostProfile setTimezone uses a CDP session", async () => {
  const calls = [];
  const session = {
    async send(method, payload) {
      calls.push({ method, payload });
    },
    async detach() {
      calls.push({ detach: true });
    }
  };
  const page = {
    context() {
      return {
        async newCDPSession() {
          return session;
        }
      };
    }
  };

  const profile = profileForPage(page);
  const result = await profile.setTimezone("tab-1", {
    timezoneId: "Asia/Shanghai"
  });

  assert.equal(calls[0].method, "Emulation.setTimezoneOverride");
  assert.equal(calls[0].payload.timezoneId, "Asia/Shanghai");
  assert.deepEqual(calls[1], { detach: true });
  assert.match(result.content[0].text, /Asia\/Shanghai/);
});
