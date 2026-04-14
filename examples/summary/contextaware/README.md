# Context-Aware Summary Routing Example

This example demonstrates a real-model version of request-scoped summary
routing with branch-isolated `filterKey`s.

It shows how business code can:

1. Put a custom request struct on `ctx`.
2. Put a business-defined sync/async marker on `ctx`.
3. Implement `summary.ContextAwareSummarizer`.
4. Route to different real `summary.NewSummarizer(...)` instances at runtime.
5. Keep billing/support history and summaries isolated with non-empty branch keys.

Unlike the lightweight stub version, this demo uses:

- a real agent model via `llmagent`
- a real summary model via `summary.NewSummarizer`
- real `Runner` / `SessionService` integration
- prompt inspection via `BeforeModel` callbacks

To keep the example deterministic, it also disables the runner's default
"append event -> auto EnqueueSummaryJob" path via an `AppendEventHook`. This
demo only wants to show the two explicitly annotated summary calls:

- one manual sync `CreateSessionSummary`
- one manual async `EnqueueSummaryJob`

## Prerequisites

- Go 1.21 or later.
- Model configuration with an OpenAI-compatible endpoint.

Environment variables:

- `OPENAI_API_KEY`
- `OPENAI_BASE_URL` (optional)

## Run

```bash
cd examples/summary/contextaware
export OPENAI_API_KEY="your-api-key"
go run . -model deepseek-chat
```

Optional flags:

- `-model`: Model name for both chat and summary generation.
- `-wait-sec`: Max wait time for async summary generation.
- `-billing-input`: First turn input before sync summary.
- `-support-input`: Second turn input before async summary.
- `-final-input`: Final turn input after async summary.

## What it demonstrates

The demo runs three phases:

1. A billing conversation turn.
2. A synchronous summary generated with:
   - `summaryRequest{Tenant: "vip", Scene: "billing"}`
   - `summaryModeSync`
3. A support conversation turn on a different branch key.
4. An asynchronous summary generated with:
   - `summaryRequest{Tenant: "standard", Scene: "support"}`
   - `summaryModeAsync`
5. A final support follow-up turn that shows the support summary injected back
   into the agent prompt.

The router picks one of these real summarizers:

- `billing-sync`
- `billing-async`
- `support-sync`
- `support-async`

Each route uses a different summary prompt. The demo also adds a deterministic
prefix such as:

```text
[route=support-async tenant=standard scene=support mode=async]
```

That prefix is added in a post-summary hook so the selected summarizer is
obvious even if the model paraphrases the body differently on each run.

## What to observe

1. `📝 Summary model route=...` lines show the real summary model request for
   the selected route.
2. `== Sync summary ==` should include `route=billing-sync`.
3. `== Async summary ==` should include `route=support-async`.
4. The support turn should not inherit billing history, because it runs on a
   different non-empty `filterKey`.
5. `🧾 Agent model request ...` lines for the final support follow-up should
   include the injected summary system message.

## Key pattern

The recommended business-side shape is:

```go
type summaryRequest struct {
    Tenant string
    Scene  string
}

type summaryMode string

const (
    summaryModeSync  summaryMode = "sync"
    summaryModeAsync summaryMode = "async"
)
```

Wrap your service calls like this:

```go
func filterKey(app string, req summaryRequest) string {
    return fmt.Sprintf("%s/%s/%s", app, req.Tenant, req.Scene)
}

func runTurn(ctx context.Context, r runner.Runner, userID, sessionID, app string, req summaryRequest, input string) error {
    _, err := r.Run(
        ctx,
        userID,
        sessionID,
        model.NewUserMessage(input),
        agent.WithEventFilterKey(filterKey(app, req)),
    )
    return err
}

func createSummaryWithRequest(
    ctx context.Context,
    svc session.Service,
    sess *session.Session,
    app string,
    req summaryRequest,
) error {
    ctx = WithSummaryRequest(WithSummaryMode(ctx, summaryModeSync), req)
    return svc.CreateSessionSummary(ctx, sess, filterKey(app, req), false)
}

func enqueueSummaryWithRequest(
    ctx context.Context,
    svc session.Service,
    sess *session.Session,
    app string,
    req summaryRequest,
) error {
    ctx = WithSummaryRequest(WithSummaryMode(ctx, summaryModeAsync), req)
    return svc.EnqueueSummaryJob(ctx, sess, filterKey(app, req), false)
}
```

The important part is that the same non-empty `filterKey` is used for:

- `runner.Run(..., agent.WithEventFilterKey(...))`
- `CreateSessionSummary`
- `EnqueueSummaryJob`

If your own code rewrites `EnqueueSummaryJob`, that wrapper is exactly where the
async marker should be attached to `ctx`.
