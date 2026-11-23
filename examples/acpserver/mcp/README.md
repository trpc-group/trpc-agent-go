# ACP stdio MCP Example

This example shows how an ACP client passes a session-scoped stdio MCP server
through `session/new`. The ACP server starts the MCP process, injects its tools
for that session, and exposes the prefixed tool name to the model.

The example has three processes:

```text
examples/acpserver/mcp/
├── client/     # ACP client; launches the ACP server
├── server/     # ACP agent server; starts the session MCP server
└── mcpserver/  # MCP server child process with a greet tool
```

The MCP server is named `demo`, so the MCP tool `greet` is exposed to the model
as `demo_greet`. The client also passes `ACP_EXAMPLE_MCP_GREETING` through the
ACP `McpServerStdio.Env` field.

## Prerequisites

Configure an OpenAI-compatible model:

```bash
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>" # Optional for compatible endpoints.
```

## Run

From the `examples` module:

```bash
cd examples
go run ./acpserver/mcp/client
```

The client starts `go run ./server`, creates an ACP session with the stdio MCP
server configuration, sends a prompt that asks for `demo_greet`, prints the
streamed ACP updates, and closes the session.

The feature directory can also be used directly:

```bash
cd examples/acpserver/mcp
go run ./client
```

## Options

| Flag | Default | Description |
| --- | --- | --- |
| `-model` | `gpt-4o-mini` | Model passed to the ACP server. |
| `-prompt` | Built-in MCP prompt | Prompt sent to the ACP agent. |

## Expected Output

The output includes an initialized protocol version, a session ID, a
`demo_greet` tool call, and assistant text containing:

```text
hello from the ACP stdio MCP example
```

The ACP server and MCP server use private pipes. Only the ACP client writes
user-facing output to the terminal.
