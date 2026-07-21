# Code Review Report

- Summary: 1 high-confidence findings and 1 human-review items detected.
- Findings: 1
- Needs human review: 1

## Findings

### [high] Opened resource may not be closed

- File: `files/read.go:6`
- Rule: `RES001`
- Evidence: `f, err := os.Open(path)`
- Recommendation: Close the returned resource with defer after checking the error.

## Needs Human Review

### [medium] Changed Go code without nearby test changes

- File: `files/read.go:5`
- Rule: `TEST001`
