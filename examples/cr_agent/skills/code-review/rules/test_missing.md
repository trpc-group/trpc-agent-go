# Missing Test Rules

Category: `missing_test` (`CategoryMissingTest`)

These rules flag substantive code changes that are not accompanied by new or
updated tests. Untested changes regress silently and erode the safety net that
lets the project refactor with confidence. Findings here are usually
`medium` severity: they signal risk rather than a runtime defect.

---

## TEST-001: New exported function without tests

- **Rule ID**: `TEST-001`
- **Severity**: `medium`
- **Description**: A newly added exported function or method is part of the
  public API contract and should be exercised by at least one test. Without a
  test, regressions in the function will not be caught by CI and the contract
  is undocumented by example.
- **Detection patterns**:
  - A diff that adds an exported (`TitleCase`) function or method in a non-`main`
    package without a corresponding addition to a `_test.go` file in the same
    package.
  - New exported types with constructors or methods that have no test coverage
    in the same package's test file.
  - Exported functions added to `main` packages that perform reusable logic
    better tested from a library package.
- **Incorrect example**:

  ```go
  // config.go
  package config

  // ParseEnv reads configuration from the process environment.
  func ParseEnv(prefix string) (*Config, error) {
      // ... new, untested logic ...
  }
  ```

  No `config_test.go` is added or updated.

- **Correct example**:

  ```go
  // config_test.go
  package config

  func TestParseEnv(t *testing.T) {
      t.Setenv("APP_HOST", "example.com")
      t.Setenv("APP_PORT", "8080")

      cfg, err := ParseEnv("APP_")
      if err != nil {
          t.Fatalf("ParseEnv failed: %v", err)
      }
      if cfg.Host != "example.com" || cfg.Port != 8080 {
          t.Errorf("got %+v, want host=example.com port=8080", cfg)
      }
  }
  ```

- **Fix recommendation**: Add a `_test.go` file in the same package that covers
  the happy path and at least one error or boundary condition. Prefer
  table-driven tests for functions with several input shapes. If the function
  is trivial (a getter or constant), note that in the review so the finding
  can be dismissed with justification.

---

## TEST-002: Logic change without test update

- **Rule ID**: `TEST-002`
- **Severity**: `medium`
- **Description**: A diff that changes the behaviour of existing logic (a new
  branch, a changed constant, a different error condition) without updating or
  adding tests means the new behaviour is unverified. The existing test suite
  may still pass while no longer describing the intended contract.
- **Detection patterns**:
  - Modified conditionals, loops, or return values in a function covered by
    tests, where the test file is not touched in the same diff.
  - A changed error message or sentinel without a test asserting the new
    message or `errors.Is` relationship.
  - A new code path (for example an additional `case` in a `switch`) with no
    test exercising it.
  - A modified default value or threshold constant with no test asserting the
    new default.
- **Incorrect example**:

  ```go
  // Before
  func RetryLimit() int { return 3 }

  // After (changed, no test update)
  func RetryLimit() int { return 5 }
  ```

  The existing `TestRetryLimit` still asserts `3` and is not updated, or no
  test exists.

- **Correct example**:

  ```go
  func TestRetryLimit(t *testing.T) {
      if got := RetryLimit(); got != 5 {
          t.Errorf("RetryLimit() = %d, want 5", got)
      }
  }
  ```

- **Fix recommendation**: When changing behaviour, update the tests that assert
  the old behaviour and add cases for the new paths. When the change is a bug
  fix, add a regression test that fails against the old code and passes against
  the fix. Run `bash scripts/run_go_test.sh ./...` and confirm the suite
  reflects the new contract.

---

## TEST-003: Test file missing for new package

- **Rule ID**: `TEST-003`
- **Severity**: `low`
- **Description**: A new package with non-trivial logic but no `_test.go` file
  at all has no local test coverage. Even a single smoke test establishes the
  package's test harness and lowers the barrier for future contributions.
- **Detection patterns**:
  - A diff adding a new directory with `.go` files (other than `main`) and no
    `*_test.go` file.
  - A new package whose only "tests" live in a separate `main` example rather
    than in the package itself.
- **Incorrect example**:

  A new package `internal/ratelimit/` with `ratelimit.go` but no
  `ratelimit_test.go`.

- **Correct example**:

  Add `ratelimit_test.go` with at least a test for the constructor and the
  core `Allow` behaviour:

  ```go
  package ratelimit

  func TestLimiterAllowsUpToLimit(t *testing.T) {
      l := New(2)
      if !l.Allow() || !l.Allow() {
          t.Error("expected two allows under the limit")
      }
      if l.Allow() {
          t.Error("expected third allow to be rejected")
      }
  }
  ```

- **Fix recommendation**: Add a `*_test.go` file in the new package covering
  the primary behaviour. If the package is purely a wire/glue package with no
  testable logic, document that explicitly so the finding can be dismissed.
