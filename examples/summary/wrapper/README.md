# Request-Scoped Summarizer Wrapper Example

This example demonstrates how to keep a session service long-lived while
building a fresh summarizer from each request's context.

This is useful when the storage backend should be reused, but the summary model
or prompt can change per request. A common case is a MySQL session service with
a shared connection pool, while user-selected custom models require a different
summary model for each turn.

## What it shows

- A `summary.ContextAwareSummarizer` wrapper that reads request metadata from
  `ctx`.
- A per-request `summary.NewSummarizer(...)` built exactly when summary work
  runs.
- One long-lived `session.Service` that can be reused independently from the
  summarizer lifetime.
- Both synchronous `CreateSessionSummary` and asynchronous `EnqueueSummaryJob`
  using request-scoped summary configuration.

The demo uses an in-memory session service to keep setup small. The same wrapper
can be passed to MySQL:

```go
wrapper := newRequestScopedSummarizer(newSummarizerForRequest)

sessionService, err := mysql.NewService(
    mysql.WithMySQLInstance("shared-session-mysql"),
    mysql.WithSummarizer(wrapper),
)
```

The important part is that callers attach request summary configuration to the
context they pass into summary operations:

```go
ctx = WithSummaryRequest(ctx, summaryRequest{
    ID:        "turn-123",
    ModelName: "deepseek-v4-flash",
    Style:     "custom model handoff",
})

err := sessionService.EnqueueSummaryJob(ctx, sess, "", false)
```

## Run

```bash
cd examples/summary/wrapper
export OPENAI_API_KEY="your-api-key"
go run . -sync-model deepseek-v4-flash -async-model deepseek-v4-flash
```

Optional flags:

- `-sync-model`: Summary model name for the synchronous request.
- `-async-model`: Summary model name for the asynchronous request.
- `-wait-sec`: Max wait time for async summary generation.

Expected output includes the request-scoped model names:

```text
== sync summary ==
[request=sync-request model=deepseek-v4-flash style=architecture notes]
...

== async summary ==
[request=async-request model=deepseek-v4-flash style=custom model handoff]
...
```

## Why use this pattern

Avoid mutating a shared summarizer with `SetModel` for each request. A shared
summarizer can be used concurrently by async summary workers, so changing its
model or prompt in place can leak one request's model into another request's
summary job.

The wrapper fails closed when no request metadata is present, which makes
missing context easier to notice and avoids silently summarizing with a stale
model.
