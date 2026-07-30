# Code Review Agent Design

A code review agent built on `trpc-agent-go`: the Agent uses the code-review Skill to understand a change, gathers evidence in a sandbox when needed, then persists structured conclusions and emits `review_report.json` and `review_report.md`

## Skill Design

The review flow distinguishes two input modes: patch-only (only `--diff-file`, no repository involved) and repo-backed (`--repo-path`, optionally combined with a diff). Both modes review the changed hunks first and read surrounding code only to confirm behavior, data flow, or lifecycle concerns; conclusions may only be anchored to this change or to an associated failure actually observed

Results fall into findings (well-evidenced and above the confidence threshold), warnings (observed but not impactful enough to call a defect), and needs_human_review (missing context, a denied execution, or a decision that needs a human). Rule criteria, rule IDs, and exemption cases live in the skill's `references/rules.md` and are progressively disclosed to the Agent

## Sandbox Isolation

The execution environment defaults to `codeexecutor/container` with `NetworkMode: "none"`. Each review task gets its own sandbox instance, cleaned up when the task ends; `codeexecutor/local` is an explicitly enabled fallback only, never an automatic downgrade

## Permission Policy

`PermissionPolicy` classifies calls before execution and returns `allow`/`deny`/`ask`: a command matching `run-go-checks.sh` or a tool with `Metadata.Destructive == true` is treated as high-risk and gets `ask`; arguments violating the environment-variable allowlist, the timeout ceiling, or similar constraints are denied outright. Once allowed, the actual execution is redacted, truncated, and status-classified by `Callbacks` and written to `sandbox_runs`, where a single failure does not abort the whole review. The framework also lets `workspace_exec` use `WithAllowedCommands`/`WithDeniedCommands` as an allow/deny list, but that interception happens after the policy has already allowed the call and only surfaces as a tool-call error, so it cannot be recorded as a `permission_decision` nor converge with high-risk tool-call records in one place

## Monitoring Fields

Each task writes `monitoring_summary_json` in its terminal state: total duration, sandbox duration, tool-call count, permission-block count, finding count, and the result-kind/severity/exception-type distributions — all derived from persisted permission decisions, `sandbox_runs`, `review_results`, and the task's start and end timestamps

## Database Schema

Code review records depend on the Review Store interface rather than the SQLite implementation directly, keeping the storage backend swappable.

Existing tables:

- `review_tasks`: task lifecycle and final conclusion
- `permission_decisions`: pre-execution governance decisions and their rationale
- `sandbox_runs`: what actually executed inside the sandbox
- `review_results`: structured review results
- `artifact_versions`: framework Artifacts

## Deduplication

Semantic judgment is left to the Agent, guided by the skill: same root cause, same affected behavior, same minimal fix means keeping one item

Deterministic collapsing happens inside `submit_review_results`. The Agent submits the complete result set in one call, and duplicates at the same spot are merged under the identity key `(file, line, rule_id)`; a category or result-kind contradiction under the same identity fails the tool call so the Agent retries. The database unique index is only a backstop

Results are submitted explicitly through the `submit_review_results` tool instead of the framework's `WithStructuredOutputJSON` so that validation failures can be returned to the Agent for correction — `WithStructuredOutputJSON` silently drops the payload without retrying when JSON deserialization fails

## Security Boundaries

`PermissionPolicy` validates the environment-variable allowlist, the timeout ceiling, and argument shape before approval; Tool Callbacks bound the returned output and record truncation, timeouts, and failures; the Artifact Service caps object size. Input preparation separately caps diff files and each Git output stream at 64 MiB, each snapshot file at 64 MiB, and the complete snapshot at 512 MiB before model or sandbox access. These constraints cannot be bypassed, and `sandbox_runs` records the status of every command actually executed

The input diff is redacted before it reaches the model, and tool arguments, execution results, and review conclusions all pass through the same `Sanitizer` before being returned or persisted; the SQLite Session Service injects that same `Sanitizer` via `WithAppendEventHook` as a last line of defense before persistence
