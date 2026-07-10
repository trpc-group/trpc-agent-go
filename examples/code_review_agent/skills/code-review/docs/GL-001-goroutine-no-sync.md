# GL-001 — Goroutine Without Synchronization

| Field | Value |
| --- | --- |
| **RuleID** | GL-001 |
| **Severity** | High |
| **Category** | Correctness |
| **Confidence** | 0.8 |

## Description

Flags goroutines launched with `go` that have no observable synchronization
path (channel send/receive, `sync.WaitGroup.Done`, or context cancellation).
Orphaned goroutines leak resources and produce data races whose failures are
hard to reproduce.

## Evidence Example

```go
func process(items []Item) {
    for _, it := range items {
        go func(i Item) {
            result := transform(i) // no WaitGroup, no channel, no cancel
            _ = result
        }(it)
    }
    // caller returns immediately; goroutine lifetime is unbounded
}
```

## Recommendation

Tie each goroutine to a cancellation context and a synchronization primitive:

```go
func process(ctx context.Context, items []Item) error {
    var wg sync.WaitGroup
    for _, it := range items {
        wg.Add(1)
        go func(i Item) {
            defer wg.Done()
            select {
            case <-ctx.Done():
                return
            default:
                transform(i)
            }
        }(it)
    }
    wg.Wait()
    return nil
}
```

## False Positive Notes

- Long-lived background goroutines started once at process startup (e.g. a
  metrics ticker) are intentionally unbounded; annotate with
  `// code-review:ignore GL-001` if they are managed by the lifecycle.
- Goroutines whose result is observed via a buffered channel may still race if
  the channel is never drained; review the drain path before suppressing.
