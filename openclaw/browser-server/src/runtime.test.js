import test from "node:test";
import assert from "node:assert/strict";
import { BrowserRuntime } from "./runtime.js";

test("BrowserRuntime normalizes press/key for chrome relay acts", async () => {
  const runtime = new BrowserRuntime({ policy: {} });
  const calls = [];
  runtime.chromeRelay.execute = async (targetId, action, args) => {
    calls.push({ targetId, action, args });
    return { content: [{ type: "text", text: "ok" }] };
  };

  const result = await runtime.act("chrome", {
    targetId: "tab-1",
    request: {
      kind: "press/key",
      key: "End"
    }
  });

  assert.equal(result.content[0].text, "ok");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].targetId, "tab-1");
  assert.equal(calls[0].action, "press");
  assert.deepEqual(calls[0].args, {
    kind: "press/key",
    key: "End"
  });
});
