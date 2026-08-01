# tool_safety_guard example

Offline demo for issue 2002. No model key: it only calls
`safety.Guard.CheckToolPermission` on a fixed sample list.

## Run

```bash
go run .
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
the demo’s only assertion; unit coverage lives under `tool/safety`.

`ask_commands` includes `go`, but `go test` / `go version` / … are exempted in
code so the “safe go test” sample can stay allow.

## Wiring the same lists into workspaceexec

This demo does not construct an ExecTool. In a real agent, reuse the policy’s
command lists so Guard and spawn-time shellsafe stay aligned:

```go
allow, deny := guard.Policy().CommandLists()
_ = workspaceexec.NewExecTool(runner,
    workspaceexec.WithAllowedCommands(allow...),
    workspaceexec.WithDeniedCommands(deny...),
)
```

## Scrubbing tool outputs

Guard only sees arguments. For results:

```go
cbs := tool.NewCallbacks()
cbs.RegisterAfterTool(safety.AfterToolRedact())
```

`go run .` prints a small before/after `RedactJSON` example at the end.

## What this demo is not

It does not start runner, workspaceexec, or a real sandbox. It only shows the
permission decision + report/audit shape for the sample matrix.
