# Automatic Go Code Review Agent Design

## Purpose

Build the Issue #2004 prototype as an independent example module under
`examples/code_review_agent`. The example reviews a unified diff, a Git working
tree, or a bundled fixture; runs deterministic Go review rules and governed
sandbox checks; optionally asks an LLM for additional semantic findings; stores
the complete review record in SQLite; and publishes canonical JSON and derived
Markdown reports.

The implementation uses the canonical-result pattern from Codex Security, but
does not port its TypeScript/Python workbench. It uses tRPC-Agent-Go's public
Skill, workspace, CodeExecutor, Permission, Artifact, Runner, structured-output,
and telemetry APIs. It must not add or modify framework public APIs.

## Scope

The prototype provides:

- `--diff-file`, `--repo-path`, and `--fixture` input modes;
- deterministic parsing of unified diffs and changed Go packages;
- rules for security, secrets, goroutine/context lifetime, resource closure,
  error handling, database lifetime, and missing tests;
- `rule-only`, `fake-model`, and real-model review modes;
- governed `go test`, `go vet`, optional `staticcheck`, and Skill script runs;
- container and E2B production runtimes, with local execution available only
  through an explicit development flag;
- SQLite records for task, input, sandbox run, governance decision, finding,
  artifact, report, and monitoring summary;
- deterministic `review_report.json` and `review_report.md` output; and
- at least eight public fixtures plus independent holdout tests.

The prototype does not implement a hosted review service, GitHub PR comments,
multi-tenant scheduling, automatic patch application, interactive approval UI,
or a new generic security framework package.

## Architecture

```text
CLI input
  -> input normalization and redaction
  -> unified diff and Go package parsing
  -> deterministic rule engine
  -> permission-gated sandbox checks
  -> optional Skill/LLM semantic review
  -> finding validation, confidence routing, and deduplication
  -> global sanitization boundary
  -> canonical JSON finalizer
  -> Markdown projection and Artifact publication
  -> atomic SQLite task completion
```

The host-side orchestrator owns the lifecycle. The model is one optional finding
source and never owns task state, identifiers, persistence, or final report
formatting. This makes rule-only and fake-model runs exercise the same pipeline
without external credentials.

### Package layout

```text
examples/code_review_agent/
  main.go
  go.mod
  go.sum
  README.md
  skills/code-review/
    SKILL.md
    references/
    scripts/
  internal/review/
    types.go
    orchestrator.go
  internal/input/
    source.go
    diff.go
    git.go
  internal/rules/
    engine.go
    ast.go
    text.go
  internal/governance/
    policy.go
    recording.go
  internal/sandbox/
    runner.go
    diagnostics.go
  internal/assist/
    agent.go
    fakemodel.go
  internal/findings/
    validate.go
    fingerprint.go
    merge.go
  internal/redact/
    redact.go
  internal/store/
    store.go
    sqlite.go
    schema.sql
  internal/report/
    finalize.go
    markdown.go
    publish.go
  internal/telemetry/
    telemetry.go
  testdata/fixtures/
  testdata/holdout/
```

Interfaces are defined by their consumers inside the example. Constructors
return concrete implementations. No example package imports repository
`internal` packages.

## Input model

Exactly one input source is selected:

- `--diff-file PATH` reads a unified diff without executing Git.
- `--repo-path PATH` obtains staged and unstaged changes using fixed Git argv.
- `--fixture NAME` loads a bundled deterministic sample.

The parser supports ordinary, added, deleted, renamed, copied, and binary files;
omitted hunk counts; and `No newline at end of file`. It normalizes `a/` and
`b/` prefixes and rejects absolute paths, backslashes, NUL bytes, and `..`
traversal. Added lines are reportable locations. Context lines are evidence,
and removed lines are never reported as locations in the new file.

Package metadata comes from `go.mod` and Go files in the staged repository
snapshot, not from model output or untrusted diff prose.

## Review modes

