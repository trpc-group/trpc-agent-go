# Experimental MCP Tasks integration

The companion `trpc-mcp-go` Tasks branch provides `WithTasks(true)` and keeps
`Connector.CallTool` unchanged. The released dependency in this repository does
not yet provide this option. The Agent adapter recognizes optional task error
and resume capabilities without changing `Connector` or existing custom clients.

With a supporting SDK, configure the ToolSet with:

```go
WithMCPOptions(
    mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28),
    mcp.WithTasks(true),
)
```

This selects the SDK's stateless HTTP client path. Omit the version option only
for a server explicitly supporting Tasks with the legacy protocol. Enable `WithSessionReconnect` as usual. After a task handle has been
received, reconnect resumes polling the same handle. If submission has an unknown
outcome and no handle, it stops without replay. For clients reporting `TasksEnabled() == true`, the timeout bounds the whole
tool invocation, including reconnection. Legacy and disabled clients retain
their existing per-attempt timeout behavior.
Keep the original endpoint and authorization identity when resuming a task.
Legacy servers may additionally bind handles to their original session. The
pinned mcpkit dual-mode example rejects cross-session recovery; this adapter
returns that failure without invoking the tool again.

The companion SDK performs `server/discover`, carries per-request metadata and
derives tool parameter headers in stateless mode. A new client under the same
authorization identity can resume the independently tested mcpkit task. This is
not full MCP 2026-07-28 support; see the companion SDK's `TASKS.md`. Normal released-SDK behavior remains available without task support.
There is no automatic cross-process handle persistence or human-input handler.

The build-tagged `task_integration_test.go` requires the companion SDK. To test
both sibling checkouts without modifying the committed dependency files, run
from the Agent repository:

```sh
scratch=$(mktemp -d)
cp go.mod "$scratch/go.mod"
cp go.sum "$scratch/go.sum"
go mod edit -modfile="$scratch/go.mod" \
  -replace="trpc.group/trpc-go/trpc-mcp-go=$(cd ../trpc-mcp-go && pwd)"
go test -race -modfile="$scratch/go.mod" -tags mcptasks ./tool/mcp
```

The fixtures expire a legacy session and drop a stateless HTTP connection.
They verify that recovery initializes or discovers through a replacement client and retrieves the original task result without
another tool invocation. It does not call any paid model endpoint.

Before marking this draft ready, settle the SDK proposal, release the agreed SDK API,
upgrade the Agent dependency, then remove the integration test build tag. Do not
commit a machine-specific local replace directive.

For independent interoperability, start the pinned mcpkit example in stateless
mode as documented by the SDK and add `MCP_TASKS_INTEROP_URL` to the test command.
`TestTasksMCPKitToolSet` calls ordinary, asynchronous and tool-error operations
through the public ToolSet/CallableTool path. Without that environment variable
it is skipped; the local reconnect fixtures still run.
