# ToolMock Evaluation Example

This example demonstrates invocation-level `toolMock` in an EvalSet. The weather tool implementation returns a changing real-tool value, while the EvalSet replaces the tool result with a stable mocked value during evaluation.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for OpenAI-compatible APIs | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` is required.

## Configuration Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model used by the weather agent | `deepseek-v4-flash` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing EvalSet and metric files | `./data` |
| `-output-dir` | Directory where EvalResult files are written | `./output` |
| `-eval-set` | EvalSet ID to execute | `toolmock-weather` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Data Layout

```shell
data/
└── toolmock-app/
    ├── toolmock-weather.evalset.json    # Invocation-level toolMock for get_weather.
    └── toolmock-weather.metrics.json    # Verifies the mocked tool result in the actual trace.
```

## Run

```bash
cd trpc-agent-go/examples/evaluation/toolmock
OPENAI_API_KEY=sk-... go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -model "gpt-4o-mini" \
  -eval-set "toolmock-weather"
```

## Output

### Summary

```log
✅ Evaluation completed with tool mock example
App: toolmock-app
Eval Set: toolmock-weather
Overall Status: passed
Runs: 1
Case shenzhen_weather_mock -> passed
  Metric tool_trajectory_avg_score: score 1.00 (threshold 1.00) => passed

Results saved under: ./output
```

### Artifacts

```shell
output/
└── toolmock-app/
    └── toolmock-app_toolmock-weather_<run-id>.evalset_result.json
```

## What It Shows

- Configures `toolMock.actual` on one EvalSet invocation.
- Omits `arguments` so the mock matches by tool name only.
- Replaces the real `get_weather` result with a deterministic EvalSet result.
- Verifies that the mocked result is captured in `Invocation.Tools[].Result` through `tool_trajectory_avg_score`.
