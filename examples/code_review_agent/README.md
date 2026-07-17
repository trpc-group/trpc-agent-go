# Code Review Agent Example

This example builds a deterministic automatic code review agent prototype. It
loads the `code-review` Skill, parses unified diffs or repository changes, gates
sandbox commands through a PermissionPolicy-style wrapper, records sandbox and
governance audit data, writes SQLite state, and emits JSON/Markdown reports.

Run from the `examples` module:

```bash
go test ./code_review_agent/...
go test ./code_review_agent/... -coverprofile=./code_review_agent.cover.out
go run ./code_review_agent --fixture clean --runtime=fake --dry-run
go run ./code_review_agent --fixture all --runtime=fake --dry-run
go run ./code_review_agent --eval-labels code_review_agent/testdata/eval_labels.json --runtime=fake --dry-run
```

CLI flags:

- `--diff-file`: unified diff or PR patch file.
- `--repo-path`: git repository path; the agent uses `git ls-files -z` for path validation and `git diff --no-ext-diff` for input.
- `--file-list`: comma-separated changed files for parser and gate testing.
- `--fixture`: fixture name, or `all` to run every fixture.
- `--fixture-dir`: fixture directory, defaulting to the bundled test fixtures.
- `--out-dir`: report and database output directory, default `code_review_agent_out`.
- `--db-path`: SQLite database path, default `<out-dir>/review_agent.db`.
- `--runtime`: `container`, `e2b`, `fake`, or `local`; default `container`.
- `--allow-trusted-local`: required before `--runtime=local` can execute.
- `--dry-run`: record allowed sandbox commands without executing them.
- `--sandbox-timeout`: per-command hard timeout, default `30s`.
- `--output-limit`: sandbox output byte limit, default `10485760`.
- `--max-diff-lines` and `--max-files`: size gates that skip sandbox execution and request human review.
- `--skills-root`: alternate Skills root for loading `code-review`.
- `--eval-labels`: labeled fixture manifest for measured recall, false-positive, and redaction rates.

Container mode is the production-shaped default and builds
`code_review_agent/sandbox/Dockerfile`. Local execution is blocked unless
`--allow-trusted-local` is set. The fake runtime is intended for CI and fixture
validation when Docker, E2B, or model credentials are unavailable.

The default outputs are:

- `code_review_agent_out/review_report.json`
- `code_review_agent_out/review_report.md`
- `code_review_agent_out/review_agent.db`
- `code_review_agent_out/eval_report.json` when `--eval-labels` is used
- `code_review_agent_out/eval_report.md` when `--eval-labels` is used

The SQLite database records task, input, sandbox run, permission decision,
finding, artifact, report, metrics, and diff-hash alias rows. Tests exercise
loading each of these records by task id.

All reported rates, detections, and persistence claims must be proven by tests
or fixture runs. `--eval-labels` accepts a JSON manifest with fixture labels and
secret probes, then emits measured recall, high-risk recall, false-positive
rate, and redaction rate. Do not claim hidden-sample precision, recall,
false-positive rate, or secret-redaction rate unless those values were measured
by an explicit test or evaluation command using the hidden labels.
