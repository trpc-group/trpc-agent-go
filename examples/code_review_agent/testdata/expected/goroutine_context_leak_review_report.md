# Code Review Report

- Summary: 1 high-confidence findings and 1 human-review items detected.
- Findings: 1
- Needs human review: 1

## Findings

### [high] Goroutine may not have a cancellation path

- File: `worker/worker.go:6`
- Rule: `GOR001`
- Evidence: `go func() {`
- Recommendation: Thread context cancellation through the goroutine and exit on ctx.Done().

## Needs Human Review

### [medium] Changed Go code without nearby test changes

- File: `worker/worker.go:5`
- Rule: `TEST001`
