# Database Transaction and Connection Lifecycle Rules

Category: `db_lifecycle` (`CategoryDBLifecycle`)

These rules detect database transaction and connection lifecycle defects:
transactions that are never committed or rolled back, missing rollback on the
error path, prepared statements that are not closed, and connection pools that
leak through misconfiguration. These bugs cause data inconsistency, lock
contention, and pool exhaustion under load.

---

## DB-001: Transaction not committed or rolled back

- **Rule ID**: `DB-001`
- **Severity**: `high`
- **Status**: Enforced
- **Description**: A transaction begun with `db.Begin` (or `db.BeginTx`) that
  reaches a `return` without calling `tx.Commit()` or `tx.Rollback()` leaves
  the transaction open. The database holds locks and the connection stays
  checked out until it times out, which can stall other transactions and
  exhaust the pool.
- **Detection patterns**:
  - `tx, err := db.Begin()` followed by one or more `return` statements that
    do not call `tx.Rollback()` or `tx.Commit()`.
  - `defer tx.Rollback()` missing on a transaction that is later committed
    (the rollback is a safety net and is a no-op after commit).
  - Error returns inside a transaction block that exit without rollback.
- **Incorrect example**:

  ```go
  func TransferFunds(db *sql.DB, from, to int64, amount float64) error {
      tx, err := db.Begin()
      if err != nil {
          return fmt.Errorf("begin tx: %w", err)
      }
      // Debit the source account.
      if _, err := tx.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amount, from); err != nil {
          return fmt.Errorf("debit: %w", err) // leaks the transaction
      }
      // Credit the destination account.
      if _, err := tx.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amount, to); err != nil {
          return fmt.Errorf("credit: %w", err) // leaks the transaction
      }
      if err := tx.Commit(); err != nil {
          return fmt.Errorf("commit: %w", err)
      }
      return nil
  }
  ```

- **Correct example**:

  ```go
  func TransferFunds(db *sql.DB, from, to int64, amount float64) error {
      tx, err := db.Begin()
      if err != nil {
          return fmt.Errorf("begin tx: %w", err)
      }
      defer tx.Rollback() // safe no-op after Commit

      if _, err := tx.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amount, from); err != nil {
          return fmt.Errorf("debit: %w", err)
      }
      if _, err := tx.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amount, to); err != nil {
          return fmt.Errorf("credit: %w", err)
      }
      if err := tx.Commit(); err != nil {
          return fmt.Errorf("commit: %w", err)
      }
      return nil
  }
  ```

- **Fix recommendation**: Immediately after `db.Begin`, add
  `defer tx.Rollback()`. The deferred rollback is a no-op once `Commit`
  succeeds, so it is safe to always include it. Commit exactly once on the
  success path and let every error return trigger the deferred rollback.

---

## DB-002: Missing rollback on error path

- **Rule ID**: `DB-002`
- **Severity**: `high`
- **Status**: Guidance (not yet enforced by the rule engine)
- **Description**: Even when a transaction is committed on the happy path,
  returning on an error without rolling back (and without a deferred rollback)
  leaves the transaction open. This often appears when developers add an early
  validation return after `Begin` but before the deferred rollback was added.
- **Detection patterns**:
  - `tx, err := db.Begin()` with no `defer tx.Rollback()` and at least one
    `return` between `Begin` and `Commit`.
  - `return err` inside a transaction scope with no preceding rollback.
  - `panic` inside a transaction without a `recover` that rolls back.
- **Incorrect example**:

  ```go
  func CreateOrder(db *sql.DB, o Order) error {
      tx, err := db.Begin()
      if err != nil {
          return err
      }
      if o.Amount <= 0 {
          return errors.New("invalid amount") // no rollback
      }
      if _, err := tx.Exec("INSERT INTO orders (...) VALUES (...)", ...); err != nil {
          return err // no rollback
      }
      return tx.Commit()
  }
  ```

- **Correct example**:

  ```go
  func CreateOrder(db *sql.DB, o Order) error {
      tx, err := db.Begin()
      if err != nil {
          return err
      }
      defer tx.Rollback()

      if o.Amount <= 0 {
          return errors.New("invalid amount")
      }
      if _, err := tx.Exec("INSERT INTO orders (...) VALUES (...)", ...); err != nil {
          return err
      }
      return tx.Commit()
  }
  ```

