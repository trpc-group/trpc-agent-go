# No Persistence (Noop)

Use `session/noop` when the upstream application already owns the complete conversation history or when Session history and state should not be retained between requests. Noop does not retain sessions, events, or state across requests, so Session storage does not grow with the number of session IDs.

Noop does not set Runner's internal Session object to `nil`. Runner still creates a transient Session during each `Run`, allowing Graph, Chain, Tool, and other components to access current-run messages and state deltas. The Session Service does not retain that data after the run.

## Use with `RunWithMessages`

When the application owns the history, pass the complete history on every request:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessionnoop "trpc.group/trpc-go/trpc-agent-go/session/noop"
)

sessionService := sessionnoop.NewService()
r := runner.NewRunner(
    "my-agent",
    agent,
    runner.WithSessionService(sessionService),
)

// The application persists and updates history.
history := []model.Message{
    model.NewSystemMessage("You are a helpful assistant."),
    model.NewUserMessage("My name is Alice."),
    model.NewAssistantMessage("Hello, Alice."),
    model.NewUserMessage("What is my name?"),
}

eventChan, err := runner.RunWithMessages(
    ctx,
    r,
    "user123",
    "session-001",
    history,
)
```

Unlike a persistent Session, the next Noop-backed call does not automatically restore this history. The application must consume `eventChan`, store the Agent response, and pass the updated complete history to `RunWithMessages` again on the next request.

## Behavior Boundaries

| Capability | Noop Behavior |
| --- | --- |
| Messages and state deltas within one `Run` | Supported |
| Cross-request conversation history and Session state | Not retained |
| `GetSession` / `ListSessions` | Return no persisted data |
| Session summary and summary restoration | Not supported |
| AG-UI Session-backed history snapshots, await-user-reply routing, and Session restoration | Not supported |

Noop only controls the Session Service. It does not disable independently configured Memory, Artifact, Session Ingestor, Evolution, or Graph checkpoint services. Those services may still retain or restore data across requests according to their own configuration.

Even with Noop, the Runner API still requires `userID` and `sessionID` to identify and validate the transient Session for the current run.

## Use Cases

- The upstream application already persists the complete conversation history
- APIs where every request carries the complete Session context explicitly
- Cross-request Session state, summaries, history snapshots, and restoration are not needed
- Graph, Chain, and Tool components still need a transient Session during one run

If Runner only needs to restore prior turns while the process remains alive, use [Memory Storage](inmemory.md). To retain Session data across process restarts or share it between instances, use a persistent backend.

## Related Examples

- [Noop Session example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/simple)
- [RunWithMessages example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runwithmessages)
