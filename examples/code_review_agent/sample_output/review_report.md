# Code Review Report

- Task: review-a57a6dec5548602b
- Status: completed
- Conclusion: changes_requested
- Input: diff (eca93c1af2be9d3a2b80688efadfd7ea4379f0ce05a13887c666f3df4de090b5)

## Findings summary

- Findings: 1
- Warnings: 0
- Needs human review: 0

### Severity distribution
- critical: 1


## Findings

### [critical] Hard-coded secret

- Location: config.go:3
- Category / rule: sensitive_information / GO-SECRET-001
- Evidence: var [REDACTED:named_secret:2ca46602]
- Recommendation: Load credential from an approved secret provider and rotate exposed value.
- Confidence / source: 0.95 / deterministic_patch


## Warnings
No warnings.


## Needs human review
No manual review items.


## Governance

- Blocked decisions: 0


## Sandbox runs
No sandbox runs.


## Monitoring

- Total duration: 186 ms
- Sandbox duration: 0 ms
- Tool calls: 0
- Permission blocks: 0
- Finding count: 1

### Error type distribution
No error types.


## Artifacts
No artifacts.
