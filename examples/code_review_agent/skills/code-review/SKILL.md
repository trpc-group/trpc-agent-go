# code-review Skill

This Skill packages a deterministic Go code review workflow for tRPC-Agent style agents.
It is designed to be loaded with `skill load` and executed with `skill run` inside an isolated workspace.

## Purpose

Review a unified diff, PR patch, or local git workspace and produce structured findings for Go projects. The Skill focuses on production code review risks, not generic prose comments.

## Inputs

- `--diff-file`: unified diff or PR patch.
- `--repo-path`: git working tree. The agent reads `git diff --no-ext-diff -- .`.
- `--files`: comma-separated path list for path-only review.
- `--fixture`: local fixture name under `testdata/fixtures`.

## Workflow

1. Parse unified diff hunks and collect changed Go files, candidate lines, context, and package names.
2. Run deterministic rules from `rules/go-code-review-rules.md`.
3. Request sandbox checks through the codeexec wrapper: `go test ./...`, `go vet ./...`, and optional `staticcheck ./...`.
4. Route every high-risk command through PermissionPolicy before execution.
5. Redact secrets before writing reports or persistent records.
6. Deduplicate findings by `(file, line, category)` and demote low-confidence issues to warnings or human review.
7. Persist task, sandbox run, permission decision, finding, artifact, monitoring summary, and final report.

## Safety Contract

The production runtime is expected to be `container`, `cube`, or `e2b`. Local execution exists only as a development fallback. All sandbox executions must use a timeout, captured output limit, environment allowlist, network-denied container mode when available, and artifact size constraints.

## Scripts

- `scripts/run_go_checks.sh`: command bundle for container or E2B workspaces.
- `scripts/custom_rules_hint.sh`: example hook for custom organization rules.

## Output

- `review_report.json`: machine-readable findings and audit data.
- `review_report.md`: human-readable CR summary.
- Persistent store: default JSON implementation plus `schema.sql` for SQLite-compatible SQL storage.
