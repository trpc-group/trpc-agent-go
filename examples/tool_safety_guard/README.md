# Tool Execution Safety Guard Example

This example scans commands only. It never executes a sample.

## Deliverables

- `tool_safety_policy.yaml`: editable command, path, network, environment,
  resource, and decision policy.
- `samples.json`: the 12 required scan-only acceptance samples.
- `tool_safety_report.json`: structured reports generated from the samples.
- `tool_safety_audit.jsonl`: one structured precheck audit event per sample.
- `main.go`: runnable scanner that validates every expected decision and rule.

The samples cover safe `go test`, dangerous deletion, credential reads,
denied and allowed network destinations, shell wrappers, pipelines, dependency
installation, long sleeps, oversized or unbounded output, hostexec PTY risk,
and explicit `ask`/`needs_human_review` decisions.

Run from the repository root:

```text
go -C examples run ./tool_safety_guard --policy tool_safety_guard/tool_safety_policy.yaml --samples tool_safety_guard/samples.json --report <report-output.json> --audit <new-or-empty-audit-output.jsonl>
```

Choose output paths outside the source tree for routine runs. Reports overwrite
their output file, while `JSONLAuditor` intentionally appends to an existing
audit file. The checked-in report and audit files are examples; using a new or
empty audit path keeps one event per sample instead of accumulating events from
earlier runs.

The policy loader is strict for YAML and JSON: unknown or duplicate fields,
trailing documents, invalid decisions, conflicting command lists, and
non-positive limits fail construction. Omitted lists use safe defaults.
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
or routing mechanism. `PermissionPolicy` and `WrapExecution` are the pre-run
gates. Using both causes two independent prechecks and is not required.

`workspaceexec` constrains workspace paths and owns its process/session policy.
`hostexec` touches the host shell, may inherit host environment, and permits PTY,
background, and long-lived sessions; Guard checks explicit inputs but cannot
clean inherited secrets or guarantee process-tree cleanup. Guard and executor
policies are independent and must both allow a request.

`codeexec` adapters scan each code block. Local, container, and E2B
`CodeExecutor` backends still own actual filesystem, process, network, and
resource isolation. Existing codeexec requests do not publish timeout/output
fields, so only the wrapper adds an upper context timeout and returned-output
cap.

Redaction covers Safety Guard reports, audit events, safety errors, and wrapper
output withheld after a detected secret. Wrapper postchecks also atomically
withhold sensitive or oversized state deltas, including inline artifact content.
They do not rewrite framework logs, host logs, traces, or dereference external
artifact files. `SpanAttributes` returns only four low-cardinality OTel
attributes and never creates a span.

Static scanning, context timeouts, and returned-output checks cannot stop every
runtime-generated command, kill an uncooperative process tree, resolve every
symlink/TOCTOU race, or enforce network/filesystem isolation. Use workspace,
container, E2B, OS, and network sandbox controls as the execution boundary.
