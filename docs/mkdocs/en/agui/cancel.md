# Cancel Route

## Core Concepts

The cancel route actively stops a backend task that is currently running in the real-time conversation route. This route is disabled by default and can be enabled with `agui.WithCancelEnabled(true)`. The default path is `/cancel`, and it can be changed with `agui.WithCancelPath(path)`. To configure a shared route prefix, see [Route Prefix](index.md#route-prefix).

When canceling, the framework uses `AppName`, `UserID`, and `threadId` to form the same `SessionKey`, then cancels the backend task running under that session. Therefore, the cancel request must resolve to the same `AppName`, `UserID`, and `threadId` as the real-time conversation request.

By default, in multi-instance deployments, the cancel request must hit the same instance that started the real-time conversation request. Running tasks are only kept locally in the instance. If the request hits another instance, it usually returns `404 Not Found`.

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

- `200 OK`: local cancellation has been triggered, or a remote cancel signal has been delivered when distributed cancel is enabled.
- `404 Not Found`: no running task was found for the corresponding `SessionKey`, usually because it has already finished or identifiers do not match.

After cancellation succeeds, the framework still performs the required run finalization, fills in protocol closing events, and tries to write the aggregation cache to `SessionService`. A successful cancel response does not mean the same `SessionKey` can immediately start a new real-time conversation request. If you need to start the next run, wait for the original real-time conversation stream to return a terminal event. Therefore, when history is later read through `/history`, the result is a valid and consistent state after cancellation rather than an unfinished intermediate state. For finalization and timeout configuration, see [Session Storage and Event Aggregation](history.md#session-storage-and-event-aggregation).

## Multi-Instance Distributed Cancel

If a multi-instance deployment cannot guarantee that the real-time conversation request and cancel request for the same `SessionKey` hit the same instance, enable distributed cancel:

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithSessionService(sessionService),
    agui.WithCancelEnabled(true),
    agui.WithDistributedCancelEnabled(true),
    agui.WithDistributedCancelPollInterval(time.Second),
)
```

After this is enabled, AG-UI instances that participate in the same request group need to use the same shared `SessionService`. When a cancel request hits an instance that does not hold the local run, the framework delivers a cancel signal through the shared `SessionService`, and the instance that holds the run triggers local cancellation.

Distributed cancel still locates running tasks by the `SessionKey` formed from `AppName`, `UserID`, and `threadId`. A successful remote cancel response only means the cancel signal has been delivered. It does not mean the instance that holds the run has finished cancellation. If you need to start the next run, wait for the original real-time conversation stream to return a terminal event.

Distributed cancel depends on the shared `SessionService` to deliver cancel signals. If reading from or writing to the shared `SessionService` fails, cross-instance cancellation may not take effect or may return an error.

The instance that holds the run checks for cancel signals at the polling interval. The default interval is `1s`, and it can be changed with `agui.WithDistributedCancelPollInterval(d)`. A shorter interval usually reduces remote cancellation latency, but increases the number of reads to `SessionService`.
