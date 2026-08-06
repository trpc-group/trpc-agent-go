# Goroutine and Context Leak Rules

Category: `goroutine_leak` (`CategoryGoroutineLeak`)

These rules detect goroutines that can run forever, contexts that are never
cancelled, and channel operations that can block indefinitely. Leaked
goroutines hold memory and goroutine slots, keep references alive, and can
silently consume CPU or block shutdown.

---

## GOR-001: Context never cancelled

- **Rule ID**: `GOR-001`
- **Severity**: `high`
- **Description**: A `context.Context` created with `context.Background()` or
  `context.TODO()` and stored on a long-lived object or passed to background
  work has no cancellation path. When the owning component shuts down, the
  work cannot be stopped and the goroutine leaks.
- **Detection patterns**:
  - Structs storing a `context.Context` field initialised from
    `context.Background()`.
  - `context.WithCancel` or `context.WithTimeout` whose `cancel` function is
    never called (detected by `go vet`'s `lostcancel` analyser and confirmed
    by inspection).
  - Background workers started with `ctx := context.Background()` and no
    explicit stop channel or `cancel`.
- **Incorrect example**:

  ```go
  type Worker struct {
      ctx context.Context
  }

  func NewWorker() *Worker {
      return &Worker{ctx: context.Background()}
  }

  func (w *Worker) Start() {
      go func() {
          for {
              select {
              case <-time.After(time.Second):
                  w.tick()
              }
          }
      }()
  }
  ```

- **Correct example**:

  ```go
  type Worker struct {
      ctx    context.Context
      cancel context.CancelFunc
  }

  func NewWorker() *Worker {
      ctx, cancel := context.WithCancel(context.Background())
      return &Worker{ctx: ctx, cancel: cancel}
  }

  func (w *Worker) Start() {
      go func() {
          for {
              select {
              case <-w.ctx.Done():
                  return
              case <-time.After(time.Second):
                  w.tick()
              }
          }
      }()
  }

  func (w *Worker) Stop() {
      w.cancel()
  }
  ```

- **Fix recommendation**: Give every long-lived worker a cancellable context
  and expose a `Stop`/`Close` method that calls `cancel`. Derive worker
  contexts from the caller's context where the lifetime is bounded by a
  request. Ensure `go vet` passes with no `lostcancel` reports.

---

## GOR-002: Goroutine with no exit condition

- **Rule ID**: `GOR-002`
- **Severity**: `high`
- **Description**: A goroutine whose loop has no branch that returns or breaks
  will run for the process lifetime even after its creator has gone away. The
  classic shape is `for { select { case <-time.After(...): ... } }` with no
  `ctx.Done()` or stop channel case.
- **Detection patterns**:
  - `go func() { for { ... } }()` with no `return`, `break`, or `ctx.Done()`
    inside the loop.
  - `for { select { case <-time.After(...): ... } }` missing a done case.
  - Goroutines started in a loop without per-iteration cancellation or a
    shared done channel.
- **Incorrect example**:

  ```go
  func (w *Worker) Start() {
      go func() {
          for {
              select {
              case <-time.After(5 * time.Second):
                  w.sendHeartbeat()
              }
          }
      }()
  }
  ```

- **Correct example**:

  ```go
  func (w *Worker) Start() {
      go func() {
          ticker := time.NewTicker(5 * time.Second)
          defer ticker.Stop()
          for {
              select {
              case <-w.ctx.Done():
                  return
              case <-ticker.C:
                  w.sendHeartbeat()
              }
          }
      }()
  }
  ```

- **Fix recommendation**: Add a done case (`<-ctx.Done()` or `<-stop`) that
  exits the loop. Prefer `time.Ticker` over repeated `time.After` to avoid
  allocating a timer per iteration. Document the goroutine's lifetime and the
  caller responsible for stopping it.

---

## GOR-003: Blocking send without select

- **Rule ID**: `GOR-003`
- **Severity**: `medium`
- **Description**: A send on an unbuffered or full channel with no `select` and
  no done case blocks forever if the receiver has stopped draining. This is a
  frequent cause of leaks in producer/consumer code when the consumer exits
  early.
- **Detection patterns**:
  - `ch <- value` inside a goroutine with no surrounding `select` that also
    listens on a done channel.
  - Sends on a channel whose receiver can return early on error without
    draining remaining values.
  - `ch <- struct{}{}` heartbeat sends where the receiver may have exited.
- **Incorrect example**:

  ```go
  func (w *Worker) sendHeartbeat() {
      w.heartbeatCh <- struct{}{} // blocks if nobody is receiving
  }

  func (w *Worker) loop() {
      go func() {
          for {
              select {
              case <-time.After(time.Second):
                  w.sendHeartbeat()
              }
          }
      }()
  }
  ```

- **Correct example**:

  ```go
  func (w *Worker) sendHeartbeat() {
      select {
      case w.heartbeatCh <- struct{}{}:
      case <-w.ctx.Done():
      default:
          // receiver is slow or gone; drop the heartbeat rather than block
      }
  }
  ```

- **Fix recommendation**: Guard sends with a `select` that includes a done case
  and, where appropriate, a `default` case to drop work instead of blocking.
  For request-scoped work, cancel the context when the receiver exits so the
  producer unblocks via `ctx.Done()`.
