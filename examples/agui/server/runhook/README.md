# Run Hook AG-UI Server

This example shows how to use `agui.WithRunHook` to emit run-scoped UI events from server-side background code.

The hook emits `CUSTOM` events while the normal agent run is also executing. These events:

- stream to the current AG-UI SSE client
- persist to `TrackAGUI` for `/history`
- do not enter normal `session.Events`
- do not become model context
- do not pass through translator callbacks

## What This Example Shows

The server wires a regular `llmagent` runner and adds one run hook:

```go
server, err := agui.New(
    run,
    agui.WithAppName(appName),
    agui.WithPath("/agui"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithRunHook(pushBackgroundReportStatus),
)
```

The hook pushes one custom event every 100ms:

```go
func pushBackgroundReportStatus(ctx context.Context, run *aguirunner.Run) error {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    for step := 1; step <= 5; step++ {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
        }
        err := run.Emit(ctx, aguievents.NewCustomEvent(
            "background.report.status",
            aguievents.WithValue(map[string]any{
                "progress": step * 20,
            }),
        ))
        if err != nil {
            return err
        }
    }
    return nil
}
```

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.

go run ./server/runhook \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

The server exposes:

- chat endpoint: `http://127.0.0.1:8080/agui`
- history endpoint: `http://127.0.0.1:8080/history`

## Inspect The Raw SSE Stream

```bash
curl -N http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "runhook-demo",
    "runId": "runhook-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Summarize why server-side progress events are useful."
      }
    ]
  }'
```

Look for events like:

```text
RUN_STARTED
CUSTOM(name="background.report.status")
CUSTOM(name="background.report.status")
CUSTOM(name="background.report.status")
TEXT_MESSAGE_*
RUN_FINISHED
```

The agent may finish before the hook completes. AG-UI delays the final run terminal event until the hook is done, so the last background UI updates still reach the client before `RUN_FINISHED`.