`rule-only` runs parsing, deterministic rules, governance, configured sandbox
checks, persistence, and report generation without constructing a model.

`fake-model` uses a deterministic `model.Model`. It emits a fixed Skill/tool
trajectory and typed findings so tests cover Runner events, permission checks,
sandbox behavior, model-result validation, persistence, and finalization.

`model` uses an OpenAI-compatible model. Evidence collection and tool execution
occur first. A second, tool-free invocation requests strict structured output,
because some providers cannot reliably combine tool calls and strict JSON schema
in one request.

Deterministic rule findings remain authoritative. Model findings are additive,
must map to a parsed changed location, and are routed to `warnings` or
`needs_human_review` when confidence is low.

## Finding contract

Each finding contains the Issue-required fields:

```text
severity, category, file, line, title, evidence, recommendation,
confidence, source, rule_id
```

It also contains a schema version, fingerprint, optional end line, and review
disposition. Enums are closed and validated before persistence.

The versioned fingerprint material is:

```text
review/v1\0rule_id\0canonical_file\0new_line\0semantic_anchor
```

Within a task, `(rule_id, file, line, semantic_anchor)` is unique. Findings are
ordered by file, line, severity, rule ID, and fingerprint. Duplicate findings
from rules, tools, and models merge their evidence and retain the highest
confidence and severity rather than producing multiple report rows.

## Governance and sandboxing

The tool Filter/allowlist and `tool.PermissionPolicy` are separate controls.
The Filter limits the tools and command shapes exposed to the model; the policy
makes the final execution decision after arguments are finalized. Recording
wrappers write both filter decisions and every permission allow, deny, or ask
decision to the review store.

The stable-main implementation fails closed and does not depend on the
unmerged `tool/safety` package. A future adapter may use that package without
changing the orchestrator contract.

Only fixed operations are available: `go test`, `go vet`, optional
`staticcheck`, and named scripts under the trusted code-review Skill. A caller
cannot supply arbitrary shell text. `deny` and `ask` decisions are persisted
and skipped; the CLI does not simulate approval.

Production defaults require a container or E2B engine with `SupportsCleanEnv`.
The workspace receives a redacted diff, a bounded source snapshot, and the
read-only trusted Skill. It does not receive `.git`, host credentials, model
keys, or the host environment. Runs use a clean include-only environment,
disabled network, timeout, output and artifact limits, disk limit,
CPU/memory/PID limits, and deterministic output collection. Local execution
requires an explicit development option.

## Persistence

SQLite is the default `ReviewStore`; the consumer-side interface allows another
SQL implementation without exposing a framework API. The schema contains:

- `review_tasks` for status, phase, timestamps, mode, and terminal error;
- `review_inputs` for source type, redacted digest, changed-file summary;
- `sandbox_runs` for command identity, status, duration, exit, timeout, and
  redacted bounded stdout/stderr;
- `governance_decisions` for decision kind (`filter` or `permission`), tool,
  action, safe reason, and rule;
- `findings` for the canonical per-task findings;
- `artifacts` for name, revision/reference, digest, MIME type, and size;
- `review_reports` for canonical report digest and artifact references; and
- `review_metrics` for durations, counts, severity distribution, and errors.

Foreign keys and uniqueness constraints are enabled. Task creation commits
before work starts, so crashes remain queryable. Runs and decisions are written
as they happen. Final findings, metrics, report metadata, and the transition to
`completed` commit in one transaction. A failed final transaction cannot leave
a completed task.

## Canonical reports and artifacts

`review_report.json` is the semantic source of truth. The finalizer validates
all paths, lines, enums, limits, ordering, fingerprints, redaction, and summary
counts. It produces deterministic JSON with a trailing newline and rejects
unsafe or oversized values.

`review_report.md` is a deterministic projection of the validated JSON. Models
and callers cannot provide independent Markdown. Reports are written through a
same-directory temporary file, synchronized, and atomically renamed. Repeated
finalization of the same canonical report produces identical bytes and digest.

