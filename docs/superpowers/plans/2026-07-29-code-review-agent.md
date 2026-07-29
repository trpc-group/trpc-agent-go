# Automatic Go Code Review Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and verify the Issue #2004 example-only automatic Go code review Agent with Skills, governed sandbox checks, SQLite persistence, deterministic reports, fake/rule-only modes, and acceptance fixtures.

**Architecture:** A host-owned orchestrator normalizes a diff, runs deterministic Go rules, executes fixed checks through a permission-gated workspace, optionally requests typed model findings, sanitizes and merges all results, then atomically persists and publishes canonical JSON plus derived Markdown. The example uses only public tRPC-Agent-Go APIs and keeps all review-domain interfaces inside its own module.

**Tech Stack:** Go 1.23, tRPC-Agent-Go Runner/LLMAgent/Skills/CodeExecutor/Permission/Artifact/Telemetry, `database/sql` with `github.com/mattn/go-sqlite3`, Go AST/parser packages, OpenTelemetry, Docker/E2B workspace runtimes.

---

## File map

- `examples/code_review_agent/main.go`: CLI flags and dependency assembly.
- `examples/code_review_agent/internal/review/types.go`: canonical domain types and enums.
- `examples/code_review_agent/internal/review/orchestrator.go`: lifecycle and degradation rules.
- `examples/code_review_agent/internal/input/{source,diff,git}.go`: input selection, safe unified-diff parsing, Git source.
- `examples/code_review_agent/internal/redact/redact.go`: the single sanitize boundary.
- `examples/code_review_agent/internal/rules/{engine,text,ast}.go`: deterministic findings.
- `examples/code_review_agent/internal/findings/{validate,fingerprint,merge}.go`: canonicalization.
- `examples/code_review_agent/internal/governance/{policy,recording}.go`: filter and permission recording.
- `examples/code_review_agent/internal/sandbox/{runner,diagnostics}.go`: fixed check execution.
- `examples/code_review_agent/internal/assist/{agent,fakemodel}.go`: optional semantic findings.
- `examples/code_review_agent/internal/store/{store,sqlite}.go` and `schema.sql`: persistence.
- `examples/code_review_agent/internal/report/{finalize,markdown,publish}.go`: canonical artifacts.
- `examples/code_review_agent/internal/telemetry/telemetry.go`: low-cardinality spans and metrics.
- `examples/code_review_agent/skills/code-review/*`: Skill instructions, references, and scripts.
- `examples/code_review_agent/testdata/{fixtures,holdout}/*`: acceptance corpus.

### Task 1: Establish the independent module and canonical types

**Files:**
- Create: `examples/code_review_agent/go.mod`
- Create: `examples/code_review_agent/main.go`
- Create: `examples/code_review_agent/internal/review/types.go`
- Test: `examples/code_review_agent/internal/review/types_test.go`

- [ ] **Step 1: Write a failing canonical-enum test**

