# Tool Safety Guard

This example demonstrates a pre-execution Tool Execution Safety Guard for
`workspace_exec`, `hostexec` and `execute_code` style requests.

Run:

```bash
go run . \
  -policy tool_safety_policy.yaml \
  -samples samples.json \
  -report tool_safety_report.json \
  -audit tool_safety_audit.jsonl

go run . -policy tool_safety_policy.yaml -demo
```

The guard scans command text, code blocks, environment variables, working
directory hints and backend metadata before execution. It returns `allow`,
`deny` or `ask`, and writes structured reports and JSONL audit events.
The committed report and audit examples normalize timestamps and durations so
reviewers can regenerate them without unrelated diffs.

The sample policy covers:

- allowed and denied commands
- network domain allowlists
- denied host and credential paths
- timeout and output-size limits
- environment variable allowlists
- shell wrapper, pipeline and parse-error handling
- hostexec TTY/background session review

The example policy uses a production-leaning posture for open-world tools:
unknown tools require review, and audit write failures fail closed. Library
defaults remain backward-compatible for applications that have not opted into a
stricter deployment.

The scanner uses `internal/shellsafe` for conservative shell parsing. Commands
that cannot be safely parsed are not silently allowed; the configured
`parse_error_action` decides whether they are denied or sent for human review.

## PermissionPolicy / wrapper integration

The reusable integration point is `tool/safety.PermissionPolicy`:

```go
policy, _ := safety.LoadPolicy("tool_safety_policy.yaml")
guard := safety.NewPermissionPolicy(safety.WithPolicy(policy))
// Pass guard to agent.WithToolPermissionPolicy(...) in the run options.
```

Use `WithStrictPolicyFile` or `LoadPolicyStrict` in CI and production to reject
unknown policy fields and invalid limits before execution.

The bridge parses model-visible tool calls before execution:

- `workspace_exec`: command, cwd, env, timeout, TTY and background fields.
- `workspace_write_stdin`: chars submitted into a running workspace session.
- `hostexec_exec_command` / `exec_command`: command, workdir, env, timeout,
  TTY and background fields.
- `write_stdin`: chars submitted into a running hostexec session.
- `execute_code`: Python, Bash/Shell and other code blocks.
- `skill_run`, `skill_exec`, and `skill_write_stdin`: legacy skill execution
  surfaces with workspace-style command, env, timeout, and stdin semantics.
- Common MCP command wrappers such as `mcp_shell`, `mcp_exec`, and
  `mcp_command`: command-shaped arguments are parsed structurally, while empty
  or nonstandard payloads fall back to raw JSON scanning.
- Custom tool names can be mapped with `WithToolBackend` when an application
  wraps these execution surfaces behind its own tool names.

`PermissionPolicy` scans the raw model-visible arguments. For `hostexec`,
tool-local options such as `WithBaseDir` are only available inside the built-in
tool. Enable `hostexec.WithSafetyScanner` when policy decisions must use the
resolved host working directory instead of the raw `workdir` argument.

Unsupported tools are not blocked by the library defaults. Production
deployments should set `unknown_tool_action: ask` or `deny`, and can also set
`fail_closed_on_unsupported_backend` when every executable tool is expected to
be registered explicitly. `safety.ProductionPolicy()` provides those stricter
defaults for production applications.

Audit write failures are best-effort by library default. The example policy
uses `audit_failure_mode: fail_closed`; use
`WithAuditFailureMode(safety.AuditFailClosed)` when a missing audit record
should block execution.

## Security boundaries

`workspace_exec` runs inside an executor workspace. The guard scans commands
before they reach the workspace, but the workspace backend still owns filesystem
isolation, `CleanEnv`, timeout enforcement, output limits and artifact handling.

`hostexec` runs commands on the host. The guard treats host TTY/PTY sessions and
background jobs as review-worthy because they can retain process state, leave
children behind or interact with a user shell. The guard can block or ask before
the command starts, but process cleanup is still the responsibility of
`hostexec` and the host runtime.

`codeexecutor` backends such as local, container and e2b execute code blocks.
The guard scans block contents before execution and emits structured findings,
but it does not replace container, sandbox or external executor isolation.

When configured with `WithSafetyScanner`, the built-in `workspaceexec`,
`hostexec` and `codeexec` tools scan returned output before handing it back to
the model, and `codeexec` also scans returned output-file content. Command
output, logs and artifact text produced by custom tools or independent
persistence/export paths should still be scanned with `Scanner.ScanOutput`;
`PermissionPolicy` itself only runs before execution.

## Known limits

This is a static pre-execution guard. It can miss obfuscated commands, generated
scripts, indirect downloads and runtime-only data flows. The built-in execution
tools scan returned output when `WithSafetyScanner` is configured, but callers
must route custom output, logs and artifact persistence through `ScanOutput`.
It can also produce false positives for legitimate shell
constructs, large outputs or sensitive-looking test fixtures. Keep sandboxing,
workspace permissions, clean environments, process cleanup, output truncation
and artifact controls enabled.

This mechanism complements sandboxing and executor isolation. It does not
replace sandboxing: static scanning has false positives, false negatives and
bypass risk. Production deployments should combine this guard with workspace
isolation, clean environments, process cleanup, output limits, artifact
controls and OpenTelemetry monitoring.

OpenTelemetry-ready report attributes:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

## Acceptance mapping

| Issue requirement | Where it is covered |
| --- | --- |
| 12 runnable command/script samples | `samples.json`, `main_test.go`, regenerated `tool_safety_report.json` and `tool_safety_audit.jsonl` |
| dangerous delete, credential read and non-allowlisted egress must be detected | `TSG-CMD-001`, `TSG-PATH-001`, `TSG-NET-001` samples and scanner tests |
| high-risk detection and safe false-positive thresholds | `TestScannerAcceptanceRates` |
| 500 command/script scan under the target latency | scanner performance tests and benchmarks in `tool/safety` |
| report contains decision, risk level, rule id, evidence and recommendation | `safety.Report`, `safety.Finding` and example JSON report |
| policy changes do not require code changes | `LoadPolicy`, `LoadPolicyStrict` and this example policy file |
| pre-execution Filter / Permission / wrapper block and audit | `safety.NewPermissionPolicy`, `WithPolicyFile`, `WithAuditFile` and `-demo` |
| shellsafe, PermissionPolicy, workspaceexec, hostexec, codeexecutor, Telemetry and sandbox relationship | sections above and package documentation |
