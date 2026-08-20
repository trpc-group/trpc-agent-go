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

Real-time streaming output is split into many small events. Writing every small chunk to `SessionService` immediately can put high pressure on MySQL, Redis, or other session storage backends, especially during long text output, reasoning output, or streaming tool arguments. By default, the framework briefly buffers history events and writes them to session storage on a flush interval.

Within one flush interval, the framework merges text chunks from the same assistant message, reasoning chunks from the same assistant message, and argument chunks from the same tool call. Other events are still stored in their original order; when such an event appears, chunks before and after it are not merged across that event. `TOOL_CALL_RESULT` represents the complete result of one tool call and is not merged.

Except for the initial best-effort flush described under Messages Snapshot Continuation, real-time SSE output does not wait for history persistence to finish, so the frontend still receives model output immediately. `/history` reads only the content that has already been written to session storage. When a conversation is still running, a messages snapshot may therefore show the state from the last successful flush. When follow mode is enabled, later successfully flushed events are pushed to the client.

Related configuration:

- `aggregator.WithEnabled(true)` controls whether streaming chunks are merged. It is enabled by default.
- `agui.WithFlushInterval(time.Second)` controls how often history events are written to session storage. The default is `1s`. Setting it to `0` disables periodic writes while a run is active. This is usually appropriate when you do not need `/history` follow during an active run; history events are mainly written during post-run finalization. During long-running or high-volume runs, unwritten history events remain in process memory until finalization.
- `agui.WithTrackPersistenceTimeout(5*time.Second)` limits how long each AG-UI history persistence attempt can wait for session storage, including the initial best-effort flush, periodic flushes, and the final `Close` flush. The default is `5s`. If the final `Close` fails or times out, the error is logged, the completed run's in-process tracker state is released, and any remaining unwritten events are discarded. Setting it to `0` means no timeout is applied.
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` sets the timeout used to generate and emit protocol closing events after a run. The default is `5s`. It does not bound the final history `Flush` or `Close`; those operations use `agui.WithTrackPersistenceTimeout`. Setting it to `0` means no timeout is applied.

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
    agui.WithTrackPersistenceTimeout(5*time.Second),
    agui.WithPostRunFinalizationTimeout(5*time.Second),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)),
    ),
)
```

Most applications do not need a custom aggregation strategy. If you need to change which events can be merged, implement `aggregator.Aggregator` and inject it through a custom factory. Custom aggregators must handle concurrent calls.

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

## User Input forwardedProps Metadata

If your business stores attachments, form context, or other request-side information in AG-UI request `forwardedProps` and needs to restore that information from the history route after a page refresh, enable event source metadata:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
)

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithEventSourceMetadataEnabled(true),
)
```

After this is enabled, when the real-time conversation request persists the user input event, it writes the `forwardedProps` field from the AG-UI request body to the user input event's `rawEvent.forwardedProps`; in the Go API, that field corresponds to `RunAgentInput.ForwardedProps`. When reading history, the message snapshot route aggregates it into `MESSAGES_SNAPSHOT.rawEvent.runs[runId].forwardedProps`:

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "user-1",
      "role": "user",
      "content": "Please check the attachment"
    }
  ],
  "rawEvent": {
    "runs": {
      "run-1": {
        "author": "demo-user",
        "forwardedProps": {
          "file_url": "https://example.com/demo.png",
          "attachments": [
            {
              "id": "file-1",
              "mimeType": "image/png"
            }
          ]
        },
        "timestamp": 1781258400000
      }
    },
    "messages": {
      "user-1": {
        "author": "demo-user",
        "timestamp": 1781258400000
      }
    }
  }
}
```

## Messages Snapshot Continuation

By default, the messages snapshot route returns a one-shot snapshot and immediately closes the connection. When a user refreshes or reconnects while a real-time conversation is running, new AG-UI events may continue to be produced after the snapshot is generated. In this case, enable messages snapshot continuation so the same SSE connection continues receiving subsequent events after returning the snapshot.

After continuation is enabled, the server continues reading and forwarding subsequent AG-UI events after sending `MESSAGES_SNAPSHOT`, until it reads `RUN_FINISHED` or `RUN_ERROR`. The returned sequence becomes:

`RUN_STARTED → MESSAGES_SNAPSHOT → subsequent AG-UI events → RUN_FINISHED/RUN_ERROR`

For a tracked run with continuation enabled and a positive flush interval, the runner makes a best-effort flush after recording the initial `RUN_STARTED` event and before emitting that event to the real-time SSE stream. After a successful flush with a synchronous `TrackService`, another instance sharing the same `SessionService` can therefore observe a non-terminal event once `RUN_STARTED` has been emitted, while later events continue to use periodic buffering. A failed initial flush is logged without stopping the run, and the pending events remain available for a later periodic flush or final close. Asynchronous `TrackService` implementations may still delay cross-instance visibility after the flush call returns.

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
