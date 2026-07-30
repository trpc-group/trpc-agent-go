# Tool Execution Safety Guard

This example demonstrates a reusable pre-execution guard for shell-like tool
calls. It scans pending `workspace_exec`, `exec_command`, and `execute_code`
requests, returns `allow`, `deny`, or `ask`, and writes structured reports plus
JSONL audit events.

The sample program does not execute the commands. It only shows the decision
that would be made before a tool call reaches the executor.

## Background And Non-Goals

Agent tools can execute commands, run code, access files, start processes, and
connect to external services. These capabilities are necessary for useful
automation, but untrusted model output or tool arguments may delete files, read
credentials, exfiltrate data, install unreviewed dependencies, consume
unbounded resources, or bypass command policy through shell features.

The Tool Execution Safety Guard adds a conservative pre-execution policy gate.
It normalizes tool arguments, parses supported shell structures, applies
backend-aware rules, returns `allow`, `ask`, or `deny`, and records a compact
audit event before execution.

The guard is not:

- a complete Bash or operating-system command interpreter;
- a kernel-level filesystem, network, process, or syscall sandbox;
- a replacement for executor time, memory, output, disk, or process limits;
- proof that an allowlisted binary or dependency is trustworthy;
- a human-approval user interface;
- protection against every behavior generated dynamically after execution.

Production deployments must combine this guard with runtime isolation,
resource limits, network policy, secret isolation, telemetry, process cleanup,
and human approval for high-risk operations.

## Threat Model

All model-generated tool arguments are treated as untrusted. Arguments modified
by callbacks, explicit environment overrides, working directories, tool
metadata, and tool results may also contain unsafe behavior or secrets.

The main protected assets are:

| Asset | Example impact |
| --- | --- |
| Workspace source and user files | deletion, overwrite, or unauthorized modification |
| Credentials and private keys | account takeover or remote repository access |
| Host filesystem and configuration | persistence, privilege escalation, or system damage |
| Network access | data exfiltration or unreviewed downloads |
| Runtime resources | CPU, memory, disk, process, or execution-time exhaustion |
| Tool output, reports, and audit logs | secret leakage or persistent sensitive artifacts |
| Agent authorization boundary | execution beyond what the user approved |

The main untrusted inputs are:

| Input | Example risk |
| --- | --- |
| Command, script, or code block | destructive or dynamically generated behavior |
| `cwd` or `workdir` | access outside the intended directory boundary |
| Environment key and value | unsafe overrides or secret leakage |
| Timeout, output, and concurrency limits | resource exhaustion |
| PTY, background, and yield flags | long-lived host sessions or residual processes |
| URL and remote host | non-allowlisted network access |
| Tool metadata | an unknown or destructive tool being treated as safe |
| Before-tool callback changes | scanning different arguments from those actually executed |
| Tool output | API keys, tokens, passwords, or private keys |

The guard assumes that attackers may use quoting, command wrappers, pipelines,
sequencing, substitution, expansion, redirection, path variants, domain
boundaries, and mixed rule hits. Unsupported or ambiguous command structures
must not silently become `allow`.

## Architecture And Execution Order

The framework execution order is:

```text
LLM-generated ToolCall
    |
    v
JSON argument repair
    |
    v
Plugin BeforeTool callbacks
    |
    v
Local BeforeTool callbacks
    |
    v
Tool PermissionChecker
    |
    v
Run-level ToolPermissionPolicy
    |
    +-- deny / ask / error --> Tool.Call is skipped
    |
    v
Tool.Call
    |
    v
Plugin AfterTool callbacks
    |
    v
Local AfterTool callbacks
    |
    v
Result returned to the model
```

The scanner itself uses this order:

```text
normalize ScanRequest
    |
    v
shellsafe.Parse
    |
    +-- parse error --> SAFE-SHELL-PARSE
    |
    v
shellsafe command policy and pipeline checks
    |
    v
semantic command, path, network, dependency, and resource checks
    |
    v
environment, backend, PTY, background, session, timeout, output, and concurrency checks
    |
    v
stable finding aggregation
    |
    v
report redaction, OTel attributes, and JSONL audit
```

Semantic checks still run after a shell parse failure with conservative textual
evidence. A parse failure therefore cannot hide a more severe delete, path,
network, dependency, infinite-loop, or secret finding.

