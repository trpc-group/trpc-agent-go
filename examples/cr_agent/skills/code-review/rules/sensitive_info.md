# Sensitive Information Leak Rules

Category: `sensitive_leak` (`CategorySensitiveLeak`)

These rules detect accidental exposure of credentials, tokens, and personally
identifiable information through logs, error messages, metrics, or hardcoded
values. Leaked secrets can be harvested from log aggregators and crash reports;
leaked PII can violate privacy regulations.

---

## SENS-001: Credentials in log output

- **Rule ID**: `SENS-001`
- **Severity**: `high`
- **Description**: Logging passwords, API keys, bearer tokens, or session
  identifiers writes them to durable storage that may be accessible to
  operators, log aggregators, or attackers with log access. Even debug-level
  logs are frequently captured in production.
- **Detection patterns**:
  - `log.Printf` / `logrus` / `zap` / `slog` calls that interpolate variables
    named `password`, `passwd`, `token`, `apiKey`, `secret`, `accessToken`,
    `sessionID`, or `authorization`.
  - Structured logging that passes a credential field without redaction (for
    example `slog.String("token", token)`).
  - Error messages constructed with `fmt.Errorf` that embed a credential and
    are later logged or returned to callers.
  - HTTP middleware logging full request headers that include `Authorization`.
- **Incorrect example**:

  ```go
  func login(user, password string) error {
      log.Printf("attempting login user=%s password=%s", user, password)
      return auth.Verify(user, password)
  }

  func callAPI(req *http.Request) error {
      log.Printf("sending request with token=%s", req.Header.Get("Authorization"))
      return do(req)
  }
  ```

- **Correct example**:

  ```go
  func login(user, password string) error {
      log.Printf("attempting login user=%s", user)
      return auth.Verify(user, password)
  }

  func callAPI(req *http.Request) error {
      log.Printf("sending request to %s", req.URL.String())
      return do(req)
  }
  ```

- **Fix recommendation**: Never log credentials. Log only non-sensitive
  identifiers such as usernames (when not themselves secret) or a truncated,
  non-reversible token prefix. When logging request headers, redact
  `Authorization`, `Cookie`, and `Set-Cookie`. Add a redaction layer in logging
  middleware to defend against accidental leaks.

---

## SENS-002: Hardcoded credentials

- **Rule ID**: `SENS-002`
- **Severity**: `high`
- **Description**: Credentials embedded directly in source become part of the
  repository history and are visible to every reader. This overlaps with
  `SEC-003` but is tracked under the sensitive-leak category when the primary
  concern is disclosure rather than injection.
- **Detection patterns**:
  - String literals assigned to identifiers named `password`, `token`,
    `secret`, `apiKey`, `accessKey`, `privateKey`.
  - DSNs or URLs containing an embedded password (`postgres://user:pass@host`).
  - Connection strings in YAML/JSON config committed to the repo.
  - Default credentials in test fixtures that match a production format.
- **Incorrect example**:

  ```go
  const dbDSN = "postgres://admin:s3cret@db.internal:5432/app"

  func OpenDB() (*sql.DB, error) {
      return sql.Open("postgres", dbDSN)
  }
  ```

- **Correct example**:

  ```go
  func OpenDB() (*sql.DB, error) {
      dsn := os.Getenv("DATABASE_DSN")
      if dsn == "" {
          return nil, errors.New("DATABASE_DSN is not set")
      }
      return sql.Open("postgres", dsn)
  }
  ```

- **Fix recommendation**: Move credentials out of source into environment
  variables, a secrets manager, or runtime-injected config. If a credential was
  committed, rotate it immediately. Use placeholder values in committed
  examples and document the required environment variables.

---

## SENS-003: PII in logs or errors

- **Rule ID**: `SENS-003`
- **Severity**: `medium`
- **Description**: Personally identifiable information such as email addresses,
  phone numbers, national IDs, or full names written to logs or error messages
  can violate privacy policies and regulations. Errors returned to callers may
  propagate into shared logs.
- **Detection patterns**:
  - Logging full user records, contact details, or addresses.
  - `fmt.Errorf` messages that embed an email, phone, or ID number.
  - Debug logs that dump request bodies containing form fields like
    `email`, `phone`, `ssn`, or `cardNumber`.
  - Error responses that echo sensitive input back to the client.
- **Incorrect example**:

  ```go
  func lookup(email string) (*User, error) {
      u, err := db.FindByEmail(email)
      if err != nil {
          return nil, fmt.Errorf("lookup failed for %s: %w", email, err)
      }
      log.Printf("found user email=%s phone=%s", u.Email, u.Phone)
      return u, nil
  }
  ```

- **Correct example**:

  ```go
  func lookup(email string) (*User, error) {
      u, err := db.FindByEmail(email)
      if err != nil {
          return nil, fmt.Errorf("lookup failed: %w", err)
      }
      log.Printf("found user id=%s", u.ID)
      return u, nil
  }
  ```

- **Fix recommendation**: Log stable, non-PII identifiers (user IDs, opaque
  references) instead of direct identifiers. Keep error messages free of user
  input; wrap with operational context only. Apply a logging redactor for known
  PII field names so accidental leaks are scrubbed before persistence.
