# Tool safety guard example

This example scans tool execution requests without executing any sample
command. It demonstrates the opt-in `tool/safety` scanner, strict policy
loading, structured reports, JSONL audit events, and the same decision path
used by `tool.PermissionPolicy`.

## Run

From the `examples` module:

```bash
cd examples
go run ./tool_safety_guard
```

The command validates all samples in `tool_safety_samples.json`, rewrites
`tool_safety_report.json`, and writes one redacted event per scan to
`tool_safety_audit.jsonl`. It exits non-zero if an expected decision or primary
rule changes.

The sample set covers safe `go test`, dangerous deletion, private-key access,
denied and allowlisted network access, a shell wrapper, a pipeline, dependency
installation, a long sleep, unbounded output, a host PTY, unknown source code,
an environment override, a literal secret, and a Python network call.

## Integration

Load the policy at startup, provide a protected audit writer, and install the
scanner as the run permission policy:

```go
policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
if err != nil {
    return err
}
audit, err := os.OpenFile("tool_safety_audit.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
if err != nil {
    return err
}
scanner, err := safety.NewScanner(policy, safety.WithAuditWriter(audit))
if err != nil {
    return err
}

runOption := agent.WithToolPermissionPolicy(scanner)
```

`Scanner` implements `tool.PermissionPolicy`. The framework checks it after
before-tool callbacks have finalized arguments and before the tool call runs.
`deny` and `ask` both skip execution. A host that receives `ask` can obtain
human approval and run again under an explicit allow decision. Direct callers
of `Tool.Call` bypass the Agent permission chain by design; they can call
`Scanner.Scan` themselves.

Interactive `write_stdin` calls require special treatment because a shell
retains incomplete input across calls. An empty `chars` value without
`submit` or `append_newline` is a poll and can be allowed. Every write or
newline submission returns `ask`: the stateless scanner cannot safely combine
the current fragment with prior session input. Approval should consider the
complete session transcript, not only the current fragment.

The scanner infers the built-in schemas:

- `command` plus `cwd`: workspace execution
- `command` plus `workdir`: host execution
- `code_blocks`: code execution
- other `command` fields: generic shell execution

Use `tool_profiles` for renamed or non-standard MCP tools. Profiles accept
dot-separated field paths. Opaque tools marked destructive or open-world in
`tool.ToolMetadata` require review when the scanner cannot inspect an
execution payload.

## Policy semantics

The loader accepts strict YAML or JSON. Unknown fields and invalid limits fail
at startup. File policies must declare `schema_version: v1` and a stable
`policy_id`. Changing the file and restarting, or explicitly loading a new
Scanner, updates commands, forbidden paths, domains, and environment names;
there is no implicit file watcher. Each normalized policy receives a
deterministic SHA-256 revision recorded in reports, audit events, and spans.

Permission requests larger than the scanner input bound are denied before JSON
decoding. A canceled context terminates scanning, including between shell
script lines, and returns an error so the pending tool call fails closed.

An empty command allowlist does not restrict ordinary executable names. Once
`allowed_commands` contains entries, every pipeline segment must be listed.
An empty network-domain allowlist denies network commands. An empty environment
allowlist denies every call-level environment override. Domain entries match
exactly; subdomains require an explicit pattern such as `*.example.com`.

Built-in minimum protections cannot be bypassed by an allowlist. They cover
shell wrappers and unsafe syntax, recursive forced deletion, system mutation,
sensitive credential paths, privilege/process-detachment commands, literal
secret material, and unbounded resource patterns. Dependency installation and
bounded host PTY/background sessions return `ask` by default. A host PTY or
background request without an explicit in-policy timeout is denied.

Network commands that use proxies, target remapping, redirects, external
configuration, SSH forwarding, or equivalent routing options are denied. Once
such an option is present, a literal allowlisted URL no longer proves the actual
connection destination.

