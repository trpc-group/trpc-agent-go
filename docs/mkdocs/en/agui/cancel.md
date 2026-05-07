# Cancel Route

## Core Concepts

The cancel route actively stops a backend task that is currently running in the real-time conversation route. This route is disabled by default and can be enabled with `agui.WithCancelEnabled(true)`. The default path is `/cancel`, and it can be changed with `agui.WithCancelPath(path)`. To configure a shared route prefix, see [Route Prefix](index.md#route-prefix).

When canceling, the framework uses `AppName`, `UserID`, and `threadId` to form the same `SessionKey`, then cancels the backend task running under that session. Therefore, the cancel request must resolve to the same `AppName`, `UserID`, and `threadId` as the real-time conversation request.

In multi-instance deployments, the cancel request must hit the same instance that started the real-time conversation request. Running tasks are only kept locally in the instance. Sharing `SessionService` alone does not let other instances cancel the task. If the request hits another instance, it usually returns `404 Not Found`.

The cancel route is commonly used when:

- The frontend has a "stop generating" button and needs to interrupt backend execution.
- The SSE connection is closed and you want to stop backend execution to avoid wasting model or tool resources.
- The server needs time or cost controls and should interrupt abnormal runs in time.

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithCancelEnabled(true),
    agui.WithCancelPath("/cancel"),
)
```

## Cancel Request

The cancel route uses `RunAgentInput` as its request body. Usually, you need to pass `threadId` and the fields required by `UserIDResolver` or `AppNameResolver` to resolve the session. `runId` can be included for caller-side correlation, but cancel lookup depends on `SessionKey`.

Request body example:

```json
{
  "threadId": "thread-id",
  "runId": "run-id",
  "forwardedProps": {
    "userId": "alice"
  }
}
```

Corresponding `curl` example:

```bash
curl -X POST http://localhost:8080/cancel \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "thread-id",
    "runId": "run-id",
    "forwardedProps": {
      "userId": "alice"
    }
  }'
```

Typical responses:

- `200 OK`: cancellation succeeded.
- `404 Not Found`: no running task was found for the corresponding `SessionKey`, usually because it has already finished or identifiers do not match.

After cancellation succeeds, the framework still performs the required run finalization, fills in protocol closing events, and tries to write the aggregation cache to `SessionService`. Therefore, when history is later read through `/history`, the result is a valid and consistent state after cancellation rather than an unfinished intermediate state. For finalization and timeout configuration, see [Session Storage and Event Aggregation](history.md#session-storage-and-event-aggregation).
