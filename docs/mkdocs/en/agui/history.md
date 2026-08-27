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

Real-time streaming output is split into many small events. Writing every small chunk to `SessionService` immediately can put high pressure on MySQL, Redis, or other session storage backends, especially during long text output, reasoning output, or streaming tool arguments. By default, the framework queues history events and writes them to session storage in batches.

The default aggregator merges adjacent text chunks from the same assistant message, adjacent reasoning chunks from the same assistant message, and adjacent argument chunks from the same tool call. A different message or tool-call ID, a different content type, or any non-content event ends the current aggregate. `TOOL_CALL_RESULT` represents the complete result of one tool call and is not merged. Aggregator output is still queued by the history tracker and written only by a startup, periodic, or final flush.

For every tracked run with a positive flush interval, the runner makes one startup best-effort flush after recording the initial `RUN_STARTED` and before publishing the first SSE event. Later SSE delivery does not wait for history persistence to finish, so the frontend continues to receive model output immediately. `/history` reads only content that has already been written to session storage. While a conversation is running, a messages snapshot may therefore show the state from the last successful flush. When follow mode is enabled, later successfully flushed events are pushed to the client.

Related configuration:

- `aggregator.WithEnabled(true)` controls whether adjacent streaming chunks are merged. It is enabled by default.
- `agui.WithFlushInterval(time.Second)` controls the startup and periodic history flushes. The default is `1s`. A positive interval enables one startup best-effort flush before the first SSE event and periodic writes while the run is active. Setting it to `0` disables both startup and periodic writes; history events are then mainly written during post-run finalization. During long-running or high-volume runs, unwritten history events remain in process memory until finalization.
- `agui.WithTrackPersistenceTimeout(5*time.Second)` limits how long each AG-UI history persistence attempt can wait for session storage, including the startup best-effort flush, periodic flushes, and the final `Close` flush. The default is `5s`. A failed storage write during an active flush returns an error and discards its drained batch instead of retrying it; events queued while that write is in progress remain eligible for later flushes. If the final `Close` fails or times out, the error is logged, the completed run's in-process tracker state is released, and any remaining unwritten events are discarded. Setting it to `0` means no timeout is applied.
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

Most applications do not need a custom aggregation strategy. If you need to change which events can be merged, implement `aggregator.Aggregator` and inject it through a custom factory. A custom implementation may, for example, buffer several `CUSTOM` events and return one merged event from `Flush`. `Append` may borrow its input only for the duration of the call; an implementation must copy any data it retains after returning. The history tracker snapshots returned events immediately and queues them for the next persistence flush. Custom aggregators must handle concurrent calls.

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

After this is enabled, when the real-time conversation request persists the user input event, it writes the `forwardedProps` field from the AG-UI request body to the user input event's `rawEvent.forwardedProps`; in the Go API, that field corresponds to `RunAgentInput.ForwardedProps`. When reading history, the message snapshot route aggregates it into `MESSAGES_SNAPSHOT.rawEvent.runs[runId].forwardedProps`, and writes `runId` into message metadata when the message can be associated with a run:

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
        "runId": "run-1",
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

Every tracked run with a positive flush interval makes a startup best-effort flush after recording the initial `RUN_STARTED` event and before publishing it to the real-time SSE stream. A successful startup flush establishes the session before the periodic writer, RunHooks, and the wrapped runner start. When continuation is enabled and the synchronous `TrackService` flush succeeds, another instance sharing the same `SessionService` can therefore observe a non-terminal event once `RUN_STARTED` has been emitted; later events continue to use periodic buffering. If the startup flush fails, the error is logged without stopping the run and its drained batch is not retried. To avoid racing first-session creation, periodic flushing then starts only after the wrapped runner completes synchronous initialization; events recorded in the meantime remain queued for a later periodic flush or final close. Asynchronous `TrackService` implementations may still delay cross-instance visibility after a flush call returns.

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

## Paginated Messages Snapshots

By default, `/history` restores the complete persisted AG-UI history for the requested session. For long-running conversations, returning the whole history in one response can increase storage reads, server-side reduction work, and network traffic. In that case, configure `agui.WithMessagesSnapshotSessionPageResolver` to let each snapshot request load one page of persisted events.

Pagination is resolved at the session layer. The AG-UI server does not define its own public cursor field or fixed request parameter names. Instead, the resolver receives the `RunAgentInput` and the resolved `session.Key`, then returns a session page request. Applications can choose where the cursor and limit come from, such as `forwardedProps`, request metadata mapped by the gateway, or another application-specific input source.

The resolver returns `*aguirunner.MessagesSnapshotPageRequest`. `Cursor` is an opaque value returned by a previous snapshot page; an empty cursor asks the session service for the latest page. `EventLimit` limits the number of persisted AG-UI track events read from session storage. It is an event limit, not a message or turn limit, because a single displayed message may be restored from several persisted AG-UI events.

Pagination is used only when the configured session service implements `session.TrackEventPageService`. If the service does not provide that capability, `/history` keeps the existing full-snapshot behavior.

A non-nil page request makes the snapshot response one-shot. Even when message snapshot follow mode is enabled, `/history` emits the paginated `MESSAGES_SNAPSHOT` and then finishes the stream.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotSessionPageResolver(
        func(ctx context.Context, input *adapter.RunAgentInput, key session.Key) (*aguirunner.MessagesSnapshotPageRequest, error) {
            forwardedProps, _ := input.ForwardedProps.(map[string]any)
            cursor, _ := forwardedProps["cursor"].(string)
            return &aguirunner.MessagesSnapshotPageRequest{
                Cursor:     cursor,
                EventLimit: 200,
            }, nil
        },
    ),
)
```

For a paginated snapshot, the response still uses the standard `MESSAGES_SNAPSHOT` event. Page information is attached to `MESSAGES_SNAPSHOT.rawEvent.page`:

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "user-1",
      "role": "user",
      "content": "Hello"
    },
    {
      "id": "assistant-1",
      "role": "assistant",
      "content": "Hello. How can I help?"
    }
  ],
  "rawEvent": {
    "page": {
      "cursor": "opaque-cursor-for-the-page-boundary",
      "hasMore": true
    }
  }
}
```

The returned `cursor` should be sent with the next snapshot request to load older history. Treat it as an opaque token: do not parse, compare, or construct it in application code.

`hasMore=true` means the client has not received all older history yet. This can be because the session store still has older events, or because the AG-UI layer trimmed part of the fetched page before returning it. The trimming is intentional: message snapshots are returned from a user-message boundary, so the frontend does not receive the trailing half of an earlier turn.

If the fetched page does not contain a user-message boundary, `/history` returns an empty `messages` array, keeps the requested cursor in `rawEvent.page.cursor`, and sets `hasMore=true`. The client can then retry with the same cursor and a larger `EventLimit` to ask the session service for a wider event window.
