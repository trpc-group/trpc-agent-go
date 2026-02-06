# ROUGE Evaluation Example

This example demonstrates `final_response_avg_score` with a ROUGE criterion. The evaluator remains deterministic and scores each invocation as **match = 1** or **mismatch = 0**. When `finalResponse.rouge` is configured, matching is decided by whether the ROUGE score meets the configured threshold.

The agent in this example is LLM-based (`llmagent`), so it requires a model API endpoint and API key.

To make ROUGE matching stable, the agent is instructed to answer in one short sentence of plain text. ROUGE precision is sensitive to verbosity, markdown, and extra formatting.

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work.

## Configuration Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the ROUGE agent | `gpt-5.2` |
| `-streaming` | Enable streaming responses from the LLM | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | EvalSet ID to execute | `rouge-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Data Layout

```shell
data/
└── rouge-app/
    ├── rouge-basic.evalset.json     # EvalSet file for rouge-basic.
    └── rouge-basic.metrics.json     # Metric file for rouge-basic EvalSet.
```

## Run

```bash
cd examples/evaluation/rouge
OPENAI_API_KEY=sk-... \
go run . \
  -model "gpt-5.2" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "rouge-basic" \
  -runs 1
```

## Output

Evaluation artifacts are saved under `./output/rouge-app`.
