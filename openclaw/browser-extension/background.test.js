import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const fileName = fileURLToPath(import.meta.url);
const currentDir = path.dirname(fileName);
const backgroundPath = path.join(currentDir, "background.js");

function chromeStub() {
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
    tabs: {
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
      }
    },
    scripting: {
      async executeScript() {
        return [{ result: null }];
      }
    }
  };
}

function loadRelayExecutor() {
  const source = fs.readFileSync(backgroundPath, "utf8");
  const sandbox = {
    URL,
    Map,
    JSON,
    chrome: chromeStub(),
    crypto: {
      randomUUID() {
        return "client-1";
      }
    },
    WebSocket: class WebSocketStub {},
    globalThis: null
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(source, sandbox, {
    filename: backgroundPath
  });
  return sandbox.relayExecutor;
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
    Event: EventStub,
    MouseEvent: EventStub,
    KeyboardEvent: EventStub,
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

test("relayExecutor click result is self-contained", () => {
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

  const result = isolatedExecutor({
    action: "click",
    args: { ref: "e1" }
  });

  assert.equal(state.clicked, true);
  assert.equal(result.content.length, 1);
  assert.equal(result.content[0].type, "text");
  assert.equal(result.content[0].text, "Clicked e1.");
});
