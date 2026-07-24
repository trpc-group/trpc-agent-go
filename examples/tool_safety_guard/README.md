# Tool Execution Safety Guard Example

This example demonstrates the `tool/safety` guard with a mock corpus. It
loads a YAML policy, scans every sample, executes a mock callable tool
through `WrapTool`, and writes a batch report and audit JSONL file.

The example never calls an external model, shell, package manager,
network endpoint, or API-key-dependent service.

The example intentionally uses 19 focused samples for readable output.
The package quality gate uses the larger
`tool/safety/testdata/tool_safety_corpus.json` fixture.

## Run

From the `examples` module:

```bash
cd examples
go run ./tool_safety_guard
```

Expected output:

```text
WrapTool("dangerous delete") -> deny
WrapTool("credential read") -> deny
WrapTool("whitelisted request") -> allow
WrapTool("dependency install") -> ask
WrapTool("safe go test") -> allow
Wrapped redacted result: {"output":"API_KEY=[REDACTED:stripe_key:len=28]"}
Scanned 19 samples: 5 allowed, 12 denied, 2 asked (duration=3ms)
Wrote tool_safety_report.json and tool_safety_audit.jsonl
```

The allow/ask permission cases pass an explicit bounded timeout
(`"timeout":10`) because the shipped policy denies an omitted timeout.

## Files

- `main.go` — the example program.
- `tool_safety_policy.yaml` — the user-editable policy. Change allowed
  domains, denied paths, or rule actions without touching Go code.
- `tool_safety_report.json` — a representative batch scan report,
  regenerated on each run.
- `tool_safety_audit.jsonl` — one JSON object per preflight or post-execute
  audit event, regenerated on each run.

## Integration sequence

The guard plugs into the existing framework at two points:

```text
model tool call
  -> safety wrapped CallableTool
  -> wrapped tool's PermissionChecker
  -> safety preflight                  <-- pre-execution interception
  -> allow/deny/ask result
  -> underlying tool execution
  -> package-owned completion           <-- result redaction + audit
  -> redacted result, audit event, existing tool span
```

`ask` returns `approval_required`. An application with a human approval UI
must perform approval in its policy integration before allowing execution.

### Wrapping a callable tool

The wrapper keeps the entire safety lifecycle inside `tool/safety`, so
Flow, Graph, plugins, and generic callback APIs do not need changes:

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

workspaceTool := workspaceexec.NewExecTool(executor)
safeWorkspaceTool, err := safety.WrapTool(workspaceTool, guard)
if err != nil {
    return err
}

// Register safeWorkspaceTool in the agent's normal tool list.
```

`WrapTool` currently accepts `tool.CallableTool`, which covers the
canonical workspaceexec, hostexec, and codeexec call surfaces. A
streamable-only tool needs a stream-aware wrapper so partial chunks are
redacted before consumers observe them.

Use `Guard.Scan` or `Guard.ScanBatch` for standalone analysis that does
not execute a tool.

### Wiring the guard's command lists into workspaceexec

The guard's policy can also feed the existing `shellsafe`/`CleanEnv` path
in `workspaceexec`:

```go
policy := guard.Policy()
allow, deny := safety.CommandPolicyLists(policy)
workspaceTool := workspaceexec.NewExecTool(
    executor,
    workspaceexec.WithAllowedCommands(allow...),
    workspaceexec.WithDeniedCommands(deny...),
)
```

This activates the existing `shellsafe` parse and per-segment executable
check. The guard remains reusable for `hostexec`, `codeexec`, and MCP
tools.

## Backend boundaries

The guard is a **static preflight check**. It does not replace a sandbox
or kernel boundary. Each backend has different isolation guarantees:

- **`workspaceexec`**: commands run through an executor workspace. Command
  policy uses `shellsafe`, `CleanEnv`, and backend capability checks. A
  local workspace is a working area, not a host filesystem sandbox.
- **`hostexec`**: invokes the host shell, normally inherits the host
  environment, and supports PTY/background sessions. The guard cannot
  retroactively undo host access. Use it only when the host user is the
  operator.
- **`codeexec` / `codeexecutor`**: code blocks are decoded and scanned
  before execution. Local, container, E2B, and sandbox backends have
  different guarantees. `ToolProfile` must describe enforced capabilities.
- **MCP**: the guard sees request arguments and metadata. Remote server
  behavior remains outside the local process boundary.
- **Telemetry**: safety attributes are bounded metadata. Deployments that
  cannot retain raw payloads must also filter existing GenAI tool argument
  and result attributes with `telemetry/trace.WithSpanAttributePolicy`.

## Why this does not replace a sandbox

Static scanning has fundamental limits:

1. **Encoded or generated commands**: encoded data can hide the actual
   command from the parser. The guard rejects known substitution forms,
   but runtime behavior can still differ from scanned text.
2. **Runtime behavior not visible before execution**: IPC, shared memory,
   resource exhaustion, child processes, and side channels are invisible
   to a preflight check.
3. **DNS rebinding**: an allowlisted domain can resolve to an attacker's IP at runtime.
4. **TOCTOU**: a safe path may be replaced by a symlink before execution.
5. **Language runtime imports**: code can construct destinations at runtime
   without embedding a literal URL.

A production deployment must combine:

- **guard** (static preflight) +
- **clean environment** (filtered env vars, no secret inheritance) +
- **timeout/output limits** (enforced by the executor) +
- **process cleanup** (process groups, cgroups) +
- **real sandbox** (container, E2B, OS-level virtualization, seccomp) +

The guard makes the first layer explicit and auditable. It is not the only
required layer.

## Policy reload and failure behavior

- A changed YAML/JSON file takes effect when a new `Guard` is constructed.
- Invalid policy fails at `NewGuard` time with a descriptive error.
- A required preflight-audit failure (`audit.required: true`) denies
  execution. A post-execute audit failure happens after the tool has
  already run, so it is logged as a warning and cannot retroactively
  deny the call.
- A policy is never partially hot-reloaded. Construct a new `Guard`.

## Safety attribute constants

The guard projects these attributes onto the active execute-tool span:

- `tool.safety.decision`: `allow`, `deny`, or `ask`
- `tool.safety.risk_level`: `low`, `medium`, `high`, or `critical`
- `tool.safety.rule_ids`: fired rule identifiers
- `tool.safety.backend`: the execution backend
- `tool.safety.intercepted`: whether execution was blocked
- `tool.safety.redacted`: whether secret redaction occurred

The same constants are available in `telemetry/semconv/trace` as
`trace.KeyToolSafety*`.
