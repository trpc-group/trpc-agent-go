# Code Review Report

- Summary: 1 high-confidence findings and 1 human-review items detected.
- Findings: 1
- Needs human review: 1

## Findings

### [medium] Error result is ignored

- File: `dup/dup.go:4`
- Rule: `ERR001`
- Evidence: `_ = writeSomething()`
- Recommendation: Handle or return the error so failures are observable.

## Needs Human Review

### [medium] Changed Go code without nearby test changes

- File: `dup/dup.go:3`
- Rule: `TEST001`
