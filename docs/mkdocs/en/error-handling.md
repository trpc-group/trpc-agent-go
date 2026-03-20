# Error Handling

## Overview

This document defines the standard error-handling model for graph workflows,
Runner completion, subgraph propagation, and A2A transport.

Agent applications usually need two things at the same time:

- A machine-readable error signal for branching, retries, and reporting
- A stable way to keep useful business error details after the run has ended

In practice, those details may come from:

- A local node error
- A child subgraph or sub-agent
- A remote A2A agent

tRPC-Agent-Go provides one standard path for all three.

## Design goals

The framework design follows four rules:

1. Keep `Response.Error` as the transport-level failure signal, and use
   `event.IsTerminalError()` to decide whether that failure is terminal.
2. Keep business-visible error collections in graph state.
3. Let recoverable errors continue execution without losing the record.
4. Let fatal errors still publish fallback business state before the run stops.

## Covered Scenarios

This design covers the requirements that often pushed business teams to keep
separate node-error helpers:

- collect local node errors, including recoverable ones
- keep business-visible error details after the run ends
- propagate child subgraph or sub-agent errors back to the parent
- let Runner expose fatal-path fallback state on `runner.completion`
- carry structured A2A task failures back into `Response.Error`

If an existing implementation stores node errors in graph state and reads them
back after completion, `graph.ExecutionErrorCollector` is the framework
equivalent of that pattern.

## Document Structure

The main sections are:

1. "Managing business error codes" explains the error-code model and ownership.
2. "Recommended graph usage" shows framework integration in graph workflows.
3. "Reading errors after the run" defines the Runner-side consumption pattern.
4. Read the subgraph and A2A sections only if your system crosses those
   boundaries.

## Responsibility Split

The framework owns transport, propagation, and collection mechanics.

Framework responsibilities:

- where transport failures live: `Response.Error`
- when a transport failure is terminal: `event.IsTerminalError()`
- where business-visible records live: graph state
- how fatal fallback state reaches `runner.completion`
- how child fallback state is separated from normal child completion
- how structured A2A task failures become `Response.Error` again

Business code decides:

- which error codes exist
- which codes are recoverable
- which fallback route should run
- whether multiple records should be deduplicated or aggregated
- how errors should be persisted, alerted, or reported

The framework does not define a global business error-code registry. It
standardizes how structured codes are carried and collected. The code namespace
itself remains application-specific.

## Managing business error codes

The framework supports error codes as a transport and normalization mechanism.
It does not define a centralized business registry.

Summary:

- the framework does not own your business error-code catalog
- `model.ResponseError.Code` is a `string`, so collected and transported codes
  are represented as strings
- existing integer-style codes are still supported and converted into decimal
  strings automatically
- for new business errors, prefer stable string code constants

### Code representation

`model.ResponseError.Code` is defined as `*string`.

This representation is intentional:

- event streams and A2A metadata are easier to keep stable with string values
- string codes support namespaced business identifiers such as
  `ORDER_INVENTORY_SOFT_TIMEOUT`
- cross-language or cross-service integrations do not need to guess numeric
  ranges or enum ownership

If a system already uses numeric codes, they remain supported. The framework
converts them into strings at the transport boundary.

### Supported error conventions

By default, `graph.NewExecutionError(...)` uses
`model.ResponseErrorFromError(err, model.ErrorTypeFlowError)`.

Go does not support overloaded methods. The table below describes alternative
conventions across different error types. A single concrete error type would
normally implement one code convention, not all of them.

| Optional method on your error type | Framework behavior |
| --- | --- |
| `ErrorType() string` | fills `ResponseError.Type` |
| `ErrorCode() string` | fills `ResponseError.Code` directly |
| `Code() string` | fills `ResponseError.Code` directly |
| `Code() int` | converts the value to a decimal string |
| `Code() int32` | converts the value to a decimal string |
| `Code() int64` | converts the value to a decimal string |

### Recommended default for new business code

The recommended pattern is:

1. Keep stable string error codes in a small domain package.
2. Return typed business errors from nodes, tools, or agents.
3. Let the collector record those codes automatically.
4. Let the default collector policy recover errors whose `Recoverable() bool`
   method returns `true`.
5. Use `WithExecutionErrorPolicy(...)` only for custom fallback routing or
   optional normalization.

Example business error package:

```go
package ordererrors

import (
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
)

const (
    CodeInventorySoftTimeout = "ORDER_INVENTORY_SOFT_TIMEOUT"
    CodeInventoryUnavailable = "ORDER_INVENTORY_UNAVAILABLE"
)

type Error struct {
    code        string
    message     string
    recoverable bool
}

func (e *Error) Error() string {
    return e.message
}

func (e *Error) ErrorCode() string {
    return e.code
}

func (e *Error) ErrorType() string {
    return model.ErrorTypeFlowError
}

func (e *Error) Recoverable() bool {
    return e.recoverable
}

func NewInventorySoftTimeout(itemID string) error {
    return &Error{
        code:        CodeInventorySoftTimeout,
        message:     fmt.Sprintf(
            "inventory lookup timed out for %s",
            itemID,
        ),
        recoverable: true,
    }
}

func NewInventoryUnavailable(itemID string) error {
    return &Error{
        code:        CodeInventoryUnavailable,
        message:     fmt.Sprintf(
            "inventory service is unavailable for %s",
            itemID,
        ),
        recoverable: false,
    }
}
```

