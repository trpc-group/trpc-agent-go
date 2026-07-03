# Tool Safety Scanner

A pre-execution safety scanner for tool calls. It returns an
`allow / deny / ask` decision for every request before the tool runs,
emits a structured report and an audit log entry, and can be wired
into a Runner via `tool.PermissionPolicy` or attached to any
`tool.ToolSet` via `safety.WrapToolSet`.

## Background

`tRPC-Agent-Go` lets an Agent call Tools, MCP Tools, Skills and the
CodeExecutor to run scripts, invoke external commands, read/write
files and reach the network. These capabilities are the foundation of
agentic automation but also create attack surface:

- A malicious or prompt-injected script can delete files, exfiltrate
  keys, or download second-stage payloads.
- Shell injection can bypass naive denylists.
- A model can install untrusted dependencies or exhaust system
  resources.

`tool/safety` sits in front of every tool call and inspects the
incoming command and code before the executor sees it.

## Supported rules (10)

| Rule ID            | Name                  | Detects                                                                                                  |
|--------------------|-----------------------|----------------------------------------------------------------------------------------------------------|
| `danger_cmd_001`   | Dangerous Command     | `rm -rf /`, `dd`, `mkfs`, `shutdown`, destructive chmod/chown, plus any `rm` with `-r` and `-f` in any order (e.g. `rm -fr /`, `rm --recursive --force /`). |
| `network_002`      | Network Access        | `curl`, `wget`, `nc`, `ssh`, `pip install`, `npm install`, and CodeBlocks calling `urllib`, `requests`, `httpx`, `http.client`, `socket.create_connection`, ...   |
| `shell_bypass_003` | Shell Bypass          | `sh -c`, `eval`, `sudo`, `base64 -d`, and other indirect-execution flags.                                |
| `install_004`      | Install / Mutation    | `apt install`, `npm install`, `go install`, `systemctl enable`, `iptables`, ...                          |
| `hostexec_005`     | Host Exec Risk        | `mount`, `insmod`, `chmod 777`, `nohup`, `setuid`, ... (skipped for non-local executors).                |
| `resource_006`     | Resource Abuse        | `while true`, fork bomb `:(){ :\|:& };:`, `stress`, `dd if=/dev/zero of=`, ...                           |
| `leak_007`         | Sensitive Info Leak   | `api_key=`, `password=`, `bearer ...`, `jwt ...` written to disk via `echo ... >` / `cat ... >`.          |
| `ask_review_008`   | Human Review          | `rm -r`, `git push`, `kubectl delete`, `drop table`, ... (returns `DecisionAsk` instead of `deny`).      |
| `parse_fail_009`   | Parse Failure         | Any command that `internal/shellsafe` rejects (command substitution, redirections, subshells, ...).      |
| `shell_wrapper_010`| Shell Wrapper         | Structural detection of shell wrappers / re-executing builtins (sh, bash, sudo, xargs, eval, ...) via the `shellsafe` parser - immune to encoding bypasses. |

## Quick start

```go
package main

import "trpc.group/trpc-go/trpc-agent-go/tool/safety"

func main() {
    // 1. Create a Scanner and register the rules you want.
    scanner := safety.NewScanner(
        safety.NewParseFailureRule(),
        safety.NewShellWrapperRule(),
        safety.NewDangerousCommandRule(),
        safety.NewNetworkAccessRule(),
        safety.NewShellBypassRule(),
        safety.NewInstallAndMutateRule(),
        safety.NewHostExecRiskRule(),
        safety.NewResourceAbuseRule(),
        safety.NewSensitiveInfoLeakRule(),
        safety.NewAskForReviewRule(),
    )

    // 2. Scan an incoming tool call.
    input := safety.ScanInput{
        Command:      "curl http://evil.com",
        ExecutorType: "local",
    }
    result := scanner.Scan(input)

    // 3. Branch on the decision.
    // result.Decision  → "deny"
    // result.RiskLevel → "high"
    // result.Evidence  → "curl"
    // result.RuleID    → "network_002"
}
```

## Policy file

A YAML policy file lets you override the deny lists without
recompiling. `LoadPolicyFile` starts from `DefaultPolicy()` and
overlays the file values, so an incomplete YAML never silently
disables a security check.

```yaml
# tool/safety/tool_safety_policy.yaml
denied_commands:
  - curl
  - rm -rf
denied_paths:
  - ~/.ssh
  - .env
max_timeout_seconds: 300
max_output_bytes: 10485760
```

