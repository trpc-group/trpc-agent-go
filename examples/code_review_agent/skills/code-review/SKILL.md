---
name: code-review
description: "Deterministic, governed review of Go unified diffs and worktrees. Use for structured Go risk findings, optional sandboxed go test/go vet checks, durable audit records, and JSON/Markdown reports. Do not use for arbitrary shell execution or non-Go repository-wide refactoring."
---

# Governed Go code review

Use this Skill as the workflow contract for the structured `code_review` tool.
The tool and application own all security-sensitive decisions; model output is
never an execution specification.

## When to use

- Review a bounded unified diff, changed-file list, or Git worktree containing
  Go changes.
- Produce deterministic findings without a model API key.
- Run the declared `go-test` and `go-vet` checks in an approved runtime.
- Persist the review, governance trail, sandbox runs, metrics, artifacts, and
  final reports for later query or replay.

## When not to use

- Do not use this Skill to execute user-provided shell commands.
- Do not use it for general repository modification, automatic fixes, or
  network-dependent dependency installation.
- Do not treat local execution as a production sandbox.
- Do not promote incomplete or ambiguous evidence to a high-confidence finding.

## Inputs and execution modes

Exactly one input mode must already be configured by the caller:

- `--diff-file`: bounded unified diff or PR patch.
- `--repo-path`: staged, unstaged, untracked, renamed, and deleted changes from
  a local Git worktree; an optional validated file list may narrow the input.
- `--fixture`: bundled deterministic acceptance data.

Runtime policy:

- `container` is the production default.
- `fake` and rule-only modes retain deterministic analysis and audit behavior
  without executing repository code.
- `local` is a development fallback and requires explicit `--allow-local`.

## Required workflow

Follow these phases in order. Do not skip governance or persistence phases.

1. **Load the Skill.** Load `code-review`, validate all required resources, and
   read `docs/rules.md` before invoking `code_review`.
2. **Validate input.** Enforce a single bounded input mode, reject path escape,
   parse changed files and hunks, and resolve candidate Go packages. Keep raw
   source and diff content ephemeral.
3. **Analyze.** Load enabled entries from `rules/rules.json`; apply only their
   declared AST and patch implementations. Emit the complete finding contract:
   severity, category, file, line, title, evidence, recommendation, confidence,
   source, and rule ID.
4. **Normalize.** Clean paths, redact evidence, bucket by confidence, deduplicate
   by file/line/category, and apply stable ordering. Ambiguous evidence belongs
   in warnings or `needs_human_review`.
5. **Authorize checks.** For every declared sandbox check, create an isolated
   workspace, then run the safety Filter and `PermissionPolicy` against its exact
   paths and runtime values. Persist both decisions before staging or execution.
   Only an allow decision may continue.
6. **Execute.** Invoke only the trusted `scripts/checkrunner` with fixed arguments,
   an exact environment whitelist, a bounded timeout and output limit, and one
   opaque result artifact. Never interpolate model or diff content into argv.
7. **Finalize.** Persist sandbox outcomes even on timeout or failure. Render,
   redact, and publish JSON and Markdown with bounded atomic file writes, then
   transactionally finalize that same snapshot.

## Governance and failure behavior

- Filter rejects unknown checks or runtimes, mutable argv, unsafe capabilities,
  invalid paths, non-whitelisted environment values, excessive timeouts, and
  untrusted runners or artifacts.
- Permission `deny`, `ask`, an unknown action, policy error, or decision-storage
  failure blocks execution. Never convert `ask` into implicit approval.
- Sandbox timeout or failure is recorded and produces
  `completed_with_warnings`; it must not terminate the whole review process.
- Caller cancellation is also recorded, but remains a failed task and is never
  converted into a sandbox warning.
- Fail closed when the Skill, store, governance decision, or final report
  cannot be validated.

## Output contract

The final JSON and Markdown reports must describe the same durable snapshot and
include the conclusion, finding and severity summaries, human-review items,
governance blocks, sandbox summary, monitoring metrics, and actionable fixes.
The store must remain queryable by task ID for task, input summary, runs,
decisions, findings, metrics, artifacts, and reports.

## Required resources

- `rules/rules.json`: executable rule metadata and confidence policy.
- `docs/rules.md`: evidence requirements, exclusions, and noise controls.
- `scripts/checkrunner`: trusted fixed-command sandbox entry point.
- `README.md`: operator-facing layout, runtime, and extension guidance.

## Non-negotiable security rules

- Never persist raw diff or source text.
- Never accept commands, argv, environment values, runner paths, or artifact
  paths from model output.
- Never expose plaintext credentials in reports, SQLite, logs, errors,
  telemetry, CLI output, or artifacts.
- Never bypass Filter, Permission, or durable decision recording, including in
  dry-run and fake-model flows.
