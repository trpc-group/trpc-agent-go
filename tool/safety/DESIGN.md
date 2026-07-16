# Tool Execution Safety Guard — Design Document

## Table of Contents

1. [What Is the Tool Safety Guard?](#1-what-is-the-tool-safety-guard)
2. [Relationship Between Components](#2-relationship-between-components)
3. [Architecture Overview](#3-architecture-overview)
4. [Rule Catalog](#4-rule-catalog)
5. [Policy Configuration](#5-policy-configuration)
6. [Integration Patterns](#6-integration-patterns)
7. [Decision Aggregation](#7-decision-aggregation)
8. [Fail-Closed Design](#8-fail-closed-design)
9. [Why This Cannot Replace Sandboxing](#9-why-this-cannot-replace-sandboxing)

---

## 1. What Is the Tool Safety Guard?

The Tool Execution Safety Guard is a **pre-execution safety scanner** that inspects commands and code before they are executed by the framework's tool backends. Its responsibilities are:

- **Scan**: Evaluate a pending tool call against a set of configurable safety rules, producing structured `Finding` values that carry a risk level and a recommended decision.
- **Aggregate**: Merge all findings into a single `ScanResult` with a unified decision (`allow`, `deny`, `ask`, or `needs_human_review`) and the highest risk level.
- **Redact**: Remove sensitive data (API keys, tokens, private keys, passwords) from scan results before they leave the guard.
- **Audit**: Record every scan as a JSONL `AuditEvent` for post-incident review.
- **Report**: Produce a structured `Report` that downstream systems can consume.
- **Intercept**: Implement the `tool.PermissionPolicy` interface so the guard can be wired into the framework's execution path and block or defer unsafe calls before they reach the backend.

The guard is **not** a runtime sandbox. It performs static analysis on the command/code text and cannot prevent damage from patterns it does not recognize. See [Section 9](#9-why-this-cannot-replace-sandboxing) for the full discussion.

---

## 2. Relationship Between Components

### shellsafe (`internal/shellsafe`)

**Relationship: The Safety Guard USES shellsafe for command parsing.**

`shellsafe` is a conservative shell command parser that accepts only a safe subset of bash — literal words, single-quoted strings, pure double-quoted strings, joined by the safe operators `|`, `&&`, `||`, and `;`. It rejects all constructs that could introduce arbitrary code execution: command substitution (`$()`, backticks), parameter expansion (`$VAR`), redirections, process substitution, subshells, brace expansion, control flow, background operators, and leading variable assignments.

The safety guard calls `shellsafe.Parse` in two rules:

- **ShellBypassRule (R-SHELL-001)**: If `shellsafe.Parse` returns an error, the command contains unsafe shell constructs and the rule produces a `deny` finding.
- **AllowListMissRule (R-CMD-001)**: If `shellsafe.Parse` succeeds, the rule checks each pipeline segment's executable name against the policy's `allowed_commands` list. If `shellsafe.Parse` fails, the rule defers to ShellBypassRule (which will deny).
- **NetworkEgressRule (R-NET-001)**: Uses `shellsafe.Parse` (via `parseCommandLoose`) to extract the tool name and arguments from the command, then dispatches to tool-specific host extraction logic (`extractCurlHosts`, `extractWgetHosts`, `extractSSHHosts`).

### PermissionPolicy (`tool.PermissionPolicy`)

**Relationship: The Safety Guard IMPLEMENTS this interface via `Guard.CheckToolPermission`.**

`tool.PermissionPolicy` is the framework's permission check interface:

```go
type PermissionPolicy interface {
    CheckToolPermission(ctx context.Context, req *PermissionRequest) (PermissionDecision, error)
}
```

`Guard` satisfies this interface (verified by a compile-time check: `var _ tool.PermissionPolicy = (*Guard)(nil)`). This means the guard can be used anywhere the framework accepts a `PermissionPolicy` — most notably as a `RunOption` via `agent.WithToolPermissionPolicy`.

### workspaceexec (`tool/workspaceexec`)

**Relationship: The Safety Guard EXTRACTS commands from its args and scans them.**

`workspaceexec` provides workspace-scoped command execution. The guard registers an extractor (`extractWorkspaceExec`) that parses the tool's JSON arguments (`command`, `cwd`, `env`, `timeout`, `background`, `pty`) into a normalized `ExecRequest`. The guard then scans the command string with all applicable rules.

Workspace isolation provides a containment boundary (restricted working directory, controlled PATH). The safety guard **complements** this boundary by catching unsafe patterns that could escape or abuse the workspace — for example, commands that access paths outside the workspace, shell metacharacters, or privilege escalation attempts.

### hostexec (`tool/hostexec`)

**Relationship: The Safety Guard applies additional checks specific to host execution.**

`hostexec` provides direct host command execution. The guard registers an extractor (`extractHostExec`) and applies the same base rules as workspaceexec, plus **HostLongSessionRule (R-HOST-001)**, which is scoped to the `hostexec` backend and checks for:

- Privilege escalation (`sudo`, `su`, `doas`)
- Background PTY sessions (both `Background=true` and `PTY=true`)
- Process residue commands (`nohup`, `disown`, `daemon`)
- Large timeout values exceeding policy

These checks are specific to host execution because they involve capabilities that are irrelevant or already controlled in workspace-scoped execution.

### codeexecutor (`codeexecutor/local`, `codeexecutor/container`, `codeexecutor/e2b`)

**Relationship: The Safety Guard scans code blocks for dangerous patterns.**

The guard registers an extractor (`extractCodeExec`) that pulls `code_blocks` from the tool arguments. Code blocks are concatenated with the command string into the scan text, so all rules that inspect the scan text (dangerous commands, secret leakage, resource abuse, etc.) apply to code as well.

- **Sandboxed executors** (`container`, `e2b`): Provide runtime isolation via containers or cloud sandboxes. The safety guard adds **policy enforcement on top** — for example, blocking code that contains secret values even though the container would prevent the secret from persisting, or flagging infinite loops that would waste compute resources inside the container.
- **Local executor** (`local`): No runtime isolation. The safety guard is especially important here because it is the only pre-execution barrier between the model's code and the host.

### Telemetry (OpenTelemetry)

**Relationship: The Safety Guard produces span attributes that OTel instrumentation can consume.**

The guard does not depend on the OTel SDK directly. Instead, it exports span attributes via `SpanAttributes(result ScanResult) map[string]string`:

| Attribute Key             | Value Source          |
|---------------------------|-----------------------|
| `tool.safety.decision`    | `ScanResult.Decision` |
| `tool.safety.risk_level`  | `ScanResult.RiskLevel`|
| `tool.safety.rule_id`     | First finding's `RuleID` |
| `tool.safety.backend`     | `ScanResult.Backend`  |

Callers use `SetSpanAttributes(result, setter)` to attach these to an active OTel span. This enables dashboards and alerts on safety decisions without coupling the guard to any specific observability framework.

### Sandbox

**Relationship: Complementary, NOT replaceable.**

Sandboxing (containers, VMs, cloud sandboxes) provides **runtime isolation**. The safety guard provides **pre-execution static analysis**. They address different threat classes and must both be present for defense in depth. See [Section 9](#9-why-this-cannot-replace-sandboxing) for the full analysis.

---

## 3. Architecture Overview

The guard operates as a pipeline. Each step transforms the data and the pipeline short-circuits on failure (fail-closed).

```text
┌──────────┐    ┌─────────────────────┐    ┌──────────┐    ┌──────────┐
│ Tool Call │───▶│ Guard.Check         │───▶│ Extract  │───▶│  Scan    │
│ Permission│    │ ToolPermission()    │    │ Request  │    │ (Rules)  │
└──────────┘    └─────────────────────┘    └──────────┘    └──────────┘
                                                                      │
                         ┌──────────┐    ┌──────────┐    ┌───────────┘
                         │ Decision │◀───│  Redact  │◀───│ Aggregate │
                         │ (Allow/  │    │ (Secrets │    │ Findings  │
                         │  Deny/   │    │  →[REDACTED])│           │
                         │  Ask)    │    └──────────┘    └──────────┘
                         └────┬─────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
              ┌─────▼─────┐     ┌───────▼───────┐
              │   Audit   │     │    Report     │
              │  (JSONL)  │     │   (JSON)      │
              └───────────┘     └───────────────┘
```

**Step-by-step flow inside `Guard.CheckToolPermission`:**

1. **Extract Request**: Find the registered `Extractor` for the tool name and parse the raw JSON arguments into a normalized `ExecRequest`. If extraction fails → **deny** (fail-closed).
2. **Scan**: Convert the `ExecRequest` to a `ScanInput` and evaluate every registered rule. Each rule returns zero or more `Finding` values.
3. **Aggregate**: Merge all findings into a single `ScanResult`. The decision is the highest-priority decision across all findings (`deny > ask > needs_human_review > allow`). The risk level is the highest severity across all findings.
4. **Redact**: Apply the `Redactor` to strip detected secrets from the command text and finding evidence, replacing them with `[REDACTED]`.
5. **Audit**: Write a JSONL `AuditEvent` to the configured audit writer (file or `io.Writer`).
6. **Report**: Send a structured `Report` to the configured report sink callback.
7. **Decision**: Convert the safety `Decision` to a `tool.PermissionDecision` and return it to the framework.

---

## 4. Rule Catalog

The guard ships 11 built-in rules, evaluated in the order listed below. Each rule implements the `Rule` interface:

```go
type Rule interface {
    ID() string
    Name() string
    Scan(ctx context.Context, input ScanInput, policy PolicyFile) []Finding
}
```

| Rule ID | Name | Risk Level | Decision | Description |
|---------|------|------------|----------|-------------|
| **R-DEL-001** | DangerousCommandRule | `critical` | `deny` | Detects destructive commands (`rm -rf`, `mkfs`, `dd if=`, `format`, `fdisk`) and access to system paths (`/etc/`, `/boot/`, `~/.ssh`, `.env`). |
| **R-CRED-001** | CredentialAccessRule | `critical` | `deny` | Detects attempts to read credential files (`~/.ssh/`, `~/.aws/credentials`, `~/.gnupg/`, `~/.kube/config`, `/etc/shadow`, `.env`, `.credentials`). |
| **R-SHELL-001** | ShellBypassRule | `high` | `deny` | Uses `shellsafe.Parse` to reject unsafe shell constructs (command substitution, parameter expansion, redirections, subshells). Also detects shell wrapper commands (`sudo`, `su`, `doas`, `nohup`, `xargs`, `env`). |
| **R-NET-001** | NetworkEgressRule | `high` | `deny` | Detects network access to domains not in the policy's `network_allowlist`. Parses curl, wget, ssh/scp/rsync arguments via `shellsafe`, extracts URLs from text, and detects Python HTTP client calls in code blocks. |
| **R-HOST-001** | HostLongSessionRule | `high` / `medium` | `deny` / `ask` | Scoped to the `hostexec` backend. Detects privilege escalation (`high`/`deny`), background PTY sessions (`high`/`deny`), process residue commands (`high`/`deny`), and large timeouts exceeding policy (`medium`/`ask`). |
| **R-DEP-001** | DependencyInstallRule | `medium` | `ask` | Detects package installation commands (`go install`, `npm install`, `pip install`, `apt install`, `yum install`, `brew install`, `cargo install`, etc.). Exceptions: commands in `allowed_commands` are skipped. |
| **R-RES-001** | ResourceAbuseRule | `high` / `medium` | `deny` / `ask` | Detects fork bombs and infinite loops (`high`/`deny`), large sleep values >300s (`medium`/`ask`), timeouts exceeding policy (`medium`/`ask`), and output redirection (`medium`/`ask`). |
| **R-SECRET-001** | SecretLeakageRule | `high` | `deny` | Detects API key prefixes (`sk-`, `key-`, `api-key-`), AWS access key IDs (`AKIA...`), PEM private key headers, bearer/authorization tokens, passwords in URLs or flags, and long opaque tokens (≥32 chars). |
| **R-CMD-001** | AllowListMissRule | `medium` | `deny` | Active only when `allowed_commands` is non-empty. Parses the command via `shellsafe.Parse` and denies any executable not in the allow list. Defers to ShellBypassRule if the command cannot be parsed. |
| **R-ENV-001** | EnvPolicyRule | `medium` | `deny` | Checks environment variables against `denied_env_vars` (explicit deny) and `allowed_env_vars` (allowlist enforcement). Any variable in the deny list or not in the allow list (when the allow list is non-empty) triggers a finding. |
| **R-ASK-001** | AskForReviewRule | `low` | `ask` | Flags tool calls where the tool name is in `ask_for_review_tools`, requiring human review before execution. |

---

## 5. Policy Configuration

The safety guard is configured via a `PolicyFile`, which can be loaded from a YAML or JSON file or constructed in code.

### YAML Example

```yaml
version: v1
default_action: deny

# Commands that are always allowed (bypass all deny rules).
allowed_commands:
  - git
  - go
  - node
  - python3

# Commands that are always denied (even if in allowed_commands).
denied_commands:
  - rm
  - mkfs
  - dd

# Filesystem paths that must not be accessed.
denied_paths:
  - ~/.ssh
  - ~/.aws
  - /etc/shadow

# Network endpoints that are permitted. Empty means all access denied.
network_allowlist:
  - api.example.com
  - "*.github.com"

# Maximum command execution timeout in seconds.
max_timeout_sec: 300

# Maximum command output size in bytes.
max_output_bytes: 1048576

# Environment variable names that may be set.
allowed_env_vars:
  - HOME
  - PATH
  - GOPATH

# Environment variable names that must not be set.
denied_env_vars:
  - LD_PRELOAD
  - LD_LIBRARY_PATH
  - BASH_ENV

# Tool names that require human review before execution.
ask_for_review_tools:
  - execute_code
  - exec_command
```

### JSON Example

```json
{
  "version": "v1",
  "default_action": "deny",
  "allowed_commands": ["git", "go", "node", "python3"],
  "network_allowlist": ["api.example.com", "*.github.com"],
  "max_timeout_sec": 300,
  "denied_env_vars": ["LD_PRELOAD", "LD_LIBRARY_PATH"]
}
```

### Loading

```go
// From file (auto-detected: .yaml/.yml → YAML, .json → JSON).
policy, err := safety.LoadPolicyFile("safety-policy.yaml")

// From bytes (try JSON first, fall back to YAML).
policy, err := safety.LoadPolicyFromBytes(data)

// Use default fail-closed policy.
policy := safety.DefaultPolicy()
```

### Overlay Semantics

When loading from a file, the parsed values are **overlaid** onto `DefaultPolicy()`. Fields not specified in the file retain safe defaults. This means:

- Omitting `default_action` → defaults to `deny` (fail-closed).
- Omitting `denied_commands` → the built-in deny list is used.
- Omitting `network_allowlist` → empty list, all network access denied.
- Omitting `max_timeout_sec` → defaults to 300 seconds.

The overlay uses pointer-typed fields internally (`policyFileRaw`) to distinguish "missing from file" (nil) from "zero value in file" (explicit `0` or empty list).

---

## 6. Integration Patterns

### As `tool.PermissionPolicy` via `agent.WithToolPermissionPolicy`

This is the primary integration point. The guard is wired as a run option and the framework calls `CheckToolPermission` before every tool execution:

```go
guard, err := safety.NewGuard(
    safety.WithPolicyFile("safety-policy.yaml"),
    safety.WithAuditFile("safety-audit.jsonl"),
)
if err != nil {
    log.Fatal(err)
}
defer guard.Close()

result, err := agent.Run(ctx,
    myAgent,
    agent.WithToolPermissionPolicy(guard),
)
```

When the guard denies a call, the framework returns a `PermissionResult` to the model instead of executing the tool:

```json
{
  "status": "denied",
  "tool": "workspace_exec",
  "reason": "safety guard: execution denied"
}
```

### As Tool Wrapper via `WrapTool` / `WrapToolSet`

For cases where you need safety scanning on specific tools without wiring a global policy:

```go
// Wrap a single tool.
safeTool := safety.WrapTool(originalTool, guard)

// Wrap all tools in a ToolSet.
safeToolSet := safety.WrapToolSet(originalToolSet, guard)
```

`WrapTool` delegates to the original tool but checks safety before each `Call`. If the guard denies the call, it returns a `PermissionResult` without invoking the inner tool. The wrapper forwards optional interfaces (`MetadataProvider`, `ConcurrencyAware`, `DeferredTool`, `PermissionChecker`) from the inner tool.

### With Audit File Output

Audit events are written as JSONL (one JSON object per line) in append mode:

```go
guard, err := safety.NewGuard(
    safety.WithAuditFile("/var/log/safety-audit.jsonl"),
)
```

Each `AuditEvent` contains:

```json
{
  "timestamp": "2025-06-15T10:30:00.123456789Z",
  "tool_name": "workspace_exec",
  "decision": "deny",
  "risk_level": "critical",
  "rule_id": "R-DEL-001",
  "duration_ms": 1,
  "redacted": true,
  "intercepted": true,
  "backend": "workspaceexec"
}
```

### With Report Sink Callback

For real-time monitoring or custom handling:

```go
guard, err := safety.NewGuard(
    safety.WithReportSink(func(r safety.Report) {
        if r.Decision == safety.DecisionDeny {
            alerting.Send("safety denial", r)
        }
    }),
)
```

### With Custom Extractors

To extend the guard with safety scanning for custom tools:

```go
myExtractor := func(toolName string, args []byte) (safety.ExecRequest, error) {
    // Parse your tool's arguments and return an ExecRequest.
    return safety.ExecRequest{
        Command: parsedCommand,
        Backend: "mybackend",
    }, nil
}

guard, err := safety.NewGuard(
    safety.WithExtractors(map[string]safety.Extractor{
        "my_custom_tool": myExtractor,
    }),
)
```

### With OpenTelemetry Span Attributes

```go
result, decision, err := guard.CheckToolPermission(ctx, req)
// After scanning, attach safety attributes to the current OTel span:
span := oteltrace.SpanFromContext(ctx)
safety.SetSpanAttributes(scanResult, func(k, v string) {
    span.SetAttributes(attribute.String(k, v))
})
```

---

## 7. Decision Aggregation

When multiple rules produce findings, the guard must decide what single action to take. The aggregation follows a strict priority ordering:

```
deny > ask > needs_human_review > allow
```

This is implemented by `aggregateDecision`, which assigns a numeric order to each decision value:

| Decision             | Order (lower = higher priority) |
|----------------------|---------------------------------|
| `deny`               | 0                               |
| `ask`                | 1                               |
| `needs_human_review` | 2                               |
| `allow`              | 3                               |

The aggregated decision is the finding with the **lowest** order value (highest priority). If no findings are produced, the result is `allow`.

**Example**: If R-DEL-001 produces `deny` and R-DEP-001 produces `ask`, the aggregated decision is `deny`.

Risk level aggregation uses the opposite direction — the **highest** severity wins:

```text
critical > high > medium > low > info
```

When the aggregated decision is not `allow`, `ScanResult.Intercepted` is set to `true`.

The safety `Decision` is then mapped to a `tool.PermissionDecision`:

| Safety Decision       | Framework Decision                        |
|-----------------------|-------------------------------------------|
| `allow`               | `AllowPermission()`                       |
| `deny`                | `DenyPermission("safety guard: execution denied")` |
| `ask`                 | `AskPermission("safety guard: human review required")` |
| `needs_human_review`  | `AskPermission("safety guard: human review required")` |
| (unknown)             | `DenyPermission("safety guard: unknown decision")` |

Note: `needs_human_review` is mapped to `AskPermission` because both require human intervention before proceeding.

---

## 8. Fail-Closed Design

The safety guard is designed to **fail closed**: when it cannot determine whether a tool call is safe, it defaults to `deny`. This applies to every point where uncertainty could lead to an unsafe execution:

### Command Cannot Be Parsed by shellsafe

If `shellsafe.Parse` returns an error, `ShellBypassRule` (R-SHELL-001) produces a `deny` finding. The reasoning is that a command containing shell metacharacters that the parser cannot safely interpret may contain injection vectors. Rejecting it is the conservative choice.

### Policy File Cannot Be Loaded

If `WithPolicyFile` is used and the file cannot be read or parsed, the guard keeps the default `DefaultPolicy()` whose `DefaultAction` is `deny`. The `LoadPolicyFile` function returns an error, but `WithPolicyFile` handles it gracefully by retaining the fail-closed default rather than crashing.

### Tool Arguments Cannot Be Extracted

If `extractRequest` fails (e.g., the JSON arguments are malformed), `Guard.CheckToolPermission` returns `DenyPermission` immediately:

```go
execReq, err := extractRequest(req.ToolName, req.Arguments, g.extractors)
if err != nil {
    return tool.DenyPermission(fmt.Sprintf("safety guard: extraction failed: %v", err)), nil
}
```

### Any Rule Encounters an Unexpected Condition

Rules that depend on external state (e.g., `AllowListMissRule` when `shellsafe.Parse` fails) are designed to defer to the more conservative rule (`ShellBypassRule` will deny). Rules do not silently allow on error — they either produce a finding or return no findings (which means "no objection", not "explicitly safe").

### Default Policy Is Deny

`DefaultPolicy()` sets `DefaultAction` to `DecisionDeny`, an empty `NetworkAllowlist` (all network denied), and a non-empty `DeniedEnvVars` list. This ensures that even without a policy file, the guard starts from a maximally restrictive position.

---

## 9. Why This Cannot Replace Sandboxing

The safety guard and sandboxing serve fundamentally different purposes. They are **complementary** and both are required for defense in depth.

### Static Analysis vs Runtime Enforcement

The safety guard performs **static analysis** on the text of commands and code before execution. It can detect known-bad patterns (destructive commands, shell injection, secret leakage) but cannot observe what the code actually does at runtime. A sandbox provides **runtime enforcement** — even if a command passes static analysis, the sandbox constrains its effects (filesystem access, network access, process creation) during execution.

### Known Pattern Matching vs Novel Attack Vectors

The guard's rules match **known patterns**: `rm -rf`, `$()`, `sk-...`, `while true`, etc. A novel attack vector — a creative encoding, a zero-day exploitation technique, or a legitimate-looking command that is unsafe in context — will not be matched. A sandbox does not depend on pattern matching; it restricts capabilities unconditionally.

### The Safety Guard Is a Policy Layer, Not an Isolation Layer

The guard decides whether to **allow or deny** a tool call based on policy. It does not create an isolated execution environment. Even a denied call can be bypassed if the guard's rules are misconfigured or incomplete. A sandbox creates an **isolation boundary** that is enforced by the OS/kernel regardless of policy configuration.

### Both Are Needed for Defense in Depth

| Threat Class | Safety Guard | Sandbox |
|-------------|-------------|---------|
| Known dangerous command (`rm -rf /`) | Caught early, denied before execution | Caught if executed, contained within sandbox |
| Shell injection (`$(curl ...)`) | Caught by ShellBypassRule | Irrelevant if the guard denies it; contained if it doesn't |
| Novel attack pattern | **Missed** — not in rule catalog | **Contained** — sandbox limits damage |
| Secret in command args | Caught by SecretLeakageRule, redacted | No help — secrets are in the text, not in runtime |
| Privilege escalation (`sudo`) | Caught by ShellBypassRule/HostLongSessionRule | Depends on sandbox configuration |
| Resource exhaustion (fork bomb) | Caught by ResourceAbuseRule | Depends on sandbox resource limits |

**Key principle**: The safety guard catches known-bad patterns **early** (before execution), reducing unnecessary sandbox load and providing clear audit signals. The sandbox contains **unknown** threats **at runtime**, preventing damage even when the guard misses something. Neither is sufficient alone; together they form a defense-in-depth strategy.
