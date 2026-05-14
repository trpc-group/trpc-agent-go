# Case-Level Rubric Evaluation Example

This example shows how case-level rubrics work when an eval set uses both a non-rubric evaluator and a rubric-based LLM evaluator.

The eval config contains two metrics:

- `tool_trajectory_avg_score` checks the agent's tool calls.
- `travel_answer_quality` routes to `llm_rubric_response` and owns the LLM judge rubrics.

The eval case adds one case-level rubric and binds it to `travel_answer_quality`. It does not bind the rubric to the tool trajectory metric, so the tool trajectory metric runs normally while the LLM rubric metric receives the extra case-specific requirement.

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
| `-model` | Model identifier used by the agent | `gpt-5.2` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `case-level-rubric-basic` |

## Run

```bash
cd examples/evaluation/llm/caselevelrubric
OPENAI_API_KEY=sk-... \
JUDGE_MODEL_API_KEY=sk-... \
go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "case-level-rubric-basic"
```

## Data Layout

```text
data/
└── case-level-rubric-app/
    ├── case-level-rubric-basic.evalset.json
    └── case-level-rubric-basic.metrics.json
```

Key points in the data files:

- `case-level-rubric-basic.metrics.json` defines both `tool_trajectory_avg_score` and `travel_answer_quality`.
- `travel_answer_quality` sets `evaluatorName` to `llm_rubric_response` and provides `criterion.llmJudge`.
- The eval case's `rubrics[0].metricName` is `travel_answer_quality`, not `tool_trajectory_avg_score`.

## Expected Behavior

The tool trajectory metric evaluates only tool usage. The case-level rubric is appended only to `travel_answer_quality` and appears in that metric's effective `criterion.llmJudge.rubrics` and rubric scores.

IDs, timestamps, tool-call details, and LLM wording in the sample output can vary by run and model; the stable expectation is the rubric merge behavior.
