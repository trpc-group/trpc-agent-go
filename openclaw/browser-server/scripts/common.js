import fs from "node:fs/promises";

export const relayChromiumChannel = "chromium";

const browserCandidates = [
  process.env.OPENCLAW_BROWSER_EXECUTABLE_PATH || "",
  "/usr/bin/chromium-browser",
  "/usr/bin/chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/msedge",
  "/usr/bin/microsoft-edge",
  "/usr/bin/brave-browser"
];

export async function findBrowserExecutable() {
  for (const candidate of browserCandidates) {
    const path = `${candidate || ""}`.trim();
    if (!path) {
      continue;
    }
    try {
      await fs.access(path);
      return path;
    } catch (_error) {
      // Try the next candidate.
    }
  }
  return "";
}

export function resolveHeadlessMode(
  argv = process.argv.slice(2),
  envValue = process.env.OPENCLAW_BROWSER_HEADLESS
) {
  if (argv.includes("--headed")) {
    return false;
  }
  if (argv.includes("--headless")) {
    return true;
  }
  const raw = `${envValue || ""}`.trim();
  if (raw === "") {
    return true;
  }
  return raw === "true";
}

export function buildRelayLaunchOptions({
  extensionPath,
  executablePath,
  headless
}) {
  const options = {
    headless,
    args: [
      `--disable-extensions-except=${extensionPath}`,
      `--load-extension=${extensionPath}`
    ]
  };
  const path = `${executablePath || ""}`.trim();
  if (path !== "") {
    options.executablePath = path;
    return options;
  }
  options.channel = relayChromiumChannel;
  return options;
}

export async function fetchJSON(url, init = undefined) {
  const response = await fetch(url, init);
  const payload = await response.json();
  if (!response.ok) {
    throw new Error(JSON.stringify(payload));
  }
  return payload;
}

export function authHeaders(token) {
  const trimmed = `${token || ""}`.trim();
  if (!trimmed) {
    return {};
  }
  return {
    Authorization: `Bearer ${trimmed}`
  };
}

export function extractText(payload) {
  return (payload.content || [])
    .filter((item) => item.type === "text")
    .map((item) => item.text)
    .join("\n");
}

export function extractFirstRef(text) {
  const match = `${text || ""}`.match(/^\[([^\]]+)\]/m);
  if (!match) {
    return "";
  }
  return match[1];
}

export async function closeServer(server, runtime = undefined) {
  if (runtime && typeof runtime.stop === "function") {
    try {
      await runtime.stop("openclaw");
    } catch (_error) {
      // Ignore shutdown errors from an already-stopped profile.
    }
  }
  await new Promise((resolve, reject) => {
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}
