import http from "node:http";
import { createNavigationPolicy } from "./ssrf.js";
import { BrowserRuntime } from "./runtime.js";

function readBool(value, fallback) {
  const raw = `${value || ""}`.trim();
  if (raw === "") {
    return fallback;
  }
  return raw === "true";
}

function readConfig(env = process.env) {
  const addr = env.OPENCLAW_BROWSER_SERVER_ADDR || "127.0.0.1:19790";
  const [host, portText] = addr.split(":");
  return {
    host,
    port: Number(portText) || 19790,
    token: `${env.OPENCLAW_BROWSER_SERVER_TOKEN || ""}`.trim(),
    headless: readBool(env.OPENCLAW_BROWSER_HEADLESS, true),
    executablePath: `${env.OPENCLAW_BROWSER_EXECUTABLE_PATH || ""}`.trim(),
    policy: createNavigationPolicy(env)
  };
}

function json(res, status, body) {
  res.writeHead(status, {
    "content-type": "application/json; charset=utf-8"
  });
  res.end(JSON.stringify(body));
}

async function readJSON(req) {
  const chunks = [];
  for await (const chunk of req) {
    chunks.push(chunk);
  }
  if (chunks.length === 0) {
    return {};
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function requireAuth(req, config) {
  if (!config.token) {
    return true;
  }
  const auth = `${req.headers.authorization || ""}`.trim();
  if (auth === `Bearer ${config.token}`) {
    return true;
  }
  const url = new URL(req.url, `http://${req.headers.host || "localhost"}`);
  return `${url.searchParams.get("token") || ""}`.trim() === config.token;
}

function profileFromURL(url, body) {
  return (
    `${body.profile || ""}`.trim() ||
    `${url.searchParams.get("profile") || ""}`.trim() ||
    "openclaw"
  );
}

export async function startServer(env = process.env) {
  const config = readConfig(env);
  const runtime = new BrowserRuntime(config);

  const server = http.createServer(async (req, res) => {
    if (!requireAuth(req, config)) {
      json(res, 401, { error: "unauthorized" });
      return;
    }

    const url = new URL(req.url, `http://${req.headers.host || "localhost"}`);
    try {
      if (req.method === "GET" && url.pathname === "/") {
        json(res, 200, {
          ok: true,
          state: "ready",
          profiles: await runtime.profiles()
        });
        return;
      }
      if (req.method === "GET" && url.pathname === "/profiles") {
        json(res, 200, await runtime.profiles());
        return;
      }
      if (req.method === "GET" && url.pathname === "/extension/status") {
        json(res, 200, await runtime.extensionStatus());
        return;
      }

      const body = req.method === "GET" ? {} : await readJSON(req);
      const profile = profileFromURL(url, body);

      if (req.method === "POST" && url.pathname === "/start") {
        json(res, 200, await runtime.start(profile));
        return;
      }
      if (req.method === "POST" && url.pathname === "/stop") {
        json(res, 200, await runtime.stop(profile));
        return;
      }
      if (req.method === "GET" && url.pathname === "/tabs") {
        json(res, 200, await runtime.tabs(profile));
        return;
      }
      if (req.method === "POST" && url.pathname === "/tabs/open") {
        json(res, 200, await runtime.open(profile, body.url || ""));
        return;
      }
      if (req.method === "POST" && url.pathname === "/tabs/focus") {
        json(res, 200, await runtime.focus(profile, body.targetId || ""));
        return;
      }
      if (
        req.method === "DELETE" &&
        url.pathname.startsWith("/tabs/")
      ) {
        const targetId = decodeURIComponent(url.pathname.slice(6));
        json(res, 200, await runtime.close(profile, targetId));
        return;
      }
      if (req.method === "POST" && url.pathname === "/snapshot") {
        json(res, 200, await runtime.snapshot(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/screenshot") {
        json(res, 200, await runtime.screenshot(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/navigate") {
        json(res, 200, await runtime.navigate(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/console") {
        json(res, 200, await runtime.console(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/pdf") {
        json(res, 200, await runtime.pdf(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/upload") {
        json(res, 200, await runtime.upload(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/dialog") {
        json(res, 200, await runtime.dialog(profile, body));
        return;
      }
      if (req.method === "POST" && url.pathname === "/act") {
        json(res, 200, await runtime.act(profile, body));
        return;
      }
      json(res, 404, { error: "not found" });
    } catch (error) {
      json(res, 400, {
        error: `${error.message || error}`
      });
    }
  });

  runtime.attachWebSocketServer(server);

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(config.port, config.host, resolve);
  });

  console.log(
    `OpenClaw browser server listening on http://${config.host}:${config.port}`
  );

  return { server, runtime, config };
}
