# Tool Safety Guard Example

This example runs the `tool/safety` scanner against offline command and tool-call samples. It does not call an LLM and does not execute the scanned commands.

The example demonstrates:

- loading `tool_safety_policy.yaml`
- scanning workspace, host, codeexec, and unknown tool surfaces
- producing `tool_safety_report.json`
- producing `tool_safety_audit.jsonl`
- preserving the distinction between scanner decisions and framework permission actions

The ordinary `env_allowlist` is not an override for process-control variables
such as `PATH`; those variables remain denied even when listed in a policy.

## Run

```bash
go run .
```

The command writes outputs next to the policy file:

- `tool_safety_report.json`
- `tool_safety_audit.jsonl`

The checked-in JSON and JSONL files are intentional reference samples for CI and reviewers. Running the example locally regenerates them, so review any diff before committing.

Expected stdout includes decisions for safe and risky samples:

```text
safe_go_test               decision=allow              risk=low      rule=evaluation.none
dangerous_rm_rf            decision=deny               risk=critical rule=command.dangerous_delete
dependency_install         decision=ask                risk=high     rule=dependency.install
human_review_custom        decision=needs_human_review risk=high     rule=unknown.requires_review
```

## Verify

Run the focused package tests and the offline example:

```bash
go test ./tool/safety ./tool/codeexec ./internal/shellsafe

cd examples
go test ./tool_safety_guard

cd tool_safety_guard
go run .
```

After `go run .`, confirm generated reports do not contain raw sensitive sample paths:

```bash
grep -E '~/.ssh|id_rsa|\.env' tool_safety_report.json tool_safety_audit.jsonl
```

The grep command should print no matches. If you are only experimenting locally, reset or remove regenerated artifacts after verification:

```bash
rm -f tool_safety_report.json tool_safety_audit.jsonl
```

## PermissionPolicy Integration

Use the scanner through `agent.WithToolPermissionPolicy` when running an agent:

```go
policy, _ := safety.LoadPolicyFile("tool_safety_policy.yaml")
scanner, _ := safety.NewDefaultScanner(policy)

events, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithToolPermissionPolicy(
        safety.NewPermissionPolicy(scanner),
    ),
)
```

The framework still uses `tool.PermissionActionAllow`, `tool.PermissionActionDeny`, and `tool.PermissionActionAsk`. A scanner decision of `needs_human_review` is preserved in reports and audit events, but maps to framework `ask` so the tool is not executed.

## Security Boundary

The safety guard is a pre-execution scanner and audit mechanism. It does not replace sandboxing, container isolation, filesystem policy, network policy, timeouts, process cleanup, or output limits.

Use it together with:

- `internal/shellsafe` for conservative shell parsing
- `tool.PermissionPolicy` for execution-before-call interception
- `workspaceexec` for workspace-scoped shell execution
- `hostexec` only for trusted host-side automation with stricter policy
- `codeexecutor` sandbox/container backends for runtime isolation
- OpenTelemetry span attribute policy to omit existing raw tool arguments/results in sensitive deployments
