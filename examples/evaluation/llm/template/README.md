# Template LLM Judge Evaluation Example

This example runs a local file-backed evaluation using the new `llm_judge_template` evaluator.

The agent answers a simple factual question. The judge model then scores the agent reply with two template-based metrics:

- one `single_score` metric
- one `rubric_scores` metric

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent and judge model. | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the agent and judge model. | `https://api.openai.com/v1` |

The metric configuration in `data/` expands `${OPENAI_API_KEY}`, `${OPENAI_BASE_URL}` at load time.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the agent and judge. | `gpt-5.2` |
| `-streaming` | Enable streaming responses from the agent. | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json`. | `./data` |
| `-output-dir` | Directory where evaluation results are written. | `./output` |
| `-eval-set` | Evaluation set identifier to execute. | `template-basic` |

## Run

```bash
cd examples/evaluation/llm/template
go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "template-basic"
```

## Data Layout

```text
data/
└── template-eval-app/
    ├── template-basic.evalset.json
    └── template-basic.metrics.json
```

The metrics use:

- `evaluatorName: "llm_judge_template"`
- `template.prompt`
- `template.variableBindings`
- `template.responseScorerName: "single_score"` for one metric
- `template.responseScorerName: "rubric_scores"` for the other metric

## Output

Results are written under `./output/template-eval-app`. The console prints a short summary of overall and per-case outcomes.
