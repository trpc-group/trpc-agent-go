# Append-only Context Diff

This example demonstrates a durable append-only context diff pattern with
`agent.WithSessionContextSource(...)`.

It uses a deterministic debug model, so it does not require an API key or
network access. The program prints the final `request.Messages` for each model
call.

```bash
cd examples
go run ./prompt/append_only_diff
```

The source owns a small `workspace_policy` state and materializes it as ordinary
session messages:

- turn 1 returns a complete snapshot because no previous projection exists
- turn 2 returns unchanged because the policy did not change
- turn 3 returns an update from `policy-v1` to `policy-v2`
- turn 4 returns an update from `policy-v2` to `policy-v3`

Runner persists every returned context message before the current user message.
The final active history is append-only:

```text
Current workspace policy context: revision policy-v1
U1
A1
U2
A2
Workspace policy context update: revision policy-v1 -> policy-v2
U3
A3
Workspace policy context update: revision policy-v2 -> policy-v3
U4
A4
```

`WithSessionContextMessages(...)` is enough when callers just want to persist a
one-off message before the current user turn. `WithSessionContextSource(...)` is
the durable context producer primitive: business code still returns plain
`[]model.Message`, while Runner stores the source-owned compact opaque state,
passes it back on the next run, and calls `args.NeedsSnapshot()` when the
previous projection is no longer guaranteed to be visible in restored history.
