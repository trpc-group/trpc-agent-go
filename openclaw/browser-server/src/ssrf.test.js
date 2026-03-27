import test from "node:test";
import assert from "node:assert/strict";
import {
  createNavigationPolicy,
  validateNavigationURL
} from "./ssrf.js";

test("validateNavigationURL blocks loopback by default", async () => {
  const policy = createNavigationPolicy({});

  await assert.rejects(
    () => validateNavigationURL("http://127.0.0.1:8080", policy),
    /Blocked loopback/
  );
});

test("validateNavigationURL allows allowed domain", async () => {
  const policy = createNavigationPolicy({
    OPENCLAW_BROWSER_ALLOWED_DOMAINS: "example.com"
  });

  await validateNavigationURL("https://example.com/docs", policy);
});

test("validateNavigationURL blocks disallowed domain", async () => {
  const policy = createNavigationPolicy({
    OPENCLAW_BROWSER_ALLOWED_DOMAINS: "example.com"
  });

  await assert.rejects(
    () => validateNavigationURL("https://not-example.com", policy),
    /not allowed/
  );
});
