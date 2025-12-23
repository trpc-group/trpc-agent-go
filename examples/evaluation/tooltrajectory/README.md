# Tool Trajectory Evaluation Example

This example demonstrates `tool_trajectory_avg_score` with multiple tools (weather, news, time, ticket) and order-insensitive matching.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for OpenAI-compatible APIs | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` is required.

## Configuration Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model used by the travel agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing EvalSet and metric files | `./data` |
| `-output-dir` | Directory where EvalResult files are written | `./output` |
| `-eval-set` | EvalSet ID to execute | `tooltrajectory-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Data Layout

```shell
data/
└── tooltrajectory-app/
    ├── tooltrajectory-basic.evalset.json    # Use case: travel to Shanghai, check weather/news/time/tickets.
    └── tooltrajectory-basic.metrics.json    # Order-agnostic matching with per-tool overrides.
```

## Run

```bash
cd trpc-agent-go/examples/evaluation/tooltrajectory
OPENAI_API_KEY=sk-... go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -model "gpt-4o-mini" \
  -eval-set "tooltrajectory-basic"
```

## Output

### Summary

```log
✅ Evaluation completed with tool trajectory example
App: tooltrajectory-app
Eval Set: tooltrajectory-basic
Overall Status: passed
Runs: 1
Case travel_alerts -> passed
  Metric tool_trajectory_avg_score: score 1.00 (threshold 1.00) => passed

Results saved under: ./output
```

### Artifacts

```shell
output/
└── tooltrajectory-app/
    └── tooltrajectory-app_tooltrajectory-basic_<run-id>.evalset_result.json
```

## What It Shows

- Evaluates an agent that chains weather, news, time, and ticket tools before summarizing travel advice in Chinese.
- Demonstrates `tool_trajectory_avg_score` with order-insensitive matching so tool call order can vary.
- Uses per-tool overrides to ignore `get_time` results and the dynamic `time` field in `get_ticket` arguments/results, making timestamps robust during matching.