Every report includes the aggregate decision, risk level, primary rule,
evidence, recommendation, tool, redacted command preview, command hash,
backend, interception state, and all findings. Aggregation is deterministic:
`deny` outranks `ask`, which outranks `allow`; risk and stable rule order break
ties when selecting the primary rule. The top-level risk level is independently
the highest severity across all findings.

## Execution boundaries

`internal/shellsafe` validates only a conservative shell subset. It accepts
literal argv and safe sequencing operators, then checks each executable. It
rejects substitutions, environment expansion, redirection, backgrounding,
wrappers such as `sh -c`, and unsupported control flow. Multi-line shell code
allows a shebang, blank lines, and full-line comments, then scans each remaining
line. Complex scripts should be reviewed, stored as fixed workspace scripts,
and explicitly allowed by path.

`workspaceexec` runs in the CodeExecutor workspace and can use container, E2B,
local, or sandbox backends. Its actual filesystem and network boundary is the
selected backend, not the static scanner. Configure `WithOutputLimits`, the
executor timeout, environment scrubbing, workspace policy, and session cleanup
to match this policy.

`hostexec` invokes a host shell. It has a default timeout, retained-output line
limit, process-group cleanup, PTY support, and resumable sessions, but it is not
workspace isolation. PTY and background calls need human review and an explicit
timeout. Configure `WithMaxLines`, `WithJobTTL`, a restricted base directory,
and a minimal base environment.

`codeexecutor` backends have different boundaries. Local execution is still
host execution; container and E2B execution depend on their runtime network,
filesystem, resource, and credential configuration. Non-shell source scanning
is intentionally lexical: it detects obvious process, path, network,
dependency, loop, output, and secret patterns but does not prove program
semantics.

The policy's timeout and output values let the scanner reject explicit requests
above a limit. They do not rewrite Tool results or replace executor enforcement.
Generic result truncation would break Tool output schemas, so runtime output
caps remain the responsibility of each executor/tool implementation.

## Audit, telemetry, and sandboxing

Reports and audit events redact common API keys, tokens, passwords, private
keys, URL credentials, and JWTs. They retain a SHA-256 command hash for
correlation. Tool names use the same redaction and length bound, and invalid
backend values are recorded as `unknown` so they cannot inject sensitive or
high-cardinality span data. The configured audit writer is serialized across
concurrent scans; a write error fails closed. Without a writer, reports and span
attributes still exist, but there is no durable audit record.

Passing nil, including a typed nil, to `WithAuditWriter` is a configuration
error returned by `NewScanner`. This distinguishes an intentionally omitted
writer from a broken explicit audit configuration.

When the current context contains an OpenTelemetry span, the scanner sets:

- `tool.safety.schema_version`
- `tool.safety.policy_id`
- `tool.safety.policy_revision`
- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

High-cardinality commands and evidence are not added as span attributes.

The framework's general tool tracing captures full tool arguments and results
by default. Safety report redaction does not modify those surrounding spans.
Production deployments using this guard should explicitly drop both payload
attributes when starting tracing:

```go
clean, err := trace.Start(ctx,
    trace.WithSpanAttributePolicy(
        trace.WithAttributeRule(
            trace.OperationExecuteTool,
            trace.AttrToolCallArguments,
            trace.Drop(),
        ),
        trace.WithAttributeRule(
            trace.OperationExecuteTool,
            trace.AttrToolCallResult,
            trace.Drop(),
        ),
    ),
)
```

This setting is explicit and process-wide. Constructing a Scanner does not
silently change telemetry configuration. The JSONL event records the preflight
decision only; executor-specific completion status, exit codes, process cleanup,
output truncation, and post-execution artifact handling remain separate runtime
responsibilities.

This guard is not a sandbox. Static checks cannot resolve symlinks, mounts,
runtime downloads, obfuscated code, interpreter behavior, kernel access,
resource races, or every data-flow path. Use it as a pre-execution policy and
audit layer in front of a sandbox or other runtime isolation, never as a
replacement for filesystem, network, process, credential, and resource
controls.
