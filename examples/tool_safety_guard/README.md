# Tool Execution Safety Guard Example

This example demonstrates the `tool/safety` guard with a mock corpus. It loads a YAML policy, scans every sample, exercises `CheckToolPermission` with a fake denied request, attaches callbacks to a local `tool.Callbacks`, and writes a batch report and audit JSONL file.

The example never calls an external model, shell, package manager, network endpoint, or API-key-dependent service.

## Run

From the `examples` module:

```bash
cd examples
go run ./tool_safety_guard
```

Expected output:

```
CheckToolPermission("rm -rf /") -> deny: safety deny: rule=command.dangerous_delete ...
AfterTool redacted result: {"output":"API_KEY=[REDACTED:stripe_key:len=28]"}
Scanned 19 samples: 5 allowed, 12 denied, 2 asked (duration=1ms)
Wrote tool_safety_report.json and tool_safety_audit.jsonl
```

## Files

- `main.go` — the example program.
- `tool_safety_policy.yaml` — the user-editable policy. Change allowed domains, denied paths, or rule actions here without touching Go code.
- `tool_safety_report.json` — a representative batch scan report. Regenerated on each run.
- `tool_safety_audit.jsonl` — one JSON object per audit event (preflight + post_execute). Regenerated on each run.

## Integration sequence

The guard plugs into the existing framework at two points:

```
model tool call
  -> before-tool callbacks
  -> tool.PermissionChecker (per-tool)
  -> safety.Guard.CheckToolPermission   <-- pre-execution interception
  -> allow/deny/ask result
  -> actual tool execution
  -> safety after-tool callback         <-- result redaction + audit
  -> redacted result, audit event, existing tool span
```

`ask` returns `approval_required`. An application with a human approval UI must perform approval inside its policy integration and only then return `tool.AllowPermission()`.

### Wiring the guard as a permission policy

The guard implements `tool.PermissionPolicy`. Register it per-run via `agent.WithToolPermissionPolicyFunc` inside `agent.NewRunOptions`, not as a `runner.Option`:

```go
guard, err := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_guard/tool_safety_policy.yaml"),
    safety.WithAuditPath("tool_safety_audit.jsonl"),
    safety.WithTelemetry(true),
    safety.WithRedaction(true),
)
if err != nil {
    return err
}
defer guard.Close()

// At each Run call:
events, err := runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicyFunc(guard.CheckToolPermission),
)
```

### Wiring the guard's after-tool callback

```go
callbacks := tool.NewCallbacks()
guard.AttachCallbacks(callbacks)
// Use callbacks in the agent/run configuration so the after-tool
// redaction and audit-completion hook runs.
```

### Wiring the guard's command lists into workspaceexec

The guard's policy can also feed the existing `shellsafe`/`CleanEnv` path in `workspaceexec`:

```go
allow, deny := safety.CommandPolicyLists(policy)
workspaceTool := workspaceexec.NewExecTool(
    executor,
    workspaceexec.WithAllowedCommands(allow...),
    workspaceexec.WithDeniedCommands(deny...),
)
```

This activates the existing `shellsafe` parse + per-segment executable check when the application chooses command lists. The guard itself remains reusable for `hostexec`, `codeexec`, and MCP tools.

## Backend boundaries

The guard is a **static preflight check**. It does not replace a sandbox or kernel boundary. Each backend has different isolation guarantees:

- **`workspaceexec`**: commands run through an executor workspace. The existing command-policy mode uses `shellsafe`, `CleanEnv`, and backend capability checks. A local workspace is **not** automatically a host filesystem sandbox; the workspace directory is a working area, not a security boundary.
- **`hostexec`**: invokes the host shell, normally inherits the host environment, supports PTY/background sessions, and already cleans process groups on termination. The guard limits requested sessions and records lifecycle state but **cannot retroactively undo host access**. Use `hostexec` only for personal-agent workflows where the host user is the operator.
- **`codeexec` / `codeexecutor`**: code blocks are decoded and scanned before execution. The local, container, E2B, and sandbox backends have different timeout, network, output, and filesystem guarantees. The selected `ToolProfile` must describe those capabilities.
- **MCP**: the guard sees only the request arguments and metadata. Remote server behavior is **outside the local process boundary**; a remote MCP server can execute arbitrary code on its host.
- **Telemetry**: safety attributes are bounded metadata (enums, booleans, rule ids). Existing raw GenAI tool argument/result attributes (`gen_ai.tool.call.arguments`, `gen_ai.tool.call.result`) must be dropped or truncated through `telemetry/trace.WithSpanAttributePolicy` for deployments that must not retain raw tool payloads.

## Why this does not replace a sandbox

Static scanning has fundamental limits:

1. **Encoded or generated commands**: `echo $(echo cm0gLXJmIC8= | base64 -d) | sh` hides the actual command from the parser. The guard rejects the substitution, but a sufficiently creative encoder can produce a command the parser accepts at scan time and behaves differently at runtime.
2. **Runtime behavior not visible before execution**: IPC, shared memory, CPU/memory exhaustion, child processes, and side channels are invisible to a preflight check.
3. **DNS rebinding**: an allowlisted domain can resolve to an attacker's IP at runtime.
4. **TOCTOU**: a path that is safe at scan time may be replaced by a symlink before the tool runs.
5. **Language runtime imports**: `python -c "import urllib; urllib.urlopen('https://evil.example')"` does not contain a URL in the code string, but the runtime can construct one.

A production deployment must combine:

- **guard** (static preflight) +
- **clean environment** (filtered env vars, no secret inheritance) +
- **timeout/output limits** (enforced by the executor) +
- **process cleanup** (process groups, cgroups) +
- **real sandbox** (container, E2B, OS-level virtualization, seccomp) +

The guard makes the first layer explicit and auditable; it does not claim to be the only layer.

## Policy reload and failure behavior

- A changed YAML/JSON file takes effect when a new `Guard` is constructed; no code change is needed.
- Invalid policy fails at `NewGuard` time with a descriptive error.
- Required-audit failure (`audit.required: true` and the writer fails) denies execution.
- A policy is never hot-reloaded partially; construct a new `Guard` for a new policy.

## Safety attribute constants

The guard projects these attributes onto the active execute-tool span:

| Attribute | Value |
| --- | --- |
| `tool.safety.decision` | `allow`, `deny`, or `ask` |
| `tool.safety.risk_level` | `low`, `medium`, `high`, or `critical` |
| `tool.safety.rule_ids` | string list of fired rule ids |
| `tool.safety.backend` | `workspace_exec`, `hostexec`, `codeexec`, `mcp`, or `unknown` |
| `tool.safety.intercepted` | boolean |
| `tool.safety.redacted` | boolean |

The same constants are available in `telemetry/semconv/trace` as `trace.KeyToolSafety*`.
