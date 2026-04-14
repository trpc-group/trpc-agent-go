import assert from "node:assert/strict";
import test from "node:test";
import {
  buildRelayLaunchOptions,
  extractFirstRef,
  relayChromiumChannel,
  resolveHeadlessMode
} from "./common.js";

test("resolveHeadlessMode prefers headed CLI flag", () => {
  const got = resolveHeadlessMode(["--headed"], "true");
  assert.equal(got, false);
});

test("resolveHeadlessMode prefers headless CLI flag", () => {
  const got = resolveHeadlessMode(["--headless"], "false");
  assert.equal(got, true);
});

test("resolveHeadlessMode falls back to env value", () => {
  assert.equal(resolveHeadlessMode([], "false"), false);
  assert.equal(resolveHeadlessMode([], "true"), true);
  assert.equal(resolveHeadlessMode([], ""), true);
});

test("buildRelayLaunchOptions prefers bundled chromium", () => {
  const got = buildRelayLaunchOptions({
    extensionPath: "/tmp/extension",
    executablePath: "",
    headless: true
  });
  assert.equal(got.channel, relayChromiumChannel);
  assert.equal(got.executablePath, undefined);
  assert.deepEqual(got.args, [
    "--disable-extensions-except=/tmp/extension",
    "--load-extension=/tmp/extension"
  ]);
});

test("buildRelayLaunchOptions keeps explicit executable path", () => {
  const got = buildRelayLaunchOptions({
    extensionPath: "/tmp/extension",
    executablePath: "/tmp/chromium",
    headless: false
  });
  assert.equal(got.channel, undefined);
  assert.equal(got.executablePath, "/tmp/chromium");
});

test("extractFirstRef reads the first snapshot ref", () => {
  const got = extractFirstRef([
    "Page: Example Domain",
    "URL: https://example.com/",
    "[abc123] a \"Learn more\""
  ].join("\n"));
  assert.equal(got, "abc123");
});
