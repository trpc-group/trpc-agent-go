# tool/safety

Pre-exec check that plugs into `tool.PermissionPolicy`. It leans on
`internal/shellsafe` for command shape, and it does not try to be a sandbox.

Wire it with `agent.WithToolPermissionPolicy`. Spawn isolation, CleanEnv, and
real timeouts still live in workspaceexec / hostexec / codeexecutor.

## What this actually covers

Guard looks at the JSON arguments of a tool call before the tool runs:

- shell-ish fields: `command`, `args`/`argv`, `stdin`, `code_blocks`
- file-ish fields: `path`, `file`, `file_path`, …
- location fields: `uri`, `url`, `href`, `location`, `file_uri`
  (`file://…` is normalized so denied_paths can match the path part)
- `env` overrides (allowlist) and secret-looking keys (`password`, `api_key`, …)

It can return allow / deny / ask, enqueue a JSONL audit line (best-effort,
non-blocking when using `AsyncAuditor` / auto-wrapped `FileAuditor`), and set
a few span attributes when the context already has a recording span.

It does **not**:

- kill a process that already started
- cap live stdout/stderr
- wrap every tool result for you (use `RedactText` / `RedactMap` if you need that)
- understand obfuscated code (`exec(base64.b64decode(...))` and friends)

## Threat model (short)

| Trust boundary | Guard role |
|---|---|
| Model → tool args (JSON) | in scope: scan before invoke |
| Tool → OS process | out of scope: workspaceexec / hostexec / sandbox |
| Tool → network | in scope for args that carry hosts/URLs; not a proxy |
| Tool → result text | out of scope for PermissionPolicy; host can call `Redact*` |

Residual bypasses (honest list):

| Bypass | Why still open |
|---|---|
| Obfuscated interpreter code (`exec(base64…)`) | no AST / decoder stage — covered by `TestAdversarial_HandChecks/residual_base64_exec_still_allows` |
| Two tool calls: download, then run file | each call’s args look clean in isolation |
| Allow then run a binary that phones home | args looked clean |
| Secrets only in tool **output** | policy never sees results — wire `AfterToolRedact` (demo shows it) |
| Dual lists drift (Guard vs spawn) | you must wire `CommandLists()` — see [DUAL_POLICY.md](DUAL_POLICY.md) |
| `go test` ask-exemption | Kept for trusted local workflows (`test`/`fmt`/`vet`/`version`/`env`). `go test` still compiles and runs workspace-controlled code (`TestMain` / `init`). Callers must use a sandbox when the workspace is untrusted. |
| Audit queue full | `AsyncAuditor` drops events (see `Dropped()`); permission decisions are never blocked for audit I/O |

Hand-check denials (mentor paste tests): `curl … -o file && python3 file`,
`bash -c 'wget … \| python3'`, `nc … evil.example` — see
`TestAdversarial_HandChecks`.


## Issue 2002 mapping (partial on purpose)

| Topic in the issue | Here | Notes |
|---|---|---|
| Dangerous delete / credential paths | mostly | command text + path/uri fields; not a full VFS policy |
| Network allowlist | mostly | exact host by default; `.suffix` only if you opt in; also `url`/`uri` fields |
| Shell bypass | mostly | fail closed when `shellsafe` cannot parse |
| Download \| interpreter | yes | e.g. `wget … \| python3` denied even if host is allowlisted |
| hostexec long session | partial | default ask; no process-residual detector |
| Install / mutate env | partial | `ask_commands` + install-ish code text |
| Resource abuse | scan-time only | long `sleep`, `/dev/zero`/`yes`, huge args, `while true` → ask. Numbers in the policy are hints, not enforcement |
| Secrets in args / reports | partial | deny + redact on the scan result. Logs/artifacts after execution are on the host |
| Secrets in tool output | host-owned | wire `AfterToolRedact` / `RedactJSON`; PermissionPolicy never sees results |
| Artifact persistence | host-owned | wrap storage with `NewRedactingArtifactService` |
| Remote `go run host/…` | yes | deny (`shell.remote_go_run`); also in `code_blocks`; local `./…` stays ask |
| `curl\|sh` / `curl\|bash` | yes | shell + code_blocks (network→interpreter) |
| PowerShell `iwr\|iex` / `iex(irm …)` | yes | same pipe-to-interpreter class |
| `subprocess`/`os.system` + curl | yes | `code.subprocess_network` on execute_code payloads |

