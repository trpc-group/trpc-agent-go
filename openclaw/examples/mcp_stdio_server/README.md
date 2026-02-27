# MCP ToolSet (STDIO) Example

This example shows how to attach an **MCP ToolSet** to the OpenClaw demo
using the built-in `tools.toolsets` configuration.

It uses:

- an MCP server implemented in `main.go` (stdio transport)
- an OpenClaw config file `openclaw.yaml` that starts that MCP server by
  running `go run ./examples/mcp_stdio_server`

## Run

1) Export a model key (OpenAI-compatible):

```bash
export OPENAI_API_KEY="your-api-key"
```

2) Start OpenClaw:

```bash
cd openclaw
go run ./cmd/openclaw -config ./examples/mcp_stdio_server/openclaw.yaml
```

If you do not have model credentials, you can still start the gateway
with `-mode mock`, but the mock model will not call tools:

```bash
cd openclaw
go run ./cmd/openclaw -config ./examples/mcp_stdio_server/openclaw.yaml -mode mock
```

## Try it

Send a message via HTTP:

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Use tool demo_mcp_echo with {\"text\":\"hello\"}"}'
```

Notes:

- Tools from ToolSets are namespaced automatically:
  `demo_mcp_echo`, `demo_mcp_add`, ...
- For MCP, setting `tools.refresh_toolsets_on_run: true` is recommended,
  so tool discovery is refreshed on each run.