If you already have a legacy numeric-code system, it still works:

```go
type legacyRPCError struct {
    code    int
    message string
}

func (e *legacyRPCError) Error() string {
    return e.message
}

func (e *legacyRPCError) Code() int {
    return e.code
}
```

That error will be stored as a string code such as `"40401"` inside
`ResponseError.Code`.

Because `ordererrors.Error` implements `Recoverable() bool`, the default
collector policy already treats `NewInventorySoftTimeout(...)` as recoverable.

Example collector policy that keeps the default judgment and adds a custom
fallback route:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = &graph.Command{
                Update: graph.State{
                    "inventory_status": "fallback",
                },
                GoTo: "fallback_lookup",
            }
            return policy
        }),
    )
}
```

If your internal errors are messy or wrapped by third-party libraries, use
`ExecutionErrorPolicy.ResponseError` to normalize them into one business-facing
shape before the record is stored.

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

`graph.NewExecutionErrorCollector()` now ships with a conservative default
policy:

- errors that implement `Recoverable() bool` and return `true` are recoverable
- errors wrapped with `graph.MarkRecoverable(err)` or created with
  `graph.NewRecoverableError(...)` are also recoverable

Example:

```go
package main

import (
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

type quotaSoftLimitError struct {
    message string
}

func (e quotaSoftLimitError) Error() string {
    return e.message
}

func (e quotaSoftLimitError) Recoverable() bool {
    return true
}

func lookupQuota() error {
    return quotaSoftLimitError{
        message: "quota service returned soft limit",
    }
}

func lookupCache() error {
    return graph.NewRecoverableError("cache lookup timed out")
}
```

If you want to extend that default rule, use
`graph.WithRecoverableExecutionErrors(...)`:

```go
package main

import (
    "errors"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

var errQuotaSoftLimit = errors.New("quota soft limit")

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithRecoverableExecutionErrors(func(err error) bool {
            return errors.Is(err, errQuotaSoftLimit)
        }),
    )
}
```

When `Recover` is `true`, the collector writes a `recoverable` record into
state and keeps the graph running.

### 4. Optionally provide a replacement result

If a recoverable error should continue with a custom state update or route, use
`ExecutionErrorPolicy.Replacement`.

Preferred replacement types:

- `graph.State`
- `*graph.Command`

If `Replacement` is `nil`, the collector keeps the original `graph.State` or
`*graph.Command` result and merges `execution_errors` into it automatically.

If you need a custom replacement and still want the default recoverable
judgment, start from `graph.DefaultExecutionErrorPolicy(...)`:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = &graph.Command{
                Update: graph.State{
                    "cache_status": "miss",
                },
                GoTo: "fallback_builder",
            }
            return policy
        }),
    )
}
```

### 5. Complete graph setup example

If you want one copy-pasteable reference for a normal graph integration, start
with this shape:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

const codeInventorySoftTimeout = "ORDER_INVENTORY_SOFT_TIMEOUT"

type inventoryError struct {
    code        string
    message     string
    recoverable bool
}

func (e *inventoryError) Error() string {
    return e.message
}

func (e *inventoryError) ErrorCode() string {
    return e.code
}

func (e *inventoryError) ErrorType() string {
    return model.ErrorTypeFlowError
}

func (e *inventoryError) Recoverable() bool {
    return e.recoverable
}

func buildAgent() (agent.Agent, error) {
    collector := graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = graph.State{
                "inventory_status": "fallback",
            }
            return policy
        }),
    )

    schema := graph.MessagesStateSchema()
    collector.AddField(schema)

    sg := graph.NewStateGraph(schema).
        WithNodeCallbacks(collector.NodeCallbacks())

    sg.AddNode("lookup_inventory", func(
        ctx context.Context,
        state graph.State,
    ) (any, error) {
        return nil, &inventoryError{
            code:        codeInventorySoftTimeout,
            message:     "inventory lookup timed out",
            recoverable: true,
        }
    })

    sg.AddNode("finalize", func(
        ctx context.Context,
        state graph.State,
    ) (any, error) {
        return graph.State{
            "done": true,
        }, nil
    })

    compiled, err := sg.
        AddEdge("lookup_inventory", "finalize").
        SetEntryPoint("lookup_inventory").
        SetFinishPoint("finalize").
        Compile()
    if err != nil {
        return nil, err
    }
    return graphagent.New("inventory-agent", compiled)
}
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

