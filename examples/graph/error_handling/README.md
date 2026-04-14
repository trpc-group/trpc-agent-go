# Graph Error Handling

This example shows the framework-level error-handling path for graph
workflows.

It demonstrates three cases:

- A recoverable local node error
- A fatal local node error
- A fatal child subgraph error that is propagated back to the parent graph

## Why this example matters

In a real agent workflow, "the run failed" is usually not enough.

Business code often needs two things at the same time:

- A structured error signal in the event stream
- A stable error collection that is still available after the run ends

This example uses `graph.ExecutionErrorCollector` to do both.

## What the code does

### 1. Recoverable local node error

The `lookup` node returns a coded error that implements
`Recoverable() bool`.

The collector's default policy marks `LOOKUP_SOFT_TIMEOUT` as recoverable, so
the graph:

- records one `recoverable` `graph.ExecutionError`
- keeps executing
- lets the `finalize` node read the collected error from state

### 2. Fatal local node error

The `write` node returns a fatal coded error.

The collector:

- records one `fatal` `graph.ExecutionError`
- emits fallback state immediately on the error path
- lets Runner copy that state to `runner.completion`

This is the important part: even though the graph stops early, the final
consumer can still read the collected business error from
`runner.completion.StateDelta`.

### 3. Fatal child subgraph error

The parent graph calls a child `GraphAgent` through `AddAgentNode(...)`.

The child graph fails before it can emit a normal `graph.execution` event.

The parent uses:

```go
graph.WithSubgraphOutputMapper(
    collector.SubgraphOutputMapper(),
)
```

That mapper reads the child fallback state from `SubgraphResult.RawStateDelta`
through the collector helper, which now reads
`SubgraphResult.EffectiveStateDelta()` for you, and merges the child's
collected `execution_errors` into the parent state.

The parent can then continue, make a business decision, and expose the same
error collection on its own final `runner.completion`.

## Run the example

```bash
go run ./examples/graph/error_handling
```

## What to look for in the output

For each scenario, the example prints:

- any terminal streamed `Response.Error`
- whether `runner.completion` was received
- the collected execution errors from `runner.completion.StateDelta`
- an optional business note written by downstream nodes

You should see:

- the recoverable case continue to completion
- the fatal local case still produce collected errors at completion
- the child fatal case surface the child error in the parent completion state

## How business code can extend this pattern

- Change recoverability with `graph.WithExecutionErrorPolicy(...)`
- Use the built-in default policy via `Recoverable() bool` or
  `graph.MarkRecoverable(err)`
- Store errors under a domain-specific key with
  `graph.WithExecutionErrorStateKey(...)`
- Merge child error state into a custom parent mapper with
  `collector.SubgraphStateUpdate(result)`
- Publish extra fatal-path business state with `graph.EmitCustomStateDelta(...)`

## Recommended production pattern

- Keep business error codes in a small domain package instead of scattering
  raw strings across nodes.
- Let business errors implement `ErrorCode()` or `Code()` so the framework can
  populate `Response.Error.Code` and `graph.ExecutionError.Error.Code`
  automatically.
- Use the default recoverable contract first. Only add
  `WithExecutionErrorPolicy(...)` when you need custom fallback routing or
  normalization.
- Persist the final collected records from `runner.completion.StateDelta`.

For a fuller design guide with complete code snippets, see
[`docs/mkdocs/en/error-handling.md`](../../../docs/mkdocs/en/error-handling.md)
and [`docs/mkdocs/en/runner.md`](../../../docs/mkdocs/en/runner.md).
