# Resource Closure Rules

Category: `resource_close` (`CategoryResourceClose`)

These rules detect resources that are opened but never closed, or closed too
late because `defer` is missing. Leaked file handles, HTTP bodies, sockets, and
database rows exhaust file descriptors or connections and can cause
production failures under load.

---

## RES-001: Missing defer Close

- **Rule ID**: `RES-001`
- **Severity**: `high`
- **Description**: A resource with a `Close` method that is opened but not
  followed by `defer close` will leak if any subsequent code returns early,
  including on the error path. Even when a manual `Close` exists at the end of
  the function, any `return` between open and close leaks the handle.
- **Detection patterns**:
  - `os.Open` / `os.Create` / `os.OpenFile` without a matching `defer f.Close()`
    on the line(s) immediately after the error check.
  - `http.Client.Do` / `http.Get` / `http.Post` where `resp.Body` is not
    closed with `defer resp.Body.Close()`.
  - `sql.DB.Query` returning `*sql.Rows` without `defer rows.Close()`.
  - `net.Dial` / `net.Listen` without `defer conn.Close()` / `defer ln.Close()`.
  - `ioutil.TempFile` / `os.CreateTemp` without `defer f.Close()` (and, for
    temp files, cleanup).
- **Incorrect example**:

  ```go
  func ReadConfig(path string) ([]byte, error) {
      f, err := os.Open(path)
      if err != nil {
          return nil, err
      }
      // TODO: add defer f.Close() here.
      data, err := io.ReadAll(f)
      if err != nil {
          return nil, err // leaks the file handle
      }
      return data, nil
  }
  ```

- **Correct example**:

  ```go
  func ReadConfig(path string) ([]byte, error) {
      f, err := os.Open(path)
      if err != nil {
          return nil, err
      }
      defer f.Close()
      data, err := io.ReadAll(f)
      if err != nil {
          return nil, err
      }
      return data, nil
  }
  ```

- **Fix recommendation**: Immediately after the open-error check, add
  `defer f.Close()`. Place the `defer` before any code that can `return` so all
  paths are covered. For `*sql.Rows`, `defer rows.Close()` is mandatory even
  when you iterate to completion.

---

## RES-002: HTTP response body never closed

- **Rule ID**: `RES-002`
- **Severity**: `high`
- **Description**: Failing to close an HTTP response body leaks the underlying
  connection and prevents connection reuse, which exhausts the connection pool
  and can stall a service. This is one of the most common Go resource leaks.
- **Detection patterns**:
  - `resp, err := client.Do(req)` (or `http.Get`/`http.Post`) without a
    following `defer resp.Body.Close()`.
  - Early returns that read part of the body then return without closing.
  - Code that checks `err != nil` and returns before closing the body when the
    `http.Client` still populated a body to drain.
- **Incorrect example**:

  ```go
  func fetch(url string) ([]byte, error) {
      resp, err := http.Get(url)
      if err != nil {
          return nil, err
      }
      return io.ReadAll(resp.Body)
  }
  ```

- **Correct example**:

  ```go
  func fetch(url string) ([]byte, error) {
      resp, err := http.Get(url)
      if err != nil {
          return nil, err
      }
      defer resp.Body.Close()
      return io.ReadAll(resp.Body)
  }
  ```

- **Fix recommendation**: Always `defer resp.Body.Close()` right after the
  error check, even when you intend to stream the body. Read the body to EOF
  before returning on error so the connection can be reused; use
  `io.Copy(io.Discard, resp.Body)` when you must discard it.

---

## RES-003: Database rows never closed

- **Rule ID**: `RES-003`
- **Severity**: `high`
- **Description**: `*sql.Rows` holds a database connection until it is closed.
  Forgetting `rows.Close()` (or returning early before `rows.Next()` returns
  `false`) keeps the connection checked out and can exhaust the pool.
- **Detection patterns**:
  - `rows, err := db.Query(...)` without `defer rows.Close()`.
  - A `for rows.Next()` loop that `break`s or `return`s without ensuring
    `rows.Close()` runs (mitigated only by `defer`).
  - `rows.Err()` checked but `rows.Close()` still missing.
- **Incorrect example**:

  ```go
  func ListUsers(db *sql.DB) ([]User, error) {
      rows, err := db.Query("SELECT id, name FROM users")
      if err != nil {
          return nil, err
      }
      var users []User
      for rows.Next() {
          var u User
          if err := rows.Scan(&u.ID, &u.Name); err != nil {
              return nil, err // leaks the connection
          }
          users = append(users, u)
      }
      return users, nil
  }
  ```

- **Correct example**:

  ```go
  func ListUsers(db *sql.DB) ([]User, error) {
      rows, err := db.Query("SELECT id, name FROM users")
      if err != nil {
          return nil, err
      }
      defer rows.Close()
      var users []User
      for rows.Next() {
          var u User
          if err := rows.Scan(&u.ID, &u.Name); err != nil {
              return nil, err
          }
          users = append(users, u)
      }
      return users, rows.Err()
  }
  ```

- **Fix recommendation**: Add `defer rows.Close()` immediately after the query
  error check. Always check `rows.Err()` after the loop to surface iteration
  errors.
