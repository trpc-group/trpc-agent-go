# Go Code Review Rules

Use this catalog to generate and verify candidate findings. A candidate signal
is not sufficient by itself: confirm the required evidence and check the listed
exemptions before reporting an issue.

## Contents

- [Shared evaluation rules](#shared-evaluation-rules)
- [Correctness](#correctness)
- [Security](#security)
- [Sensitive information](#sensitive-information)
- [Concurrency and context](#concurrency-and-context)
- [Resource lifecycle](#resource-lifecycle)
- [Error handling](#error-handling)
- [Tests](#tests)
- [Database lifecycle](#database-lifecycle)

## Shared evaluation rules

- Review the behavior introduced or changed by the patch.
- Prefer direct data-flow and lifecycle evidence over keyword matches.
- Inspect callers or surrounding code when ownership or trust boundaries are
  unclear.
- Do not report a rule when an exemption is visibly satisfied.
- Lower confidence or route to human review when confirmation requires missing
  repository context, but only after visible evidence establishes a plausible
  harmful path. Do not report a changed API, ownership boundary, or lifecycle
  merely because unseen callers might use it incorrectly.
- Keep evidence small and mask any credential value.
- Use the default severity only as a starting point. Raise or lower it based on
  exploitability, reachability, data sensitivity, and operational impact.

## Correctness

### GO-COR-001 — Changed behavior violates an established contract

- Category: `correctness`
- Default severity: `high`
- Candidate signals:
  - Changed argument order, conditionals, state transitions, return values, or
    transformations contradict an API contract, nearby invariant, caller
    expectation, documentation, or existing test.
  - A changed operation succeeds but acts on the wrong value, target, unit, or
    semantic role.
- Required evidence:
  - Show the changed expression and concrete local evidence for the expected
    contract. Explain the observable incorrect outcome.
- Exemptions:
  - The apparent contract is only an assumption unsupported by available code,
    tests, documentation, or language/library semantics.
  - The behavior change is intentional and all visible callers, documentation,
    and tests consistently adopt it.
- Recommendation: Restore the established behavior or update the full contract
  and all dependent code consistently.

### GO-COR-002 — Boundary or range calculation is invalid

- Category: `correctness`
- Default severity: `medium`
- Candidate signals:
  - Changed bounds use the wrong inclusive or exclusive endpoint.
  - Index, slice, pagination, retry, batch, or length arithmetic can skip valid
    data, duplicate work, panic, or exceed the intended range.
- Required evidence:
  - Give a concrete reachable boundary input and show the resulting wrong range,
    panic, omission, or duplication.
- Exemptions:
  - Earlier validation proves the boundary input cannot occur.
  - The endpoint convention is explicit and the changed arithmetic implements it
    correctly.
- Recommendation: Use one explicit endpoint convention, validate inputs, and add
  focused boundary tests.

### GO-COR-003 — Nil, zero, or empty state is mishandled

- Category: `correctness`
- Default severity: `high`
- Candidate signals:
  - Changed code dereferences a possibly nil pointer, writes to a nil map, indexes
    an empty collection, or treats a meaningful zero value as absent or valid.
  - A newly reachable empty-input path returns an invalid result or panics.
- Required evidence:
  - Trace a reachable nil, zero, or empty value to the changed operation and show
    the incorrect outcome.
- Exemptions:
  - Construction, validation, or type invariants visibly exclude the state on
    every reachable path.
  - The Go zero value is deliberately supported by the type's contract.
- Recommendation: Validate or initialize the value at the ownership boundary and
  preserve the documented zero-value behavior.

## Security

### GO-SEC-001 — Shell command injection

- Category: `security`
- Default severity: `high`
- Candidate signals:
  - Untrusted data reaches `sh -c`, `bash -c`, PowerShell command text, or a
    similar shell interpreter.
  - A command string is assembled with concatenation or formatting and then
    executed.
- Required evidence:
  - Show the changed execution site and the data path that makes the value
    externally controllable.
- Exemptions:
  - The command text is a compile-time constant with no untrusted interpolation.
  - The code invokes a fixed executable and passes untrusted values only as
    separated arguments without a shell.
- Recommendation: Invoke the target executable directly with separated,
  validated arguments or replace command execution with a native library.

### GO-SEC-002 — Untrusted filesystem path escapes its allowed root

- Category: `security`
- Default severity: `high`
- Candidate signals:
  - User-controlled input is joined to a filesystem root and then read, written,
    extracted, or deleted.
  - Archive entry names or URL paths are converted directly into local paths.
- Required evidence:
  - Show the untrusted source, the path construction, and the sensitive file
    operation.
- Exemptions:
  - The path is cleaned and the resolved path is proven to remain beneath an
    intended root.
  - Input is selected from a closed allowlist that cannot contain separators or
    traversal segments.
- Recommendation: Resolve the path, verify containment beneath the allowed root,
  and reject absolute paths, traversal, and unsafe symlink behavior.

### GO-SEC-003 — Security validation is disabled or bypassed

- Category: `security`
- Default severity: `high`
- Candidate signals:
  - TLS certificate verification, authentication, authorization, signature
    validation, or permission checking is disabled.
  - A privileged operation becomes reachable before its guard executes.
- Required evidence:
  - Show the changed guard or configuration and the protected operation that can
    now be reached.
- Exemptions:
  - The behavior is isolated to an explicit test-only path that cannot enter a
    production build or runtime configuration.
  - An equivalent validation is performed earlier on every reachable path.
- Recommendation: Restore fail-closed validation and keep test overrides scoped
  to test code or an explicit non-production configuration.

### GO-SEC-004 — Untrusted data is interpolated into a SQL statement

- Category: `security`
- Default severity: `high`
- Candidate signals:
  - Externally controlled data is concatenated or formatted into SQL text passed
    to `Query`, `Exec`, or their context variants.
  - A dynamic identifier or clause is accepted without a closed allowlist.
- Required evidence:
  - Show the untrusted source, SQL construction, and database execution path.
- Exemptions:
  - Untrusted values are passed only through driver parameters.
  - Dynamic identifiers are selected from a closed allowlist and cannot inject
    SQL syntax.
- Recommendation: Use driver parameters for values and a closed allowlist for
  identifiers or structural clauses.

## Sensitive information

### GO-SECRET-001 — Credential or private key is embedded in source

- Category: `sensitive_info`
- Default severity: `critical`
- Candidate signals:
  - Added string content resembles an API key, access token, password, private
    key, connection credential, or signed secret.
  - A credential is assigned to a constant or default configuration value.
- Required evidence:
  - Show the credential type and location without reproducing the plaintext
    value.
- Exemptions:
  - The value is an unmistakable inert placeholder used only in test data.
  - The code reads the value from an external secret provider and contains no
    embedded secret.
- Recommendation: Revoke exposed credentials, remove them from source history,
  and load replacements from an approved secret store.

### GO-SECRET-002 — Sensitive value is exposed through logs or errors

- Category: `sensitive_info`
- Default severity: `high`
- Candidate signals:
  - Tokens, passwords, authorization headers, private keys, or full connection
    strings are formatted into logs, errors, traces, metrics, or responses.
  - A whole request, environment map, or configuration object is logged.
- Required evidence:
  - Show the sensitive value's source and the output operation that exposes it.
- Exemptions:
  - A proven sanitizer replaces the value before it reaches the output.
  - Only non-sensitive metadata such as key type or fingerprint is emitted.
- Recommendation: Remove the value or emit a masked representation with only
  the minimum diagnostic metadata.

## Concurrency and context

### GO-CONC-001 — Goroutine has no bounded exit path

- Category: `concurrency`
- Default severity: `high`
- Candidate signals:
  - A goroutine loops on a channel, timer, retry, or blocking operation without
    observing cancellation or closure.
  - A request-scoped function starts background work with an unbounded lifetime.
- Required evidence:
  - Show the goroutine creation and why every reachable blocking path can outlive
    its owner.
- Exemptions:
  - The goroutine is process-lifetime infrastructure with explicit ownership and
    shutdown elsewhere.
  - The loop has a guaranteed finite completion or observes a cancellation or
    closed-channel path.
- Recommendation: Define ownership, propagate cancellation, and ensure each
  blocking operation participates in shutdown.

### GO-CONC-002 — Derived context cancellation is not released

- Category: `concurrency`
- Default severity: `medium`
- Candidate signals:
  - `context.WithCancel`, `WithTimeout`, or `WithDeadline` returns a cancel
    function that is not called on all owner exit paths.
  - A derived context is retained beyond its intended operation.
- Required evidence:
  - Show where the derived context is created and why the owner can return
    without releasing it.
- Exemptions:
  - Ownership of the cancel function is deliberately transferred and documented.
  - A visible `defer cancel()` or equivalent cleanup covers every exit path.
- Recommendation: Call the cancel function as soon as the operation completes,
  normally with a defer placed immediately after successful construction.

### GO-CONC-003 — Shared mutable state lacks synchronization

- Category: `concurrency`
- Default severity: `high`
- Candidate signals:
  - Multiple goroutines access a map, slice, counter, pointer, or object and at
    least one access mutates it.
  - A lock is removed, narrowed, or bypassed around shared state.
- Required evidence:
  - Establish at least two concurrent access paths and identify the missing or
    inconsistent synchronization.
- Exemptions:
  - State is immutable after publication.
  - Confinement to one goroutine or an existing synchronization primitive is
    visible.
- Recommendation: Establish single ownership or protect all accesses with one
  consistent synchronization strategy, then validate with the race detector.

### GO-CONC-004 — Channel or synchronization protocol can fail

- Category: `concurrency`
- Default severity: `high`
- Candidate signals:
  - Changed code can send on or close a channel after another path closes it.
  - `WaitGroup.Add` races with `Wait`, a lock is copied after use, or a blocking
    send/receive loses its cancellation path.
- Required evidence:
  - Establish the competing paths and a reachable ordering that causes a panic,
    deadlock, race, or premature completion.
- Exemptions:
  - One visible owner serializes the protocol and every participant follows that
    ownership rule.
  - The suspected ordering is excluded by construction before concurrency begins.
- Recommendation: Assign one owner for close/state transitions and make ordering
  explicit with cancellation or synchronization that covers every path.

## Resource lifecycle

### GO-RES-001 — Acquired closer is not closed

- Category: `resource_lifecycle`
- Default severity: `high`
- Candidate signals:
  - `os.Open`, `http.Get`, `Client.Do`, or another acquisition returns an
    `io.Closer` that is not closed.
  - Cleanup exists only on the success path while earlier returns leak the
    resource.
- Required evidence:
  - Show the acquisition, ownership, and an exit path with no matching close.
- Exemptions:
  - Ownership is returned or transferred to a caller whose contract requires
    closure.
  - A visible defer or structured cleanup covers every relevant exit path.
- Recommendation: Close the resource in the owner, usually by deferring cleanup
  immediately after successful acquisition.

### GO-RES-002 — Timer, ticker, or subscription is left active

- Category: `resource_lifecycle`
- Default severity: `medium`
- Candidate signals:
  - `time.NewTicker`, `time.NewTimer`, watchers, subscriptions, or registrations
    are created without a corresponding stop, close, or unregister operation.
  - Repeated calls accumulate background resources.
- Required evidence:
  - Show repeated or request-scoped construction and the missing release path.
- Exemptions:
  - The object is intentionally process-lifetime and owned by a long-lived
    manager with shutdown elsewhere.
  - The runtime helper being used does not require explicit release.
- Recommendation: Assign explicit ownership and release the resource on every
  shutdown and error path.

## Error handling

### GO-ERR-001 — Operational error is ignored

- Category: `error_handling`
- Default severity: `medium`
- Candidate signals:
  - A returned error is assigned to `_`, overwritten, or discarded.
  - Cleanup, persistence, flush, commit, decode, or security errors are ignored.
- Required evidence:
  - Show the ignored error and the incorrect success state or lost action it can
    cause.
- Exemptions:
  - The operation is explicitly best-effort and failure cannot affect correctness,
    security, or observability.
  - A comment and surrounding behavior establish a deliberate, safe policy.
- Recommendation: Handle, propagate, aggregate, or deliberately record the
  error according to the operation's contract.

### GO-ERR-002 — Failure is converted into misleading success

- Category: `error_handling`
- Default severity: `high`
- Candidate signals:
  - A function returns nil, an empty result, or a completed status after a
    required operation failed.
  - Partial work is committed or published while the corresponding failure is
    hidden.
- Required evidence:
  - Show the failed required operation and the success value or state observed by
    the caller.
- Exemptions:
  - The function contract explicitly defines the failure as a valid empty result.
  - Compensating behavior restores a consistent state and exposes the degraded
    result.
- Recommendation: Preserve the failure in the return value and keep state
  transitions consistent with the reported outcome.

## Tests

### GO-TEST-001 — Behavioral change has no matching test

- Category: `tests`
- Default severity: `warning`
- Candidate signals:
  - Production behavior gains a branch, validation rule, transformation, or
    error path without a related test change.
  - A bug fix has no regression test.
- Required evidence:
  - Identify the changed behavior. In a repository-backed review, confirm that
    visible tests do not exercise it. In a patch-only review, state narrowly that
    the supplied change contains no matching test and keep confidence below
    `0.70`; do not claim repository-wide absence.
- Exemptions:
  - Existing tests already cover the changed behavior and are visible in the
    available repository context.
  - The change is non-behavioral, such as comments or equivalent refactoring.
- Recommendation: Add a focused test that fails without the behavioral change
  and covers both success and relevant failure paths.

### GO-TEST-002 — High-risk failure or concurrency path is untested

- Category: `tests`
- Default severity: `warning`
- Candidate signals:
  - Security rejection, cancellation, timeout, rollback, cleanup, or concurrent
    shutdown behavior changes without targeted tests.
- Required evidence:
  - Show the high-risk path and the absence of a test that drives its defining
    condition.
- Exemptions:
  - An existing integration or unit test visibly exercises the same condition
    and asserts the relevant outcome.
- Recommendation: Add a deterministic test for the exact failure, cancellation,
  cleanup, or ordering condition.

## Database lifecycle

### GO-DB-001 — Query rows are not closed

- Category: `database_lifecycle`
- Default severity: `high`
- Candidate signals:
  - `Query` or `QueryContext` returns `*sql.Rows` and the owner does not call
    `Close`.
  - A loop returns early without protected cleanup.
- Required evidence:
  - Show the query acquisition and an owner exit path without closure.
- Exemptions:
  - Ownership is explicitly transferred to a caller that is responsible for
    closing rows.
  - A visible defer covers every relevant exit path.
- Recommendation: Defer `rows.Close()` immediately after successful acquisition
  and check `rows.Err()` after iteration.

### GO-DB-002 — Transaction lacks a complete terminal path

- Category: `database_lifecycle`
- Default severity: `high`
- Candidate signals:
  - `Begin` or `BeginTx` succeeds but some paths reach neither `Commit` nor
    `Rollback`.
  - An error after partial work returns without rollback protection.
- Required evidence:
  - Trace transaction ownership through success and every visible error path.
- Exemptions:
  - A deferred rollback safely covers all non-committed exits and expected
    post-commit rollback errors are intentionally ignored.
- Recommendation: Establish one transaction owner, defer rollback immediately,
  and commit only after all required operations succeed.

### GO-DB-003 — Database handle or statement ownership is violated

- Category: `database_lifecycle`
- Default severity: `medium`
- Candidate signals:
  - Request-scoped code closes a shared `*sql.DB`.
  - Repeated code creates prepared statements or database handles without
    closing them.
  - A handle intended to be long-lived is recreated for each operation.
- Required evidence:
  - Establish the expected owner and lifetime from construction and call sites.
- Exemptions:
  - The function clearly constructs and exclusively owns the handle it closes.
  - A dependency container or application lifecycle visibly performs cleanup.
- Recommendation: Align construction and closure with the owning lifecycle and
  avoid closing caller-owned shared handles.

### GO-DB-004 — Row iteration failure is ignored

- Category: `database_lifecycle`
- Default severity: `medium`
- Candidate signals:
  - Code iterates `*sql.Rows` but returns success without checking `rows.Err()`.
  - A scan or iteration error is overwritten or converted into a partial success
    without an explicit partial-result contract.
- Required evidence:
  - Show the iteration and the path that reports success after a driver or scan
    failure.
- Exemptions:
  - The function returns the iterator and transfers error handling to its caller.
  - A visible helper checks and propagates both scan and iteration errors.
- Recommendation: Check every `Scan` error, check `rows.Err()` after iteration,
  and expose partial results only through an explicit contract.
