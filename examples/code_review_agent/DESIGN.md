# Design

This prototype keeps the review path deterministic so it can run without model
credentials and produce stable reports for fixtures and hidden tests. The
`code-review` Skill describes the review workflow, rule catalog, and sandbox
scripts, while the Go CLI owns orchestration: it parses unified diffs or local
`git diff` output, maps changed Go hunks to candidate line numbers, runs
rule-only detectors, gates external commands through a permission policy, and
writes structured results.

Sandbox execution is separated from rule scanning. By default the example uses
mock/dry-run execution; `--sandbox managed` attempts `codeexecutor/sandbox` with
restricted networking, core environment inheritance, secret-name environment
exclusion, output caps, and timeouts. The same workspace flow also supports
`--sandbox container` through `codeexecutor/container` and `--sandbox e2b`
through `codeexecutor/e2b`, both via the shared `codeexecutor.Engine`
interface. `local-dev` is explicit because it is only a development fallback.
Every command receives a permission decision before execution. Allow-listed
commands are limited to Go static checks and scripts under the review skill;
high-risk shell, network, privilege, and destructive commands are denied or
marked for human review.

SQLite stores the minimum audit schema: task, finding, sandbox run, permission
decision, artifact, and report metadata. The storage interface is intentionally
small so another SQL backend can replace the SQLite implementation. Findings
are deduplicated by file, line, category, and rule id, keeping the highest
severity and confidence. Low-confidence results go to human-review buckets
instead of high-confidence findings. Redaction is applied before reporting and
persistence to prevent API keys, tokens, passwords, and long secret-like values
from leaking into artifacts or database rows. Metrics capture duration,
sandbox time, tool calls, permission denies, severity distribution, and
exception counts for monitoring and replay.
