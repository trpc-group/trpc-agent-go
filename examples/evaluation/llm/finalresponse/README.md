# Final Response (LLM) Evaluation Example

This example runs a final-response evaluation using the built-in `llm_final_response` evaluator with local file-backed managers. Eval sets, metrics, and results live on disk so you can inspect or version them.

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
| `-model` | Model identifier used by the agent | `gpt-4o-mini` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `final-response-basic` |

## Run

```bash
cd examples/evaluation/llm/finalresponse
OPENAI_API_KEY=sk-... \
JUDGE_MODEL_API_KEY=sk-... \
go run . \
  -model "gpt-4o-mini" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "final-response-basic"
```

The example issues one QA prompt, asks the agent to answer, then uses the `llm_final_response` evaluator to judge the agent’s final reply against the reference.

## Data Layout

```
data/
└── final-response-app/
    ├── final-response-basic.evalset.json   # EvalSet with one QA case
    └── final-response-basic.metrics.json   # Uses llm_final_response metric
```

## Output

Results are written under `./output/final-response-app`, mirroring the eval set structure. The console prints a short summary of overall and per-case outcomes.
