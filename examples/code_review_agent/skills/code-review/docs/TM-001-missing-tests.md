# TM-001 — Missing Tests

| Field | Value |
| --- | --- |
| **RuleID** | TM-001 |
| **Severity** | Low |
| **Category** | Quality |
| **Confidence** | 0.7 |

## Description

Flags new non-test Go source files (e.g. `foo.go`) added in a diff that have
no corresponding `_test.go` file (e.g. `foo_test.go`) in the same package.
Untested code is the most common source of regressions and should be paired
with at least a basic table-driven test.

## Evidence Example

```diff
+// user.go
+package user
+
+func ValidateEmail(s string) bool {
+    return strings.Contains(s, "@")
+}
```

No `user_test.go` is added alongside `user.go`, so the new logic has no test
coverage.

## Recommendation

Add a companion test file exercising the public API:

```go
// user_test.go
package user

import "testing"

func TestValidateEmail(t *testing.T) {
    cases := []struct {
        in   string
        want bool
    }{
        {"a@b.com", true},
        {"no-at", false},
    }
    for _, c := range cases {
        if got := ValidateEmail(c.in); got != c.want {
            t.Errorf("ValidateEmail(%q) = %v, want %v", c.in, got, c.want)
        }
    }
}
```

## False Positive Notes

- `main.go` entry points and `doc.go` package documentation files rarely need
  dedicated tests; the rule excludes them by filename convention.
- Generated code (e.g. `*.pb.go`, files with `// Code generated` headers) is
  skipped because tests live alongside the generator, not the output.
- Glue packages that only wire dependencies may legitimately have integration
  tests elsewhere; suppress with a comment if coverage exists.
