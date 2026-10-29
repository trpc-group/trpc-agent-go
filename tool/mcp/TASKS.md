# Experimental MCP Tasks integration

The companion `trpc-mcp-go` Tasks branch provides `WithTasks(true)` and keeps
`Connector.CallTool` unchanged. The released dependency in this repository does
not yet provide this option. The Agent adapter recognizes optional task error
and resume capabilities without changing `Connector` or existing custom clients.

With a supporting SDK, configure `WithMCPOptions(mcp.WithTasks(true))` on the
ToolSet and enable `WithSessionReconnect` as usual. After a task handle has been
received, reconnect resumes polling the same handle. If submission has an unknown
outcome and no handle, it stops without replay. For clients reporting `TasksEnabled() == true`, the timeout bounds the whole
tool invocation, including reconnection. Legacy and disabled clients retain
their existing per-attempt timeout behavior.
Keep the original endpoint and authorization identity when resuming a task.
Legacy servers may additionally bind handles to their original session. The
pinned mcpkit dual-mode example rejects cross-session recovery; this adapter
returns that failure without invoking the tool again.

This is a local compatibility prototype, not full MCP 2026-07-28 support. The SDK
still uses its existing initialization protocol; see the companion SDK's
`TASKS.md`. Normal released-SDK behavior remains available without task support.
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

The fixture expires the first polling connection and verifies that recovery
initializes another connection and retrieves the original task result without
another tool invocation. It does not call any paid model endpoint.

Before marking this draft ready, settle the SDK proposal, release the agreed SDK API,
upgrade the Agent dependency, then remove the integration test build tag. Do not
commit a machine-specific local replace directive.
