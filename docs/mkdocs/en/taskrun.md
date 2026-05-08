# Agent Task Run Runtime

The `agent/taskrun` package defines a control-plane API for persistent
background task runs that execute agents through `runner.Run`. It is useful
when an application wants to return from the current user turn while delegated
work continues in another session.

The package separates the stable API from the single-process implementation:

- `agent/taskrun` defines `Run`, `Status`, `SpawnRequest`, `ListFilter`, and
  `Controller`.
- `agent/taskrun/inprocess` provides a goroutine-based implementation with
  `MemoryStore` and `FileStore`.
- `tool/taskrun` exposes optional control tools for agent-driven task runs.

`inprocess.Service` is intended for tests, local runtimes, and product
adapters that run in one process. Distributed deployments should provide their
own `taskrun.Controller` backed by external storage, queueing, leases, and a
cross-node cancellation mechanism. `SpawnRequest` is a Go API, not a wire
protocol: fields such as `RuntimeState map[string]any` are only safe inside
implementations that call `runner.Run` directly. Distributed controllers
should normalize those values at the product adapter boundary instead of
serializing arbitrary Go objects.

## Code-Side Usage

Create one normal `runner.Runner`, then wrap it with
`inprocess.Service`. The same runner decides which agent executes the child
run. Pass `AgentName` when you want to select a named runner agent.

```go
store, err := inprocess.NewFileStore("task-runs/runs.json")
if err != nil {
	return err
}

svc, err := inprocess.NewService(
	r,
	inprocess.WithStore(store),
	inprocess.WithObserver(taskrun.ObserverFunc(
		func(ctx context.Context, run taskrun.Run) {
			if run.Status.IsTerminal() {
				log.Printf("task run %s finished: %s", run.ID, run.Status)
			}
		},
	)),
)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

run, err := svc.Spawn(ctx, taskrun.SpawnRequest{
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

## Tool Usage

Use `tool/taskrun` when the parent agent should decide when to delegate work
to background task runs. The tools use the current invocation session as the
parent session.

```go
taskRunTools := taskruntool.NewTools(
	svc,
	taskruntool.WithDefaultAgentName("worker"),
)

parent := llmagent.New(
	"parent",
	llmagent.WithTools(taskRunTools.All()),
)
```

The tool names are:

- `start_task_run`
- `list_task_runs`
- `get_task_run`
- `cancel_task_run`
- `wait_task_run`

Nested task runs are rejected by default. Enable them explicitly with
`taskruntool.WithNestedSpawns(true)` only when the application has its own
fan-out limits.

## Runtime State

Every child run receives these runtime-state keys:

- `taskrun.RuntimeStateKeyRun`
- `taskrun.RuntimeStateKeyRunID`
- `taskrun.RuntimeStateKeyParentSessionID`

Application adapters can merge additional runtime state through
`SpawnRequest.RuntimeState`, or override the injected key names through
`SpawnRequest.RuntimeStateKeys` when they expose a product-specific runtime
surface.
