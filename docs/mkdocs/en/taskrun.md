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

When `taskruntool.WithSessionService` is configured, `tool/taskrun` also
adds:

- `read_task_run_transcript`

`start_task_run` accepts a `mode` field:

- `async` is the default. The tool starts the child run and returns the run
  id immediately.
- `sync` starts the child run and waits until it reaches a terminal status
  before returning.

`timeout_seconds` limits the child run itself. `wait_timeout_seconds` only
limits how long the tool waits in `sync` mode; it does not cancel the child
run when the wait times out.

Nested task runs are rejected by default. Enable them explicitly with
`taskruntool.WithNestedSpawns(true)` only when the application has its own
fan-out limits.

## Progress and Transcript

`Run.Progress` is a small status snapshot collected from the child run event
stream. It records event count, tool call count, tool result count, token
counts, and the latest observed event time. The full task transcript is not
copied into `Run`; it remains in the child session identified by
`Run.ChildSessionID`.

The in-process controller keeps active progress available through polling
APIs. Observers still receive persisted lifecycle updates; the final progress
snapshot is persisted with the terminal run state.

This keeps the task control plane small:

- `list_task_runs`, `get_task_run`, and `wait_task_run` return lightweight
  status and progress.
- The child session stores the detailed transcript.
- Applications remain free to build their own UI, notifications, logs, or
  artifact storage on top of task run observers and session events.

To let an agent inspect the child transcript, pass the same session service
used by the runner:

```go
taskRunTools := taskruntool.NewTools(
	svc,
	taskruntool.WithDefaultAgentName("worker"),
	taskruntool.WithSessionService(sessionService),
)
```

The `read_task_run_transcript` tool reads recent events from the child
session. Its `limit` field is optional, defaults to 40 recent events, and is
capped at 200 events. Access is limited to runs owned by the current user and
created from the current parent session. When the run records an app name, for
example from `agent.WithAppName`, transcript reads use that app name to locate
the child session.

## Runtime State

Every child run receives these runtime-state keys:

- `taskrun.RuntimeStateKeyRun`
- `taskrun.RuntimeStateKeyRunID`
- `taskrun.RuntimeStateKeyParentSessionID`

Application adapters can merge additional runtime state through
`SpawnRequest.RuntimeState`, or override the injected key names through
`SpawnRequest.RuntimeStateKeys` when they expose a product-specific runtime
surface. Set `SpawnRequest.AppName` when the child run should use a specific
session app namespace. Local adapters that call `runner.Run` directly can
also pass per-run `agent.RunOption` values through `SpawnRequest.RunOptions`;
this is for in-process runner configuration and should not be serialized as a
cross-node contract. `SpawnRequest.RunContext` can add local context values to
the child runner context for the same in-process use case.
