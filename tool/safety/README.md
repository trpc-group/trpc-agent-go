# Tool Safety Guard

Tool Execution Safety Guard for tRPC-Agent-Go.

Pre-execution safety scanning, Filter/Permission interception, and audit monitoring
for Agent tool calls — covering workspaceexec, hostexec, and codeexec backends.

## Overview

The Safety Guard sits **before** tool execution. It scans every command for
seven risk categories and returns a structured decision: **allow**, **deny**,
or **ask** (needs human review).

```
Agent calls Tool.Call()
        │
        ▼
SafetyPermissionPolicy (adapter.go)
   └─ Scanner.Scan()
        ├─ command checker     → shellsafe + dep-install detection
        ├─ secret-cmd checker  → hard-coded API keys in arguments
        ├─ env checker         → environment variable allow/deny/value patterns
        ├─ network checker     → domain whitelist/blacklist
        ├─ path checker        → sensitive file patterns
        ├─ host checker        → background proc, privilege esc, sessions
        └─ resource checker    → timeout, output limits, infinite loops, concurrency
             │
             ▼
        SafetyReport { decision, risk_level, rule_id, evidence, ... }
             │
             ▼
        AuditLogger → tool_safety_audit.jsonl
```

## Quick Start

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// 1. Load policy.
policy, _ := safety.LoadPolicy("tool_safety_policy.yaml")

// 2. Create scanner and audit logger.
audit, _ := safety.NewJSONLAuditLogger("tool_safety_audit.jsonl", policy)
scanner := safety.NewScanner(policy, audit)

// 3. Wrap any PermissionPolicy with safety scanning.
safe := safety.NewSafetyPermissionPolicy(
    myExistingPolicy,  // nil if you have no existing policy
    scanner,
    audit,
)