## Decisions

Priority is deny > ask > allow. Later findings can tighten; they do not clear a deny.

Common rule ids (also land in audit / report):

- `shellsafe.unparsable`, `shellsafe.policy`
- `shell.pipe_network_to_interpreter`, `shell.pipe_to_interpreter`
- `shell.remote_go_run`
- `code.subprocess_network`
- `network.denied_host`
- `extra.deny_tool_name`, `extra.ask_tool_name`, `extra.deny_command_substring`
- `path.denied`, `env.not_allowed`
- `danger.destructive_delete`
- `secret.*`
- `ask.dependency_or_mutation`
- `resource.long_sleep`, `resource.oversized_payload`, `resource.infinite_loop`
- `resource.unbounded_device`, `resource.unbounded_yes`
- `hostexec.long_session_risk`
- `code.install_mutation`
- `allow` (nothing matched)

## Dual policy with workspaceexec

See **[DUAL_POLICY.md](DUAL_POLICY.md)** for the one-page diagram and wire-up.

workspaceexec may also run `shellsafe` at spawn time. Guard is an earlier,
argument-level pass. To keep one source of truth without importing
`internal/shellsafe` from apps:

```go
guard := safety.NewGuard(safety.WithPolicyFile("tool_safety_policy.yaml"))
allow, deny := guard.Policy().CommandLists()
execTool := workspaceexec.NewExecTool(runner,
    workspaceexec.WithAllowedCommands(allow...),
    workspaceexec.WithDeniedCommands(deny...),
)
```

If you skip that wiring, you can still get “Guard allowed, spawn denied”
(or the reverse). There is no automatic sync on purpose — spawn options stay
explicit.

## Usage

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor), // any non-memory Auditor is auto-wrapped
)
defer guard.Close() // drains the audit queue at shutdown

events, err := runner.Run(ctx, user, session, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

`WithAuditor` wraps every sink except `*MemoryAuditor` / `*AsyncAuditor` in
`AsyncAuditor` (bounded queue, drop-on-full) so `CheckToolPermission` never
waits on disk or a custom auditor's I/O. Prefer `NewAsyncFileAuditor` when
constructing the sink yourself. `WithSyncAuditor` opts out of wrapping for
tests or hosts that already own a non-blocking sink.

Optional:

- `safety.Compose(guard, otherPolicy)` — first non-allow wins
- `safety.WithExtraRules(...)` — can only tighten; helpers:
  - `DenyToolNames`, `AskToolNames`, `DenyCommandSubstrings`
  - or `NamedRule` for custom checks
- `Policy.CommandLists()` — feed workspaceexec / skill_run command lists
- output scrubbing (PermissionPolicy never sees results):

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithExtraRules(
        safety.DenyToolNames("host_exec"),
        safety.DenyCommandSubstrings("terraform apply"),
    ),
)
cbs := tool.NewCallbacks()
cbs.RegisterAfterTool(safety.AfterToolRedact())
// also: safety.RedactText / RedactJSON / RedactValue / RedactMap
// artifacts: safety.NewRedactingArtifactService(inner)
```

Audit JSONL stamps `schema_version`, `policy_id`, and `policy_revision`
(content hash via `Policy.Revision()`) so hosts can join decisions to a
deployed policy without re-parsing YAML.

OTel attribute keys (JSONL audit uses the same suffixes):

- `tool.safety.decision` / `risk_level` / `rule_id` / `backend`
- `tool.safety.blocked` / `tool_call_id`

## Policy load behavior

- Missing deny lists in a file keep `DefaultPolicy` values (overlay).
- Unknown YAML/JSON keys are rejected.
- Load failure → deny on every check (`loadErr`), auditor is not overwritten.
- Bare `allowed_hosts` entries are exact match. Use `.example.com` for suffix.

`max_timeout_seconds` / `max_output_bytes` only affect the pre-exec ask/deny
hints. Executor timeouts and output caps are separate.

## Demo

```bash
cd examples/tool_safety_guard
go run .
```

See that folder’s README for samples, `want` checks, and where reports go.