Post-execution and storage containment use separate boundaries:

```text
Tool result -> redacting AfterTool callback -> model-visible result
Artifact data -> RedactingArtifactService -> artifact storage
Tool failure -> low-sensitivity failure summary -> application log
```

The artifact wrapper is required because an artifact may be stored before the
tool returns. An after-tool callback cannot sanitize data that has already
crossed the artifact storage boundary.

## Quick Start And 12 Samples

From the repository root:

```bash
cd examples/tool_safety_guard
GOCACHE=/tmp/trpc-agent-go-cache go run .
```

The example loads `tool_safety_policy.yaml`, scans only, and never calls
`exec.Command`, `Tool.Call`, `workspace_exec`, `hostexec`, or a `CodeExecutor`.
It prints a decision summary and recreates:

- `tool_safety_report.json`, containing 12 full scan reports;
- `tool_safety_audit.jsonl`, containing one compact event per scan.

Custom paths are supported:

```bash
go run . \
  -policy ./tool_safety_policy.yaml \
  -report /tmp/tool_safety_report.json \
  -audit /tmp/tool_safety_audit.jsonl
```

| # | Sample | Expected decision | Primary Rule ID |
| --- | --- | --- | --- |
| 1 | `go test ./tool/safety` | `allow` | `SAFE-ALLOW` |
| 2 | `rm -rf /` | `deny` | `SAFE-DANGEROUS-DELETE` |
| 3 | `cat ~/.ssh/id_rsa` | `deny` | `SAFE-SENSITIVE-PATH` |
| 4 | non-allowlisted `curl` | `deny` | `SAFE-NETWORK-DOMAIN` |
| 5 | allowlisted `curl` to `github.com` | `allow` | `SAFE-ALLOW` |
| 6 | `sh -c ...` wrapper | `deny` | `SAFE-COMMAND-POLICY` |
| 7 | pipeline | `ask` | `SAFE-SHELL-PIPELINE` |
| 8 | dependency installation | `ask` | `SAFE-DEPENDENCY-INSTALL` |
| 9 | long `sleep` | `ask` | `SAFE-RESOURCE-TIMEOUT` |
| 10 | oversized requested output | `ask` | `SAFE-RESOURCE-OUTPUT` |
| 11 | hostexec PTY and background session | `ask` | `SAFE-HOSTEXEC-PTY` |
| 12 | non-allowlisted environment key | `ask` | `SAFE-ENV-VAR` |

The example is a separate Go module under `examples/go.mod`. Run its tests from
the repository root with:

```bash
GOCACHE=/tmp/trpc-agent-go-cache \
  go -C examples test ./tool_safety_guard -count=1
```

## Package Shape

The reusable implementation lives in `tool/safety`:

- `Policy` loads strict YAML or JSON configuration.
- `ScanRequest` normalizes safety-relevant tool arguments and metadata.
- `Scanner` parses, checks, aggregates, redacts, audits, and emits OTel fields.
- `PermissionPolicy` adapts `Scanner` to `tool.PermissionPolicy`.
- `JSONLAuditor` serializes compact concurrent-safe JSONL records.
- `RedactString`, `RedactValue`, and `NewRedactingAfterToolCallback` remove
  recognized secrets before data crosses report, audit, or tool-result
  boundaries.
- `NewRedactingArtifactService` protects artifact storage: it redacts text,
  rejects recognized secrets in binary data, and delegates non-save methods.

`internal/shellsafe` is a conservative structure and command-policy gate, not a
complete shell interpreter. Passing it means only that the supported shell
shape and command policy were accepted; it does not prove the invoked binary is
safe.

## Policy Semantics

### Policy Fields

`LoadPolicyFile` starts with `DefaultPolicy`, then overlays fields present in
the YAML or JSON file. Missing fields retain their defaults. Explicit zero or
invalid values do not restore defaults and are rejected during validation.