// 4. Use safe as your agent's PermissionPolicy.
```

## Configuration

See `tool_safety_policy.yaml` for a full annotated example.

Policy changes take effect on next `LoadPolicy` — no code changes required.

## Architecture

### Relationship with other components

```
┌────────────────────────────────────────────┐
│  Sandbox isolation (container/e2b)          │
│  Runtime: process, filesystem, network ns   │
├────────────────────────────────────────────┤
│  Safety Guard (this module)                 │
│  Pre-execution: static scan, policy, audit  │
│  Post-execution: output desensitization     │
├────────────────────────────────────────────┤
│  PermissionPolicy / Filter                  │
│  Decision layer: Allow / Deny / Ask         │
├────────────────────────────────────────────┤
│  workspaceexec / hostexec / codeexec        │
│  Execution layer                            │
└────────────────────────────────────────────┘
```

### Why this mechanism cannot replace sandbox isolation

The Safety Guard is a **pre-execution gate**: it decides whether a command
*should* run based on static analysis. It cannot:

- Enforce resource limits at runtime (cgroups, namespaces)
- Isolate the filesystem (chroot, overlayfs)
- Restrict network access after execution starts (iptables, network namespaces)
- Prevent a determined attacker from bypassing user-space checks

A sandbox (container, e2b, firecracker) provides **runtime isolation**: even
if a command executes, it runs in a confined environment where it cannot
damage the host. The two mechanisms are **complementary**:

- **Safety Guard** = "don't run what shouldn't run"
- **Sandbox** = "if it runs, it can't break anything"

Neither alone is sufficient for production safety.

### Relationship with shellsafe

`internal/shellsafe` handles command-structure parsing and executable-name
allow/deny policies. This module:

- **Uses** shellsafe for command validation — never modifies it
- **Adds** multi-dimensional checks beyond command names (network, paths, secrets, resources)
- **Wraps** shellsafe.Policy with structured reports and audit logging
- **Respects** the shellsafe implicit-deny set unconditionally

### workspaceexec vs hostexec — Security Boundary Comparison

The Safety Guard applies different risk models to each backend:

| Dimension          | workspaceexec                           | hostexec                                     |
|--------------------|-----------------------------------------|----------------------------------------------|
| **Execution mode** | Shell command in a workspace directory  | Direct host shell (PTY or non-PTY)           |
| **Process isolation** | ✅ CodeExecutor isolates processes via workspace boundaries | ❌ Commands run as the agent process user |
| **Filesystem scope** | Limited to workspace root + declared inputs | Full host filesystem access                 |
| **Environment**    | Scrubbed by policy mode: HOME, PATH, LD_PRELOAD, etc. stripped | Host environment inherited unless explicitly cleaned |
| **PTY sessions**   | Not applicable (batch execution)        | ⚠️ PTY sessions can persist, attach/detach, leave residual processes |
| **Background procs** | Killed when workspace session ends    | ⚠️ `&`, `nohup`, `disown` may leave orphaned processes on the host |
| **Privilege escalation** | N/A (container/workspace user)    | ⚠️ `sudo`, `su`, `doas` can escalate to root |
| **Session residual** | Cleaned up with workspace            | ⚠️ `tmux`, `screen` sessions survive tool completion |
| **Output limits**  | Enforced by CodeExecutor (max output bytes) | Enforced by hostexec manager (max lines)  |
| **Network**        | Container network namespace (if configured) | Host network stack, full egress            |

**Integration points:**

- **workspaceexec:** Wrap the tool's PermissionPolicy with `SafetyPermissionPolicy`.
  The scanner automatically applies `checker_command` (shellsafe policy),
  `checker_network`, `checker_path`, and `checker_resource`. `checker_host`
  skips because Backend is `"workspaceexec"`.

- **hostexec:** Wrap with the same `SafetyPermissionPolicy`. The scanner applies
  ALL checkers including `checker_host` (background processes, privilege
  escalation, session residual). The `checker_host` is a no-op for non-hostexec
  backends.

- **codeexec:** Same wrapper pattern. `checker_command` and `checker_resource`
  are the primary checks; `checker_path` and `checker_network` may apply
  depending on the CodeExecutor's isolation level.

### Relationship with CodeExecutor

The Safety Guard runs **before** the CodeExecutor. The execution flow is:

```
PermissionPolicy (Safety Guard) → CodeExecutor.Run(workspace, spec)
```

- **local codeexecutor:** The guard MUST enforce command policies because
  the local executor shares the agent process's OS user. Network and path
  checks are essential — there is no built-in isolation.

- **container codeexecutor:** The guard enforces command policies as a
  first line of defense, but the container provides additional isolation
  (filesystem, network namespace, resource limits). The guard's resource
  checks (timeout, output limit) complement — but do NOT replace — the
  container's cgroups limits.

- **e2b codeexecutor:** Similar to container — the cloud sandbox provides
  the strongest isolation. The guard serves as a fast pre-filter to avoid
  sending obviously dangerous commands to the remote sandbox, reducing
  latency and cost.

The CodeExecutor's `SupportsCleanEnv` capability is required for policy
mode (enforced by workspaceexec's `checkRunnerSupportsPolicy`). Without
clean environment support, policy mode is refused at startup — the guard
never silently degrades to "command name check only, host env inherited."

### Relationship with PermissionPolicy

`adapter.go` implements `tool.PermissionPolicy` via a decorator pattern. Any
existing permission policy can be wrapped with safety scanning without
changing the original tool code:

```go
safetyPolicy := safety.NewSafetyPermissionPolicy(originalPolicy, scanner, audit)
```

### Relationship with Telemetry

`otel.go` provides `SafetyReport.ToSpanAttributes()` which returns a
`map[string]string`. Callers with OpenTelemetry enabled can set these
attributes on their spans directly. The safety package does not import
any OTel SDK — it only produces data for OTel consumers.

## Risk Categories

| Category    | RuleID Examples                          | Checker           |
|------------|------------------------------------------|-------------------|
| Command    | `CMD_DENIED_BY_POLICY`, `CMD_SHELL_WRAPPER`, `CMD_DEP_INSTALL` | checker_command |
| Network    | `NET_DOMAIN_BLACKLISTED`, `NET_DOMAIN_NOT_WHITELISTED` | checker_network |
| Path       | `PATH_SENSITIVE_SSH`, `PATH_SENSITIVE_ENV`, `PATH_SENSITIVE_CRED` | checker_path |
| Host       | `HOST_BACKGROUND_PROC`, `HOST_PRIVILEGE_ESC`, `HOST_SESSION_RESIDUAL` | checker_host |
| Env        | `ENV_DENIED_KEY`, `ENV_NOT_ALLOWED`, `ENV_DENIED_VALUE` | checker_env |
| Secret     | `SECRET_IN_COMMAND`                     | checker_secret_cmd / _output |
| Resource   | `RESOURCE_TIMEOUT`, `RESOURCE_OUTPUT_LIMIT`, `RESOURCE_CONCURRENCY`, `RESOURCE_INFINITE_LOOP` | checker_resource |

## Acceptance Criteria

| Criterion | Result |
|-----------|--------|
| 12+ test scenarios pass with structured reports | ✅ 19 scenarios, 100% pass |
| High-risk detection ≥ 90% | ✅ 100% (17/17) |
| Safe false-positive ≤ 10% | ✅ 0% (0/2) |
| Credential/deletion/network detection 100% | ✅ 5/5 detected |
| ≤1s for 500-command scan | ✅ 3.6ms (0.36% of budget, verified by benchmark) |
| Reports include decision, risk_level, rule_id, evidence, recommendation | ✅ |
| Policy file changes take effect without code changes | ✅ YAML/JSON hot-load (8 config sections) |
| Filter/Permission/wrapper denies before execution + audit event | ✅ |
| Audit events include trace_id (when available from context) | ✅ |
| Env whitelist (allowed_keys, denied_keys, deny_values) configured | ✅ `checker_env.go` |