Both reports and selected sandbox outputs are saved through `artifact.Service`.
SQLite records pinned artifact references and digests rather than depending
only on temporary local paths.

## Redaction boundary

Repository content is untrusted data, including comments and repository-local
instructions. It cannot install Skills or authorize commands.

All persistence and output pass through one `Sanitize -> Validate -> Write`
boundary. Redaction covers input summaries, model evidence, tool stdout/stderr,
errors, findings, SQLite values, JSON, Markdown, and telemetry attributes.
Rules cover common API key, bearer token, password, private key, and credential
URL forms. Raw diffs, tool output, model messages, and secrets are dropped from
traces rather than merely truncated.

## Telemetry

One root review span links parsing, deterministic rules, permission decisions,
sandbox runs, model calls, storage writes, artifact publication, and
finalization. Low-cardinality attributes include task mode, phase, decision,
tool identity, outcome, and error type. Source code, diff text, stdout, stderr,
and model messages are excluded.

The application also persists an exact per-task metrics summary containing:

- total duration and sandbox duration;
- tool invocation and permission-block counts;
- finding total and severity distribution;
- warning and human-review counts; and
- error-type distribution.

Persisted metrics are the acceptance-test source of truth; OpenTelemetry is an
external projection.

## Failure behavior

- Invalid or unsafe input fails before sandbox or model execution and leaves a
  queryable failed task.
- A denied or approval-required command is recorded and skipped.
- A sandbox timeout, non-zero exit, unavailable optional checker, or bounded
  output truncation is recorded and surfaced in the report, but does not panic
  or discard other findings.
- Model failure degrades to deterministic results.
- Artifact publication failure prevents successful finalization.
- Canonical validation, redaction, or final database transaction failure leaves
  the task failed rather than completed.
- Context cancellation stops new work, cleans up owned workspaces, and records
  the terminal state with a bounded safe error.

## Verification strategy

Development follows test-first red/green cycles. Unit tests cover parser path
and line semantics, AST and text rules, diagnostics, fingerprinting, merge and
confidence routing, redaction, permission decisions, SQLite queries and
transactions, deterministic report bytes, and failure behavior.

Public fixtures cover clean code, security risk, goroutine/context leak,
resource leak, database lifetime, missing tests, duplicate findings, sandbox
failure, and secret redaction. Separate holdout fixtures are not used while
authoring individual rules and calculate high-severity recall, false-positive
rate, and redaction rate.

Integration tests use fake model and fake executor for deterministic full-flow
coverage. Container smoke tests verify clean environment, network restriction,
timeouts, output limits, and `go test` execution. E2B tests are optional and
credential-gated. Rule-only and fake-model acceptance runs must complete within
two minutes without model credentials.

## Acceptance mapping

1. Fixture runner executes at least eight samples and emits both reports.
2. Holdout evaluation enforces high-risk recall at least 80% and false-positive
   rate at most 15%.
3. A task-ID query reconstructs task, input, runs, decisions, findings,
   artifacts, metrics, and final conclusion.
4. Sandbox integration verifies timeout/output bounds and non-panicking failure.
5. Redaction evaluation requires at least 95% detection and scans SQLite and
   both report files for forbidden plaintext.
6. Fake-model/rule-only acceptance completes within two minutes.
7. Deny and ask cases prove no sandbox invocation occurred.
8. Report assertions cover finding summary, severity counts, human review,
   governance, metrics, sandbox summary, and actionable recommendations.

## Baseline and delivery constraints

The branch starts from `upstream/main` at `0f5bfe97`. At design time, the root
suite has two reproducible environment-specific failures unrelated to this
example: `internal/skillstage` panics while cleaning a read-only symlink on the
current Go 1.25/macOS runtime, and `tool/duckduckgo` cannot bind its Unix socket
under the current temporary path. These are recorded as baseline exceptions;
all example tests, affected modules, example builds, formatting, imports, and
other applicable repository checks must pass before delivery.