| Field | Purpose | Default |
| --- | --- | --- |
| `allowed_commands` | Commands eligible to pass command-policy checking; other rules still apply. | Common read, build, test, and network tools |
| `denied_commands` | Commands rejected by command policy; denial takes precedence over the allowlist. | `rm`, `nc`, `netcat`, `ssh`, `scp`, `sftp` |
| `forbidden_paths` | Sensitive paths and credential markers that commands must not access. | SSH, Git, Docker, Kubernetes and cloud credential files, dotenv files, process environments, mounted secrets, system password files, Docker socket |
| `network_allowlist` | Exact hosts and real subdomains that network operations may access. | `github.com`, `proxy.golang.org`, `sum.golang.org` |
| `env_allowlist` | Environment names that callers may explicitly override. Backend-specific rules remain stricter. | `PATH`, `HOME`, `TMPDIR`, `GOCACHE`, `GOMODCACHE`, `GOPATH` |
| `max_timeout_sec` | Maximum requested execution timeout in seconds. | `300` |
| `max_output_bytes` | Maximum requested output size in bytes. | `4194304` (4 MiB) |
| `max_concurrency` | Maximum recognized command concurrency, such as `go test -p`, `xargs -P`, or `make -j`. | `128` |
| `parse_failure_action` | Decision when a command cannot be parsed safely. | `ask` |
| `unknown_tool_action` | Decision for an unknown execution tool or backend. | `ask` |
| `dependency_action` | Decision for dependency installation or environment changes. | `ask` |
| `pipeline_action` | Decision for pipelines or command sequencing. | `ask` |
| `host_pty_action` | Decision when host execution requests a PTY. | `ask` |
| `background_action` | Decision when a tool requests background execution. | `ask` |
| `disallowed_env_action` | Decision for an environment name outside `env_allowlist`. | `ask` |

Action fields accept only `allow`, `ask`, or `deny`; empty action fields become
`ask`. Timeout, output, and concurrency limits must be positive. Unknown
YAML/JSON fields, invalid environment names, invalid hosts, trailing JSON
values, and multiple YAML documents are rejected.

Important policy boundaries:

- invalid policy, invalid tool arguments, scanner errors, and audit errors fail
  closed;
- `denied_commands` takes precedence over `allowed_commands`;
- command allowlisting never bypasses path, network, resource, environment, or
  secret rules;
- host matching accepts only an exact allowlisted host or a real subdomain, so
  `evilgithub.com` and `github.com.evil.example` do not match `github.com`;
- command-aware network extraction covers common `curl` and `wget` targets,
  including bare host/path values, `curl --url`, HTTP(S), FTP(S), and Git
  SCP-like remotes such as `git@host:owner/repo.git`; output filenames and
  quoted documentation passed to `echo` or `printf` are not network targets;
- explicit Git proxy and URL rewrite configuration is scanned. Network targets
  loaded indirectly from curl/wget configuration files, named Git remotes, or
  `.gitmodules` cannot be proven allowlisted and therefore require review;
- sensitive filename variants include `.env.local`, `.env.production`,
  `credentials.json`, `credential.yaml`, Git credentials, Docker/Kubernetes
  configuration, mounted secret directories, and cloud application-default
  credentials, while documentation/template forms such as `.env.example`,
  `.env.template`, and `credentials.md` remain safe counterexamples;
- hostexec rejects explicit `PATH`, `HOME`, `BASH_ENV`, `ENV`, `SHELLOPTS`,
  `BASHOPTS`, and `PROMPT_COMMAND` overrides even when a name appears in the
  general environment allowlist, because they can change which program or
  shell startup code runs;
- changing command, path, domain, environment, or action fields changes
  behavior after policy reload without a code change.

### Rule IDs And Decision Priority

