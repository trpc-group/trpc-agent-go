# Codex CLI Agent Example

This example shows how to wrap a locally installed Codex CLI as an `agent.Agent` implemented at `agent/codex` and run it through the `runner`.

The agent executes `codex exec --json` and parses JSONL output to emit:

- tool-call events for Codex command, MCP, built-in tool, and skill items
- tool-result events for completed tool items
- partial assistant chunk events for completed Codex assistant message items
- a final response event from the Codex agent message
- a session state update containing `codex.StateKeyThreadID` when Codex reports a new thread id

It also uses `codex exec resume --json <thread-id>` on later turns in the same framework session. If resume fails before emitting any transcript events, it starts a fresh `codex exec` run and stores the new thread id when one is reported. If resume has already emitted events, the agent surfaces the failure instead of starting a fresh run to avoid duplicating visible progress or tool side effects.

The example can configure two integrations:

- a temporary streamable HTTP MCP server from `-mcp-url`, injected with `-c mcp_servers...` at runtime
- a project-local Codex skill at `.agents/skills/weather-query/SKILL.md`

Neither integration writes to the user's global Codex config.

## Run

Start the demo MCP server in one terminal:

```bash
cd examples/codex
go run ./mcpserver
```

Then run the interactive chat with multi-turn support from another terminal:

```bash
cd examples/codex
go run .
```

## Sample prompts

At the `You:` prompt, try inputs like:

- `Run pwd and summarize the workspace in one sentence.`
- `Use the weather-query skill to answer: what's the weather in Shenzhen? Answer in one line.`
- Ask `Use the MCP calculator tool to compute 1 + 2 and answer with only the numeric result.`
- `List the files in this directory and explain what this example does.`

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-codex-bin` | Codex CLI executable path | `codex` |
| `-model` | Optional Codex model override | empty |
| `-mcp-url` | Streamable HTTP MCP server URL. Pass an empty value to disable it. | `http://localhost:3002/mcp` |
| `-approval-policy` | Codex approval policy | `never` |
| `-sandbox` | Codex sandbox mode | `workspace-write` |
| `-work-dir` | CLI process working directory | `.` |
| `-log-dir` | Persist raw stdout/stderr logs under this directory | `log` |

## Notes

- The example passes `--ask-for-approval` as a configurable Codex root flag. The default `never` value lets non-interactive `codex exec` run without prompting.
- The temporary MCP server is injected through `WithGlobalArgs("-c", ...)`, so it applies only to this example process. The example also sets its MCP tool approval mode to `approve` for non-interactive `codex exec` runs.
- The demo skill is project-local. Codex loads it because the example runs in `examples/codex`; current Codex CLI usually handles skills through shell commands instead of a dedicated skill tool, so the emitted framework event is typically `command_execution`, not `skill_run`.
- When `-log-dir` is set, the example appends raw stdout/stderr under `<log-dir>/codex-cli-logs/<thread-id>.log.txt`.
- The printed `Thread state:` line shows the framework session state key used for later `codex exec resume` calls.
