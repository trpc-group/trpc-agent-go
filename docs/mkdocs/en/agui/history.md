# Messages Snapshot Route

## Core Concepts

The messages snapshot route restores historical conversations after page initialization, refresh, or reconnect. It does not start a new agent run. Instead, it reads persisted AG-UI events from session storage and restores them as a `MESSAGES_SNAPSHOT` event.

The default route is `/history`, and it can be customized with `agui.WithMessagesSnapshotPath`. To configure a shared route prefix, see [Route Prefix](index.md#route-prefix). On a successful request, the server returns the event stream `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED`. For the protocol fields of `MESSAGES_SNAPSHOT`, see [AG-UI MessagesSnapshot](https://docs.ag-ui.com/concepts/events#messagessnapshot).

Messages snapshots use the same session lookup rules as real-time conversations. The framework locates sessions with `AppName`, `UserID`, and `threadId`. This route supports concurrent access with the real-time conversation route, so a page can read a snapshot for the same session while a real-time conversation is running.

## Enable Messages Snapshots

To enable messages snapshots, enable the snapshot route and configure a `session.Service` that can read historical events. You also need to configure a default `AppName`, which is used together with `UserID` and `threadId` to locate sessions.

The minimum configuration includes:

- `agui.WithMessagesSnapshotEnabled(true)` enables the messages snapshot route.
- `agui.WithAppName(name)` sets the default `AppName`.
- `agui.WithSessionService(service)` injects session storage.

Example:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()
server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
)
```

To change the default route, configure `agui.WithMessagesSnapshotPath(path)`. To resolve the user or application identifier from each request, configure [`aguirunner.WithUserIDResolver(resolver)`](chat.md#custom-useridresolver) or [`agui.WithAppNameResolver(resolver)`](chat.md#custom-appnameresolver).

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

userIDResolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if !ok {
        return "anonymous", nil
    }
    userID, ok := forwardedProps["userId"].(string)
    if !ok || userID == "" {
        return "anonymous", nil
    }
    return userID, nil
}

server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
    ),
)
```

When requesting a messages snapshot, pass the same `threadId` used by the real-time conversation, plus the fields required to resolve the user or application identifier:

```bash
curl -N -X POST http://localhost:8080/history \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "thread-id",
    "runId": "snapshot-run-id",
    "forwardedProps": {
      "userId": "alice"
    }
  }'
```

For the complete example, see [examples/agui/messagessnapshot](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/messagessnapshot).

## Session Storage and Event Aggregation

`SessionService` is the data source for messages snapshots. AG-UI events produced by the real-time conversation route are written into session storage. The messages snapshot route then reads persisted AG-UI events from the same `SessionService` and restores historical messages.

In multi-instance deployments, different instances must share the same `SessionService`; otherwise, the messages snapshot route cannot read historical events written by other instances.

Streaming responses usually produce multiple incremental text events or reasoning events. To reduce pressure on session storage, the framework aggregates consecutive `TEXT_MESSAGE_CONTENT` and `REASONING_MESSAGE_CONTENT` events with the same `messageId` before writing them to session storage by default.

Aggregated results are flushed once per second by default. When a run finishes normally, is canceled, or fails, the framework also performs post-run finalization. This fills in protocol stream closing events that are still open and tries to write the aggregation cache to session storage.

Related configuration:

- `aggregator.WithEnabled(true)` controls whether event aggregation is enabled. It is enabled by default.
- `agui.WithFlushInterval(time.Second)` controls the periodic flush interval for aggregation results. The default is `1s`. Setting it to `0` disables periodic flushing.
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` limits the maximum duration of post-run finalization. The default is `5s`. Finalization needs to fill in protocol closing events and write the aggregation cache to `SessionService`; if session storage becomes slow or fails, the timeout prevents the request from blocking for too long. Setting it to `0` means no timeout is applied.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithFlushInterval(time.Second),
    agui.WithPostRunFinalizationTimeout(5*time.Second),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)),
    ),
)
```

For more complex aggregation strategies, implement `aggregator.Aggregator` and inject it through a custom factory. Although each session gets its own aggregator, so cross-session state management and concurrency handling are not needed, aggregation methods themselves may still be called concurrently and must handle concurrency properly.

## Historical Run Lifecycle Events

The messages snapshot route itself returns `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED`. These `RUN_*` events only represent the start and end of the current `/history` request. If loading history fails, the route returns `RUN_ERROR`.

By default, `MESSAGES_SNAPSHOT.messages` does not include historical `RUN_STARTED`, `RUN_FINISHED`, or `RUN_ERROR` events from the conversation.

If the frontend needs to display the start, end, or error status of each historical run, enable `agui.WithMessagesSnapshotRunLifecycleEventsEnabled(true)`:

```go
server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotRunLifecycleEventsEnabled(true),
)
```

After this is enabled, persisted `RUN_STARTED`, `RUN_FINISHED`, and `RUN_ERROR` events from the historical conversation are written into `MESSAGES_SNAPSHOT.messages` as messages with `role=activity`, so they can be used to display historical run status.

Historical `RUN_*` messages in `MESSAGES_SNAPSHOT` have the following shape:

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "event-id-1",
      "role": "activity",
      "activityType": "RUN_STARTED",
      "content": {
        "threadId": "thread-1",
        "runId": "run-1"
      }
    },
    {
      "id": "event-id-2",
      "role": "assistant",
      "content": "hello"
    },
    {
      "id": "event-id-3",
      "role": "activity",
      "activityType": "RUN_ERROR",
      "content": {
        "runId": "run-1",
        "message": "model call failed",
        "code": "MODEL_ERROR"
      }
    }
  ]
}
```

## Messages Snapshot Continuation

By default, the messages snapshot route returns a one-shot snapshot and immediately closes the connection. When a user refreshes or reconnects while a real-time conversation is running, new AG-UI events may continue to be produced after the snapshot is generated. In this case, enable messages snapshot continuation so the same SSE connection continues receiving subsequent events after returning the snapshot.

After continuation is enabled, the server continues reading and forwarding subsequent AG-UI events after sending `MESSAGES_SNAPSHOT`, until it reads `RUN_FINISHED` or `RUN_ERROR`. The returned sequence becomes:

`RUN_STARTED → MESSAGES_SNAPSHOT → subsequent AG-UI events → RUN_FINISHED/RUN_ERROR`

Related configuration:

- `agui.WithMessagesSnapshotFollowEnabled(true)` enables messages snapshot continuation.
- `agui.WithMessagesSnapshotFollowMaxDuration(time.Duration)` limits the maximum continuation duration to avoid waiting indefinitely for a running conversation to finish.
- `agui.WithFlushInterval(time.Duration)` controls how often historical events are persisted. The continuation polling interval reuses this value.

Example:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotFollowEnabled(true),
    agui.WithMessagesSnapshotFollowMaxDuration(30*time.Second),
    agui.WithFlushInterval(50*time.Millisecond),
)
```

For the complete example, see [examples/agui/server/follow](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/follow). For the frontend, see [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat).