| Rule ID | Meaning | Typical decision | Risk |
| --- | --- | --- | --- |
| `SAFE-ALLOW` | No safety rule matched. | `allow` | `low` |
| `SAFE-UNKNOWN-TOOL` | The tool or execution backend is not recognized. | configurable, default `ask` | `medium` or `high` |
| `SAFE-SHELL-PARSE` | The command could not be parsed as a supported shell structure. | configurable, default `ask` | `medium` |
| `SAFE-CODE-POLICY` | A code language or dynamic operation cannot be conservatively verified. | configurable or `ask` | `medium` or `high` |
| `SAFE-COMMAND-POLICY` | A command is denied, not allowlisted, or is an unsafe wrapper or builtin. | `deny` | `high` |
| `SAFE-DANGEROUS-DELETE` | Recursive, forced, or system-targeted deletion was detected. | `deny` | `critical` |
| `SAFE-SENSITIVE-PATH` | A forbidden path or credential marker was referenced. | `deny` | `critical` |
| `SAFE-NETWORK-DOMAIN` | A network operation targets a non-allowlisted host. | `deny` | `high` |
| `SAFE-SHELL-PIPELINE` | A pipeline or command-sequencing structure was detected. | configurable, default `ask` | `medium` |
| `SAFE-DEPENDENCY-INSTALL` | A dependency installation or environment change was detected. | configurable, default `ask` | `high` |
| `SAFE-RESOURCE-TIMEOUT` | A timeout or sleep duration exceeds policy. | `ask` | `medium` |
| `SAFE-RESOURCE-OUTPUT` | A requested output limit exceeds policy. | `ask` | `medium` |
| `SAFE-RESOURCE-CONCURRENCY` | Recognized command concurrency exceeds policy. | `ask` | `high` |
| `SAFE-RESOURCE-INFINITE-LOOP` | An obvious loop or unbounded wait was detected. | `deny` | `high` |
| `SAFE-HOSTEXEC-PTY` | Host execution requests an interactive PTY session. | configurable, default `ask` | `high` |
| `SAFE-HOSTEXEC-SESSION` | Non-PTY host execution may return a running session after its yield interval. | `ask` | `medium` |
| `SAFE-HOSTEXEC-PATH` | Host execution explicitly overrides executable lookup through `PATH`. | `deny` | `critical` |
| `SAFE-HOSTEXEC-STARTUP-ENV` | Host execution overrides an execution-affecting variable such as `HOME`, `BASH_ENV`, a dynamic-loader variable, or Git helper configuration. | `deny` | `critical` |
| `SAFE-BACKGROUND-PROCESS` | Background execution was requested. | configurable, default `ask` | `medium` |
| `SAFE-VCS-MUTATION` | A version-control operation can delete untracked files or discard workspace changes. | `ask` | `high` |
| `SAFE-ENV-VAR` | An explicit environment name is outside the allowlist. | configurable, default `ask` | `medium` |
| `SAFE-SECRET-REDACTION` | A command or environment value contains a recognized secret. | `deny` | `critical` |
| `SAFE-AUDIT-FAILURE` | The audit event could not be recorded. | `deny` | `high` |

All findings are retained. The primary finding is selected first by decision
priority and then by risk priority:

```text
deny > ask > allow
critical > high > medium > low
```

Decision priority always wins, so a low-risk `deny` outranks a critical-risk
`ask`. Equal decision and risk retain discovery order, making the primary Rule
ID stable. `blocked` is true whenever the final decision is not `allow`.

An audit write failure changes the report to `SAFE-AUDIT-FAILURE`, blocks
execution, and returns an error.

## PermissionPolicy Integration

The relevant framework order is:

```text
BeforeTool callbacks
    -> tool.PermissionChecker
    -> run-level ToolPermissionPolicy
    -> Tool.Call
    -> AfterTool callbacks
```

The safety policy therefore scans the final arguments produced by before-tool
callbacks. A callback cannot change a previously scanned safe command into an
unscanned dangerous command. `allow` reaches `Tool.Call` once; `deny`, `ask`,
argument-extraction errors, scanner errors, and audit errors skip `Tool.Call`.

Minimal integration:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
if err != nil {
    return err
}

scanner := safety.NewScanner(
    policy,
    safety.WithAuditor(
        safety.NewJSONLAuditor("tool_safety_audit.jsonl"),
    ),
)

callbacks := tool.NewCallbacks()
callbacks.RegisterAfterTool(
    safety.NewRedactingAfterToolCallback(),
)

baseArtifactService := inmemory.NewService()
safeArtifactService := safety.NewRedactingArtifactService(
    baseArtifactService,
)

guardedAgent := llmagent.New(
    "guarded-agent",
    llmagent.WithToolCallbacks(callbacks),
    // Add workspace_exec, exec_command, execute_code, or other tools here.
)
_ = guardedAgent

runOption := agent.WithToolPermissionPolicy(
    safety.NewPermissionPolicy(scanner),
)
_ = runOption // Pass this option when invoking the agent.

