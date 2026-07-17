# Integration Proof

This file records reproducible integration evidence for the code review
agent example. It is intentionally separate from the README so reviewers
can quickly verify the commands that exercise the full path.

## Verified Locally

Date: 2026-07-16

Commands:

```bash
go test ./...
go vet ./...
./scripts/run_all_fixtures.sh
go run . --container-smoke --container-install-staticcheck \
  --container-base-image docker.m.daocloud.io/library/golang:1.23-bookworm \
  --output-dir output/container-proof-latest --timeout 120s
```

Results:

- `go test ./...`: passed.
- `go vet ./...`: passed.
- Public fixture matrix: 10/10 fixtures completed and generated JSON,
  Markdown, SQLite records, sandbox run summaries, and permission
  decisions.
- Holdout quality regression: covered by
  `TestHoldoutFixtureQualityThresholds` with 10 risk fixtures and 3
  benign fixtures under `internal/review/testdata/holdout`.
- Container smoke: passed with real `codeexecutor/container` execution.
  The proof preinstalls staticcheck so `staticcheck ./...` must run
  successfully instead of being recorded as an optional unavailable tool.

Container smoke sandbox runs:

| Command | Args | Status | Exit | Duration |
| --- | --- | --- | ---: | ---: |
| `bash` | `skills/code-review/scripts/diff_summary.sh work/change.diff out/diff_summary.json` | success | 0 | varies |
| `go` | `test ./...` | success | 0 | varies |
| `go` | `vet ./...` | success | 0 | varies |
| `staticcheck` | `./...` | success | 0 | varies |

Container smoke persistence check:

| Table | Rows |
| --- | ---: |
| `review_tasks` | 1 |
| `sandbox_runs` | 4 |
| `permission_decisions` | 4 |
| `findings` | 0 |
| `artifacts` | 5 |
| `audit_metrics` | 1 |
| `reports` | 1 |

Fixture matrix summary:

| Fixture | Findings | Warnings | Human Review | Sandbox Runs | Permission Decisions | Status |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `no_issue` | 0 | 0 | 0 | 2 | 1 | completed |
| `security_issue` | 1 | 0 | 1 | 2 | 1 | completed |
| `goroutine_context_leak` | 1 | 0 | 1 | 2 | 1 | completed |
| `resource_not_closed` | 1 | 0 | 2 | 2 | 1 | completed |
| `db_lifecycle` | 1 | 0 | 2 | 2 | 1 | completed |
| `missing_test` | 0 | 0 | 1 | 2 | 1 | completed |
| `duplicate_finding` | 2 | 0 | 1 | 2 | 1 | completed |
| `sandbox_failure` | 1 | 0 | 1 | 1 | 1 | completed |
| `sensitive_redaction` | 2 | 0 | 1 | 2 | 1 | completed |
| `advanced_risks` | 6 | 0 | 4 | 2 | 1 | completed |

To regenerate this evidence, run:

```bash
./scripts/integration_proof.sh
```
