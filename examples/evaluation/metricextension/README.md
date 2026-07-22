# Metric Extension Evaluation Example

This example shows how a custom evaluator can read caller-defined settings from `metric.EvalMetric.Extension`.

It uses local file managers and a real LLM support agent.

## What It Demonstrates

- Registering a custom evaluator in `evaluation/evaluator/registry`.
- Routing a metric instance to that evaluator with `MetricName`.
- Storing evaluator-specific policy settings in `EvalMetric.Extension`.
- Returning normal metric `Score` and `Status` from the evaluator.

## Run

Configure the OpenAI-compatible model service first.

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
```

```bash
cd examples/evaluation/metricextension
go run . \
  -model "deepseek-v4-flash" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "support-policy-basic" \
  -runs 1
```

## Key Metric Configuration

```json
{
  "metricName": "support_response_policy",
  "threshold": 1,
  "extension": {
    "requiredPhrase": "support"
  }
}
```

`MetricName` selects the evaluator implementation from Registry and is also the metric name shown in results. The custom evaluator reads `requiredPhrase` from `EvalMetric.Extension` and checks whether the final response contains it.

## Data Layout

```shell
data/
└── metric-extension-app/
    ├── support-policy-basic.evalset.json
    └── support-policy-basic.metrics.json
```

## Output

```log
Evaluation completed with metric extension
App: metric-extension-app
Eval Set: support-policy-basic
Overall Status: passed
Runs: 1
Case sign_in_help -> passed
  Metric support_response_policy: score 1.00 (threshold 1.00) => passed

Results saved under: ./output
```