```go
policy, err := safety.LoadPolicyFile("tool/safety/tool_safety_policy.yaml")
```

## Structured report

Every scan produces a JSON report:

```json
{
  "tool_name": "exec_command",
  "command": "curl http://evil.com",
  "decision": "deny",
  "risk_level": "high",
  "rule_id": "network_002",
  "evidence": "curl",
  "reason": "network access: curl",
  "recommendation": "command blocked, use a safe alternative"
}
```

## Audit log

Each scan also produces a JSONL event that can be streamed to your
log pipeline:

```jsonl
{"tool_name":"exec_command","command":"ls -la","decision":"allow","risk_level":"none","blocked":false}
{"tool_name":"exec_command","command":"rm -rf /","decision":"deny","risk_level":"critical","blocked":true}
```

## Wiring into the framework

`tool/safety` plugs into the framework at two layers:

| Layer        | How                                                                              |
|--------------|----------------------------------------------------------------------------------|
| Runner       | `agent.WithToolPermissionPolicy(safety.NewGuard())`                              |
| ToolSet      | `safety.WrapToolSet(hostexecTS, safety.NewGuard())`                              |

Both paths are safe to combine: the Runner policy runs first, then
each wrapped tool performs its own check before the inner
implementation runs.

## Relation to existing safety mechanisms

| Component                 | Existing capability                                                              | What this module adds                                                            |
|---------------------------|----------------------------------------------------------------------------------|----------------------------------------------------------------------------------|
| `internal/shellsafe`      | Command-structure parsing (rejects `$()`, backticks, redirections) + per-segment allow/deny. | Adds argument-level semantic checks (paths, domains, resource keywords).        |
| `tool.PermissionPolicy`   | Defines the `allow / deny / ask` interface; callers must implement it themselves.  | `safety.Guard` implements it directly; the framework picks the right translation. |
| `tool/workspaceexec`      | Workspace isolation + `shellsafe` allow/deny.                                    | Scanner applies on top of `shellsafe` and adds path/host/resource checks.        |
| `tool/hostexec`           | Direct host execution, no built-in scanner.                                     | Scanner is the first pre-execution gate for this path.                          |
| `tool/codeexec`           | Code execution (Python/Bash), no content check.                                  | Scanner inspects the code payload for sensitive API calls and installs.          |
| `codeexecutor/container`  | Docker container isolation (process boundary).                                    | Complements the scanner: container isolation + pre-execution scan, defense in depth. |
| `telemetry`               | OTel tracing/metrics.                                                            | Scanner publishes its own span attributes (`tool.safety.*`).                      |

### Why this does not replace a sandbox

The scanner is a **static rule matcher**: it cannot observe
behaviour, intercept syscalls, or stop a process once spawned. For
high-risk inputs (untrusted users, external agents) it must be paired
with `codeexecutor/container`.

```
input command -> Scanner (static rules) -> PermissionPolicy (decision)
              -> Sandbox/Container (runtime isolation)
```

## Performance

- Single command scan: < 1ms
- 500 commands scanned sequentially: < 1s
- All rules are O(n) substring matches; no recursion, no network.

## Directory layout

```text
tool/safety/
├── scanner.go              # Scanner core (Rule interface, decision merge)
├── scanner_test.go
├── scanner_bench_test.go
├── rules.go                # 10 rules: danger/network/bypass/install/host/resource/leak/ask/parse/wrapper
├── rules_test.go
├── parse.go                # internal/shellsafe wrapper (ParseCommand, IsShellWrapper)
├── parse_test.go
├── parse_rules_test.go
├── redact.go               # secret Redactor (14 literal + 4 regex patterns)
├── redact_test.go
├── guard.go                # safety.Guard -> tool.PermissionPolicy bridge
├── guard_test.go
├── wiring.go               # safety.WrapTool / WrapTools / WrapToolSet (pre-exec wiring)
├── wiring_test.go
├── config.go               # YAML policy, ScanReport, AuditEvent, OTel hooks
├── config_test.go
├── samples_test.go         # data-driven tests from testdata/samples.json
├── testdata/samples.json   # 28 sample inputs with expected decision/rule
├── tool_safety_policy.yaml # example policy file
├── tool_safety_report.json # example report
├── tool_safety_audit.jsonl # example audit log
├── README.md               # this file
└── INTEGRATION.md          # architecture + wiring guide
```
