# Code Review Report

- Task: `sample-security`
- Status: `completed`
- Findings: `1`
- Human review: `0`

## Summary

- Severity: `high=1`
- Tools: `3`, duration_ms `0`, errors `success=3`
- Sandbox: runs `3`, non_zero `0`, timed_out `0`, truncated `0`, unisolated `false`
- Governance decisions: `3`
- Artifacts: `2`

## Metrics

- `duration_ms`: `194`
- `files`: `2`
- `findings`: `1`
- `human_review`: `0`
- `permission_blocks`: `0`
- `permission_decisions`: `3`
- `sandbox_duration_ms`: `0`
- `sandbox_runs`: `3`
- `severity_high`: `1`
- `suppressed`: `0`
- `tool_calls`: `3`

## Reviewed Files

- `app.go`
- `app_test.go`

## Parser Warnings

None.

## Findings

| Severity | Category | File | Line | Title | Evidence | Recommendation |
| --- | --- | --- | ---: | --- | --- | --- |
| high | security | app.go | 2 | unsafe dynamic execution or query | func run(user string) { exec.Command("sh", "-c", user) } | use fixed commands or parameterized queries with validated arguments |

## Needs Human Review

None.

## Governance

- `allow:go-test::3190296ecc68873ab9fd816e588ba5c52419a95ec0a5250446a898770df5e80d`
- `allow:go-vet::9ab3aefa14b63f3a63e661edcb355ae4bd03e348aa65053abbd68f4b8299e49d`
- `allow:staticcheck::91590cdf0adfc9eebe630259ccab557c12dedff43180033b1d47f3460b4aad09`

## Sandbox

- `go-test`: outcome `success`, exit `0`, timeout `false`, truncated `false`, duration_ms `0`
- `go-vet`: outcome `success`, exit `0`, timeout `false`, truncated `false`, duration_ms `0`
- `staticcheck`: outcome `success`, exit `0`, timeout `false`, truncated `false`, duration_ms `0`

## Artifacts

- `review_report.json`
- `review_report.md`
