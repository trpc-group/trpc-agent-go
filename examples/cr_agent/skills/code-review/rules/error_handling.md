# Error Handling Rules

Category: `error_handling` (`CategoryErrorHandling`)

These rules detect mishandled errors: return values that are silently dropped,
errors that are overwritten or shadowed, and errors that lose their cause when
returned. Mishandled errors hide bugs, break observability, and make failures
impossible to diagnose in production.

---

## ERR-001: Ignored error return value

- **Rule ID**: `ERR-001`
- **Severity**: `high`
- **Description**: A function that returns an `error` whose result is discarded
  cannot signal failure to the caller. The most common form is calling a
  function for its side effects and ignoring the error, which leaves the
  program in an undefined state when the operation fails.
- **Detection patterns**:
  - Calls to functions returning `error` whose result is not assigned or
    checked (for example `ValidateConfig(cfg)` when `ValidateConfig` returns
    `error`).
  - `fmt.Fprint*` / `io.Writer.Write` results discarded in non-trivial paths.
  - `rows.Err()` not checked after a `for rows.Next()` loop.
  - `defer f.Close()` without checking the error when the close error matters
    (for example on writes).
- **Incorrect example**:

  ```go
  func LoadConfig(path string) error {
      cfg, err := readYAML(path)
      if err != nil {
          return fmt.Errorf("read config: %w", err)
      }

      // Validate the parsed configuration.
      ValidateConfig(cfg) // error return value ignored

      return nil
  }
  ```

- **Correct example**:

  ```go
  func LoadConfig(path string) error {
      cfg, err := readYAML(path)
      if err != nil {
          return fmt.Errorf("read config: %w", err)
      }
      if err := ValidateConfig(cfg); err != nil {
          return fmt.Errorf("validate config: %w", err)
      }
      return nil
  }
  ```

- **Fix recommendation**: Check every `error` return value. When the error is
  actionable, wrap it with context using `%w` and return it. When the error is
  genuinely safe to ignore (for example `defer f.Close()` on a read-only
  file), add a comment explaining why so reviewers and `errcheck` understand
  the intent.

---

## ERR-002: Error shadowing

- **Rule ID**: `ERR-002`
- **Severity**: `medium`
- **Description**: Declaring a new `err` with `:=` inside an inner scope (often
  an `if` init or a `for` loop) shadows an outer `err`, so the outer error is
  never observed. This can cause a function to return `nil` even though an
  earlier operation failed.
- **Detection patterns**:
  - `if err := ...; err != nil { ... }` followed by a later `return err` that
    refers to an outer `err` declared before the `if`.
  - `for ... { if err := ...; err != nil { continue } }` where a post-loop
    `err` check reads a different (outer) `err`.
  - `:=` assignments that reintroduce `err` in a block where the caller expects
    the outer `err` to be updated.
- **Incorrect example**:

  ```go
  func process(items []Item) error {
      var err error
      for _, it := range items {
          if err := transform(it); err != nil {
              log.Println(err) // inner err, does not set outer err
          }
      }
      return err // always nil
  }
  ```

- **Correct example**:

  ```go
  func process(items []Item) error {
      for _, it := range items {
          if err := transform(it); err != nil {
              return fmt.Errorf("transform %s: %w", it.Name, err)
          }
      }
      return nil
  }
  ```

- **Fix recommendation**: Avoid reusing the name `err` in a way that shadows
  an outer variable you intend to return. Either return immediately on error
  or assign explicitly to the outer variable with `=` instead of `:=`. Run
  `go vet` and `shadow` analyser to catch shadowing automatically.

---

## ERR-003: Unwrapped error

- **Rule ID**: `ERR-003`
- **Severity**: `medium`
- **Description**: Returning an error directly (or creating a new error with
  `errors.New`/`fmt.Errorf` without `%w`) discards the original cause, so
  callers cannot use `errors.Is` / `errors.As` to inspect it. This breaks
  error-based control flow and observability.
- **Detection patterns**:
  - `return errors.New("...")` in a branch that received a concrete `err`,
    dropping `err` entirely.
  - `fmt.Errorf("...: %v", err)` where `%v` should be `%w`.
  - `return fmt.Errorf("...")` after logging `err` without including it in the
    returned error.
- **Incorrect example**:

  ```go
  func Open(path string) (*Config, error) {
      data, err := os.ReadFile(path)
      if err != nil {
          return nil, errors.New("failed to open config")
      }
      return parse(data)
  }
  ```

- **Correct example**:

  ```go
  func Open(path string) (*Config, error) {
      data, err := os.ReadFile(path)
      if err != nil {
          return nil, fmt.Errorf("read config %s: %w", path, err)
      }
      return parse(data)
  }
  ```

- **Fix recommendation**: Wrap errors with `%w` when the caller may need to
  inspect the cause. Use `%v` only when the underlying error must not be
  unwrapped (for example to avoid leaking internal details across a trust
  boundary). Preserve the cause through every layer so `errors.Is` and
  `errors.As` work end to end.
