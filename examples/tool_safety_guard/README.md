# tool_safety_guard example

Offline demo for issue 2002. No model key.

Default `go run .` shows the full host wiring reviewers expect:

1. **PermissionPolicy** — `Guard.CheckToolPermission` on the sample matrix
2. **CommandLists** — same allow/deny slices for `workspaceexec.WithAllowedCommands` /
   `WithDeniedCommands` (see `tool/safety/DUAL_POLICY.md`)
3. **AfterToolRedact** — real callback scrub of a leaky tool result
4. **Audit** — first JSONL line from the file auditor after drain

## Run

```bash
go run .
go test .
```

Reads:

- `tool_safety_policy.yaml` — policy overlay (see comments in the file)
- `tool_safety_samples.json` — cases with optional `want` (`allow` / `deny` / `ask`)

Writes:

- `output/tool_safety_report.json`
- `output/tool_safety_audit.jsonl`
- also refreshes `tool_safety_report.json` in this directory for convenience

`output/` is gitignored. The committed `tool_safety_report.json` /
`tool_safety_audit.jsonl` are fixtures from a previous run; treat `output/`
after `go run .` as the live result.

Oversized-stdin is appended in `main.go` so the JSON file stays small.

## Samples

Each entry looks like:

```json
{"title": "…", "tool": "workspace_exec", "args": {"command": "…"}, "want": "deny"}
```

If `want` is set and the decision differs, `go run .` exits non-zero. That is
the demo’s only assertion; unit coverage lives under `tool/safety`
(`TestAdversarial_HandChecks` for mentor paste cases).

`ask_commands` includes `go`, but `go test` / `go version` / … are exempted in
code so the “safe go test” sample can stay allow.

## What this demo is not

It does not start runner, workspaceexec, or a real sandbox. It prints the
exact CommandLists → workspaceexec option shape so the dual-policy path is
visible without constructing an ExecTool here.
