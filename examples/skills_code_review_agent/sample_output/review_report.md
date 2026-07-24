# Code Review Report

- Task: `review-1784866294951236597-c7f80d64e7f8`
- Status: `completed`
- Conclusion: `changes_requested`
- Mode: `dry-run`
- Runtime: `fake`
- Skill: `code-review`

## Findings Summary

2 high-confidence findings and 1 warnings across 1 changed files.

## Severity Statistics

- `critical`: 0
- `high`: 2
- `medium`: 0
- `low`: 1

## Findings

### HIGH: Shell command can execute untrusted input

- Location: `internal/query/query.go:3`
- Category: `security`
- Rule: `SEC002`
- Confidence: `0.94`
- Source: `rule`
- Evidence: `exec.Command("sh", "-c", userInput).Run()`
- Recommendation: Avoid a shell and pass validated arguments directly to exec.CommandContext.

### HIGH: SQL statement is built by string interpolation

- Location: `internal/query/query.go:4`
- Category: `security`
- Rule: `SEC003`
- Confidence: `0.93`
- Source: `rule`
- Evidence: `query := fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", userInput)`
- Recommendation: Use a parameterized query and pass values separately.

## Human Review

### LOW: Changed Go code has no matching test change

- Location: `internal/query/query.go:2`
- Category: `testing`
- Rule: `TST001`
- Confidence: `0.62`
- Source: `rule`
- Evidence: `No _test.go file changed in the same package.`
- Recommendation: Add or update focused tests for the changed behavior.

## Governance Decisions

- `allow` risk=`low`: `"bash" "skills/code-review/scripts/check_diff.sh" "work/input.diff"`

## Sandbox Execution

- `"bash" "skills/code-review/scripts/check_diff.sh" "work/input.diff"`: status=`passed`, exit=`0`, duration=`1ms`, timeout=`false`

## Monitoring

- Total duration: `0ms`
- Sandbox duration: `0ms`
- Tool calls: `1`
- Permission blocks: `0`
- Exceptions: `0`
