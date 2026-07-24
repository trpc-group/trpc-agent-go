# tool_safety_guard example

This example shows how to wire `safety.Guard` into the `trpc-agent-go` framework
as a `tool.PermissionPolicy` so that every tool call is scanned before execution.

## What it does

- Constructs a default `safety.Guard` (with the eight built-in rules).
- Simulates several tool calls (with JSON `{"command": "..."}` arguments).
- Prints the resulting `tool.PermissionDecision` for each call.

Expected output (one line per case):

```
[safe: list directory] command="ls -la" -> action=allow reason=""
...
[deny: rm -rf /] command="rm -rf /" -> action=deny reason="dangerous command: rm -rf /"
...
[ask: git push origin main] command="git push origin main" -> action=ask reason="requires human review: git push"
```

## How to run

```bash
cd trpc-agent-go
go run ./examples/tool_safety_guard
```

## Integration with Runner

To use this Guard inside a real Runner, pass it as a per-run option:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

guard := safety.NewGuard()
r := runner.NewRunner(...)
events, err := r.Run(
    ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

Any tool call (e.g. `exec_command`, `run_python`, `http_fetch`) will be scanned
before the framework dispatches it. `deny` skips execution, `ask` returns an
approval-required result, and `allow` proceeds normally.
