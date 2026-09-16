# Case Aggregation Evaluation Example

This example shows how to compute a case-level score and status with a custom `service.EvalCaseResultAggregator`.

It uses local file managers, a real LLM support agent, built-in final-response metrics, and a custom aggregator.

## What It Demonstrates

- Injecting a custom case result aggregator through `evaluation.WithEvalCaseResultAggregator`.
- Routing multiple metric instances to the built-in `final_response_avg_score` evaluator with `evaluatorName`.
- Reading metric weights from `EvalMetric.Extension`.
- Computing each metric's normal `score`, `threshold`, and `evalStatus`.
- Computing the eval-case `score` and `finalEvalStatus` with a weighted aggregation policy.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work.

## Run

Configure the OpenAI-compatible model service first.

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
```

```bash
cd examples/evaluation/caseaggregation
go run . \
  -model "deepseek-v4-flash" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "support-quality-basic"
```

## Key Metric Configuration

```json
[
  {
    "metricName": "answer_is_substantive",
    "evaluatorName": "final_response_avg_score",
    "threshold": 1,
    "criterion": {
      "finalResponse": {
        "text": {
          "matchStrategy": "skip",
          "length": {
            "min": 40
          }
        }
      }
    },
    "extension": {
      "weight": 0.8
    }
  },
  {
    "metricName": "max_six_chars",
    "evaluatorName": "final_response_avg_score",
    "threshold": 1,
    "criterion": {
      "finalResponse": {
        "text": {
          "matchStrategy": "skip",
          "length": {
            "max": 6
          }
        }
      }
    },
    "extension": {
      "weight": 0.2
    }
  }
]
```

`metricName` is the business metric name shown in results. `evaluatorName` selects the built-in evaluator implementation from Registry. The custom aggregator reads `weight` from `EvalMetric.Extension` and uses its own threshold configuration to decide the case status.

## Data Layout

```shell
data/
└── case-aggregation-app/
    ├── support-quality-basic.evalset.json
    └── support-quality-basic.metrics.json
```

## Output

```log
Evaluation completed with case aggregation
App: case-aggregation-app
Eval Set: support-quality-basic
Saved Result: output/case-aggregation-app/case-aggregation-app_support-quality-basic_xxx.evalset_result.json
Case support_weighted_tradeoff run 1 -> passed (case score 0.80)
  Actual Response: Please contact your workspace administrator or support team to review the account lock and restore access.
  Metric answer_is_substantive: score 1.00 (threshold 1.00) => passed
  Metric max_six_chars: score 0.00 (threshold 1.00) => failed
```

The case demonstrates the custom policy: one metric fails, but the case passes because the weighted case score reaches the case threshold.

Because this example runs once, the top-level summary follows the case-level `finalEvalStatus`. Metric summaries still keep each metric's own pass/fail status for diagnostics, so a failed metric can appear in the summary while the case and overall result are passed.
