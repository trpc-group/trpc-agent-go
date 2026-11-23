# ACP stdio Agent Example

This example exposes a tRPC Agent runner as an Agent Client Protocol (ACP) v1
agent over line-delimited JSON-RPC on standard input and output.

## Examples

- [`mcp/`](mcp/) shows an ACP client starting an ACP server with a
  session-scoped stdio MCP server. The example client launches the ACP server
  and the MCP server so the complete tool-call flow runs with one command.
- [`clientfiles/`](clientfiles/) shows ACP client file reads and writes. The
  client advertises filesystem capabilities, handles permission requests, and
  verifies a file copied through the ACP file tools.

## Run

```bash
export OPENAI_API_KEY="..."
cd examples/acpserver
go run . -model gpt-4o-mini
```

Normally an ACP client starts this command and owns its standard streams. For
example, configure the client command as:

```text
go run /absolute/path/to/trpc-agent-go/examples/acpserver
```

Do not print application logs to standard output because it carries the ACP
protocol. Send diagnostics to standard error.

The feature examples in `mcp/` and `clientfiles/` also use standard input and
output for the ACP connection. Their README files show the recommended
one-command client flow.

## Initial capability scope

The adapter supports:

- ACP v1 initialization
- session creation and close
- text and resource-link prompts
- streaming assistant text updates, with opt-in thought updates
- tool-call start/completion updates
- prompt usage and stop reasons
- session cancellation
- session-scoped stdio MCP servers
- optional ACP client file reads and writes

ACP client file reads and writes are disabled by default. Enable them
independently when constructing the server:

```go
server, err := acp.New(
	r,
	acp.WithClientFileReadEnabled(true),
	acp.WithClientFileWriteEnabled(true),
)
```

The tools are injected only when the client also advertises the corresponding
`fs.readTextFile` or `fs.writeTextFile` capability. Enabling a capability on
the server when the client does not advertise it logs a warning and exposes no
corresponding tool. Writes request ACP permission before calling the client.
Session load/list/resume, terminal callbacks, image/audio prompts, and HTTP/SSE
MCP remain unsupported.

Client file reads default to `line=1` and `limit=2000`. Explicit values must be
positive. `line` has no upper cap because it is a pagination cursor; `limit` is
capped at 10,000. The file tool descriptions include the current session
workspace so the model can construct paths without a separate `cwd` argument.
Successful writes return only the resolved absolute `path` and a `message`.

ACP supplies an absolute working directory for each session. Use
`acp.WithRunOptionsFunc` when constructing the server to pass that directory to
a request-scoped coding agent or agent factory:

```go
acp.WithRunOptionsFunc(func(session acp.Session) []agent.RunOption {
	return []agent.RunOption{
		agent.WithRuntimeState(map[string]any{"workspace": session.CWD}),
	}
})
```
