import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const fileName = fileURLToPath(import.meta.url);
const currentDir = path.dirname(fileName);
const backgroundPath = path.join(currentDir, "background.js");

function chromeStub(overrides = {}) {
  const windows = {
    async update() {},
    ...(overrides.windows || {})
  };
  const tabs = {
    onActivated: { addListener() {} },
    onUpdated: { addListener() {} },
    onRemoved: { addListener() {} },
    async query() {
      return [];
    },
    async get() {
      return { id: 1, windowId: 1 };
    },
    async update() {},
    async remove() {},
    async captureVisibleTab() {
      return "data:image/png;base64,";
    },
    ...(overrides.tabs || {})
  };
  const scripting = {
    async executeScript() {
      return [{ result: null }];
    },
    ...(overrides.scripting || {})
  };
  return {
    runtime: {
      onInstalled: { addListener() {} },
      onMessage: { addListener() {} }
    },
    storage: {
      local: {
        async get() {
          return {};
        },
        async set() {}
      }
    },
    tabs,
    windows,
    scripting,
    ...overrides,
    tabs,
    windows,
    scripting
  };
}

function loadBackgroundSandbox(overrides = {}) {
  const source = fs.readFileSync(backgroundPath, "utf8");
  const sandbox = {
    URL,
    Buffer,
    Map,
    JSON,
    Promise,
    Uint8Array,
    btoa(value) {
      return Buffer.from(value, "binary").toString("base64");
    },
    setTimeout,
    clearTimeout,
    chrome: chromeStub(overrides.chrome),
    crypto: {
      randomUUID() {
        return "client-1";
      }
    },
    WebSocket: class WebSocketStub {},
    ...overrides.globals,
    globalThis: null
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(source, sandbox, {
    filename: backgroundPath
  });
  return sandbox;
}

function loadRelayExecutor() {
  return loadBackgroundSandbox().relayExecutor;
}

function executorSandbox(target) {
  class EventStub {
    constructor(type, init = {}) {
      this.type = type;
      Object.assign(this, init);
    }
  }

  return {
    Math,
    Promise,
    Date,
    setTimeout,
    clearTimeout,
    Event: EventStub,
    MouseEvent: EventStub,
    KeyboardEvent: EventStub,
    DragEvent: EventStub,
    window: {
      location: { href: "https://example.com" },
      getComputedStyle() {
        return {
          visibility: "visible",
          display: "block"
        };
      }
    },
    document: {
      title: "Example",
      activeElement: null,
      body: {
        innerText: "",
        dispatchEvent() {}
      },
      querySelector() {
        return target;
      },
      querySelectorAll() {
        return [];
      }
    }
  };
}

test("relayExecutor click result is self-contained", async () => {
  const relayExecutor = loadRelayExecutor();
  const state = { clicked: false };
  const target = {
    click() {
      state.clicked = true;
    }
  };
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    executorSandbox(target)
  );

  const result = await isolatedExecutor({
    action: "click",
    args: { ref: "e1" }
  });

  assert.equal(state.clicked, true);
  assert.equal(result.content.length, 1);
  assert.equal(result.content[0].type, "text");
  assert.equal(result.content[0].text, "Clicked e1.");
});

test("relayExecutor evaluate returns element value", async () => {
  const relayExecutor = loadRelayExecutor();
  const target = {
    textContent: "Example Domain",
    getAttribute() {
      return null;
    }
  };
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    executorSandbox(target)
  );

  const result = await isolatedExecutor({
    action: "evaluate",
    args: {
      ref: "e1",
      fn: "(element) => element.textContent"
    }
  });

  assert.equal(result.value, "Example Domain");
  assert.equal(result.content[0].text, "\"Example Domain\"");
});

test("relayExecutor type supports slowly and submit", async () => {
  const relayExecutor = loadRelayExecutor();
  const state = { submitted: false };
  const target = {
    value: "",
    form: {
      requestSubmit() {
        state.submitted = true;
      }
    },
    focus() {},
    dispatchEvent() {},
    getAttribute() {
      return null;
    }
  };
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    executorSandbox(target)
  );

  await isolatedExecutor({
    action: "type",
    args: {
      ref: "e1",
      text: "hello",
      slowly: true,
      submit: true
    }
  });

  assert.equal(target.value, "hello");
  assert.equal(state.submitted, true);
});

