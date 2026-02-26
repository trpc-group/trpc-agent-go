# Claude Code CLI Agent Example

This example shows how to wrap a locally installed Claude Code CLI as an `agent.Agent` implemented at `agent/claudecode` and run it through the `runner`.

The agent executes the CLI in `--print` mode and parses the JSON output to emit:

- tool-call events: `tool_use`
- tool-result events: `tool_result`
- a final response event. It is taken from the last `type:"result"` record's `result` field.

It also uses `--resume <session-id>` and falls back to `--session-id <session-id>` to support multi-turn conversations in the same session.

The example includes a project-scoped MCP configuration file named `.mcp.json` that registers a local STDIO MCP server. The server exposes a calculator tool so you can see MCP tool calls in the JSON output.

It also includes a project-level weather skill at `.claude/skills/weather-query/SKILL.md` and a project-level news subagent at `.claude/agents/news-query-agent.md` so you can see Skill/Task events in the JSON output without relying on pre-existing CLI configuration.

## Run

```bash
cd examples/claudecode

# Interactive chat with multi-turn support.
go run .
```

## Sample prompts

At the `You:` prompt, try inputs like:

- `Use the MCP calculator tool to compute 1 + 2 and answer with only the numeric result.`
- `Use the weather-query skill to answer: what's the weather in Shenzhen? Answer in one line.`
- `Use the Task tool to ask the news-query-agent subagent to return today's headlines. Then answer with only the JSON.`

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-claude-bin` | Claude Code CLI executable path | `claude` |
| `-output-format` | Output format: `json` or `stream-json` | `json` |
| `-log-dir` | Persist raw stdout/stderr logs under this directory | `log` |

## Notes

- Try prompts that would naturally use the configured integrations, e.g. ask for Shenzhen weather, or ask for news headlines.
- Configure the output format via `-output-format`.
- When `-log-dir` is set, the example appends a session-scoped log file under `<log-dir>/claude-cli-logs/<cli-session-id>.log.txt`.
- Claude Code can also load user-level MCP servers; if you want to use only `.mcp.json`, add `--strict-mcp-config --mcp-config .mcp.json` in `examples/claudecode/agent.go`.
