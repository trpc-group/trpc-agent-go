# Design

A Chinese version of this document is available in
[DESIGN.zh_CN.md](DESIGN.zh_CN.md).

This prototype keeps the review path deterministic so it can run without model
credentials and produce stable reports for fixtures and hidden tests. The
`code-review` Skill describes the review workflow, rule catalog, and sandbox
scripts, while the Go CLI owns orchestration: it parses unified diffs or local
`git diff` output, maps changed Go hunks to candidate line numbers, runs
rule-only detectors, gates external commands through a permission policy, and
writes structured results.

Model-assisted review runs through the real agent stack: `agent/llmagent` +
`runner` with an in-memory session drive one review prompt per task.
`--mode fake-model` swaps in a deterministic offline `model.Model` so the full
chain (prompt building, event streaming, JSON parsing, merge, persistence) is
testable without keys, while `--mode llm` uses an OpenAI-compatible model.
Only redacted diff content is sent to the model. Model replies are parsed
against a strict JSON contract; confidence is clamped, locations outside the
diff are downgraded to human review, and all model findings are merged with
rule findings through the shared dedup/split pipeline. Model failures degrade
the task to rule-only results and are recorded as `model_error` exceptions.

Sandbox execution is separated from rule scanning. By default the example uses
`--sandbox managed`, which attempts `codeexecutor/sandbox` with restricted
networking, core environment inheritance, secret-name environment exclusion,
output caps, and timeouts. The same workspace flow also supports
`--sandbox container` through `codeexecutor/container` and `--sandbox e2b`
through `codeexecutor/e2b`, both via the shared `codeexecutor.Engine`
interface. Skill scripts run through the framework `tool/skill` tools
(`skill_load`/`skill_run`) over the same executor choices. `mock` is
explicit for dry-run/testing paths, and `local-dev` is
explicit because it is only a development fallback.
Every command receives a permission decision before execution. The command
governance rules implement the framework `tool.PermissionPolicy` interface;
allow-listed commands are limited to Go static checks and scripts under the
review skill, while high-risk shell, network, privilege, and destructive
commands are denied or marked for human review.

SQLite stores the minimum audit schema: task, finding, sandbox run, permission
decision, artifact, and report metadata. The `store.Store` interface is
intentionally small so another SQL backend can replace the SQLite
implementation. Findings
are deduplicated by file, line, category, and rule id, keeping the highest
severity and confidence. Low-confidence results go to human-review buckets
instead of high-confidence findings. Redaction is applied before reporting and
persistence to prevent API keys, tokens, passwords, and long secret-like values
from leaking into artifacts or database rows. Metrics capture duration,
sandbox time, tool calls, permission denies, severity distribution, and
exception counts for monitoring and replay.
