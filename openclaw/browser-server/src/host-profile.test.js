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

test("HostProfile navigate opens a tab when none exists", async () => {
  const profile = new HostProfile({
    headless: true,
    policy: {}
  });
  profile.currentPage = () => null;
  profile.openTab = async (url) => {
    assert.equal(url, "about:blank");
    return { targetId: "tab-1", tabs: [] };
  };

  const result = await profile.navigate("", "about:blank");

  assert.equal(result.targetId, "tab-1");
  assert.match(result.content[0].text, /Navigated to about:blank/);
});

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

test("HostProfile act accepts press/key as a press alias", async () => {
  const calls = [];
  const page = {
    keyboard: {
      async press(key, options) {
        calls.push({ key, options });
      }
    }
  };

  const profile = profileForPage(page);
  const result = await profile.act("tab-1", {
    kind: "press/key",
    key: "End",
    delayMs: 25
  });

  assert.deepEqual(calls, [{ key: "End", options: { delay: 25 } }]);
  assert.equal(result.content[0].text, "Pressed End.");
});

test("HostProfile act clicks a CSS element target", async () => {
  const calls = [];
  const locator = {
    first() {
      calls.push({ first: true });
      return locator;
    },
    async click(options) {
      calls.push({ click: options });
    }
  };
  const page = {
    locator(selector) {
      calls.push({ selector });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.act("tab-1", {
    kind: "click",
    element: "#download",
    timeoutMs: 1234
  });

  assert.equal(calls[0].selector, "#download");
  assert.deepEqual(calls[1], { first: true });
  assert.equal(calls[2].click.timeout, 1234);
  assert.equal(result.content[0].text, "Clicked #download.");
});

test("HostProfile act clicks a visible text button target", async () => {
  const calls = [];
  const locator = {
    first() {
      calls.push({ first: true });
      return locator;
    },
    async click(options) {
      calls.push({ click: options });
    }
  };
  const page = {
    getByRole(role, options) {
      calls.push({ role, options });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.act("tab-1", {
    kind: "click",
    target: "button that says Download or Export"
  });

  assert.equal(calls[0].role, "button");
  assert.match("Download", calls[0].options.name);
  assert.match("Export", calls[0].options.name);
  assert.deepEqual(calls[1], { first: true });
  assert.equal(calls[2].click.timeout, 30000);
  assert.equal(
    result.content[0].text,
    "Clicked button that says Download or Export."
  );
});

test("HostProfile preserves role-like words in accessible names", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async click() {}
  };
  const page = {
    getByRole(role, options) {
      calls.push({ role, options });
      return locator;
    }
  };

  const profile = profileForPage(page);
  await profile.act("tab-1", {
    kind: "click",
    target: "Search button"
  });
  await profile.act("tab-1", {
    kind: "click",
    target: "Search combobox"
  });

  assert.equal(calls[0].role, "button");
  assert.match("Search", calls[0].options.name);
  assert.equal(calls[1].role, "combobox");
  assert.match("Search", calls[1].options.name);
});

test("HostProfile act clicks a bare visible text target", async () => {
  const calls = [];
  const locator = {
    first() {
      calls.push({ first: true });
      return locator;
    },
    async click(options) {
      calls.push({ click: options });
    }
  };
  const page = {
    locator() {
      throw new Error("css locator should not run");
    },
    getByText(text, options) {
      calls.push({ text, options });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.act("tab-1", {
    kind: "click",
    target: "Submit"
  });

  assert.equal(calls[0].text, "Submit");
  assert.equal(calls[0].options.exact, false);
  assert.deepEqual(calls[1], { first: true });
  assert.equal(calls[2].click.timeout, 30000);
  assert.equal(result.content[0].text, "Clicked Submit.");
});

test("HostProfile keeps Search and Copy link as visible text", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async click() {}
  };
  const page = {
    getByRole() {
      throw new Error("role locator should not run");
    },
    getByText(text) {
      calls.push(text);
      return locator;
    }
  };

  const profile = profileForPage(page);
  await profile.act("tab-1", { kind: "click", target: "Search" });
  await profile.act("tab-1", { kind: "click", target: "Copy link" });

  assert.deepEqual(calls, ["Search", "Copy link"]);
});

test("HostProfile accepts descendant and child CSS selectors", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async click() {}
  };
  const page = {
    locator(selector) {
      calls.push(selector);
      return locator;
    },
    getByText() {
      throw new Error("text locator should not run");
    }
  };

  const profile = profileForPage(page);
  for (const target of ["form button", "ul li.item", "div > button"]) {
    await profile.act("tab-1", { kind: "click", target });
  }

  assert.deepEqual(calls, ["form button", "ul li.item", "div > button"]);
});

test("HostProfile act fill preserves falsey field values", async () => {
  const calls = [];
  const locator = {
    first() {
      return locator;
    },
    async fill(value, options) {
      calls.push({ value, options });
    }
  };
  const page = {
    locator(selector) {
      calls.push({ selector });
      return locator;
    }
  };

  const profile = profileForPage(page);
  const result = await profile.act("tab-1", {
    kind: "fill",
    fields: [
      { element: "#age", value: 0 },
      { element: "#enabled", value: false }
    ],
    timeoutMs: 1234
  });

  assert.equal(calls[0].selector, "#age");
  assert.deepEqual(calls[1], {
    value: "0",
    options: { timeout: 1234 }
  });
  assert.equal(calls[2].selector, "#enabled");
  assert.deepEqual(calls[3], {
    value: "false",
    options: { timeout: 1234 }
  });
  assert.equal(result.content[0].text, "Filled form fields.");
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