runnerOption := runner.WithArtifactService(safeArtifactService)
_ = runnerOption // Pass this option when constructing the runner.
```

The after-tool callback is a defense-in-depth output redactor. It runs only
after an allowed tool returns and does not replace the pre-execution permission
decision. The artifact wrapper must be installed on every artifact service used
by guarded execution; it protects the earlier save boundary that AfterTool
cannot reach.

## Backend Boundaries

The three extractors use explicit tool contracts rather than guessing fields
from an untyped map:

| Tool | Safety backend | Safety-relevant input contract | Execution and remaining boundary |
| --- | --- | --- | --- |
| `workspace_exec` | `workspaceexec` | `command`, `cwd`, `env`, timeout aliases, `max_output_bytes`, `background`, and `tty`/`pty` | Runs through a configured `CodeExecutor` workspace. `cwd` is workspace-relative. Output retention defaults to 4 MiB and reports `truncated: true` when exceeded, but actual filesystem, network, environment, session, and other resource isolation depend on that executor. |
| `exec_command` | `hostexec` | `command`, `workdir`, `env`, `yield-time_ms`/`yieldMs`, timeout aliases, `max_output_bytes`, `background`, and `tty`/`pty` | Runs in a host shell. Output retention defaults to 4 MiB for foreground and session execution. PTY, background or yielded sessions, privilege wrappers, inherited host access, and process cleanup remain high-risk surfaces. It is not a sandbox. |
| `execute_code` | `codeexec` | required `code_blocks`, accepted as an array, one object, or double-encoded JSON | Delegates to local, container, e2b, or another `CodeExecutor`. Shell blocks use shellsafe. Python and JavaScript/TypeScript use conservative import, alias, literal, risky-API and constant-loop analysis; Go uses its parser and AST plus import and function-alias resolution. Dynamic filesystem or network targets, unresolved risky capabilities, risky wildcard/dot imports, syntax failures and unsupported languages require review. This is not complete data-flow analysis. |

`workspace_exec.cwd` and `exec_command.workdir` are different contracts and must
not be interchanged. Timeout extraction mirrors execution precedence:
`timeout_sec`, then legacy `timeoutSec`, then the workspace-only `timeout`.
Both execution tools and the supported CodeExecutor runtimes accept and enforce
`max_output_bytes` across combined stdout and stderr. The sandbox runtime uses
one shared collector for both streams, so each stream cannot independently
consume the full allowance. Runtimes continue draining excess child output to
avoid blocking the process, discard bytes beyond the limit, and expose
`truncated: true`. Omitting the field uses a 4 MiB execution default. The safety
policy separately reviews a requested limit that exceeds its configured
maximum.

The shell policy rejects known secondary-executor forms even when the outer
binary is allowlisted. These include `find -exec`/`-delete` and output actions,
Awk process, pipe, redirected `getline`, and external-program forms, Sed
execute/read/write or external-script forms, and Git command-producing
configuration such as aliases, external diff commands, SSH commands, helpers,
filters, hooks, or included config. Git object paths are separately checked for
sensitive files.

Host extraction also mirrors executor defaults. `yield-time_ms` takes precedence
over `yieldMs`; omission or a negative value uses `10000` ms. `timeout_sec`
takes precedence over `timeoutSec`; omission or a non-positive value uses
`1800` seconds. Consequently, a bounded non-PTY foreground request should set
`yield-time_ms: 0` and an explicit timeout within policy. A positive effective
yield may return a running session and produces `SAFE-HOSTEXEC-SESSION`.

Host execution unconditionally rejects explicit overrides that can change shell
startup, executable loading, language startup, or Git helper behavior. This
includes `PATH`, `HOME`, shell startup variables, dynamic-loader variables,
`PYTHONPATH`/`PYTHONSTARTUP`, `NODE_OPTIONS`, and Git config or SSH command
overrides. Separately, proxy environment variables are checked against the
network allowlist even if a policy explicitly includes their names in
`env_allowlist`.

Unknown tools use backend `unknown` and default to `ask`. Destructive tool
metadata upgrades the result to `deny`.

## Reports, Audit, Telemetry, And Redaction

### Scan report

Each `ScanReport` contains:

| Field | Purpose |
| --- | --- |
| `decision` | Final `allow`, `ask`, or `deny` |
| `risk_level` | Risk of the primary finding |
| `rule_id` | Stable primary Rule ID |
| `evidence` | Redacted evidence from all findings |
| `recommendation` | Remediation for the primary finding |
| `tool_name` | Requested tool |
| `command` | Redacted normalized scan text |
| `backend` | `workspaceexec`, `hostexec`, `codeexec`, or `unknown` |
| `blocked` | `true` unless decision is `allow` |
| `redacted` | Whether recognized secret material was removed |
| `duration_ms` | Scan duration in milliseconds |
| `findings` | Complete, deterministically ordered findings |
| `otel_attributes` | Low-cardinality safety attributes |

The example writes the report with mode `0600` and reapplies that mode when the
target file already exists.

### JSONL audit

Every completed scan writes one independently decodable JSON object with
exactly these compact fields:

| Field | Meaning |
| --- | --- |
| `timestamp` | UTC event time |
| `tool_name` | Tool being guarded |
| `decision` | Final safety decision |
| `risk_level` | Primary risk level |
| `rule_id` | Primary Rule ID |
| `duration_ms` | Scanner duration |
| `redacted` | Whether report data required redaction |
| `blocked` | Whether execution is blocked |
| `backend` | Normalized backend |

Audit records intentionally exclude the full command, cwd/workdir, env, output,
token, password, and private-key content. `JSONLAuditor` serializes concurrent
writes with a mutex and enforces mode `0600` on its file. Audit failure is
fail-closed.

Argument extraction happens before `Scanner.Scan`. A malformed tool payload is
denied, but does not produce the normal scanner audit event; callers should
record that explicit permission failure in framework telemetry.

### OpenTelemetry

Only these low-cardinality span attributes are emitted:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

Commands, cwd/workdir, env, output, evidence, and secrets must not be attached
to spans.

### Secret redaction

Recognized patterns include credential assignments, bearer tokens, AWS access
keys, OpenAI-style keys, GitHub tokens, and PEM private keys. Redaction is
applied to report commands and evidence, environment findings, JSON-compatible
tool results, and the optional after-tool callback. Structured result keys such
as `password`, `access_token`, `apiKey`, `client_secret`, and `private_key`
also cause their values to be replaced; ordinary keys such as
`password_policy` and `token_budget` are not treated as credentials.

`NewRedactingArtifactService` must wrap the artifact service used by guarded
execution. It parses JSON artifacts for key-aware redaction, redacts recognized
secrets in other text artifacts, rejects malformed declared JSON, and rejects
recognized secrets in binary artifacts rather than corrupting binary data.
Safe binary artifacts pass through unchanged.

Tool execution failure logs record only the tool name, failure stage, and Go
error type. Full arguments, results, and error messages are intentionally
excluded because after-tool redaction occurs too late to protect an earlier
log write.

Redaction is a last-resort containment layer, not secret storage. Secrets should
be injected through approved isolation mechanisms and should never be placed in
tool arguments, logs, reports, artifacts, or telemetry.

## Quality Evaluation, Benchmarks, And Fuzzing

The independent corpus is separate from the 12 demonstration samples:

| Corpus | Result | Target |
| --- | --- | --- |
| 25 high-risk command cases | 25/25 detected as `deny` (100%) | at least 90% |
| 20 safe command cases | 1/20 non-`allow` (5% false-positive rate) | at most 10% |
| 20 safe CodeExecutor cases | 0/20 non-`allow` (0% false-positive rate) | at most 10% |
| 10 review cases | 10/10 detected as `ask` (100%) | expected decision |
| Dangerous deletion | 4/4 `deny` (100%) | 100% |
| Sensitive key/path access | 4/4 `deny` (100%) | 100% |
| Non-allowlisted network | 4/4 `deny` (100%) | 100% |

The 25 high-risk cases consist of the 12 mandatory delete, sensitive-path, and
network cases plus 13 additional high-risk cases. The remaining known
conservative false positive in the safe command corpus is a documentation-only
command that quotes a sensitive path. It is retained as a visible quality
boundary rather than removed from the corpus. The separate CodeExecutor corpus
verifies that ordinary validated non-shell code is not rejected merely because
it has no shell command.

Run the corpus with:

```bash
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/safety -run '^TestIndependent' -count=1 -v
```

Recorded pure-scan benchmark evidence:

| Item | Value |
| --- | --- |
| Platform | Darwin arm64 |
| CPU | Apple M4 |
| Go | go1.25.3 |
| Auditor I/O | disabled |
| 500 ordinary commands | 6.936 ms/op, 1,119,390 B/op, 24,002 allocs/op |
| 500 mixed safe/risky commands | 15.036 ms/op, 1,305,192 B/op, 26,407 allocs/op |
| One 500-line script | 7.167 ms/op, 76,484 B/op, 1,050 allocs/op |
| Long non-allowlisted URL | 6.888 ms/op, 49,012 B/op, 49 allocs/op |
| Long argv | 9.799 ms/op, 93,326 B/op, 38 allocs/op |

All recorded cases are far below the one-second acceptance limit. Reproduce the
benchmark rather than treating these machine-specific numbers as a permanent
guarantee:

```bash
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/safety \
  -run '^$' \
  -bench '^BenchmarkScanner' \
  -benchtime=5x \
  -benchmem \
  -count=1
