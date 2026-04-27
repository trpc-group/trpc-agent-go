# Dynamic Subagent Runtime

The `subagent` package runs dynamic background agents from application code.
It is useful when a user turn should return quickly while delegated work keeps
running in another session.

The runtime provides:

- `Spawn` to create a background run.
- `List`, `Get`, `Cancel`, and `Wait` to control runs.
- `MemoryStore` and `FileStore` for run state.
- lifecycle observation through `Observer`.
- `tool/subagent` tools for agent-driven spawning from a parent agent.

## Code-side usage

Create one normal `runner.Runner`, then wrap it with `subagent.Service`.
The same runner decides which agent executes the child run. Pass `AgentName`
when you want to select a named runner agent.

```go
store, err := subagent.NewFileStore("subagents/runs.json")
if err != nil {
	return err
}

svc, err := subagent.NewService(
	r,
	subagent.WithStore(store),
	subagent.WithObserver(subagent.ObserverFunc(
		func(ctx context.Context, run subagent.Run) {
			if run.Status.IsTerminal() {
				log.Printf("subagent %s finished: %s", run.ID, run.Status)
			}
		},
	)),
)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

run, err := svc.Spawn(ctx, subagent.SpawnRequest{
	OwnerUserID:     "user-123",
	ParentSessionID: "chat-456",
	AgentName:       "worker",
	Task:            "review the generated frontend and summarize issues",
	Timeout:         10 * time.Minute,
})
if err != nil {
	return err
}

final, err := svc.Wait(ctx, run.ID)
if err != nil {
	return err
}
log.Printf("result: %s", final.Result)
```

## Tool usage

Use `tool/subagent` when the parent agent should decide when to delegate work.
The tools use the current invocation session as the parent session.

```go
subagentTools := subagenttool.NewTools(
	svc,
	subagenttool.WithDefaultAgentName("worker"),
)

parent := llmagent.New(
	"parent",
	llmagent.WithTools(subagentTools.All()),
)
```

The tool names are:

- `subagents_spawn`
- `subagents_list`
- `subagents_get`
- `subagents_cancel`
- `subagents_wait`

Nested spawning is rejected by default. Enable it explicitly with
`subagenttool.WithNestedSpawns(true)` only when the application has its own
fan-out limits.

## Runtime state

Every child run receives these runtime-state keys:

- `subagent.RuntimeStateKeyRun`
- `subagent.RuntimeStateKeyRunID`
- `subagent.RuntimeStateKeyParentSessionID`

Application adapters can merge additional runtime state through
`SpawnRequest.RuntimeState`.
