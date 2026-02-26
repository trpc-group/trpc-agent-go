# Claude Code Agent Evaluation Example

This example demonstrates how to evaluate **Claude Code CLI agent** tool usage with the existing evaluation pipeline.

It covers three Claude Code capabilities without relying on any user-level configuration:

- **MCP tool call**: a project-scoped MCP server exposes a calculator tool.
- **Skill**: a project-level skill returns a fixed weather answer.
- **Subagent**: a project-level subagent returns fixed contact lookup data, triggered via the **Task** tool.

The evaluation uses `tool_trajectory_avg_score` to verify the expected tool calls, including names and selected arguments/results.

Notes:

- MCP tool names follow the pattern `mcp__<server>__<tool>`, e.g. `mcp__eva_cli_example__calculator` in this example.
- Claude Code `Skill` tool calls are normalized to `skill_run` by `agent/claudecode` for easier trajectory matching.
- Subagent routing is represented by a `Task` tool call; the agent also emits a separate transfer event, which is not captured by the tool-trajectory metric.

## Prerequisites

1. Claude Code CLI is installed locally and authenticated.
2. The `claude` binary is available in `PATH`, or pass `-claude-bin`.
3. Go is installed. The MCP server starts via `go run ./mcpserver` from `.mcp.json`.

## Run

```bash
cd trpc-agent-go/examples/evaluation/claudecode

go run . \
  -claude-bin "claude" \
  -output-format "json" \
  -work-dir "." \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "claudecode-basic" \
  -runs 1
```

## Layout

```text
claudecode/
  agent.go
  main.go
  .mcp.json
  .claude/
    skills/weather-query/SKILL.md
    agents/contact-lookup-agent.md
  mcpserver/
    main.go
  data/
    claudecode-eval-app/
      claudecode-basic.evalset.json
      claudecode-basic.metrics.json
  output/
    claudecode-eval-app/
      *.evalset_result.json
```
