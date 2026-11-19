# Cancellation and context propagation

tRPC-Agent-Go uses Go's `context.Context` to signal that work should stop.
Cancellation is cooperative: the framework, model adapters, tools, and application
code must observe the context passed to them. Calling `cancel()` does not kill a
goroutine, undo an external write, or prove that a remote model stopped computing.

This page describes cancellation controls, dispatch checks, and callback context
replacement. A tool or model call is rejected when its effective context is
already cancelled at a framework dispatch boundary. This changes the earlier
no-retry tool path, which could call a tool with an already-cancelled context.

## Requesting cancellation

Pass a cancellable context to `Runner.Run`, and call its cancel function when the
user requests a stop. Keep consuming events until the channel closes; merely
leaving the reader loop does not request cancellation and may block producers.
In this example, `r` is an existing `runner.Runner` and the stop request happens
immediately after starting the run:

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

events, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
if err != nil {
    return err
}

cancel()
for range events {
    // Drain events until the public output channel closes.
}
```

For request-ID control, pass `agent.WithRequestID(requestID)` to `Run` and use
`runner.ManagedRunner.Cancel(requestID)`. Its boolean result indicates whether a
registered cancel function was found and invoked; it does not acknowledge tool
completion or durable storage. An expired deadline also cancels its context,
with `context.DeadlineExceeded` instead of `context.Canceled`.

See [Runner](runner.md) for request-ID control and
[AG-UI cancellation](agui/cancel.md) for the server route.

## Which context controls subsequent work?

The effective context is the context currently passed to an operation. A
non-nil `BeforeModelResult.Context` or `BeforeToolResult.Context` replaces the
context for subsequent operations in that callback/execution path. A nil Context
leaves it unchanged. Other callback results with a Context field follow their
documented scope; replacing one does not rewrite contexts already held by callers,
sibling operations, or the outer Runner.

Dispatch uses the following context:

| Boundary | Cancellation check |
| --- | --- |
| Before admitting work or running callbacks | Check the incoming context. |
| After waiting for a concurrency slot | Check the current context; release acquired capacity if dispatch stops. |
| After callbacks and permission checks, when proceeding to execution | Check the current effective context, including an explicit callback replacement. |
| Before a tool/model call or a new attempt | Reject work if its effective context is already cancelled. |

Do not save an earlier `dispatchCtx` solely to give it an additional cancellation
veto after a callback has replaced the effective context. This preserves callback
replacement semantics. It does not make cancellation checks atomic with external
I/O, and it does not promise that callbacks run when admission has already failed.
This rule applies to model and tool dispatch, including Graph paths.

Under this contract, when a callback cancels its input and returns no replacement,
the next call is rejected. A child derived from that input is also cancelled. An
explicitly detached replacement may allow the affected downstream call to proceed.
That is an application decision, not restoration of the original context.

## Effects of replacing a context

| Callback returns | Inherits input cancellation/deadline? | Retains input values? |
| --- | --- | --- |
| `ctx` or no replacement | Yes | Yes |
| `context.WithValue(ctx, key, value)` | Yes | Yes, with the selected key added or overridden |
| Child from `context.WithCancel(ctx)` | Yes; also has its own cancel function | Yes |
| Child from `context.WithTimeout(ctx, duration)` | Yes; cannot extend an earlier parent deadline | Yes |
| `context.Background()` or `context.TODO()` | No | No |
| `context.WithoutCancel(ctx)` | No; the parent deadline is also removed | Yes |

These are Go context semantics; see the [context package documentation](https://pkg.go.dev/context).

Returning Background/TODO can discard request-scoped tracing, invocation, tool-call,
and other framework values carried by the input. Some paths restore specific
values, but there is no general guarantee that all discarded metadata is restored.
WithoutCancel preserves value lookup, but removes cancellation and the deadline;
its Done channel is nil until a new cancellable child is created.

An explicitly detached context affects only work that receives it. The outer
Runner can still observe its own cancellation and close output. Do not infer that
it will keep waiting, collect the detached work's result, or persist that result.
The application owns the detached work's timeout, cleanup, result handling, and
any background goroutines. Retained context values do not by themselves establish
a valid background execution lifecycle.

## Recommended callback patterns

### Preserve cancellation when adding request information

Derive from the callback's input. Use an application-specific key type and keep
values request-scoped. The following registrations assume the `context`, `model`,
and `tool` packages are imported:

```go
type requestTagKey struct{}

modelCallbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        return &model.BeforeModelResult{
            Context: context.WithValue(ctx, requestTagKey{}, "checkout"),
        }, nil
    },
)

toolCallbacks := tool.NewCallbacks().RegisterBeforeTool(
    func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
        return &tool.BeforeToolResult{
            Context: context.WithValue(ctx, requestTagKey{}, "checkout"),
        }, nil
    },
)
```

Pass these registrations through `llmagent.WithModelCallbacks(modelCallbacks)` and
`llmagent.WithToolCallbacks(toolCallbacks)`. If no context changes are needed,
return `nil, nil` to keep the incoming context.

### Bound callback-local work without shortening downstream lifetime

If only a callback's permission lookup or other I/O needs a shorter timeout, use
a child context for that work and keep the incoming context for the actual call.
Here `check` is an application function of type `func(context.Context) error`:

```go
toolCallbacks.RegisterBeforeTool(
    func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
        checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
        defer cancel()

        if err := check(checkCtx); err != nil {
            return nil, err
        }
        return nil, nil
    },
)
```

Do **not** return `checkCtx` as the result's Context while also deferring its
cancel function in this callback: the defer runs when the callback returns, so
the downstream tool/model would receive an already-cancelled context. A timeout
that must span later execution needs an owner whose lifetime includes that
execution. Prefer setting it around the run or inside the operation that owns
the work. Do not rely on a paired After callback running on every error or
short-circuit path.

### Keep cancellation for ordinary work; detach only bounded cleanup

A lock unlock or another local release usually needs no context. Cleanup that
performs I/O after cancellation may need a separate bounded context. Keep that
context local to cleanup instead of returning it to the ordinary execution chain.
Here `cleanup` is an application function of type `func(context.Context) error`:

```go
toolCallbacks.RegisterAfterTool(
    func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
        cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
        defer cancel()

        if err := cleanup(cleanupCtx); err != nil {
            return nil, err
        }
        return nil, nil
    },
)
```

AfterTool is not a general `finally` paired with BeforeTool. Cancellation handling
preserves the distinction between rejecting a call and completing a call:

| Situation | AfterTool behavior |
| --- | --- |
| Cancelled before admission, while queued, or after before callbacks/permission checks but before the first tool attempt | Return the cancellation error without entering after-tool callbacks. |
| Before callback returns an error/custom result, or permission checking denies/fails | Preserve the existing early return; no paired AfterTool guarantee. |
| A tool attempt started and then returned an error, including cancellation, or cancellation stopped later retries/stream consumption | Continue the existing after-tool callback chain with the result/error. |

Existing plugin overrides and callback errors can still short-circuit that chain.
The error value alone cannot distinguish cancellation before invocation from a
tool returning the same error after starting. AfterTool does not imply successful
execution; inspect its result and error before performing success-dependent work.

The example above applies only when AfterTool is reached. Release callback-local
resources with a `defer` in that callback. Resources needed throughout a tool call
should normally be acquired and released inside the tool or a wrapper that owns
the call, using `defer` there. Do not acquire a resource in BeforeTool and assume
AfterTool will always release it. If a resource must span callbacks, its owner
must cover every early return as well. The example propagates a cleanup error
through the callback API; choose the application's error-reporting policy
explicitly.

## Letting an entire run outlive parent cancellation

When the entire run should continue after an HTTP request disconnects, use the
existing Runner-level option rather than replacing a context inside one callback:

```go
events, err := r.Run(
    ctx, "user", "session", model.NewUserMessage("prepare a report"),
    agent.WithRequestID("report-123"),
    agent.WithDetachedCancel(true),
    agent.WithMaxRunDuration(30*time.Second),
)
```

Runner detaches parent cancellation but reapplies the earlier of the original
parent deadline and MaxRunDuration. Explicit ManagedRunner.Cancel still cancels
the registered execution context. Keep an owner consuming the run's events.
These controls reach downstream work that retains their context lineage; a later
callback can explicitly detach that lineage again under the selected contract.

## What cancellation does and does not establish

Distinguish these events: cancellation requested, public output closed, work
actually finished, and results acknowledged by storage. None of the first three
alone proves all the later events. The observation, persistence, and safe Resume
features discussed in #2757 are separate draft changes, not implied guarantees
of context cancellation.

In a sequential tool batch, rejecting a later call after cancellation does not
discard results already produced by earlier tools. If the batch deadline has
expired, those results use the existing bounded persistence fallback, and the
caller still receives `context.DeadlineExceeded`. Persistence can still fail;
explicit cancellation does not enable this deadline-only fallback.

Tools should check the effective context before side effects, pass it to HTTP,
database, and other I/O APIs, and observe Done in long waits. Already-running
non-cooperative code may continue. External writes may require idempotency keys
or an outcome query before retrying. A cancelled model stream does not prove
remote inference or billing stopped. Replacing a context also does not authorize
replaying a tool whose previous outcome is unknown.

See [Callbacks](callbacks.md), [Runner](runner.md), and
[Error handling](error-handling.md) for the surrounding APIs.

## Optional live validation

Normal tests do not call a paid provider. Set `OPENAI_BASE_URL`, `OPENAI_API_KEY`,
and `MODEL_NAME` for an OpenAI-compatible endpoint, then explicitly opt in:

```sh
TRPC_CANCEL_LIVE=1 go test ./runner -run '^TestCancellationLive_' -v -count=1 -timeout=5m
```

The probes use only a local counter as a tool side effect. They check that a
cancelled effective context prevents the tool and AfterTool from running, and
that cancelling a streamed response closes output. They do not prove remote
termination, rollback, or billing behavior. Keep credentials out of test logs.

## Durable tool execution evidence

`runner.WithToolExecutionTracking(true)` opts LLMAgent tools into synchronous
admission/outcome recording, independently of batch or per-call result events.
The default is disabled. The SessionService interface is unchanged. Framework
state keys under `__tool_execution_v1/` contain versioned round snapshots linked
to the exact source event, request, invocation and call IDs. They include the
original response, arguments, rendered results and admitted raw attempt count.
Reused call IDs in different rounds do not overwrite each other.

The existing contentless state-update protocol persists these snapshots even
when a backend excludes such events from its conversation event list. Therefore
records do not become model messages, trigger summaries or alter the legacy
last-event Resume boundary. Keep this namespace when backing up session state;
its retention and sensitivity follow the session, including tool arguments and
results. This is a recovery snapshot, not an append-only audit log.

Before callbacks or tool dispatch, admission must be acknowledged by storage.
Each callable retry/stream admission is also recorded. A failed write prevents
that dispatch. A persisted intent with no complete result means the operation
**may have executed**. Callback short circuits count as admitted work because
callbacks can have side effects. Rendering failure, cancellation errors and
long-running tools without a final result remain unknown. Successful results
can be recorded after output closure with an independent one-second context.
Stores must honor context deadlines; the framework cannot interrupt a backend
that ignores them. Absence of evidence does not prove absence of execution.

This option covers LLMAgent's standard tool processor, not custom agent tasks
or Graph checkpoint recovery. Tracking adds synchronous storage operations to
tool dispatch; it does not provide external exactly-once execution.

## Cancellation finalization

When execution tracking is enabled, normal event persistence, explicit-cancel
fallback and deadline fallback share a per-run coordinator. Completion notices
are checked against the actual append result. A one-second independent context
allows a completed tool response to be saved after cancellation. Repeated
submissions of the same event ID share the first append acknowledgement; an
ambiguous failure is retained and not automatically retried. This is local
submission deduplication, not backend exactly-once persistence.

Session restoration and framework event appends are serialized within this
Runner. Starting another run advances its session generation. A stale run's
late result retains its original IDs and result text; application StateDelta
is retained in `trpc_agent.stale_state_delta.v1` audit metadata instead of
applying over the newer state. A unique `__tool_stale_state_v1/<event-id>`
state key also retains that audit delta when a backend omits contentless events.
Unique execution-record keys still update the
old run's evidence. Unknown routed sessions are first claimed on append; a
session already owned by another generation is treated conservatively as stale.
Generation metadata lasts for this Runner's lifetime.

This cannot fence writes made directly by a user tool or another Runner/process.
For those, use backend conditional updates and external idempotency. It also
does not restart the old model loop or send late events to closed output.

## Observe and wait for execution settlement

With tracking enabled, type-assert the returned Runner to the optional
`runner.ExecutionObserver`. `ExecutionStatus(requestID)` reports cancellation
requested, output closed, upstream execution settled, active logical tools and
any failed/uncertain persistence acknowledgement separately. `WaitExecution`
waits using the caller's own context; timing out only stops the wait. A successful
wait can still return a status with `PersistenceError`.

`Cancel` remains a fast request, and `ManagedRunner` is unchanged. A tool that
ignores context can keep execution unsettled after public output closes. The
observer continues draining the agent's event channel until it closes. Custom
agents must keep that channel open until their workers finish; the framework
cannot observe hidden goroutines or remote operations. `Close` requests
cancellation and releases owned services; it does not wait for arbitrary tools.
Wait before Close when acknowledged finalization is needed.

Snapshots last up to ten minutes after settlement, with a maximum of 128 tracked
runs per Runner. Capacity evicts the oldest settled snapshot; if all slots are
unsettled, new tracked runs fail explicitly. Request IDs cannot be reused while
a snapshot is retained. Unknown, disabled or expired observation returns false;
waiting returns `ErrExecutionNotFound`. Observation is in memory; durable tool
records are the source for restart recovery.

## Next-turn history after interrupted tools

For rounds carrying version-1 evidence, ordinary history projection fills only
missing tool results after branch, history-window and summary-cutoff selection.
Real completed sibling results are retained even when the batch was never
emitted. Not-dispatched and admitted/unknown calls receive explicit `[framework]`
explanations, including the possibility of side effects or still-running work.
Existing actual results take precedence. Projection does not execute tools or
write synthetic responses back into session history/state.

Source event IDs associate rounds, so reused call IDs do not borrow another
round's results. Retained evidence does not resurrect events excluded by the
caller-selected history window. Old sessions without records keep their prior
projection behavior. Malformed/unsupported evidence fails explicitly.

Opaque native ProviderData is not rewritten. When a selected native tool round
is missing actual persisted responses, projection emits a
`tool_history_projection` error describing incomplete native history and stops
before the model call. Complete native rounds keep their existing adapter
validation. Provider-specific native recovery is outside this series.

## Safe tool Resume and migration

`agent.WithResume(true)` uses recorded facts when available. Completed/failed
calls are reused through history; only `not_started` calls execute. Dispatched,
in-flight and unknown outcomes produce a `tool_resume` error before any tool or
model call. An ordinary new prompt does not replay tools. Recorded native
pending tools require provider-specific recovery and are rejected before work.

Recorded Resume requires `runner.WithToolExecutionTracking(true)`. The Runner
claims a round and rereads its state before deciding, preventing concurrent
Resume requests on that Runner from dispatching the same pending call from
stale snapshots. The original round identity stays stable; each new admission
records its current request/invocation owner. These are Runner-local claims,
not distributed leases: do not concurrently recover one session from multiple
Runner instances without storage-level coordination.

For old sessions, `WithResume(true)` retains its legacy last-event behavior.
Add `agent.WithStrictToolResume(true)` to reject pending legacy calls that lack
evidence. This flag has no effect without Resume; newly recorded rounds always
use conservative recovery. A caller must reconcile external effects before
explicitly starting a new operation after an unknown outcome. No option in this
series silently retries unknown writes.

**Storage requirement:** crash-safe admission requires AppendEvent to apply
state synchronously and acknowledge storage errors. Disable asynchronous
persistence (for example, SQLite `WithEnableAsyncPersist(false)`). A queue-accept
acknowledgement is insufficient: after a crash it could leave an older
`not_started` snapshot. Generic SessionService implementations cannot be
introspected to enforce this requirement. In-memory storage only supports
same-process recovery, and even synchronous storage does not provide external
exactly-once effects or undo writes already made by tools.

The E2E SQLite test closes and reopens the database and creates a fresh Runner:
a completed A is not replayed, only pending B executes, and an intent without
an outcome blocks the entire recovery round. Live Hunyuan tests additionally
validate the model request after safe continuation.
