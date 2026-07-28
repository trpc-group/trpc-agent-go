# Code Review Report

**Task ID**: 02_security_issue
**Generated**: 2026-07-28T09:00:00Z
**Diff**: 1 file, 1 modified, 14 additions, 3 deletions

## Risk Summary

- **Total Findings**: 1
- **High/Critical**: 1
- **Need Human Review**: 0

### By Severity

- **critical**: 1

## Findings

### [critical] Potential SQL injection: string concatenation in SQL query
- **File**: `handler.go` line 6
- **Category**: security
- **Rule**: `GO_SECURITY_INJECTION`
- **Confidence**: high
- **Evidence**:
```
query := "SELECT * FROM users WHERE id = " + id
```
- **Recommendation**: Use parameterized queries or prepared statements instead of string concatenation

## Monitoring

- **Total Duration**: 8ms
- **Finding Count**: 1
- **Severity Distribution**:
  - critical: 1

## Recommendations

1. Use parameterized queries or prepared statements instead of string concatenation
