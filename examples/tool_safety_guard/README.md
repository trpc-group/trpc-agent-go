# Tool Execution Safety Guard

A **pre-execution** safety layer for tRPC-Agent-Go exec tools (`workspace_exec`,
`exec_command`/hostexec, codeexec). It statically scans a command / script /
code block, decides `allow / ask / needs_human_review / deny`, and emits a
structured report, a JSONL audit trail and OpenTelemetry span attributes. The
engine lives in [`tool/safety`](../../tool/safety); this directory is a runnable
demo.

## Design note

The guard turns the framework's existing primitives into one auditable decision
point. Commands are parsed conservatively by `internal/shellsafe`, which rejects
command substitution, redirection, subshells and leading assignments; anything
it cannot parse becomes a **deny** (never a silent allow). Parsed pipeline
segments are matched against a **data-driven policy** — allowed/denied commands,
denied path globs (secrets/credentials), a network allowlist, dependency-install
patterns, resource limits and secret regexes — so changing behaviour never
requires code changes. Rules cover the seven risk types (dangerous commands,
network exfiltration, shell bypass, host-exec risk, dependency/env changes,
resource abuse, sensitive-data leakage); host-backend commands are weighted one
level higher because they run on the real machine rather than an isolated
workspace. Findings aggregate to the most restrictive decision, secrets are
redacted from every output, and the verdict is exposed through
`tool.PermissionPolicy` so the framework blocks a tool **before** it executes
and records an audit event. It is defence-in-depth, not a sandbox replacement.

## How it relates to the framework

The guard is glue over existing tRPC-Agent-Go primitives — it adds one decision
point, it does not fork the stack:

| Component | Relationship to the guard |
|---|---|
| `internal/shellsafe` | The conservative command parser. `parsePipeline` calls `shellsafe.Parse` to get pipeline segments and to reject unsafe constructs (`$()`, redirection, subshells); the guard's command-runner deny set mirrors shellsafe's implicit-deny wrappers so both agree on what can smuggle execution past argv[0]. |
| `tool.PermissionPolicy` | The integration seam. `safety.PermissionPolicy.CheckToolPermission` implements this interface, and the framework calls it in `internal/flow/processor/functioncall.go` **before** running any tool. A deny/ask verdict skips execution. |
| `tool/workspaceexec` (`workspace_exec`) | A guarded backend: commands run in an isolated executor workspace with a scrubbed env. The guard scans its `command` argument; findings use the baseline risk weight. |
| `tool/hostexec` (`exec_command`) | A guarded backend that runs on the **real host** shell (PTY, long sessions). The guard scans its `command`/`workdir` and weights host-risk rules one level higher because the blast radius is the machine, not a workspace. |
| `codeexecutor` (local/container/e2b) | The isolation layer the guard complements. The guard runs *before* execution; the executor (ideally container/e2b) provides isolation, resource limits and syscall confinement *during* execution. The guard never replaces it. |
| `telemetry` / OpenTelemetry | The observability sink. `SetSpanAttributes` records `tool.safety.decision`, `tool.safety.risk_level`, `tool.safety.rule_id` and `tool.safety.backend` on the active span; the JSONL audit trail is the post-hoc record. |

## Run

```bash
# Scan the bundled samples -> tool_safety_report.json + tool_safety_audit.jsonl
go run .

# Use a custom policy / samples
go run . --policy tool_safety_policy.yaml --samples samples.json

# Demonstrate the pre-execution permission gate (what functioncall.go calls)
go run . --demo
```

Everything runs offline and deterministically — no model API key required.

## Wiring into an agent

`CheckToolPermission` implements `tool.PermissionPolicy`, so the framework
calls it before every tool call (see
`internal/flow/processor/functioncall.go`). A non-allow verdict skips execution.

```go
policy, _ := safety.LoadPolicy("tool_safety_policy.yaml")
scanner := safety.NewScanner(policy)
auditFile, _ := os.Create("tool_safety_audit.jsonl")
pol := safety.NewPermissionPolicy(scanner,
    safety.WithAuditWriter(safety.NewAuditWriter(auditFile)),
    safety.WithTelemetry(true),
    // Map a custom codeexec tool name if you use one:
    safety.WithToolBackend("execute_code", safety.BackendCodeExec),
)

runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicyFunc(pol.CheckToolPermission))
```

See [`examples/toolpolicy`](../toolpolicy) for the run-level policy wiring in a
full agent loop.

## Policy is hot-configurable

Editing `tool_safety_policy.yaml` changes allowed domains, forbidden paths,
allowed/denied commands, limits and secret patterns **without touching code**:

| Field | Purpose |
|---|---|
| `allowed_commands` / `denied_commands` | command-name allow/deny (deny always enforced; allow only when `enforce_allowlist: true`) |
| `denied_path_patterns` | secret/credential/system paths that may not be accessed (glob: `**`, `*`, `?`) |
| `network.allowed_domains` | hosts a network command may reach; others are denied |
| `dependency_install.patterns` | install invocations that require review |
| `limits.max_timeout_sec` | requested timeouts above this are flagged for review |
| `limits.max_output_bytes` | advisory only — surfaced for the executor/runtime to enforce; the static guard does not cap output |
| `secret_patterns` | inline-secret regexes used for detection and redaction |
| `default_decision_on_parse_failure` | `deny` (default) or `ask` for unparsable commands |
| `risk_overrides` | bump/lower a rule's risk by id |

## Backend boundaries

| Aspect | `workspace_exec` | `exec_command` (hostexec) |
|---|---|---|
| Isolation | shared executor workspace (local/container/e2b) | **real host shell** in a base dir |
| Env hardening | scrubbed env + `CleanEnv` under policy mode | inherits host env |
| PTY / long sessions | interactive sessions in the workspace | PTY on the host — process residue risk |
| Guard weighting | baseline | host-risk rules bumped one level |
| Blast radius | workspace files | host files/processes/network |

Prefer `workspace_exec` on an isolating runtime (container/e2b) for untrusted
work; reserve `exec_command` for trusted local automation.

## Why this is not a sandbox replacement

Static scanning is a **best-effort, pre-execution** filter. It cannot observe
runtime behaviour, defeat determined obfuscation, or confine syscalls, file
descriptors and network at execution time. Treat it as the first of three
layers:

1. **Scan (before)** — this guard: reject the obviously dangerous, require
   review for the ambiguous, and produce an audit trail.
2. **Sandbox (during)** — `codeexecutor/container` or `codeexecutor/e2b`:
   isolation, resource limits, filesystem/network confinement.
3. **Audit (after)** — the JSONL trail and OTel spans for monitoring and replay.

Removing any layer weakens the others; the guard raises the floor, it does not
replace the sandbox.