```go
func TestFindingValidateRejectsUnknownEnums(t *testing.T) {
	f := Finding{Severity: "urgent", Confidence: ConfidenceHigh}
	require.Error(t, f.Validate())
}
```

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/review`
Expected: FAIL because `Finding` and `Validate` do not exist.

- [ ] **Step 3: Implement minimal versioned types**

Define `Task`, `ReviewInput`, `SandboxRun`, `GovernanceDecision`, `Finding`, `ArtifactRecord`, `Metrics`, and `Report` with closed status, phase, severity, confidence, source, disposition, and decision enums. Implement validation without persistence or orchestration.

- [ ] **Step 4: Verify GREEN and module tidiness**

Run: `cd examples/code_review_agent && go test ./internal/review && go mod tidy`
Expected: PASS; `go.mod` and `go.sum` are stable.

- [ ] **Step 5: Commit**

Commit: `feat(examples): define review domain model`

### Task 2: Parse unified diffs safely

**Files:**
- Create: `examples/code_review_agent/internal/input/diff.go`
- Test: `examples/code_review_agent/internal/input/diff_test.go`
- Test data: `examples/code_review_agent/internal/input/testdata/*.patch`

- [ ] **Step 1: Write failing parser tests**

Cover ordinary hunks, omitted counts, add/delete/rename/binary files, no-newline markers, multiple hunks, added-line mapping, and rejection of absolute, backslash, NUL, and traversal paths.

```go
func TestParseDiffMapsAddedLines(t *testing.T) {
	d, err := Parse(strings.NewReader("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -2 +2,2 @@\n old\n+new\n"))
	require.NoError(t, err)
	require.Equal(t, 3, d.Files[0].Hunks[0].Added[0].NewLine)
}
```

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/input -run ParseDiff`
Expected: FAIL because `Parse` does not exist.

- [ ] **Step 3: Implement a bounded line parser**

Use `bufio.Reader`, explicit maximum bytes/line counts, strict hunk counters, POSIX path normalization, and deterministic file/hunk ordering. Do not invoke Git or use a model.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/input`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): parse review diffs safely`

### Task 3: Add diff-file, repository, and fixture sources

**Files:**
- Create: `examples/code_review_agent/internal/input/source.go`
- Create: `examples/code_review_agent/internal/input/git.go`
- Test: `examples/code_review_agent/internal/input/source_test.go`
- Test: `examples/code_review_agent/internal/input/git_test.go`

- [ ] **Step 1: Write failing source exclusivity and Git argv tests**

Use a fake command runner to assert that repository mode calls fixed `git -C <repo> diff --binary --no-ext-diff` argv and never a shell. Assert exactly one source flag is required.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/input -run 'Source|Git'`
Expected: FAIL because source constructors do not exist.

- [ ] **Step 3: Implement bounded sources**

Define the consumer-side `Source` and command-runner interfaces. Resolve repository roots, read fixtures through `fs.FS`, cap input size, compute SHA-256, and return the normalized parsed diff.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/input`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): add review input sources`

### Task 4: Enforce one redaction boundary

**Files:**
- Create: `examples/code_review_agent/internal/redact/redact.go`
- Test: `examples/code_review_agent/internal/redact/redact_test.go`

- [ ] **Step 1: Write failing table and recursive-value tests**

Include OpenAI-style keys, bearer tokens, passwords, private keys, DSNs, URL credentials, mixed case, multiple secrets, and benign lookalikes. Assert idempotence and bounded output.

```go
func TestStringIsIdempotent(t *testing.T) {
	one := String(`token="sk-test-secret-value"`)
	require.Equal(t, one, String(one))
	require.NotContains(t, one, "sk-test-secret-value")
}
```

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/redact`
Expected: FAIL because `String` does not exist.

- [ ] **Step 3: Implement redaction**

Compile bounded regexes once, preserve safe context, use a common `[REDACTED:<kind>]` marker, support strings/errors/JSON-compatible values, and truncate only after redaction.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/redact`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): redact review data`

### Task 5: Implement deterministic Go review rules

**Files:**
- Create: `examples/code_review_agent/internal/rules/engine.go`
- Create: `examples/code_review_agent/internal/rules/text.go`
- Create: `examples/code_review_agent/internal/rules/ast.go`
- Test: `examples/code_review_agent/internal/rules/engine_test.go`
- Test data: `examples/code_review_agent/internal/rules/testdata/*`

- [ ] **Step 1: Write one failing test per rule family**

Tests cover leaked goroutines/context, missing response/body/rows/file closure, ignored errors, transaction lifecycle, hard-coded secrets, dangerous command construction, and changed production code without related tests. Include clean neighbors to bound false positives.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/rules`
Expected: FAIL because the rule engine does not exist.

- [ ] **Step 3: Implement the minimal engine**

Parse staged Go source with `go/parser`, bind candidate lines to added hunks, emit versioned rule IDs, and keep rule functions side-effect free. Text rules run only on added lines; AST rules require evidence in the changed function. File-scope missing-test findings anchor to the first added production line.

- [ ] **Step 4: Verify GREEN and add clean controls**

Run: `cd examples/code_review_agent && go test ./internal/rules`
Expected: PASS with every vulnerable and clean case asserted.

- [ ] **Step 5: Commit**

Commit: `feat(examples): add deterministic review rules`

### Task 6: Validate, fingerprint, and merge findings

**Files:**
- Create: `examples/code_review_agent/internal/findings/validate.go`
- Create: `examples/code_review_agent/internal/findings/fingerprint.go`
- Create: `examples/code_review_agent/internal/findings/merge.go`
- Test: `examples/code_review_agent/internal/findings/findings_test.go`

- [ ] **Step 1: Write failing identity and merge tests**

Assert stable versioned fingerprints, path/line scope validation, duplicate collapse, highest severity/confidence retention, deterministic order, and low-confidence routing to human review.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/findings`
Expected: FAIL because normalizers do not exist.

- [ ] **Step 3: Implement canonicalization**

Use `sha256("review/v1\\x00" + ruleID + ...)`, validate against parsed added lines, merge evidence after redaction, and sort by canonical fields.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/findings`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): canonicalize review findings`

### Task 7: Persist complete reviews in SQLite

**Files:**
- Create: `examples/code_review_agent/internal/store/store.go`
- Create: `examples/code_review_agent/internal/store/schema.sql`
- Create: `examples/code_review_agent/internal/store/sqlite.go`
- Test: `examples/code_review_agent/internal/store/sqlite_test.go`

- [ ] **Step 1: Write failing schema and reconstruction tests**

Open a temporary SQLite database, enable foreign keys, create a task, record runs and decisions, complete it, and query the full aggregate by task ID. Add rollback, uniqueness, concurrent read, and plaintext-secret scan tests.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/store`
Expected: FAIL because the store does not exist.

- [ ] **Step 3: Implement the consumer interface and SQLite store**

Expose only methods required by the orchestrator: create task, transition phase, record run/decision, fail task, complete task transactionally, and get review. Use prepared statements, bounded context-aware operations, UTC timestamps, foreign keys, WAL for file databases, indexes, and unique task/finding constraints.

- [ ] **Step 4: Verify GREEN and race safety**

Run: `cd examples/code_review_agent && go test -race ./internal/store`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): persist code reviews in sqlite`

### Task 8: Gate and record tool execution

**Files:**
- Create: `examples/code_review_agent/internal/governance/policy.go`
- Create: `examples/code_review_agent/internal/governance/recording.go`
- Test: `examples/code_review_agent/internal/governance/policy_test.go`

- [ ] **Step 1: Write failing allow/deny/ask tests**

Allow exact `go test`, `go vet`, optional `staticcheck`, and trusted Skill scripts. Deny shell composition, arbitrary interpreters, path escapes, environment overrides, network commands, and unknown tools. Ask for dependency installation. Assert every decision is sanitized and recorded once.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/governance`
Expected: FAIL because policy does not exist.

- [ ] **Step 3: Implement fail-closed `tool.PermissionPolicy`**

Decode finalized tool arguments, classify only fixed operations, return framework allow/deny/ask decisions, and wrap the policy with a recorder. Keep filter records distinct from permission records.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/governance`
Expected: PASS, including proof that denied/ask callbacks never invoke a runner.

- [ ] **Step 5: Commit**

Commit: `feat(examples): govern review tool execution`

### Task 9: Execute bounded sandbox checks and parse diagnostics

**Files:**
- Create: `examples/code_review_agent/internal/sandbox/runner.go`
- Create: `examples/code_review_agent/internal/sandbox/diagnostics.go`
- Test: `examples/code_review_agent/internal/sandbox/runner_test.go`
- Test: `examples/code_review_agent/internal/sandbox/diagnostics_test.go`

- [ ] **Step 1: Write failing fake-engine tests**

Assert `CleanEnv`, timeout, resource limits, staged read-only inputs, no network capability, output truncation, timeout recording, non-zero exit degradation, optional staticcheck behavior, and diagnostic line mapping.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/sandbox`
Expected: FAIL because the sandbox coordinator does not exist.

- [ ] **Step 3: Implement fixed check plans**

Accept a public `codeexecutor.Engine`, require `SupportsCleanEnv` for production modes, create one owned workspace, stage only approved inputs, execute argv without a shell, sanitize results, and always clean up with a bounded context. Provide explicit container/E2B factories in CLI assembly and opt-in local development fallback.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/sandbox`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): run governed review checks`

### Task 10: Add Skill and fake/real model assistance

**Files:**
- Create: `examples/code_review_agent/internal/assist/agent.go`
- Create: `examples/code_review_agent/internal/assist/fakemodel.go`
- Test: `examples/code_review_agent/internal/assist/assist_test.go`
- Create: `examples/code_review_agent/skills/code-review/SKILL.md`
- Create: `examples/code_review_agent/skills/code-review/references/*.md`
- Create: `examples/code_review_agent/skills/code-review/scripts/*.sh`

- [ ] **Step 1: Write failing fake trajectory and typed-output tests**

The fake model must deterministically emit Skill load, one allowed workspace check, and a typed finding response. Test malformed, out-of-scope, low-confidence, secret-bearing, and model-error responses.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/assist`
Expected: FAIL because assistance is absent.

- [ ] **Step 3: Implement the two-stage assistant**

Build an `llmagent` using the filesystem Skill repository, full Skill profile, CodeExecutor, command allowlist, workspace bootstrap, recording PermissionPolicy, and in-memory Session/Artifact services. First collect evidence with tools; then run a tool-free strict structured-output request. Implement fake model through the public `model.Model` interface.

- [ ] **Step 4: Verify GREEN without credentials**

Run: `cd examples/code_review_agent && go test ./internal/assist`
Expected: PASS with no API key.

- [ ] **Step 5: Commit**

Commit: `feat(examples): add code review skill assistant`

### Task 11: Finalize and publish deterministic reports

**Files:**
- Create: `examples/code_review_agent/internal/report/finalize.go`
- Create: `examples/code_review_agent/internal/report/markdown.go`
- Create: `examples/code_review_agent/internal/report/publish.go`
- Test: `examples/code_review_agent/internal/report/report_test.go`
- Test data: `examples/code_review_agent/internal/report/testdata/golden.*`

- [ ] **Step 1: Write failing deterministic-report tests**

Assert exact JSON/Markdown golden bytes, stable digest, idempotence, summary consistency, no secret plaintext, canonical order, atomic replacement behavior, and Artifact revisions.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/report`
Expected: FAIL because finalizer does not exist.

- [ ] **Step 3: Implement finalization**

Validate and sanitize the complete report, encode canonical indented JSON with one trailing newline, derive Markdown only from the validated structure, write same-directory temporary files with sync and atomic rename, and save both through `artifact.Service` with pinned revisions and SHA-256 records.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test ./internal/report`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): finalize review reports`

### Task 12: Assemble orchestration, telemetry, and CLI

**Files:**
- Create: `examples/code_review_agent/internal/review/orchestrator.go`
- Create: `examples/code_review_agent/internal/telemetry/telemetry.go`
- Modify: `examples/code_review_agent/main.go`
- Test: `examples/code_review_agent/internal/review/orchestrator_test.go`
- Test: `examples/code_review_agent/main_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Cover successful rule-only and fake-model runs; parse failure; permission deny/ask; sandbox timeout/non-zero exit; model degradation; cancellation; artifact failure; final transaction failure; phase ordering; persisted metrics; and exit codes.

- [ ] **Step 2: Verify RED**

Run: `cd examples/code_review_agent && go test ./internal/review .`
Expected: FAIL because orchestration and flags are absent.

- [ ] **Step 3: Implement lifecycle and CLI**

Wire dependencies explicitly. Start one root span, create the task before work, persist phase transitions and runtime records, degrade optional failures, finalize once, and close every owned service. Add mutually exclusive input flags, modes, runtime selection, output/database paths, limits, and `--allow-local`.

- [ ] **Step 4: Verify GREEN**

Run: `cd examples/code_review_agent && go test -race ./internal/review .`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `feat(examples): orchestrate code reviews`

### Task 13: Add public fixtures, holdout evaluation, and documentation

**Files:**
- Create: `examples/code_review_agent/testdata/fixtures/*`
- Create: `examples/code_review_agent/testdata/holdout/*`
- Create: `examples/code_review_agent/acceptance_test.go`
- Create: `examples/code_review_agent/README.md`
- Create: `examples/code_review_agent/review_report.json`
- Create: `examples/code_review_agent/review_report.md`

- [ ] **Step 1: Write failing acceptance tests**

The corpus includes clean, security, goroutine/context, resource, database,
missing-test, duplicate, sandbox-failure, and secret cases. Calculate recall,
false-positive rate, redaction rate, and elapsed fake/rule-only time. Query every
stored entity by task ID and scan reports/database strings for fixture secrets.

- [ ] **Step 2: Verify RED against missing fixtures/docs**

Run: `cd examples/code_review_agent && go test -run 'Acceptance|Holdout' ./...`
Expected: FAIL until the corpus and complete pipeline exist.

- [ ] **Step 3: Add corpus, examples, and README**

Document architecture, Skill behavior, sandbox isolation, Filter/Permission,
schema, deduplication, redaction, telemetry, all CLI modes, production runtime
recommendations, and commands for deterministic and credentialed runs. Generate
sample reports through the CLI; never hand-edit Markdown independently.

- [ ] **Step 4: Verify all acceptance thresholds**

Run: `cd examples/code_review_agent && go test -count=1 -run 'Acceptance|Holdout' ./...`
Expected: PASS with recall >= 0.80, false positives <= 0.15, redaction >= 0.95,
and fake/rule-only duration < 2 minutes.

- [ ] **Step 5: Commit**

Commit: `test(examples): cover code review acceptance`

### Task 14: Run production-runtime smoke tests

**Files:**
- Create: `examples/code_review_agent/container_integration_test.go`
- Modify: `examples/code_review_agent/README.md`

- [ ] **Step 1: Write credential-free container smoke tests**

Tests verify `go test`, CleanEnv, no secret inheritance, network denial, timeout,
output limit, read-only input, writable output, and cleanup. Skip only when the
container runtime is unavailable and print the exact opt-in command.

- [ ] **Step 2: Verify the test detects a non-isolated fake**

Run the capability test against a fake engine reporting `SupportsCleanEnv=false`.
Expected: PASS only because the coordinator fails closed before execution.

- [ ] **Step 3: Run real container smoke**

Run: `cd examples/code_review_agent && TRPC_REVIEW_CONTAINER_TEST=1 go test -count=1 -run Container ./...`
Expected: PASS when Docker is available.

- [ ] **Step 4: Document optional E2B verification**

Document the credential-gated command and expected behavior without requiring it
in ordinary CI.

- [ ] **Step 5: Commit**

Commit: `test(examples): verify review container isolation`

### Task 15: Final validation, API review, and PR preparation

**Files:**
- Modify only files required by validation findings.

- [ ] **Step 1: Run focused validation**

```text
cd examples/code_review_agent && go test -race -count=1 ./...
cd examples/code_review_agent && go test -count=1 -run 'Acceptance|Holdout' ./...
cd examples/code_review_agent && go build ./...
```

Expected: all PASS.

- [ ] **Step 2: Run repository validation**

```text
.github/scripts/check-examples.sh
.github/scripts/run-go-tests.sh
go build ./...
go test ./...
gofmt -r 'interface{} -> any' -l .
goimports -l .
golangci-lint run --timeout=10m
```

Expected: all applicable checks PASS, except only the two documented unchanged
upstream baseline failures if they remain reproducible.

- [ ] **Step 3: Perform mandatory public-API and compatibility review**

Confirm the complete diff is example-only plus docs, imports no repository
`internal` package, adds no public framework surface, changes no defaults, and
keeps report/storage schema ownership inside the example. Verify every exported
example declaration is necessary and documented; make unnecessary declarations
unexported.

- [ ] **Step 4: Review final diff and acceptance evidence**

Map each Issue #2004 deliverable and A1-A8 criterion to a file, test, generated
artifact, or command result. Treat self-reported fixture metrics as insufficient
unless the holdout test independently computes them.

- [ ] **Step 5: Commit final fixes, push, and open a draft PR**

Use an English title in project format, include `Fixes #2004`, list actual test
commands and the two pre-existing main failures separately, push
`feat/examples-code-review-agent`, and create a draft PR for review.
