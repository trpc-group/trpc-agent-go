# Error Message Plugin Example

This example shows how to use `plugin/errormessage` to customise the
assistant-visible content of error events before Runner persists them into the
session.

The example does **not** call any real model backend. A tiny custom agent
emits a single `stop_agent_error` event — the same shape `llmflow` produces
when a `StopError` propagates out of a tool or callback — and the demo runs
two Runners so the persisted session events can be compared side by side.

## What this example demonstrates

- Create a Runner with `errormessage.New(...)`
- Plug in a `Resolver` that decides the user-facing content per error type
- Compare the session events persisted with and without the plugin

## Prerequisites

- Go 1.21 or later

No environment variables are required.

## Usage

```bash
cd examples/plugin/errormessage
go run .
```

You will see two blocks in the output. The first block is persisted **without**
the plugin, so the default fallback message is stored:

```text
  Persisted error event:
    Response.Error.Type : stop_agent_error
    Response.Error.Msg  : max iterations reached
    Visible Content     : An error occurred during execution. Please contact the service provider.
```

The second block is persisted **with** the plugin, which uses the resolver
above to choose a friendlier message:

```text
  Persisted error event:
    Response.Error.Type : stop_agent_error
    Response.Error.Msg  : max iterations reached
    Visible Content     : 本次执行已按策略停止，请稍后再试。
```

In both cases `Response.Error` is left intact, so debugging tools and
downstream consumers still see the original reason.

## Core integration

The example wires the plugin at Runner construction time:

```go
rewriter := errormessage.New(
    errormessage.WithResolver(func(
        _ context.Context,
        _ *agent.Invocation,
        e *event.Event,
    ) (string, bool) {
        if e == nil || e.Response == nil || e.Response.Error == nil {
            return "", false
        }
        if e.Response.Error.Type == agent.ErrorTypeStopAgentError {
            return "本次执行已按策略停止，请稍后再试。", true
        }
        return "执行失败，请稍后重试。", true
    }),
)

runnerInstance := runner.NewRunner(
    "error-message-demo",
    agentInstance,
    runner.WithSessionService(svc),
    runner.WithPlugins(rewriter),
)
```

For a fixed, static message `errormessage.WithContent("...")` is equivalent to
`WithResolver` returning the same string for every error event.

## Files

- `main.go`: program entry, Runner setup, and side-by-side comparison.
- `agent.go`: a minimal agent that emits a single `stop_agent_error` event.
