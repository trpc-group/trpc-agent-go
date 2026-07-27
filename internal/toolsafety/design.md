# Tool Execution Safety Guard — Design

## Overview

The Tool Execution Safety Guard (`internal/toolsafety`) provides pre-execution
safety scanning for tools that execute shell commands or code blocks
(workspaceexec, hostexec, codeexec). It produces structured scan reports with
risk findings and execution decisions (allow / deny / ask), and records audit
events for observability.

## Relationship with existing components

| Component | Role | What it does not replace |
|-----------|------|--------------------------|
| `internal/shellsafe` | Command-structure parsing + executable-name policy | Shellsafe does not check network destinations, sensitive file paths, or output content. The Safety Guard reuses shellsafe's parser and builds on its deny list. |
| `tool.PermissionPolicy` | Per-tool-call permission check point | PermissionPolicy is only a pre-flight gate — it does not provide runtime isolation or command-level analysis. The Safety Guard's `SafetyGuardPermissionPolicy` implements this interface. |
| `tool.FilterFunc` | Tool-visibility filter (which tools the model can see) | Filter is tool-name-level, not command-level. The Safety Guard operates inside the tool call, not at registration time. |
| `codeexecutor/sandbox` | OS-level sandbox (bubblewrap, sandbox-exec) | The Safety Guard is a static pre-check; it cannot prevent zero-day exploits or runtime bypasses. Sandbox isolation is still required for untrusted code. |
| OpenTelemetry | Tracing and observability | OTel records events; the Safety Guard makes execution decisions. The Guard emits `tool.safety.*` span attributes for downstream monitoring. |

## Why this is not a sandbox replacement

Static scanning analyses the command string *before* execution. It cannot detect:

- Obfuscated or dynamically constructed commands
- Polymorphic shellcode or encoded payloads
- Runtime exploits in the interpreter or runtime
- Time-of-check to time-of-use (TOCTOU) races where a file changes between the
  scan and execution

Sandbox isolation (bubblewrap, sandbox-exec, container runtimes) constrains what
a process can do *during* execution — filesystem access, network, syscalls. The
two layers are complementary: the Safety Guard rejects obviously dangerous
commands early, while the sandbox limits damage from commands that pass the
static check but turn out to be malicious at runtime.

## Architecture

```
ScanRequest (command + metadata)
    │
    ▼
Scanner.Scan()
    ├── shellsafe.Parse() → structural validation
    ├── Checker[0] (DangerousCmd)  → dangerous patterns, denied commands, sensitive paths
    ├── Checker[1] (NetworkEgress) → network commands, domain whitelist
    ├── Checker[2] (ShellBypass)   → shell wrappers, injection detection
    ├── Checker[3] (ResourceAbuse) → timeouts, output size, sleep loops
    ├── Checker[4] (SensitiveLeak) → credentials in command text
    └── Checker[5] (HostExecRisk)  → PTY sessions, background processes, privilege
        │
        ▼
    decide() → DecisionPolicy → allow / deny / ask
        │
        ▼
    ScanReport → JSON serialization
               → AuditLogger (JSONL)
               → OTel span attributes
```

## Extension

Add a new checker by implementing the `toolsafety.Checker` interface:

```go
type Checker interface {
    ID() string
    Check(ctx context.Context, req *ScanRequest) ([]RiskFinding, error)
    IsEnabled(policy *SafetyPolicy) bool
}
```

Register it with the Scanner via `scanner.Add(checker)`.

## Policy

The `SafetyPolicy` is loaded from a YAML or JSON file and controls:

- Allowed and denied command names (passed to shellsafe)
- Regex-based dangerous patterns
- Network domain whitelist / blacklist
- Sensitive and denied filesystem paths
- Resource usage limits (timeout, output size, sleep)
- Credential patterns for output sanitization
- Decision thresholds (when to deny vs ask)
- Audit output configuration

Modifying the policy file changes behaviour without code changes.
