# Tool Execution Safety Guard

This example applies one policy file to `workspace_exec`, `exec_command`
(`hostexec`), and `execute_code` (`codeexec`) requests before execution. It
scans 14 deterministic samples and writes:

- `tool_safety_report.json`: full scan reports.
- `tool_safety_audit.jsonl`: one compact audit event per decision.

Run it from this directory:

```bash
go run .
```

The checked-in outputs were generated with:

```bash
go run . \
  -policy tool_safety_policy.yaml \
  -report tool_safety_report.json \
  -audit tool_safety_audit.jsonl
```

## Wiring

Load the policy at application startup and install the guard as the run's
permission policy:

```go
policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
if err != nil {
    return err
}
scanner, err := safety.NewScanner(policy)
if err != nil {
    return err
}
guard := safety.NewGuard(
    scanner,
    safety.WithAuditor(safety.NewJSONLAuditor(auditWriter)),
)

events, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithToolPermissionPolicy(guard),
)
```

The runner checks `tool.PermissionPolicy` after argument repair and before
calling the tool. `deny` and `ask` both skip execution. An approval UI should
ask the user and retry with an explicitly approved policy; it must not convert
`ask` to `allow` silently.

Direct calls that bypass the runner need a wrapper:

```go
safeHostTool := safety.WrapTool(hostTool, guard, safety.BackendHost)
safeExecutor := safety.WrapCodeExecutor(
    local.New(),
    guard,
    safety.BackendLocal,
)
```

The wrappers also apply the configured timeout, redact credential-like output,
and truncate aggregate string output. The CodeExecutor wrapper sanitizes
artifact names and inline artifact content.

`tool.FilterFunc` remains useful for coarse exposure control, such as hiding
all host tools from untrusted roles. A filter has no finalized arguments, so it
cannot replace the argument-aware permission check.

## Decisions And Rules

Reports always contain `decision`, `risk_level`, `rule_id`, `evidence`,
`recommendation`, `tool_name`, `backend`, and `blocked`.

- `allow`: no blocking rule matched.
- `deny`: execution is blocked by a non-negotiable policy rule.
- `ask`: execution is blocked pending human review.

The scanner checks command allow/deny lists, credential and system paths,
network domains, shell wrappers and expansions, dependency installation,
privilege escalation, background or PTY host sessions, timeout and concurrency
limits, input and output byte limits, statically unbounded loops or output, and
credential-like literals.
Unknown shell syntax follows `actions.unparsable`, which must be `ask` or
`deny`.

Changing command lists, forbidden paths, allowed domains, limits, environment
variables, or review actions only requires editing and reloading
`tool_safety_policy.yaml`; scanner code does not change.

## Execution Boundaries

### `shellsafe`

`internal/shellsafe` conservatively parses literal argv segments and rejects
substitution, expansion, redirection, background operators, control flow, and
other ambiguous shell features. The guard reuses it before semantic checks and
examines every pipeline segment. Safe pipelines can pass; an unparsable
pipeline never defaults to allow.

### `workspaceexec`

Workspace paths reduce accidental access outside the logical workspace, and
the existing workspace command policy can scrub the child environment on
backends that support `RunProgramSpec.CleanEnv`. A workspace is not
automatically an OS sandbox. The local backend still runs a host process, and
container or remote backends must separately enforce mounts, networking,
process limits, and cleanup.

### `hostexec`

Host commands run through a host shell and have the largest impact radius. PTY
and background requests can create long-lived sessions, retain processes, and
produce large logs. The sample policy denies background execution and asks for
PTY approval. Hostexec already tracks process groups, timeouts, retained
lines, and session cleanup, but a daemon can deliberately detach. Production
hosts still need a restricted identity, a clean environment, cgroups or
equivalent limits, and process supervision.

### `codeexecutor`

The same scanner can guard `local`, `container`, and `e2b` implementations.
`local` is explicitly unsafe because it executes on the host. Container and
E2B backends improve isolation but require their own filesystem, network,
resource, image, and credential policies. The wrapper is useful when code
execution is invoked outside the normal tool permission path.

## Audit And Telemetry

JSONL audit events contain the tool name, decision, risk level, primary rule,
backend, scan duration, redaction status, and whether execution was blocked.
They store a SHA-256 command digest instead of raw command text.

When the context has an OpenTelemetry span, the guard sets:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

Do not add raw commands, environment values, output, or artifacts to span
attributes. Exporters and log pipelines are additional disclosure boundaries.

## Why This Is Not A Sandbox

Static policy cannot prove runtime behavior. An allowed binary can be replaced
after scanning, a script can interpret data as code, a remote service can
return malicious content, and a child can exploit the kernel or escape process
tracking. The guard also cannot recover data already sent before cancellation.

Use this mechanism as defense in depth:

1. Filter tools by role and deployment.
2. Scan finalized arguments with PermissionPolicy or a wrapper.
3. Execute with filesystem, network, identity, resource, and process isolation.
4. Redact results and artifacts.
5. Export audit events, metrics, and traces to monitored storage.
