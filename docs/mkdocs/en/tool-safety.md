# Tool Execution Safety

The `tool/safety` package provides an opt-in, conservative guard for commands,
scripts and tool arguments before execution, plus secret-minimizing audit and
explicit result processing. It adds a policy boundary around execution tools;
it does not make arbitrary code safe and does not replace a sandbox.

## Threat model

The guard addresses seven risk families:

1. **Dangerous commands and paths:** destructive commands, recursive root
   deletion, system-directory writes, `.env`, credentials and private keys.
2. **Network egress:** `curl`, `wget`, `nc`, SSH-family clients, custom clients
   and destination-changing options outside an allowlist.
3. **Shell bypasses:** shell `-c` wrappers, `eval`, substitutions, expansions,
   pipelines and redirections that obscure the effective command.
4. **Host execution:** PTY and long-lived sessions, background processes,
   privilege escalation, inherited state and process cleanup.
5. **Dependency or environment mutation:** package installation and persistent
   toolchain or package-manager configuration.
6. **Resource abuse:** excessive timeout/output, long sleeps, obvious infinite
   loops and high concurrency.
7. **Sensitive-data disclosure:** tokens, passwords, private keys and
   credentials in reports, tool output, errors or audit data.

Scanning is heuristic. Obfuscation, custom interpreters, runtime downloads,
indirect imports and behavior selected by remote data can escape static
inspection. A clean scan means only that no configured rule found a problem.

## Policy and decisions

`LoadPolicy` strictly decodes YAML or JSON and overlays explicitly configured
fields on `DefaultPolicy`. Unknown fields and invalid decision values fail.

```yaml
allowed_commands: [go, cat, curl]
denied_commands: [dd, mkfs, sudo]
denied_paths: [/, /etc, /root, ~/.ssh, .env, credentials]
network_allowlist: [github.com]
env_allowlist: [PATH, HOME, TMPDIR]
review_commands: [go install, npm install]
max_timeout_seconds: 30
max_output_bytes: 1048576
parse_error_action: ask
pipeline_action: needs_human_review
```

The fields control executable allow/deny lists, path denial, domain suffix
allowlisting, environment-variable names, commands requiring review, and
requested time/output bounds. Environment values are never allowlisted by
value. An explicitly empty network allowlist denies recognized network
destinations. Default parse errors deny; default pipelines require human
review. Defaults also deny common privilege/system commands and sensitive
paths, review common installers, allow a small environment-name set, cap
timeout at 300 seconds and output at 4 MiB.

`Guard.Scan` returns `allow`, `deny`, `ask` or `needs_human_review` with a risk
level, rule ID, evidence, recommendation, tool, backend and blocked flag.
`deny` is blocked. Both review decisions require an application-controlled
approval path and are not silent allows.

## Conservative shell parsing

The guard reuses `internal/shellsafe` to parse command structure once and apply
command policy to parsed argument vectors. It deliberately rejects shell
wrappers and structures that cannot be reasoned about conservatively. A parse
failure follows `parse_error_action`; an unquoted pipeline follows
`pipeline_action`. Configure either to `allow` only when another control owns
that risk. This parser is not a complete shell interpreter and cannot resolve
all expansions, aliases, sourced files or runtime-generated commands.

## Filter and permission boundaries

The boundaries serve different purposes:

- `agent.WithToolFilter` controls which tools are visible to the model.
- `agent.WithToolExecutionFilter` can defer selected visible calls for the
  caller to execute externally; it does not authorize those external calls.
- `agent.WithToolPermissionPolicy` runs immediately before framework-managed
  execution, after JSON repair and before-tool callbacks have finalized the
  arguments. `safety.NewPermissionPolicy` adapts a `Guard` to this boundary.

Permission scanning sees finalized arguments, maps both guard review decisions
to the framework ask action, and emits at most one preflight audit event. A
filter alone cannot validate arguments that do not exist until the model makes
a call. External execution deferred by a filter needs its own guard wrapper.

## Backend boundaries

### workspaceexec