```

Parser, redactor, and scanner fuzz targets check that arbitrary input does not
panic, malformed shell input does not silently allow, decisions and primary
Rule IDs remain valid, and recognized secrets do not reappear in reports:

```bash
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./internal/shellsafe \
  -run '^$' -fuzz '^FuzzParseNeverPanics$' -fuzztime=10s

GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/safety \
  -run '^$' -fuzz '^FuzzRedactStringProperties$' -fuzztime=10s

GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/safety \
  -run '^$' -fuzz '^FuzzScannerProperties$' -fuzztime=10s
```

Run one fuzz target per process.

## Cross-Platform Process Lifecycle

`Pdeathsig` is a Linux-only `syscall.SysProcAttr` field. Linux hostexec sets it
to `SIGTERM`; the Linux tests live in `hostexec_linux_test.go`. macOS and other
non-Linux Unix systems compile a no-op `applyParentDeathSignal` and test that
boundary in `hostexec_nonlinux_test.go`.

On macOS, hostexec process cleanup instead relies on owned process groups or PTY
sessions, signals, timeouts, and the session manager. This is not identical to
Linux parent-death behavior. Linux CI should compile and run the Linux-specific
tests. A macOS developer can at least verify Linux compilation with:

```bash
CGO_ENABLED=0 \
GOOS=linux \
GOARCH=amd64 \
GOCACHE=/tmp/trpc-agent-go-cache \
  go test -c ./tool/hostexec \
  -o /tmp/trpc-agent-go-hostexec-linux.test
