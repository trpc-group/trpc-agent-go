import fs from "node:fs/promises";

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

export async function closeServer(server) {
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
