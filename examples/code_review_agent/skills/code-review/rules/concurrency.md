# Concurrency

Flags added goroutines without visible context cancellation or owned lifetime.
Context-aware goroutines waiting on `ctx.Done()` are safe negatives.
