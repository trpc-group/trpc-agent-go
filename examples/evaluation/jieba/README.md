# Jieba Evaluation Example

This example demonstrates how to register a custom `jieba` tokenizer through `evaluation.WithMetricRegistry(...)` and use it in a `final_response_avg_score` metric with a ROUGE-1 F1 criterion. The example uses the local file-backed managers for EvalSet, Metric, and EvalResult, and the EvalSet and Metric are stored as checked-in local JSON files under `data/`.

The agent in this example is LLM-based (`llmagent`), so it requires a model API endpoint and API key. The example asks the model to rewrite a Chinese sentence into one short semantically similar sentence, then evaluates the generated answer against the checked-in expected answer using a Jieba-tokenized ROUGE-1 F1 criterion.

## Requirements

`gojieba` uses CGO. Make sure `CGO_ENABLED=1` and a C/C++ toolchain are available.

## Environment Variables

| Variable | Description | Default Value |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Configuration Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set identifier to execute | `jieba-rouge-zh` |
| `-model` | Model identifier used by the Chinese rewrite agent | `gpt-5.2` |
| `-streaming` | Enable streaming responses from the LLM | `false` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Run

```bash
cd examples/evaluation/jieba
OPENAI_API_KEY=sk-... \
go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "jieba-rouge-zh" \
  -model "gpt-5.2" \
  -runs 1
```

## What The Example Loads

The checked-in EvalSet is:

- App: `jieba-eval-app`
- EvalSet ID: `jieba-rouge-zh`
- Prompt: `今天天气很好，我们一起去公园散步。`
- Expected answer: `天气不错，一起去公园散步吧`

The checked-in metric is:

- Metric name: `final_response_avg_score`
- Criterion: `finalResponse.rouge`
- ROUGE type: `rouge1`
- Measure: `f1`
- Thresholds: precision `0.3`, recall `0.6`, f1 `0.4`
- Tokenizer name: `jieba`

## Expected Output

The program prints:

- Overall evaluation status.
- Per-case metric score and threshold.
- The output directory where EvalResult JSON is written.

The console summary is intentionally short. Detailed actual/expected invocations and metric results are stored in the generated EvalResult JSON under `output/`.

## Data Layout

```shell
data/
└── jieba-eval-app/
    ├── jieba-rouge-zh.evalset.json
    └── jieba-rouge-zh.metrics.json

output/
└── jieba-eval-app/
    └── *.evalset_result.json
```

`jieba-rouge-zh.metrics.json` configures `tokenizerName: "jieba"`, and the example resolves that name at runtime through `evaluation.WithMetricRegistry(...)`.

## Example Summary

```log
✅ Evaluation completed with ROUGE criterion
App: jieba-eval-app
Eval Set: jieba-rouge-zh
Overall Status: passed
Runs: 1
Case rewrite-weather -> passed
  Metric final_response_avg_score: score 1.00 (threshold 1.00) => passed

Results saved under: ./output
```