```

This guard change documents and tests the platform split; broader Darwin
parent-lifecycle guarantees should be handled as separate hostexec work.

## Known Limitations

- Parsing is conservative and intentionally supports only a subset of shell
  syntax. Unsupported syntax becomes `ask` or `deny`.
- Quoted documentation containing a URL or sensitive path can produce a
  conservative false positive.
- Textual secret recognition cannot identify every credential format and may
  match secret-like non-secret strings.
- `execute_code` performs direct shell scanning plus conservative,
  language-aware risky-capability checks for Python, JavaScript/TypeScript and
  Go. Python and JavaScript analysis is lexical; Go syntax is parsed into an
  AST. None of these analyzers proves arbitrary code safe or performs complete
  interprocedural data-flow analysis. Dynamic paths and destinations,
  reflection/evaluation, unresolved risky APIs, risky wildcard/dot imports,
  syntax failures and unsupported languages require review.
- An allowlisted executable can still load behavior from source files,
  repository configuration, plugins, shared libraries, DNS, or runtime
  downloads. The guard blocks documented high-risk secondary-executor and
  indirect-network forms, but runtime isolation remains necessary for new or
  application-specific execution mechanisms.
- Command report redaction combines text patterns with parsed argv semantics
  for credential-bearing Curl and Wget options. Structured output and artifact
  redaction also use normalized secret field names. Unknown credential formats
  remain possible and should be isolated from tool-visible inputs.
- Artifact protection requires callers to install
  `NewRedactingArtifactService`; storage paths that bypass the wrapper are not
  protected. Binary artifacts are not rewritten and are rejected only when a
  recognized textual secret pattern is present.
- Static checks cannot observe behavior generated after execution.
- Policy reload is not automatic; the caller must reload or restart.
- Permission argument-extraction failures are denied and emit a redacted audit
  event through the scanner's audit sink.
- Output byte limits are enforced by workspaceexec, hostexec and the supported
  CodeExecutor runtimes. CPU, memory, disk and process-count isolation still
  require an executor, operating-system limit, container, or sandbox.

## Why This Does Not Replace Sandboxing

The guard is a static pre-execution policy layer. It cannot:

- prove that an allowlisted binary, dependency, or script is safe;
- prevent a program from downloading code or changing behavior after it starts;
- provide kernel-level filesystem, network, process, or syscall isolation;
- reliably control CPU, memory, disk, output, or process counts by itself;
- eliminate container escape risk or host/container configuration errors.

Production execution must combine Permission checks, a sandbox, runtime
resource limits, network policy, secret isolation, process cleanup, telemetry,
and human approval. The guard reduces obvious risk before execution; it does
not establish the runtime security boundary.

## Verification

From the repository root:

```bash
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/safety -count=1

