# Trace Evaluation Example

This example evaluates a pre-recorded execution trace without re-running the agent. Each `evalCase` sets `evalMode` to `"trace"`, which tells the evaluation service to skip the runner inference phase and use `actualConversation` as the actual trace.

In this sample EvalSet, `actualConversation` contains the recorded trace (user prompts, tool calls, and final responses). `conversation` provides expected outputs (optional) and is used here as the reference trace for tool-trajectory evaluation.

This example runs two metrics:

- `tool_trajectory_avg_score` compares tool calls in `actualConversation` against `conversation`.
- `llm_rubric_response` judges the final answer quality from the actual trace and does not require expected outputs.

The sample trace includes tool calls under `tools`, including the tool call ID, tool name, input arguments, and execution result.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | Optional API key for the agent model (inference is skipped in trace mode) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the agent model | `https://api.openai.com/v1` |
| `JUDGE_MODEL_API_KEY` | API key for the judge model (required) | `` |
| `JUDGE_MODEL_BASE_URL` | Optional custom endpoint for the judge model | `https://api.openai.com/v1` |

**Note**: In trace mode inference is skipped, so the agent model is not invoked. The `OPENAI_*` variables are kept for consistency with other examples.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the agent (inference is skipped in trace mode) | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the agent (inference is skipped in trace mode) | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `trace-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Run

```bash
cd examples/evaluation/trace
JUDGE_MODEL_API_KEY=sk-... \
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "trace-basic"
```

## Data Layout

```
data/
└── trace-eval-app/
    ├── trace-basic.evalset.json    # EvalSet with trace-mode cases
    └── trace-basic.metrics.json    # tool_trajectory_avg_score + llm_rubric_response metrics
```

## Output

Results are written under `./output/trace-eval-app`.
