# Tool Execution Safety Guard

`internal/toolsafety` provides pre-execution safety scanning for tools that run
shell commands or code blocks (workspaceexec, hostexec, codeexec).

## Quick start

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
    "trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

func main() {
    // 1. Load policy from file (or use DefaultPolicy()).
    policy, err := toolsafety.LoadPolicy("tool_safety_policy.yaml")
    if err != nil { panic(err) }

    // 2. Create scanner and register checkers.
    scanner := toolsafety.NewScanner(policy)
    scanner.Add(checkers.NewDangerousCmdChecker(policy))
    scanner.Add(checkers.NewNetworkEgressChecker(policy))
    scanner.Add(checkers.NewShellBypassChecker())
    scanner.Add(checkers.NewResourceAbuseChecker(policy))
    scanner.Add(checkers.NewSensitiveLeakChecker(policy))
    scanner.Add(checkers.NewHostExecRiskChecker())

    // 3. Use as PermissionPolicy wrapper.
    guard := toolsafety.NewSafetyGuardPermissionPolicy(scanner)

    // 4. Attach to workspace_exec (or hostexec, codeexec).
    _ = workspaceexec.NewExecTool(executor,
        workspaceexec.WithSafetyScanner(scanner),
    )
}
```

## Scanning manually

```go
report, err := scanner.Scan(ctx, &toolsafety.ScanRequest{
    ToolName: "workspace_exec",
    Command:  "curl http://evil.com",
    Backend:  "workspaceexec",
})
if report.Decision == toolsafety.DecisionDeny {
    // Command was rejected.
    fmt.Println(report.Findings[0].Evidence)
}
```

## Audit logging

```go
logger, err := toolsafety.NewAuditLogger(policy.AuditPolicy)
guard.WithAuditLog(logger.Logger())
```

## Output sanitization

Sensitive patterns (API keys, tokens, private keys) are automatically redacted
from audit evidence by the `Sanitize` method on `AuditLogger`.

## Policy file

Example policy: see `testdata/tool_safety_policy.yaml`.

Modify the YAML file to change allowed commands, denied paths, network domains,
resource limits, or decision thresholds — no code changes needed.

## Checkers

| Checker | Risk dimension | Policy fields used |
|---------|---------------|-------------------|
| DangerousCmdChecker | Dangerous commands, destructive paths, sensitive files | DeniedCommands, DangerousPatterns, PathPolicy |
| NetworkEgressChecker | Network access to non-whitelisted domains | NetworkPolicy |
| ShellBypassChecker | Shell wrappers (sh, eval, sudo), command injection | (always enabled) |
| ResourceAbuseChecker | Timeout, output size, sleep loops | ResourcePolicy |
| SensitiveLeakChecker | Credentials in command text | SensitivePatterns |
| HostExecRiskChecker | PTY sessions, background processes, privilege escalation | (always enabled, hostexec only) |
