# Goal Extension

The Goal extension adds a session-scoped objective contract to an `LLMAgent`.
It is useful when a user wants the agent to keep working until a larger
objective is either complete or blocked.

This is an Agent extension, not a Runner mode:

- install it with `llmagent.WithExtensions(goal.New())`;
- the model gets `get_goal`, `create_goal`, and `update_goal`;
- goal state is stored in session state;
- streaming progress output can still be emitted, while premature final model
  responses are blocked inside the same model loop;
- the outer `Runner.Run` still ends with one `runner.completion`.

## Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/extension/goal"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

ag := llmagent.New(
    "planner",
    llmagent.WithModel(modelInstance),
    llmagent.WithExtensions(goal.New(
        goal.WithMaxRetries(3),
    )),
    // Optional hard limits for one LLMAgent invocation.
    llmagent.WithMaxLLMCalls(20),
    llmagent.WithMaxToolIterations(10),
)
```

Applications may provide their own command layer. For example, a CLI can parse
`/goal <objective>` and create the session goal before calling `Runner.Run`:

```go
key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
_, err := goal.Start(ctx, sessionService, key, objective)
```

The framework does not parse slash commands.

## Semantics

When a goal is active, a final model response is not enough. The model must
either continue working or call `update_goal` with:

- `complete`: the objective has actually been achieved;
- `blocked`: the same blocking condition has repeated across goal attempts, and
  the agent cannot make meaningful progress without user input or an external
  state change.

If the model repeatedly emits premature final responses, the extension retries
up to `WithMaxRetries`. After the retry budget is exhausted, the response passes
through unchanged so the run cannot loop forever.

`WithMaxRetries` only bounds the premature-final-response -> reminder ->
continuation cycle. Applications can also configure `WithMaxLLMCalls` and
`WithMaxToolIterations` on the same `LLMAgent` to impose hard limits on model
calls and tool-call iterations in that agent's invocation. When Goal is installed
on the Runner's primary `LLMAgent`, this usually bounds one `Runner.Run`.
Reaching either limit stops the run, but does not automatically mark the session
goal `complete` or `blocked`; a later invocation receives a new budget. These
limits are therefore invocation-level circuit breakers, not goal terminal-state
decisions.

Goal does not change streaming configuration. Streaming remains controlled by
the `LLMAgent` generation config or run options such as `agent.WithStream(...)`.
Callers may see intermediate assistant output before the agent continues; that
output does not mean the goal has completed. The run is still terminal only when
`runner.completion` is emitted.

Goal tools need serial semantics. Do not enable `llmagent.WithEnableParallelTools(true)`
on the same `LLMAgent` that owns the Goal extension. A model response should not
call `create_goal` and `update_goal` in the same parallel tool batch, because
parallel execution uses isolated invocation/session views for each tool call.
If an application needs parallel business tools, keep Goal on a serial owner
agent or use a separate agent for the parallel work.

## Boundaries

- The extension is installed on one `LLMAgent`; sub-agents do not inherit it.
- Multiple agents can share the same session goal by using the same state key,
  but the usual recommendation is to install it on the agent that owns the
  completion decision.
- `token_budget` is not part of this extension. Budgeting is a separate runtime
  policy.
- Concurrency control is still the caller's responsibility. If the same session
  is run concurrently, session state writes follow the behavior of the selected
  `session.Service`.

See `examples/goal` for a runnable example.
