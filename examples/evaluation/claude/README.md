# Claude CLI Output Evaluation Example

This example combines the `evaluation` framework with the Claude CLI to grade Claude outputs using evaluation data stored under this directory.

For each eval case prompt, it runs the Claude CLI in JSON mode and captures the full stdout/stderr output:

```bash
claude -p "..." --verbose --output-format json
```

The agent uses the full JSON output (including intermediate tool calls) as the evaluation input and evaluates it with `tool_trajectory_avg_score` (built-in tool trajectory matcher).

The agent also parses the JSON output to emit tool-call and tool-result events, so tool usage can be captured in `Invocation.Tools` for tool-trajectory style metrics (the tool arguments are normalized for deterministic matching).

## Eval Set

- `claude-mcp-basic`: asks the model to use an MCP tool call `mcp__eva_eval_example__calculator` with `{operation,a,b}` arguments and expects a `{operation,a,b,result}` JSON result.

## Data Layout

```text
data/
└── claude-eval-app/
    ├── claude-mcp-basic.evalset.json
    └── claude-mcp-basic.metrics.json
```

## MCP Demo Tool

The agent starts a local MCP SSE server, registers it via `claude mcp add`, and removes it after the run:

```bash
claude mcp add -s local --transport sse eva_eval_example http://127.0.0.1:<port>/sse
claude -p "..." --verbose --output-format json --allowedTools mcp__eva_eval_example__calculator
claude mcp remove -s local eva_eval_example
```

It passes a narrow `--allowedTools` list to avoid interactive approval prompts, and shuts down the demo MCP server after the run.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `ANTHROPIC_API_KEY` | API key used by Claude CLI (required by your CLI setup) | `` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-app-name` | App name used to locate evalset/metrics under `-data-dir` | `claude-eval-app` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `claude-mcp-basic` |
| `-claude-bin` | Claude executable | `claude` |
| `-claude-save-log` | Save claude stdout/stderr under `output-dir/claude-cli-logs` | `true` |

## Run

```bash
cd examples/evaluation/claude
ANTHROPIC_API_KEY=sk-... \
go run . \
  -data-dir "./data" \
  -output-dir "./output"
```

## Output

```text
output/
├── claude-cli-logs/
│   └── <invocation-id>.log.txt
└── claude-eval-app/
    └── claude-eval-app_claude-mcp-basic_<run-id>.evalset_result.json
```

## Troubleshooting

- If the CLI hangs or prompts for approval, verify that your Claude CLI supports `mcp add/remove` and that `--allowedTools` is set for the MCP tool used by the eval set.
- If the evaluation fails due to missing tool calls, check whether your model answered without calling the tool and adjust the prompt or model settings accordingly.
