# Experimental MCP Tasks integration

The released `trpc-mcp-go v0.0.20` dependency provides `WithTasks(true)` and keeps
`Connector.CallTool` unchanged. The Agent adapter recognizes optional task error
and resume capabilities without changing `Connector` or existing custom clients.

Select the streamable HTTP transport and configure its SDK client with:

```go
set := NewMCPToolSet(
    ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "http://localhost:8080/mcp",
    },
    WithMCPOptions(
        mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28),
        mcp.WithTasks(true),
    ),
    WithSessionReconnect(3),
)
```

`ConnectionConfig.Transport` selects the transport; the SDK options select the
stateless protocol and enable Tasks on that streamable client. Omit the version
option only for a server explicitly supporting Tasks with the legacy protocol.
After a task handle has been received, reconnect resumes polling the same handle.
If submission has an unknown outcome and no handle, it stops without replay.
For clients reporting `TasksEnabled() == true`, the timeout bounds the whole tool
invocation, including waiting to reconnect. If the context expires while a
caller is waiting, that caller stops waiting. A task status returned by the
server, such as `failed` or `input_required`, does not trigger reconnection.
Clients with Tasks disabled retain their existing per-attempt timeout behavior.
Keep the original endpoint and authorization identity when resuming a task.
Legacy servers may additionally bind handles to their original session. The
pinned mcpkit dual-mode example rejects cross-session recovery; this adapter
returns that failure without invoking the tool again.

The companion SDK performs `server/discover`, carries per-request metadata and
derives tool parameter headers in stateless mode. A new client under the same
authorization identity can resume the independently tested mcpkit task. This is
not full MCP 2026-07-28 support; see the SDK's `TASKS.md`. Tasks are disabled by
default, preserving ordinary tool-call behavior.
There is no automatic cross-process handle persistence or human-input handler.

Framework tool-call retries in LLMAgent and Graph, and Graph node-level retries,
honor any error reporting `ToolCallReplaySafe() == false` in the wrapped or joined
error tree, including custom retry policies. An interrupted task returns its
original error and handle rather than submitting the tool again after the session
reconnect budget is exhausted. This does not turn the task error into an Agent
stop signal.

The reconnect integration tests run in the normal package test suite using the
released SDK, without build tags or local replacements. From the Agent repository:

```sh
go test -race ./tool/mcp
```

The fixtures expire a legacy session and drop a stateless HTTP connection.
They verify that recovery initializes or discovers through a replacement client and retrieves the original task result without
another tool invocation. It does not call any paid model endpoint.

Additional regressions exercise the LLMAgent callable-tool processor, Graph's
callable-tool closure, and a complete Graph tools node with node-level retries.
They verify single execution and error propagation using replay-unsafe test errors.

For independent interoperability, start the pinned mcpkit example in stateless
mode as documented by the SDK and add `MCP_TASKS_INTEROP_URL` to the test command.
`TestTasksMCPKitToolSet` calls ordinary, asynchronous and tool-error operations
through the public ToolSet/CallableTool path. Without that environment variable
it is skipped; the local reconnect fixtures still run.