If a fatal error stops the graph before `graph.execution`, Runner now copies
the fallback business state onto the final `runner.completion` event and also
attaches the terminal `Response.Error` there.

That means application code can use one simple rule:

- keep consuming until `runner.completion`
- read the collector key from its `StateDelta`
- use `event.IsTerminalError()` to find terminal failures, then read
  `Response.Error`

Complete Runner-side pattern:

```go
package main

import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

type RunSummary struct {
    TransportError  *model.ResponseError
    ExecutionErrors []graph.ExecutionError
}

func ConsumeUntilCompletion(
    ctx context.Context,
    events <-chan *event.Event,
) (*RunSummary, error) {
    summary := &RunSummary{}

    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case evt, ok := <-events:
            if !ok {
                return summary, nil
            }
            if evt.IsTerminalError() &&
                evt.Response != nil {
                summary.TransportError = evt.Response.Error
            }
            if !evt.IsRunnerCompletion() {
                continue
            }

            executionErrors, err := graph.ExecutionErrorsFromStateDelta(
                evt.StateDelta,
                graph.StateKeyExecutionErrors,
            )
            if err != nil {
                return nil, err
            }
            summary.ExecutionErrors = executionErrors
            return summary, nil
        }
    }
}

func PrintSummary(summary *RunSummary) {
    if summary.TransportError != nil {
        fmt.Printf(
            "transport error: type=%s code=%s message=%s\n",
            summary.TransportError.Type,
            ptrValue(summary.TransportError.Code),
            summary.TransportError.Message,
        )
    }
    for _, record := range summary.ExecutionErrors {
        if record.Error == nil {
            continue
        }
        fmt.Printf(
            "execution error: severity=%s node=%s code=%s message=%s\n",
            record.Severity,
            record.NodeName,
            ptrValue(record.Error.Code),
            record.Error.Message,
        )
    }
}

func ptrValue(value *string) string {
    if value == nil {
        return ""
    }
    return *value
}
```

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

For custom mappers, the child result now keeps those two cases separate:

- `SubgraphResult.FinalState` and `SubgraphResult.RawStateDelta` are only for
  the normal terminal `graph.execution` snapshot
- `SubgraphResult.FallbackState` and `SubgraphResult.FallbackStateDelta` are
  only for fatal child fallback state

If you intentionally want one code path for both, use:

- `SubgraphResult.EffectiveState()`
- `SubgraphResult.EffectiveStateDelta()`

`ExecutionErrorCollector.SubgraphOutputMapper()` already does this for you.

If you need custom parent-side state in addition to child error propagation,
compose your own mapper around `collector.SubgraphStateUpdate(result)`:

```go
package main

import "trpc.group/trpc-go/trpc-agent-go/graph"

func parentOutputMapper(
    collector *graph.ExecutionErrorCollector,
) graph.SubgraphOutputMapper {
    return func(
        parent graph.State,
        result graph.SubgraphResult,
    ) graph.State {
        update := collector.SubgraphStateUpdate(result)
        if update == nil {
            update = graph.State{}
        }
        update["child_status"] = "degraded"
        return update
    }
}
```

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
- only terminal errors become failed tasks; intermediate graph events such as
  `graph.node.error` continue to flow as graph observability events

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

Complete server and client setup:

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
    a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

func buildA2AServer(myAgent agent.Agent) error {
    _, err := a2aserver.New(
        a2aserver.WithHost("127.0.0.1:18888"),
        a2aserver.WithAgent(myAgent, true),
        a2aserver.WithStructuredTaskErrors(true),
    )
    return err
}

func buildA2AClient() (agent.Agent, error) {
    return a2aagent.New(
        a2aagent.WithAgentCardURL("http://127.0.0.1:18888"),
        a2aagent.WithEnableStreaming(true),
    )
}
```

If you integrate a third-party A2A provider with a different metadata
convention, business code should extend the framework at the converter layer:

- keep the framework error model as `Response.Error`
- implement `a2aagent.A2AEventConverter`
- register it with `a2aagent.WithCustomEventConverter(...)`

That is the correct place for provider-specific adaptation. The business code
should not re-invent a second error transport format after the conversion step.

## Recommended ownership model

The cleanest production split is:

- framework: collect, propagate, serialize, and expose structured errors
- business error package: define code constants and typed errors
- graph policy: decide recoverable versus fatal behavior
- runner consumer: persist `ExecutionErrors` from `runner.completion`
- transport consumer: use `event.IsTerminalError()` together with
  `Response.Error` for terminal failure handling

That split is broad enough to replace an older business-side node-error helper
without taking ownership of your domain-specific error taxonomy.

## Example code

See these runnable examples:

- `examples/graph/error_handling`
- `examples/a2aagent/error_handling`

The graph example shows recoverable and fatal node errors with final state
reading.

The A2A example shows server-side structured task errors and client-side
reconstruction into `Response.Error`.
