import test from "node:test";
import assert from "node:assert/strict";
import { ChromeRelay } from "./chrome-relay.js";

function socketStub() {
  return {
    readyState: 1,
    sent: [],
    send(payload) {
      this.sent.push(JSON.parse(payload));
    }
  };
}

test("ChromeRelay updates tabs from attach messages", () => {
  const relay = new ChromeRelay();

  relay.handleMessage("client-1", {
    type: "attached",
    targetId: "tab-7",
    tabId: 7,
    title: "Example",
    url: "https://example.com",
    active: true
  });

  const tabs = relay.listTabs();
  assert.equal(tabs.length, 1);
  assert.equal(tabs[0].targetId, "tab-7");
  assert.equal(tabs[0].title, "Example");
});

test("ChromeRelay execute sends command and resolves response", async () => {
  const relay = new ChromeRelay();
  const socket = socketStub();
  relay.registerSocket("client-1", socket);
  relay.updateAttachedTab({
    clientId: "client-1",
    targetId: "tab-5",
    tabId: 5,
    active: true
  });

  const pending = relay.execute("tab-5", "snapshot", { targetId: "tab-5" });
  assert.equal(socket.sent.length, 1);
  assert.equal(socket.sent[0].action, "snapshot");

  relay.handleMessage("client-1", {
    type: "result",
    id: socket.sent[0].id,
    ok: true,
    data: { content: [{ type: "text", text: "done" }] }
  });

  const result = await pending;
  assert.equal(result.content[0].text, "done");
});

test("ChromeRelay status exposes clients and tabs", () => {
  const relay = new ChromeRelay();
  const socket = socketStub();
  relay.registerSocket("client-1", socket);
  relay.updateAttachedTab({
    clientId: "client-1",
    targetId: "tab-5",
    tabId: 5,
    title: "Example",
    url: "https://example.com",
    active: true
  });

  const status = relay.status();
  assert.equal(status.clients.length, 1);
  assert.equal(status.clients[0].clientId, "client-1");
  assert.equal(status.clients[0].tabCount, 1);
  assert.equal(status.tabs.length, 1);
});
