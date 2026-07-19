# Tool Execution Safety Guard example

This example runs the [`tool/safety`](../../tool/safety) pre-execution
safety guard over a fixed corpus of sample commands and code blocks,
prints a decision summary, and writes two artifacts. Nothing in the
sample set is ever executed — the guard only scans.

## Run

```bash
cd examples/tool_safety_guard
go run . -policy tool_safety_policy.yaml \
    -report tool_safety_report.json \
    -audit  tool_safety_audit.jsonl
```

Output:

```
safe_go_test                 -> allow              risk=none     rules=[]
dangerous_delete_root        -> deny               risk=critical rules=[dangerous_command]
read_ssh_private_key         -> deny               risk=high     rules=[sensitive_path]
network_egress_denied        -> deny               risk=high     rules=[network_egress]
network_egress_allowlisted   -> allow              risk=none     rules=[]
shell_wrapper_bypass         -> deny               risk=high     rules=[shell_bypass]
dependency_install           -> ask                risk=medium   rules=[dependency_change]
hostexec_pty_session         -> ask                risk=high     rules=[host_exec_risk]
secret_in_command            -> deny               risk=high     rules=[secret_leak shell_bypass]
code_block_host_bridge       -> ask                risk=medium   rules=[host_exec_risk]
...
```

## Files

| File | Description |
| --- | --- |
| `main.go` | Loads the policy, scans the samples, writes the artifacts. |
| `tool_safety_policy.yaml` | Example policy (fully commented). Edit it to change enforcement — no recompile needed. |
| `tool_safety_report.json` | Golden structured reports: `decision`, `risk_level`, `evidence`, `recommendation`, per finding. |
| `tool_safety_audit.jsonl` | Golden JSONL audit stream: one flattened event per sample for monitoring. |
| `main_test.go` | Regenerates the artifacts in a temp dir and asserts they byte-match the checked-in golden files. |

## Samples covered

The corpus exercises every risk category the safety guard must detect,
plus benign baselines:

1. safe `go test` / `git status` (allow)
2. dangerous recursive delete of `/` (deny, critical)
3. reading an SSH private key (deny)
4. reading `.env` (deny)
5. non-allowlisted network egress (deny)
6. allowlisted network egress (allow)
7. shell-wrapper bypass `sh -c '... | sh'` (deny)
8. benign pipeline (allow)
9. dependency install `pip install` (ask)
10. long-running `sleep 3600` (ask)
11. unbounded output `cat /dev/urandom` (ask)
12. hostexec PTY + background session (ask, high)
13. inline secret in a command (deny + redaction)
14. code block that shells out to the host (ask)

## Regenerating the golden artifacts

If you change the policy or the rule engine, regenerate and re-run the
test:

```bash
go run . -report tool_safety_report.json -audit tool_safety_audit.jsonl
go test ./...
```

The generated report drops the scan timestamp/duration and the audit
file zeroes them, so the artifacts are byte-stable across machines. To
confirm no secret leaked into an artifact:

```bash
grep -E 'id_rsa|BEGIN PRIVATE KEY|ghp_[0-9A-Za-z]{20}' \
    tool_safety_report.json tool_safety_audit.jsonl && echo LEAK || echo CLEAN
```
