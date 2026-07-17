# Code Review Report

- Task: `review-92d75268a95063c75fc76c54`
- Status: `completed`
- Files changed: `2`
- Go files changed: `2`
- Findings: `0`
- Needs human review: `0`

## Package Summary

| Package path | Package name | Files |
| --- | --- | ---: |
| `calc` | `calc` | 2 |

## Severity Summary

- critical: 0
- high: 0
- medium: 0
- low: 0

## Category Summary

No finding categories recorded.

## Findings

No high-confidence findings.

## Fix Recommendations

No executable recommendations recorded.

## Human Review Items

No items require human review.

## Warnings

No low-confidence warnings.

## Governance

- Permission allow decisions: `4`
- Permission deny decisions: `0`
- Permission ask decisions: `0`
- Permission needs human review decisions: `0`
- Artifact policy: retained `5`, rejected `0`, max `5` files, max `1048576` bytes per file
- `bash skills/code-review/scripts/diff_summary.sh work/change.diff out/diff_summary.json`: action=`allow`, disposition=`allow`
- `go test ./...`: action=`allow`, disposition=`allow`
- `go vet ./...`: action=`allow`, disposition=`allow`
- `staticcheck ./...`: action=`allow`, disposition=`allow`

## Sandbox

- `bash skills/code-review/scripts/diff_summary.sh work/change.diff out/diff_summary.json`: success, exit=0, timeout=false, duration=45ms
- `go test ./...`: success, exit=0, timeout=false, duration=5687ms
- `go vet ./...`: success, exit=0, timeout=false, duration=492ms
- `staticcheck ./...`: success, exit=0, timeout=false, duration=1242ms

## Metrics

- Total duration: `9676ms`
- Sandbox duration: `7466ms`
- Tool calls: `4`
- Error types: `map[]`

## Conclusion

No high-confidence code review issues were detected. Review sandbox warnings before merging if any checks were skipped or unavailable.
