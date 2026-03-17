# Error Handling

## Why this page exists

Agent applications usually need two different things at the same time:

- A machine-readable error signal for branching, retries, and reporting
- A stable way to keep useful business error details after the run has ended

In a graph workflow, those details may come from:

- A local node error
- A child subgraph or sub-agent
- A remote A2A agent

tRPC-Agent-Go now provides a standard path for all three.

## Design goals

The framework design follows four rules:

1. Keep `Response.Error` as the transport-level failure signal.
2. Keep business-visible error collections in graph state.
3. Let recoverable errors continue execution without losing the record.
4. Let fatal errors still publish fallback business state before the run stops.

## Core building blocks

### `graph.ExecutionError`

`graph.ExecutionError` is the normalized business record stored in state.

It contains:

- `Severity`: `recoverable` or `fatal`
- `NodeID` / `NodeName` / `NodeType`
- `StepNumber`
- `Timestamp`
- `Error`: a structured `*model.ResponseError`

### `graph.ExecutionErrorCollector`

`graph.ExecutionErrorCollector` is the recommended framework helper.

It gives you:

- A ready-to-use state field and reducer
- A node callback that records recoverable and fatal errors
- A subgraph output mapper for propagating child errors back to the parent

### `graph.EmitCustomStateDelta`

Fatal errors have a special problem: the graph may stop before it emits the
normal final `graph.execution` snapshot.

`graph.EmitCustomStateDelta(...)` solves that problem. It emits a custom event
with business state delta immediately, so downstream consumers can still see the
fallback state on the error path.

The execution error collector uses this helper automatically for fatal errors.

## Recommended graph usage

### 1. Add a state field

```go
schema := graph.MessagesStateSchema()

collector := graph.NewExecutionErrorCollector()
collector.AddField(schema)
```

This adds the default key `graph.StateKeyExecutionErrors`.

If you want a custom key:

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorStateKey("node_errors"),
)
collector.AddField(schema)
```

### 2. Register the collector callbacks

```go
sg := graph.NewStateGraph(schema).
    WithNodeCallbacks(collector.NodeCallbacks())
```

This is the simplest framework-level setup. Any node error that reaches
`AfterNode` will now be recorded in the collector field.

### 3. Decide which errors are recoverable

By default, every collected error is still fatal.

If you want some errors to continue execution, provide a policy:

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorPolicy(func(
        ctx context.Context,
        cb *graph.NodeCallbackContext,
        state graph.State,
        err error,
    ) graph.ExecutionErrorPolicy {
        if errors.Is(err, errQuotaSoftLimit) {
            return graph.ExecutionErrorPolicy{
                Recover: true,
            }
        }
        return graph.ExecutionErrorPolicy{}
    }),
)
```

When `Recover` is `true`, the collector writes a `recoverable` record into
state and returns a replacement node result so the graph can continue.

### 4. Optionally provide a replacement result

If a recoverable error should continue with a custom state update or route, use
`ExecutionErrorPolicy.Replacement`.

Preferred replacement types:

- `graph.State`
- `*graph.Command`

The collector merges the `execution_errors` update into those replacement
results automatically.

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorPolicy(func(
        ctx context.Context,
        cb *graph.NodeCallbackContext,
        state graph.State,
        err error,
    ) graph.ExecutionErrorPolicy {
        if !errors.Is(err, errRemoteCacheMiss) {
            return graph.ExecutionErrorPolicy{}
        }
        return graph.ExecutionErrorPolicy{
            Recover: true,
            Replacement: &graph.Command{
                Update: graph.State{
                    "cache_status": "miss",
                },
                GoTo: "fallback_builder",
            },
        }
    }),
)
```

## Reading errors after the run

### Graph-only consumers

If the run reaches its normal end, read the collector key from the final
`graph.execution` event.

```go
errors, err := graph.ExecutionErrorsFromStateDelta(
    evt.StateDelta,
    graph.StateKeyExecutionErrors,
)
```

### Runner consumers

If a fatal error stops the graph before `graph.execution`, Runner now copies the
fallback business state onto the final `runner.completion` event.

That means application code can use one simple rule:

- keep consuming until `runner.completion`
- read the collector key from its `StateDelta`
- separately inspect the earlier fatal event for `Response.Error`

## Subgraphs and sub-agents

There are two different needs here.

### Live observation during child execution

Use `graph.WithAgentNodeEventCallback(...)` or graph-level node callbacks with
`RegisterAgentEvent(...)` when you want streaming observation of child events.

This is for:

- live SSE dashboards
- logging
- metrics

It is observational. It is not the recommended place to persist final state.

### Final child-to-parent propagation

Use the collector's subgraph mapper:

```go
collector := graph.NewExecutionErrorCollector()

sg.AddAgentNode(
    "child_agent",
    "planner",
    graph.WithSubgraphOutputMapper(
        collector.SubgraphOutputMapper(),
    ),
)
```

This works for both:

- normal child completion (`graph.execution`)
- fatal child fallback state emitted before the child stops

The parent side now receives that child fallback state through
`SubgraphResult.RawStateDelta`, so the same mapper works for both cases.

## A2A structured errors

### Server side

If your A2A server should expose agent business errors as structured task
failures, enable:

```go
server, err := a2aserver.New(
    a2aserver.WithHost("http://localhost:8080"),
    a2aserver.WithAgent(myAgent, true),
    a2aserver.WithStructuredTaskErrors(true),
)
```

With this option enabled:

- unary A2A responses return a failed `Task`
- streaming A2A responses emit a failed `TaskStatusUpdateEvent`
- structured error fields are preserved in task metadata

### Client side

`A2AAgent` recognizes those structured task failures automatically.

For failed, rejected, or canceled remote tasks, it now emits a normal
`event.Event` with:

- `Response.Object = "error"`
- `Response.Error.Type`
- `Response.Error.Message`
- `Response.Error.Code` when available

In streaming mode, `A2AAgent` also stops emitting the synthetic final assistant
message after a terminal task error. This avoids the ambiguous pattern of
"error first, then normal final message".

## What business code still owns

The framework standardizes transport and collection mechanics. Business code
still owns policy.

### Business-owned pieces

- Which errors are recoverable
- Which fallback route should run after a recoverable error
- Which state key name is appropriate for your domain
- How to group, deduplicate, or post-process error records
- How to interpret third-party A2A task states like `input-required`

### Recommended extension points

- Use `WithExecutionErrorPolicy(...)` for recovery policy.
- Use `collector.SubgraphStateUpdate(result)` when composing your own parent
  output mapper.
- Use `graph.EmitCustomStateDelta(...)` if you need to publish extra fatal-path
  business state beyond the standard execution error record.
- Use a custom `a2aagent.A2AEventConverter` if a third-party A2A provider uses
  a different metadata convention.

## Example code

See these runnable examples:

- `examples/graph/error_handling`
- `examples/a2aagent/error_handling`

The graph example shows recoverable and fatal node errors with final state
reading.

The A2A example shows server-side structured task errors and client-side
reconstruction into `Response.Error`.
