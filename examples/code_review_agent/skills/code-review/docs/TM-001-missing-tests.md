# TM-001 — Missing Tests

| Field | Value |
| --- | --- |
| **RuleID** | TM-001 |
| **Severity** | Low |
| **Category** | Quality |
| **Confidence** | 0.7 |

## Description

Flags new non-test Go source files (e.g. `foo.go`) added in a diff. The
evaluator reports a finding for every newly added non-test `.go` file
without checking whether a companion `_test.go` exists. This serves as a
reminder to pair new source files with tests.

> **Scope note**: The evaluator does **not** check for the existence of a
> companion `_test.go` file, nor does it implement exclusions for `main.go`,
> `doc.go`, generated code, or glue packages. All non-test `.go` files are
> flagged uniformly.

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

- The evaluator flags **all** new non-test `.go` files, including `main.go`,
  `doc.go`, and generated code (`*.pb.go`). Suppress with
  `// code-review:ignore TM-001` if the file does not require a companion
  test.
- Glue packages that only wire dependencies may legitimately have integration
  tests elsewhere; suppress with a comment if coverage exists.
