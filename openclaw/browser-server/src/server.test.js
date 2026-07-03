import assert from "node:assert/strict";
import test from "node:test";
import { readConfig } from "./server.js";

test("readConfig accepts TRPC_CLAW browser env aliases", () => {
  const got = readConfig({
    OPENCLAW_BROWSER_SERVER_ADDR: "127.0.0.1:19790",
    TRPC_CLAW_BROWSER_MODE: "interactive",
    TRPC_CLAW_BROWSER_PATH: "/tmp/chromium"
  });

  assert.equal(got.host, "127.0.0.1");
  assert.equal(got.port, 19790);
  assert.equal(got.headless, false);
  assert.equal(got.executablePath, "/tmp/chromium");
});

test("readConfig keeps OPENCLAW browser env precedence", () => {
  const got = readConfig({
    OPENCLAW_BROWSER_SERVER_ADDR: "127.0.0.1:19790",
    OPENCLAW_BROWSER_HEADLESS: "true",
    OPENCLAW_BROWSER_EXECUTABLE_PATH: "/opt/chrome",
    TRPC_CLAW_BROWSER_MODE: "interactive",
    TRPC_CLAW_BROWSER_PATH: "/tmp/chromium"
  });

  assert.equal(got.headless, true);
  assert.equal(got.executablePath, "/opt/chrome");
});