- **Fix recommendation**: Always pair `db.Begin` with `defer tx.Rollback()`
  so every exit path is covered. Perform validation before `Begin` when
  possible so you do not open a transaction you immediately abandon.

---

## DB-003: Connection pool leak

- **Rule ID**: `DB-003`
- **Severity**: `high`
- **Status**: Guidance (not yet enforced by the rule engine)
- **Description**: A `*sql.DB` opened with `sql.Open` that is never closed
  leaks its connections for the process lifetime. This most often affects
  short-lived commands, test helpers, and constructors called in loops. Pool
  exhaustion also results from connections checked out and never returned
  (for example an un-closed `*sql.Rows` inside a transaction).
- **Detection patterns**:
  - `sql.Open(...)` in a function that does not `defer db.Close()` and is not
    a long-lived constructor.
  - `sql.Open` in a loop or per-request handler without a corresponding close.
  - `db.SetMaxOpenConns` set very high or left unlimited with no
    `SetConnMaxLifetime`, causing stale connections to accumulate.
  - `*sql.Rows` or `*sql.Stmt` opened inside a transaction and not closed.
- **Incorrect example**:

  ```go
  func queryOnce(dsn, q string) (string, error) {
      db, err := sql.Open("postgres", dsn)
      if err != nil {
          return "", err
      }
      // no defer db.Close() -- leaks a pool per call
      var result string
      err = db.QueryRow(q).Scan(&result)
      return result, err
  }
  ```

- **Correct example**:

  ```go
  func queryOnce(dsn, q string) (string, error) {
      db, err := sql.Open("postgres", dsn)
      if err != nil {
          return "", err
      }
      defer db.Close()

      var result string
      err = db.QueryRow(q).Scan(&result)
      return result, err
  }

  // For a long-lived pool, configure limits:
  //   db.SetMaxOpenConns(25)
  //   db.SetMaxIdleConns(25)
  //   db.SetConnMaxLifetime(5 * time.Minute)
  ```

- **Fix recommendation**: Open the `*sql.DB` once at application startup and
  share it; call `Close` only during shutdown. For short-lived uses, always
  `defer db.Close()`. Configure `SetMaxOpenConns`, `SetMaxIdleConns`, and
  `SetConnMaxLifetime` to bound pool growth and recycle stale connections.
  Ensure every `*sql.Rows` and `*sql.Stmt` is closed (see `RES-001`).

---

## DB-004: Prepared statement not closed

- **Rule ID**: `DB-004`
- **Severity**: `medium`
- **Status**: Guidance (not yet enforced by the rule engine)
- **Description**: A `*sql.Stmt` created with `db.Prepare` (or `tx.Prepare`)
  holds database resources on both the client and server side. Failing to
  close it leaks a statement handle per call; on the transaction side, the
  statement is released when the transaction ends but the Go-side handle still
  should be closed promptly.
- **Detection patterns**:
  - `stmt, err := db.Prepare(...)` without `defer stmt.Close()`.
  - `tx.Prepare(...)` used in a loop without closing each statement.
  - A `*sql.Stmt` stored in a struct with no `Close` on the owning type's
    shutdown path.
- **Incorrect example**:

  ```go
  func insertBatch(db *sql.DB, rows []Row) error {
      for _, r := range rows {
          stmt, err := db.Prepare("INSERT INTO t (a, b) VALUES (?, ?)")
          if err != nil {
              return err
          }
          if _, err := stmt.Exec(r.A, r.B); err != nil {
              return err // leaks stmt
          }
      }
      return nil
  }
  ```

- **Correct example**:

  ```go
  func insertBatch(db *sql.DB, rows []Row) error {
      stmt, err := db.Prepare("INSERT INTO t (a, b) VALUES (?, ?)")
      if err != nil {
          return err
      }
      defer stmt.Close()
      for _, r := range rows {
          if _, err := stmt.Exec(r.A, r.B); err != nil {
              return err
          }
      }
      return nil
  }
  ```

- **Fix recommendation**: Prepare once outside the loop and reuse the
  statement. Always `defer stmt.Close()` right after the prepare-error check.
  When storing a `*sql.Stmt` on a long-lived type, close it in that type's
  `Close` method.
