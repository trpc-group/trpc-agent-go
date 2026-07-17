# Code Review Report

- Task: `review-88128b232348d460f3ee2048`
- Status: `completed`
- Files changed: `1`
- Go files changed: `1`
- Findings: `1`
- Needs human review: `1`

## Package Summary

| Package path | Package name | Files |
| --- | --- | ---: |
| `service` | `service` | 1 |

## Severity Summary

- critical: 1
- high: 0
- medium: 0
- low: 1

## Category Summary

| Category | Count |
| --- | ---: |
| security | 1 |
| test_coverage | 1 |

## Findings

### critical: Hard-coded secret or credential-like value

- Rule: `go/security/secret-literal`
- Category: `security`
- Location: `service/config.go:7`
- Confidence: `0.95`
- Evidence: `const apiKey = [REDACTED]`
- Recommendation: Move secrets to a managed secret store or environment variable and rotate any exposed value.

## Fix Recommendations

- `go/security/secret-literal`: Move secrets to a managed secret store or environment variable and rotate any exposed value.
- `go/test/missing-test-change`: Add or update focused tests for changed behavior, especially error and lifecycle paths.

## Human Review Items

### low: Production Go change has no accompanying test change

- Rule: `go/test/missing-test-change`
- Category: `test_coverage`
- Location: `service/config.go:1`
- Confidence: `0.64`
- Evidence: `No *_test.go file changed in this diff.`
- Recommendation: Add or update focused tests for changed behavior, especially error and lifecycle paths.

## Warnings

No low-confidence warnings.

## Governance

- Permission allow decisions: `1`
- Permission deny decisions: `0`
- Permission ask decisions: `0`
- Permission needs human review decisions: `0`
- Artifact policy: retained `4`, rejected `0`, max `5` files, max `1048576` bytes per file
- `bash skills/code-review/scripts/diff_summary.sh work/change.diff out/diff_summary.json`: action=`allow`, disposition=`allow`

## Sandbox

- `bash skills/code-review/scripts/diff_summary.sh work/change.diff out/diff_summary.json`: skipped, exit=0, timeout=false, duration=0ms
- `go test ./... vet ./... staticcheck ./...`: skipped, exit=0, timeout=false, duration=0ms

## Metrics

- Total duration: `6ms`
- Sandbox duration: `0ms`
- Tool calls: `0`
- Error types: `map[dry_run:1 no_repo_path:1]`

## Conclusion

Critical security findings were detected. Do not merge until the listed secret or credential issues are remediated and rotated.
