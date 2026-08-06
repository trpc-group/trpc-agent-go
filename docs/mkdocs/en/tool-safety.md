# Tool Execution Safety

The `tool/safety` package provides an opt-in pre-execution guard for tool
arguments. Applications attach it through `tool.PermissionPolicy`. The guard
returns `allow`, `deny`, or `ask`, emits JSONL audit events, and may set
`tool.safety.*` span attributes when a recording span is present.

It does not replace workspace isolation, host-process controls, CodeExecutor
sandboxes, or runtime resource limits.

## Scope

The guard inspects finalized tool JSON arguments before the framework executes
the tool:

- command-like fields: `command`, `args` / `argv`, `stdin`, `code_blocks`
- path-like fields: `path`, `file`, `file_path`, …
- location fields: `uri`, `url`, `href`, `location`, `file_uri`
- environment overrides and secret-shaped keys

Risk families covered by the default policy include destructive commands and
sensitive paths, non-allowlisted network destinations, shell wrappers and
pipelines that feed interpreters, dependency mutation prompts, coarse resource
abuse signals, host-exec session prompts, and credential material in arguments.

Scanning is static and heuristic. Obfuscation, multi-step tool chains, and
behavior chosen by remote data can escape inspection. A clean scan means only
that no configured rule matched.

## Integration

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)
defer guard.Close()

runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

`Guard` implements `tool.PermissionPolicy` directly. Use `safety.Compose` when
several policies must run in sequence; the first non-allow decision wins.

`Policy.CommandLists()` returns allow/deny slices suitable for
`workspaceexec` spawn options so the PermissionPolicy list and the spawn-time
list stay aligned. See `tool/safety/DUAL_POLICY.md`.

PermissionPolicy never observes tool results. Hosts that persist outputs should
use `AfterToolRedact` or `RedactText` / `RedactMap`.

## Policy

`LoadPolicyFile` / `LoadPolicy` decode YAML or JSON with unknown fields
rejected. Omitted deny lists keep `DefaultPolicy` denials (fail-closed). Explicit
empty slices clear those defaults.

Configurable surfaces include allowed/denied commands, denied paths, allowed
hosts, environment-name allowlists, ask-commands, and scan-time size/timeout
hints. Changing the file does not require code changes.

## Relation to other controls

| Mechanism | Role |
|---|---|
| `tool/safety` | Argument scan and permission decision before execution |
| `agent.WithToolFilter` | Visibility of tools to the model |
| `workspaceexec` / `hostexec` / CodeExecutor | Process isolation, env hygiene, timeouts |
| OpenTelemetry / audit JSONL | Observability after a decision |

Defense in depth still requires a sandbox or equivalent isolation for untrusted
workspaces. The guard is a policy boundary, not an execution jail.

## Example and tests

- Example: `examples/tool_safety_guard`
- Acceptance corpus: `tool/safety/testdata/acceptance_corpus.json`
- Flow integration: `internal/flow/processor/safety_guard_integration_test.go`

Package details and residual limitations are documented in
[`tool/safety/README.md`](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/safety/README.md).