test("relayExecutor wait polls until the url matches", async () => {
  const relayExecutor = loadRelayExecutor();
  const sandbox = executorSandbox({
    getAttribute() {
      return null;
    }
  });
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    sandbox
  );

  setTimeout(() => {
    sandbox.window.location.href = "https://example.com/docs";
  }, 10);

  const result = await isolatedExecutor({
    action: "wait",
    args: {
      url: "https://example.com/docs",
      timeoutMs: 500
    }
  });

  assert.equal(result.content[0].text, "URL matched: https://example.com/docs");
});

test("relayExecutor wait supports fn predicates", async () => {
  const relayExecutor = loadRelayExecutor();
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    executorSandbox({
      getAttribute() {
        return null;
      }
    })
  );

  const result = await isolatedExecutor({
    action: "wait",
    args: {
      fn: "() => document.title",
      timeoutMs: 100
    }
  });

  assert.equal(result.content[0].text, "Wait predicate matched.");
});

test("relayExecutor scrollIntoView uses the DOM API when available", async () => {
  const relayExecutor = loadRelayExecutor();
  const state = { scrolled: false };
  const isolatedExecutor = vm.runInNewContext(
    `(${relayExecutor.toString()})`,
    executorSandbox({
      scrollIntoView() {
        state.scrolled = true;
      },
      getAttribute() {
        return null;
      }
    })
  );

  const result = await isolatedExecutor({
    action: "scrollIntoView",
    args: { ref: "e1" }
  });

  assert.equal(state.scrolled, true);
  assert.equal(result.content[0].text, "Scrolled e1 into view.");
});

test("executeCommand resize updates the tab window", async () => {
  const updates = [];
  const sandbox = loadBackgroundSandbox({
    chrome: chromeStub({
      windows: {
        async update(windowId, options) {
          updates.push({ windowId, options });
        }
      }
    })
  });

  const result = await sandbox.executeCommand({
    action: "resize",
    tabId: 1,
    args: {
      width: 900,
      height: 700
    }
  });

  assert.equal(updates.length, 1);
  assert.equal(updates[0].windowId, 1);
  assert.equal(updates[0].options.width, 900);
  assert.equal(updates[0].options.height, 700);
  assert.equal(result.content[0].text, "Resized window.");
});

test("executeCommand screenshot crops relay refs and honors jpeg", async () => {
  const calls = {
    drawImage: null,
    executeArgs: null,
    format: "",
    type: ""
  };
  class OffscreenCanvasStub {
    constructor(width, height) {
      calls.canvas = { width, height };
    }

    getContext(kind) {
      assert.equal(kind, "2d");
      return {
        drawImage(...args) {
          calls.drawImage = args;
        }
      };
    }

    async convertToBlob(options) {
      calls.type = options.type;
      return {
        async arrayBuffer() {
          return Uint8Array.from(Buffer.from("cropped")).buffer;
        }
      };
    }
  }

  const sandbox = loadBackgroundSandbox({
    chrome: chromeStub({
      tabs: {
        async get() {
          return { id: 1, windowId: 7 };
        },
        async captureVisibleTab(windowId, options) {
          assert.equal(windowId, 7);
          calls.format = options.format;
          return "data:image/png;base64,ZmFrZS1mdWxs";
        }
      },
      scripting: {
        async executeScript(options) {
          calls.executeArgs = options.args;
          return [{
            result: {
              x: 10,
              y: 20,
              width: 30,
              height: 40,
              devicePixelRatio: 2
            }
          }];
        }
      }
    }),
    globals: {
      async fetch(url) {
        assert.equal(url, "data:image/png;base64,ZmFrZS1mdWxs");
        return {
          async blob() {
            return { kind: "image" };
          }
        };
      },
      async createImageBitmap(blob) {
        assert.deepEqual(blob, { kind: "image" });
        return {
          width: 500,
          height: 400,
          close() {}
        };
      },
      OffscreenCanvas: OffscreenCanvasStub
    }
  });

  const result = await sandbox.executeCommand({
    action: "screenshot",
    tabId: 1,
    args: {
      ref: "e1",
      type: "jpeg"
    }
  });

  assert.equal(calls.format, "jpeg");
  assert.equal(calls.executeArgs.length, 1);
  assert.equal(calls.executeArgs[0].ref, "e1");
  assert.equal(calls.executeArgs[0].element, "");
  assert.deepEqual(calls.canvas, {
    width: 60,
    height: 80
  });
  assert.deepEqual(calls.drawImage.slice(1), [
    20,
    40,
    60,
    80,
    0,
    0,
    60,
    80
  ]);
  assert.equal(calls.type, "image/jpeg");
  assert.equal(result.content[0].mimeType, "image/jpeg");
  assert.equal(
    result.content[0].data,
    Buffer.from("cropped").toString("base64")
  );
});
