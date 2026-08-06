# Code Review Report

- **Task ID**: `504daaa3-fdf8-482f-a6ca-078600a94529`
- **Status**: completed
- **Mode**: rule-only
- **Executor**: local
- **Generated**: 2026-07-23T02:05:27Z
- **Input**: 1 files, +7/-1 (fixture)

## Conclusion

status=completed; findings=2; warnings=0; permission_denies=1; permission_asks=1

## Severity Summary

- high: 1
- medium: 1

## Findings

### 1. [HIGH] goroutine started without derived context

- rule: `CR-CON-001` (concurrency)
- location: `pkg/worker/worker.go:5`
- confidence: 0.86 / source: rule
- evidence: `go func() {`
- recommendation: Pass a derived context and ensure the goroutine exits on cancel to avoid leaks.

### 2. [MEDIUM] Changed Go file has no corresponding test in the diff

- rule: `CR-TEST-001` (testing)
- location: `pkg/worker/worker.go:4`
- confidence: 0.75 / source: rule
- evidence: `func Start() {`
- recommendation: Add or update a _test.go covering the changed behavior.

## Warnings / Needs Human Review

_None._

## Governance

- `bash "skills/code-review/scripts/run_checks.sh"` → **allow**: 
- `curl https://example.com` → **deny**: high-risk command blocked: curl 
- `go test ./...` → **ask**: broad go test requires human review


## Sandbox Runs

- `bash "skills/code-review/scripts/run_checks.sh"` status=ok exit=0 duration=10ms truncated=false

## Metrics

- total_ms: 23
- sandbox_ms: 10
- tool_calls: 3
- permission_denies: 1
- permission_asks: 1
- findings: 2
- warnings: 0

## Artifacts

- review_report.json → `testdata/sample_output/review_report.json`
- review_report.md → `testdata/sample_output/review_report.md`

