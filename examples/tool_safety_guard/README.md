# Tool Execution Safety Guard Example

This example scans commands only. It never executes a sample.

## Deliverables

- `tool_safety_policy.yaml`: editable command, path, network, environment,
  and resource policy.
- `samples.json`: the 12 required scan-only acceptance samples.
- `tool_safety_report.json`: structured reports generated from the samples.
- `tool_safety_audit.jsonl`: one structured precheck audit event per sample.
- `main.go`: runnable scanner that validates every expected decision and rule.

The samples cover safe `go test`, dangerous deletion, credential reads,
denied and allowed network destinations, shell wrappers, pipelines, dependency
installation, long sleeps, oversized or unbounded output, hostexec PTY risk,
and explicit `ask`/`needs_human_review` decisions.

## Coverage contract

- Dangerous execution: destructive deletion, system overwrite, privilege
  escalation, denied commands, SSH keys, `.env`, credential files, and denied
  paths are denied before execution.
- Network egress: built-in and custom clients, every parsed pipeline segment,
  literal/IP/dynamic targets, ambient proxy/config, redirects, curl destination
  remapping, and SSH routing options are checked against the domain allowlist.
- Shell bypass: nested shells, `eval`, process wrappers, substitutions,
  expansion, pipelines, and redirection use conservative `internal/shellsafe`
  parsing; unparseable input is never implicitly allowed.
- Host execution: PTY, interactive input, background work, privilege, and
  residual-process risk are surfaced before execution; the executor remains
  responsible for process-tree termination.
- Environment and dependencies: only configured environment keys are accepted;
  PATH overrides, sensitive values, and Go/npm/pip/system installs are denied or
  escalated according to policy.
- Resource abuse: required/max timeout, max returned output, long sleep,
  unbounded output, infinite loops, fork bombs, and concurrency are bounded.
- Secret leakage: reports, synchronous JSONL audit events, safety errors,
  callable results, returned errors, and inline artifact content are redacted or
  atomically withheld before return.

Run from the repository root:

```text
go -C examples run ./tool_safety_guard --policy tool_safety_guard/tool_safety_policy.yaml --samples tool_safety_guard/samples.json --report <report-output.json> --audit <new-or-empty-audit-output.jsonl>
```

Choose output paths outside the source tree for routine runs. Reports overwrite
their output file, while `JSONLAuditor` intentionally appends to an existing
audit file. The checked-in report and audit files are examples; using a new or
empty audit path keeps one event per sample instead of accumulating events from
earlier runs.

The policy loader is strict for YAML and JSON: unknown fields, trailing
documents, conflicting command lists, and non-positive limits fail
construction. Omitted lists use safe defaults.
Explicit empty lists have field-specific meaning: an empty command allowlist
denies every command, while configurable deny/path lists can be cleared.
Compiled `Policy` values are immutable snapshots; rebuild `Guard` after a file
change.

Safety decisions are `allow`, `deny`, `ask`, and `needs_human_review`.
Permission integration maps the latter two to `tool.PermissionActionAsk` while
reports and audit events retain the original decision. `AdaptRequest` and an
explicit `Binding` normalize final JSON arguments for workspaceexec, hostexec,
codeexec, or a caller-provided custom `InputAdapter`. Tool names must be the
model-visible names after naming or declaration wrappers.

`internal/shellsafe` performs conservative shell parsing. Safety Guard uses a
bounded 512-segment entry point; parse failures are fail-closed. A
`ToolExecutionFilter` has no per-call arguments and remains a static exposure
or routing mechanism. `NewPermissionPolicy` is the only pre-execution gate.
`WrapOutputGuard` is an optional post-execution guard that enforces the runtime
context deadline, returned-output limit, secret redaction, and synchronous
postcheck audit. It does not repeat argument scanning. Use both for complete
pre- and post-execution coverage.

`WrapOutputGuard` accepts callable, non-streaming tools only. It rejects tools
that expose streaming or state-delta capabilities because partial output could
escape before an atomic postcheck. Streaming and state-delta backends must
provide an equivalent executor-owned boundary before being used.

`workspaceexec` is expected to constrain filesystem access to its workspace and
to own process-tree cleanup. Safety Guard additionally checks explicit command,
path, network, environment, timeout, concurrency, and output-limit inputs, but
does not create the workspace sandbox.

`hostexec` touches the host shell and therefore has a wider trust boundary.
Safety Guard denies or escalates PTY, interactive, background, privilege, and
long-lived-session risks found in explicit inputs. The host executor must still
isolate inherited environment variables, enforce the deadline/output cap, and
terminate the complete process tree. Guard cannot clean already inherited
secrets or guarantee process cleanup by itself. Guard and executor policies are
independent and must both allow a request.

`codeexec` adapters scan each code block. Local, container, and E2B
`CodeExecutor` backends still own actual filesystem, process, network, and
resource isolation. Existing codeexec requests do not publish timeout/output
fields, so only the wrapper adds an upper context timeout and returned-output
cap.

Redaction covers Safety Guard reports, audit events, safety errors, and callable
tool output withheld after a detected secret, including inline artifact content.
It does not rewrite framework logs, host logs, traces, or dereference external
artifact files. `SpanAttributes` returns the low-cardinality
`tool.safety.decision`, `tool.safety.risk_level`, `tool.safety.rule_id`, and
`tool.safety.backend` attributes and never creates a span.

Static scanning, context timeouts, and returned-output checks cannot stop every
runtime-generated command, kill an uncooperative process tree, resolve every
symlink/TOCTOU race, or enforce network/filesystem isolation. Use workspace,
container, E2B, OS, and network sandbox controls as the execution boundary.
