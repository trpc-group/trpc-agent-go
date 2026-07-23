# AG-UI Runner Factory Server

This example demonstrates `agui.WithRunnerFactory`.

It replaces the AG-UI layer runner construction with a custom factory while still reusing the framework's built-in AG-UI runner internally. The example is intentionally small: it only rewrites plain text file IDs to file links before a run, then rewrites those links back to file IDs when serving message history.

## What This Example Shows

- How to provide an AG-UI runner factory with `agui.WithRunnerFactory`.
- How to wrap the built-in AG-UI runner by calling `aguirunner.New(base, opts...)`.
- How to customize `Run` and `MessagesSnapshot` behavior without changing the framework agent runner.
- How to keep `Cancel` support by forwarding cancellation to the inner runner.

The example uses plain text markers only. It does not use AG-UI multimodal input.

## Rewrite Behavior

When the client sends:

```text
Summarize file:report-1
```

the custom runner passes this text to the built-in AG-UI runner as:

```text
Summarize https://files.example.local/report-1
```

When `/history` returns a `MessagesSnapshotEvent`, the custom runner rewrites the stored link back to:

```text
Summarize file:report-1
```

This mirrors a common integration pattern where clients use stable file IDs while the runtime needs temporary file URLs.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.

go run ./server/runner_factory \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui \
  -messages-snapshot-path /history \
  -cancel-path /cancel
```

The server exposes:

- chat endpoint: `http://127.0.0.1:8080/agui`
- messages snapshot endpoint: `http://127.0.0.1:8080/history`
- cancel endpoint: `http://127.0.0.1:8080/cancel`

## Try with curl

Send a run request with a plain text file ID:

```bash
curl -N --location http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "runner-factory-demo-thread",
    "runId": "runner-factory-demo-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Summarize file:report-1"
      }
    ]
  }'
```

Inspect the message snapshot for the same thread:

```bash
curl -N --location http://127.0.0.1:8080/history \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "runner-factory-demo-thread",
    "runId": "runner-factory-demo-history-1"
  }'
```

Look for `file:report-1` in the snapshot output. The built-in runner saw the generated link during `Run`, but the custom runner rewrote the snapshot back to the client-facing file ID.

Cancel a running request by using the same `threadId` and `runId` as the active run:

```bash
curl --location http://127.0.0.1:8080/cancel \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "runner-factory-demo-thread",
    "runId": "runner-factory-demo-run-1"
  }'
```

The cancel command is only useful while the run is still active.

## Implementation Notes

The custom factory receives the framework agent runner and the AG-UI runner options collected by `agui.New`:

```go
func newFileLinkRunner(base runner.Runner, opts ...aguirunner.Option) (aguirunner.Runner, error) {
	inner := aguirunner.New(base, opts...)
	return &fileLinkRunner{inner: inner}, nil
}
```

Passing `opts...` through is important. These options include runner-level values derived from server options such as app name, session service, timeout, messages snapshot follow behavior, distributed cancel settings, event translation controls, and values passed with `agui.WithAGUIRunnerOptions`.

File layout:

- `main.go`: flags, startup logs, and HTTP serving.
- `agent.go`: model and generation configuration.
- `agui.go`: AG-UI server construction and custom runner wrapper.