GOCACHE=/tmp/trpc-agent-go-cache \
  go test -race ./tool/safety -count=1

GOCACHE=/tmp/trpc-agent-go-cache \
  go test \
  ./internal/shellsafe \
  ./tool/workspaceexec \
  ./tool/hostexec \
  ./tool/codeexec \
  -count=1

GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./internal/flow/processor \
  -run 'Permission|TestExecuteToolWithCallbacksFailureLogExcludesPayload' \
  -count=1

GOCACHE=/tmp/trpc-agent-go-cache \
  go -C examples test ./tool_safety_guard -count=1

GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./... -count=1 -timeout=10m

GOCACHE=/tmp/trpc-agent-go-cache \
  go vet \
  ./tool/safety/... \
  ./internal/shellsafe/... \
  ./internal/flow/processor \
  ./tool/hostexec

GOCACHE=/tmp/trpc-agent-go-cache \
  go -C examples vet ./tool_safety_guard/...

CGO_ENABLED=0 \
GOOS=linux \
GOARCH=amd64 \
GOCACHE=/tmp/trpc-agent-go-cache \
  go test -c ./tool/hostexec \
  -o /tmp/trpc-agent-go-hostexec-linux.test

TMPDIR=/private/tmp \
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./tool/duckduckgo \
  -run '^TestNewDefaultHTTPClient_UsesTransportNetwork$' \
  -count=1

TMPDIR=/private/tmp \
GOCACHE=/tmp/trpc-agent-go-cache \
  go test ./codeexecutor/sandbox \
  -run '^TestFilesystemSymlinkAndCopyHelperBranches$' \
  -count=1
```

The example is a nested module, so root `go test ./...` does not include it.
Classify root failures as change-caused, platform-specific, external-service,
pre-existing, or flaky. Change-caused failures must be fixed; other categories
must include reproducible evidence rather than being silently ignored.

The recorded Darwin arm64 full-repository run completed with all changed and
target packages passing. It returned a nonzero status only for three tests in
unchanged areas:

- `codeexecutor/sandbox.TestFilesystemSymlinkAndCopyHelperBranches` observes
  the macOS `/var` to `/private/var` symlink; it passes with
  `TMPDIR=/private/tmp`.
- `tool/duckduckgo.TestNewDefaultHTTPClient_UsesTransportNetwork` exceeds the
  Unix socket path limit under the long default macOS temporary path; it passes
  with `TMPDIR=/private/tmp`.
- `internal/skillstage.TestStager_StageSkillWithOptionsReadOnly` panics during
  cleanup in unchanged `codeexecutor/local` code and still reproduces with the
  shorter temporary directory. It should be handled separately from this
  Safety Guard change.

## Acceptance Coverage

| Acceptance item | Evidence |
| --- | --- |
| 1. Twelve samples produce reports | Quick-start CLI and example tests |
| 2. High-risk detection and safe false-positive targets | Independent corpus results |
| 3. Mandatory delete, key/path, and network detection | Three 4/4 corpus groups |
| 4. 500-command or 500-line performance | Recorded pure-scan benchmarks |
| 5. Required report fields | `ScanReport` field table and example JSON |
| 6. Policy-only behavior changes | Strict YAML/JSON policy fields and reload semantics |
| 7. Pre-execution denial and audit | Permission ordering, spy tests, and JSONL contract |
| 8. Relationship to sandbox, Telemetry, and CodeExecutor | Backend, OTel, limitations, and sandbox sections |
