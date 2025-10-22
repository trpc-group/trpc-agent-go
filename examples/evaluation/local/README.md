# Local Evaluation Example

This example runs the evaluation pipeline with a local file-backed manager. Evaluation sets, metric definitions, and run results all live on disk so you can inspect or version them alongside source code.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work.

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-model` | Model identifier used by the calculator agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the LLM | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `math-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Run

```bash
cd trpc-agent-go/examples/evaluation/local
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "math-basic" \
  -runs 1
```

It prints a case-by-case summary and writes detailed JSON artifacts to `./output/math-eval-app`.

## Data Layout

```shell
data/
└── math-eval-app/
    ├── math-basic.evalset.json    # EvalSet file for math-basic.
    └── math-basic.metrics.json    # Metric file for math-basic EvalSet.
```

You can add new cases or metrics by editing these JSON files or by creating additional evaluation set IDs under the same directory.

## Output

### EvalResult file

```shell
output/
└── math-eval-app/
    └── math-eval-app_math-basic_76798060-dcc3-41e9-b20e-06f23aa3cdbc.evalset_result.json    # EvalResult file for math-basic EvalSet.
```

### Log

```log
✅ Evaluation completed
App: math-eval-app
Eval Set: math-basic
Overall Status: passed
Runs: 1
Case calc_add -> passed
  Metric tool_trajectory_avg_score: score 1.00 (threshold 1.00) => passed

Case calc_multiply -> passed
  Metric tool_trajectory_avg_score: score 1.00 (threshold 1.00) => passed
```
