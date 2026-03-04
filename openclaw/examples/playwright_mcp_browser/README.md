# Playwright MCP Browser Use (STDIO) Example

This example shows how to give the OpenClaw demo a real "browser use"
capability by attaching the **Playwright MCP server** as an **MCP ToolSet**
(`transport: stdio`).

What you get:

- OpenClaw starts the Playwright MCP server via `npx`.
- The agent can call the server's browser tools (navigate/click/type/etc.).
- When a tool returns a screenshot, OpenClaw forwards it back to the model as a
  real image message (required for vision-based browsing).

## Requirements

- Go (to run OpenClaw).
- Node.js with `npx` on your PATH (to start `@playwright/mcp`).

Notes:

- The first run may download Playwright + a Chromium browser. This can take a
  few minutes.
- If you are on Linux, Playwright may require additional system deps.

## The MCP server command (what OpenClaw runs)

In this example, OpenClaw starts the Playwright MCP server as a subprocess:

```bash
npx --yes @playwright/mcp@latest --headless --isolated --caps vision
```

This uses MCP **stdio** transport (no HTTP port).

## Run

1) Export a model key (OpenAI-compatible):

```bash
export OPENAI_API_KEY="your-api-key"
```

2) Start OpenClaw with the example config:

```bash
cd openclaw
go run ./cmd/openclaw -config ./examples/playwright_mcp_browser/openclaw.yaml
```

If port `:8080` is already in use, override it with a CLI flag:

```bash
cd openclaw
go run ./cmd/openclaw \
  -config ./examples/playwright_mcp_browser/openclaw.yaml \
  -http-addr :18080
```

If you change the port, update the URLs in the `curl` commands below.

## Try it

Send one message via HTTP:

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Use the browser toolset to open https://example.com, take a screenshot, then tell me the page title."}'
```

Tips:

- Tools from ToolSets are namespaced automatically. In this config the toolset
  name is `browser`, so tool names will look like `browser_<tool_name>`.
- If the model does not call tools, try being more explicit:
  "You must use the browser tools for this task."
