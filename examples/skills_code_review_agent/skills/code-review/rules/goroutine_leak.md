# GL-001 - Goroutine Started Without Synchronisation

**Severity:** high  
**Category:** goroutine_leak

## Description

A `go func()` literal is added without any visible WaitGroup, channel
synchronisation, or context cancellation in the same hunk. If the goroutine
outlives the caller it becomes a leak.

## Detection

Triggered when:
- `go func(` appears in an added line.
- No `wg.Add`, `wg.Wait`, `wg.Done`, `<-done`, channel send, or `cancel()` is
  visible in the same hunk.

## False positive rate

Medium - the rule cannot see code outside the diff hunk. Synchronisation that
lives outside the changed lines will not suppress this finding.

## Fix

```go
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    // ...
}()
wg.Wait()
```
