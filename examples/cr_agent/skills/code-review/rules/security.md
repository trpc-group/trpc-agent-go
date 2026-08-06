# Security Risk Rules

Category: `security` (`CategorySecurity`)

These rules detect security-sensitive defects in Go code, including injection
vulnerabilities, hardcoded credentials, and misuse of cryptographic primitives.
Findings in this category default to `critical` or `high` severity because they
can lead to data exfiltration, privilege escalation, or service compromise.

---

## SEC-001: SQL injection

- **Rule ID**: `SEC-001`
- **Severity**: `critical`
- **Description**: SQL statements built by concatenating or formatting
  untrusted input allow an attacker to alter query semantics. Even read-only
  queries can leak data or bypass filters. The risk applies to `database/sql`,
  query builders, and raw drivers alike.
- **Detection patterns**:
  - String concatenation (`+`) or `fmt.Sprintf` used to assemble a SQL query
    that incorporates a function parameter, struct field, or external value.
  - `db.Query` / `db.Exec` / `db.QueryRow` called with a single assembled
    string instead of placeholders (`?`, `$1`) and arguments.
  - `LIKE` clauses built with user input where wildcards are not escaped.
- **Incorrect example**:

  ```go
  func GetUserByName(db *sql.DB, name string) (*User, error) {
      query := "SELECT id, name FROM users WHERE name = '" + name + "'"
      row := db.QueryRow(query)
      var u User
      if err := row.Scan(&u.ID, &u.Name); err != nil {
          return nil, err
      }
      return &u, nil
  }
  ```

- **Correct example**:

  ```go
  func GetUserByName(db *sql.DB, name string) (*User, error) {
      query := "SELECT id, name FROM users WHERE name = ?"
      row := db.QueryRow(query, name)
      var u User
      if err := row.Scan(&u.ID, &u.Name); err != nil {
          return nil, err
      }
      return &u, nil
  }
  ```

- **Fix recommendation**: Use parameterized queries with placeholders for every
  value derived from input. When dynamic identifiers (table or column names)
  are unavoidable, validate them against an allowlist rather than interpolating
  raw input. For `LIKE` patterns, escape user-supplied wildcards before
  binding.

---

## SEC-002: Command injection

- **Rule ID**: `SEC-002`
- **Severity**: `critical`
- **Description**: Shell commands constructed with user input allow arbitrary
  command execution. `exec.Command` invoked through a shell (`sh -c`) with
  interpolated arguments is the most common vector.
- **Detection patterns**:
  - `exec.Command("sh", "-c", fmt.Sprintf(...))` or `exec.Command("bash", "-c", ...)`
    with input embedded in the command string.
  - `os/exec` calls where the command string is assembled from request
    parameters, environment-derived values, or file contents.
  - `cmd := exec.Command(name); cmd.Args = append(cmd.Args, userInput)` when
    `name` itself comes from input.
- **Incorrect example**:

  ```go
  func runLint(repo string) ([]byte, error) {
      cmd := exec.Command("sh", "-c", "golangci-lint run "+repo)
      return cmd.Output()
  }
  ```

- **Correct example**:

  ```go
  func runLint(repo string) ([]byte, error) {
      cmd := exec.Command("golangci-lint", "run", repo)
      return cmd.Output()
  }
  ```

- **Fix recommendation**: Avoid invoking a shell. Pass the program name and each
  argument as separate `exec.Command` arguments so the kernel handles argument
  boundaries. Validate or sanitize any value used as the program name against an
  allowlist.

---

## SEC-003: Hardcoded secrets

- **Rule ID**: `SEC-003`
- **Severity**: `high`
- **Description**: API keys, passwords, private keys, and tokens committed to
  source are exposed to anyone with repository access and cannot be rotated
  without a code change. Secrets in `const`, `var`, struct literals, and
  string literals are all in scope.
- **Detection patterns**:
  - Identifiers named `apiKey`, `password`, `secret`, `token`, `privateKey`,
    `accessKey` assigned a string literal.
  - `const` blocks grouping credential-like names with literal values.
  - URLs containing embedded credentials (`https://user:pass@host`).
  - String literals matching common secret formats (for example `sk-live-...`,
    `AKIA...`, `ghp_...`, `eyJ...` JWTs).
- **Incorrect example**:

  ```go
  const (
      apiKey = "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
  )

  func NewClient() *Client {
      return &Client{key: apiKey}
  }
  ```

- **Correct example**:

  ```go
  func NewClient() (*Client, error) {
      key := os.Getenv("PAYMENT_API_KEY")
      if key == "" {
          return nil, errors.New("PAYMENT_API_KEY is not set")
      }
      return &Client{key: key}, nil
  }
  ```

- **Fix recommendation**: Load secrets from environment variables, a secrets
  manager, or a mounted file at runtime. If a secret was committed, rotate it
  immediately and purge it from history where feasible.

---

## SEC-004: Insecure cryptography

- **Rule ID**: `SEC-004`
- **Severity**: `high`
- **Description**: Weak or misused cryptography undermines confidentiality and
  integrity. Common issues include MD5/SHA1 for security purposes, ECB mode,
  hardcoded IVs, `math/rand` for security tokens, and custom crypto.
- **Detection patterns**:
  - `md5.New`, `sha1.New`, or `crypto/md5` / `crypto/sha1` used for password
    hashing, signing, or integrity checks.
  - `des.NewCipher`, `aes.NewCipher` used directly without a secure mode
    (`cipher.NewGCM`) or with a static IV.
  - `math/rand` used to generate tokens, nonces, or keys instead of
    `crypto/rand`.
  - `base64` used as an "encryption" layer.
- **Incorrect example**:

  ```go
  func token() string {
      b := make([]byte, 16)
      rand.Read(b) // math/rand, not crypto/rand
      return hex.EncodeToString(b)
  }

  func hashPassword(pw string) string {
      sum := md5.Sum([]byte(pw))
      return hex.EncodeToString(sum[:])
  }
  ```

- **Correct example**:

  ```go
  func token() (string, error) {
      b := make([]byte, 16)
      if _, err := crand.Read(b); err != nil {
          return "", err
      }
      return hex.EncodeToString(b), nil
  }

  func hashPassword(pw string) (string, error) {
      b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
      return string(b), err
  }
  ```

- **Fix recommendation**: Use `crypto/rand` for all randomness that must be
  unpredictable. Use `crypto/sha256` or stronger for integrity; use
  authenticated encryption such as AES-GCM with a random nonce; use a
  password-specific algorithm such as bcrypt, scrypt, or argon2 for password
  storage. Never reuse a nonce with the same key.
