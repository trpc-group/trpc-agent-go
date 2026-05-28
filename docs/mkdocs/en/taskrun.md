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

## agenttool.NewTool, transfer_to_agent, and taskrun

tRPC-Agent-Go has three common ways to delegate work to another agent. They
all let another agent participate, but they do not mean the same thing.

| Mechanism | Use it when | Waits for a result | Has a manageable run id |
| --- | --- | --- | --- |
| `agenttool.NewTool` / `tool/agent.Tool` | A parent agent needs to call a specialist agent like a normal synchronous tool | Yes | No |
| `transfer_to_agent` | The current invocation should hand control to a sub-agent that continues the turn | Continues in the same invocation | No |
| `tool/taskrun` | Work should run in a separate child session and remain queryable, waitable, or cancelable | Sync or async | Yes |

Use the distinction as three separate questions:

- "Do I need a specialist to return a value now?" Use `agenttool.NewTool`.
- "Should another agent take over this turn?" Use `transfer_to_agent`.
- "Should this become a tracked task that can be queried, waited for, or
  canceled?" Use `taskrun`.

### Synchronous specialist calls: agenttool.NewTool

`agenttool.NewTool` wraps an already constructed agent as a normal tool. When
the parent calls that tool, it waits for the child agent to finish. By default,
the tool result aggregates non-empty assistant messages from the child agent.
When configured with `agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly)`,
as shown below, the tool returns only the last complete assistant message. This
is a good fit for small synchronous subtasks such as reviewing a paragraph,
checking a design, or producing a short analysis.

```go
reviewer := llmagent.New(
	"reviewer",
	llmagent.WithModel(reviewModel),
	llmagent.WithInstruction(
		"Review the input carefully and return concise findings.",
	),
)

reviewTool := agenttool.NewTool(
	reviewer,
	agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
)

parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools([]tool.Tool{reviewTool}),
)
```

There is no background run and no run id. The model sees a normal tool named
`reviewer`; after the tool call finishes, the parent continues reasoning in
the current invocation.

### Handing off the current turn: transfer_to_agent

When the parent decides that another agent should continue the current turn,
configure a sub-agent and use transfer instead of a task run. When an
LLMAgent has sub-agents, the framework exposes `transfer_to_agent`.

```go
support := llmagent.New(
	"support",
	llmagent.WithModel(supportModel),
	llmagent.WithInstruction(
		"Handle product support questions and ask for missing details.",
	),
)

router := llmagent.New(
	"router",
	llmagent.WithModel(routerModel),
	llmagent.WithInstruction(
		"Route product support requests to the support agent.",
	),
	llmagent.WithSubAgents([]agent.Agent{support}),
)
```

The model can call:

```json
{
  "agent_name": "support",
  "message": "The user is asking how to configure retries. Continue from here."
}
```

This is not a background task. It means control for the current invocation
moves to `support`, and the emitted events still belong to that run.

### Manageable background work: taskrun

Use `taskrun` when the delegated work may take longer, when the parent should
continue without waiting, or when the application needs status, waiting, or
cancellation. Every `start_task_run` creates a child session and returns a run
id. The parent can later call `get_task_run`, `wait_task_run`, or
`cancel_task_run`.

```json
{
  "task": "Read the design notes and summarize the migration risks.",
  "agent_name": "worker",
  "mode": "async",
  "timeout_seconds": 600
}
```

If the parent must wait for the answer, use `sync`:

```json
{
  "task": "Compare the two API designs and return a recommendation.",
  "agent_name": "worker",
  "mode": "sync",
  "timeout_seconds": 600,
  "wait_timeout_seconds": 120
}
```

`sync` only means the tool waits for the child run to reach a terminal status.
It still creates a run id and follows the same taskrun lifecycle. If
`wait_timeout_seconds` expires, the child run is not canceled; the parent can
still use the returned run id to check it later.

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

If the parent agent needs the taskrun tools, and `inprocess.Service` also
needs the same runner, create the tools first without a controller and call
`SetController` after the service is created. This avoids an initialization
cycle where the parent needs the service, the service needs the runner, and
the runner needs the parent.

```go
taskRunTools := taskruntool.NewTools(
	nil,
	taskruntool.WithDefaultAgentName("worker"),
)

worker := llmagent.New(
	"worker",
	llmagent.WithModel(workerModel),
	llmagent.WithInstruction(
		"Work on delegated tasks and return a clear final result.",
	),
)

parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools(taskRunTools.All()),
	llmagent.WithInstruction(
		"Use taskrun tools only when the work can run in a separate task.",
	),
)

r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgent("worker", worker),
)

svc, err := inprocess.NewService(r)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

taskRunTools.SetController(svc)
```

If your application already has a `taskrun.Controller`, pass it directly:

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

## Guidance for the Parent Agent

After exposing `tool/taskrun`, describe when the parent agent should use it.
Do not only tell the model that it can start tasks; explain the boundary.

```go
parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools(taskRunTools.All()),
	llmagent.WithInstruction(`
You coordinate user requests.

Use start_task_run when a delegated task can continue in a separate child
session, especially when it may take a long time or the user does not need the
result immediately.

Use mode="async" when you can continue the current response after receiving the
run id. Use mode="sync" only when the delegated result is required before you
can answer.

After starting an async task, keep the returned run id. Use get_task_run or
wait_task_run when you need the latest status or final result. Use
cancel_task_run only when the user asks to stop the task or continuing it would
be wasteful.
`),
)
```

If the parent only needs a specialist to synchronously return analysis, wrap
that specialist with `agenttool.NewTool`. If the current invocation should be
routed to another agent, configure sub-agents and use `transfer_to_agent`. Use
`taskrun` only when you need a run id, status checks, waiting, or cancellation.

## Choosing Named Agents

The `agent_name` field in `start_task_run` maps to a named agent registered on
the runner. The simplest form is `runner.WithAgent`, which registers an
already constructed worker.

```go
r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgent("worker", worker),
)
```

If the worker must be built per request, for example with tenant-specific
prompts, models, or sandboxes, use `runner.WithAgentFactory`. taskrun passes
`agent_name` to the runner, and the runner resolves the target agent when the
child run starts.

```go
r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgentFactory(
		"worker",
		func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
			return llmagent.New(
				"worker",
				llmagent.WithModel(workerModel),
				llmagent.WithInstruction(
					"Handle delegated background work for this request.",
				),
			), nil
		},
	),
)
```

Prefer named agents such as `fast_worker`, `deep_reviewer`, or
`readonly_researcher` to represent execution policies. The model chooses a
business role, while the application owns the actual model, tools, and
permission boundary.

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
