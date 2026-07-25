# GL-002 — Context Leak

| Field | Value |
| --- | --- |
| **RuleID** | GL-002 |
| **Severity** | Medium |
| **Category** | Correctness |
| **Confidence** | 0.9 |

## Description

Detects contexts created with `context.WithCancel`, `context.WithTimeout` or
`context.WithDeadline` whose cancel function is not scheduled for execution
via `defer`.

Failing to call cancel retains context-package bookkeeping for the child
context tree. The resource cost differs by constructor:

- `context.WithCancel` retains cancellation bookkeeping (no timer).
- `context.WithTimeout` / `context.WithDeadline` additionally retain timer
  resources until the deadline fires or cancel is called.

In long-running services, unreclaimed cancel funcs accumulate and can exhaust
memory. Always `defer cancel()` immediately after creation.

## Evidence Example

```go
func fetch(ctx context.Context) (*Result, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    // cancel is never called — timer leaks until 5s elapses,
    // even if the caller has already returned.
    return doRequest(ctx)
}
```

## Recommendation

Always defer the cancel func immediately after creation:

```go
func fetch(ctx context.Context) (*Result, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel() // releases timer resources as soon as fetch returns
    return doRequest(ctx)
}
```

## False Positive Notes

- When the cancel func is intentionally propagated to a caller (e.g. returned
  alongside a value), the rule may still fire. Document the ownership transfer
  at the call site with a comment to suppress.
- `context.Background()`-derived contexts without a deadline do not trigger
  this rule.
