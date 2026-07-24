# ACP stdio Agent Example

This example exposes a tRPC Agent runner as an Agent Client Protocol (ACP) v1
agent over line-delimited JSON-RPC on standard input and output.

## Run

```bash
export OPENAI_API_KEY="..."
cd examples/acp
go run . -model gpt-4o-mini
```

Normally an ACP client starts this command and owns its standard streams. For
example, configure the client command as:

```text
go run /absolute/path/to/trpc-agent-go/examples/acp
```

Do not print application logs to standard output because it carries the ACP
protocol. Send diagnostics to standard error.

## Initial capability scope

The adapter supports:

- ACP v1 initialization
- session creation and close
- text and resource-link prompts
- streaming assistant text updates, with opt-in thought updates
- tool-call start/completion updates
- prompt usage and stop reasons
- session cancellation

Dynamic MCP servers, session load/list/resume, client filesystem and terminal
callbacks, image/audio prompts, and permission requests are not advertised.

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
