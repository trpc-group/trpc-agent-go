# Rubric Response (LLM) Evaluation Example

This example scores an agent's final reply against rubric items for correctness and relevance. The agent uses a simple calculator tool, and the judge model applies the `llm_rubric_response` metric with multiple samples.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the agent model | `https://api.openai.com/v1` |
| `JUDGE_MODEL_API_KEY` | API key for the judge model (required) | `` |
| `JUDGE_MODEL_BASE_URL` | Optional custom endpoint for the judge model | `https://api.openai.com/v1` |

The metric configuration in `data/` references the judge settings via `${JUDGE_MODEL_API_KEY}` and `${JUDGE_MODEL_BASE_URL}` placeholders.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `rubric-response-basic` |

## Run

```bash
cd examples/evaluation/llm/rubricresponse
OPENAI_API_KEY=sk-... \
JUDGE_MODEL_API_KEY=sk-... \
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "rubric-response-basic"
```

The run issues one QA prompt, lets the agent answer (with an optional calculator tool call), then asks the judge model to grade the final reply on two rubric items using three samples.

## Data Layout

```
data/
└── rubric-response-app/
    ├── rubric-response-basic.evalset.json    # EvalSet with one QA case
    └── rubric-response-basic.metrics.json    # llm_rubric_response metric with two rubrics
```

## Output

Results are written under `./output/rubric-response-app`, mirroring the eval set structure. The console prints a summary of overall status and per-case rubric scores.