`workspaceexec` constrains paths to its configured workspace root and validates
the working directory. `CleanEnv` reduces inherited environment exposure, and
callers should enforce timeout and output limits. Workspace path isolation does
not constrain CPU, memory, process creation or network access, and a process
may still exploit the host kernel, mounted sockets, credentials or permissive
filesystem mounts. Resolve symlinks and mount boundaries in the sandbox layer.

### hostexec

`hostexec` runs a host shell. PTY sessions, its long default session timeout,
background processes, inherited environment, privilege-changing commands and
process descendants all enlarge the trust boundary. The guard reviews PTY use,
denies requested background execution and applies effective timeout checks,
but the host must still own process groups, cancellation, descendant cleanup,
environment construction and output capture. Do not expose host execution to
untrusted input without strong isolation.

### codeexec and CodeExecutor

`codeexec` decodes code blocks and delegates execution to a CodeExecutor. The
guard scans every block, routes shell languages through command scanning and
looks conservatively for process/network bridges and resource abuse in common
languages. `codeexecutor/local` runs with local-host privileges;
`codeexecutor/container` can add container isolation when configured with
restricted mounts, user, capabilities and networking; E2B moves execution to
a remote sandbox with its own identity, network and retention controls. The
guard does not make these backends equivalent and does not configure them.

### MCP tools and Skills

MCP tool arguments are open-world JSON supplied under a remote tool contract.
The guard recursively examines raw arguments for executable strings and
network destinations, but cannot understand every server-specific field or
what a remote server does after receiving it. Treat unknown MCP tools and
missing annotations conservatively.

Skills may create persistent sessions or produce commands across multiple
turns. Review the entire session lifetime, backend and cleanup behavior, not
only one command. Re-check every finalized execution and do not treat a prior
approval as an unlimited session grant.

## Audit and telemetry

`NewJSONLAuditSink` writes one concurrency-safe JSON object per line. A
preflight `AuditEvent` contains schema/timestamp/scan correlation, stage, tool,
backend, decision, risk, rule, duration, redaction and interception state. It
intentionally excludes commands, arguments, evidence, environment values and
results. Store audit files with restricted permissions, rotation, retention
and access control.

`AuditBestEffort` preserves the scan decision if a sink fails.
`AuditRequired` fails closed for an otherwise allowed permission check; an
already blocked decision remains blocked. Monitor sink failures separately so
best-effort mode does not hide an outage. The permission integration adds
exactly these five OpenTelemetry span attributes:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`
- `tool.safety.blocked`

Keep attribute values low-cardinality; do not add commands, paths or secrets.

## Processing execution results

`ResultProcessor` is explicit and is never injected into callbacks. In a
direct wrapper that retains the `Report`, execute the tool and its normal
callbacks first, then call `ResultProcessor.Process(ctx, report, result, err)`.
It copies through JSON, recursively redacts sensitive names and values,
single-lines and redacts the execution error, and enforces `max_output_bytes`
over the complete serialized `ProcessedResult`. It then emits a correlated
`post_execute` event.

The framework permission adapter returns a permission decision rather than its
internal report. Applications that need correlated post-processing must retain
the report in their own direct execution wrapper; do not claim that callback
integration is automatic. Treat `AuditRequired` post-execution failure as an
operational failure even though a safe processed value may also be returned.

## Defense in depth

Static and policy checks run before behavior occurs, so they cannot stop a
permitted binary that later changes behavior, enforce kernel resources, revoke
network packets or reliably kill descendants. Production controls still need:

- kernel, VM or container isolation with a non-root identity and restricted
  mounts/capabilities;
- independent network egress controls and DNS/proxy policy;
- minimal, short-lived credentials and a clean environment;
- CPU, memory, process, timeout and total-output quotas;
- process-group cleanup, artifact limits and audit retention; and
- human review for ambiguous, high-impact or persistent-session operations.

Use the guard to reject obvious unsafe requests early and make decisions
observable. Use the sandbox and operating environment to contain everything
that still executes.

The runnable [scan-only example][tool-safety-example]
includes twelve scenarios plus deterministic report and audit fixtures.

[tool-safety-example]: https://github.com/trpc-group/trpc-agent-go/tree/main/examples/tool_safety_guard
