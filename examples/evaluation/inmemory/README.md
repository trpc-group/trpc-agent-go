# In-Memory Evaluation Example

This example runs the evaluation pipeline with in-memory managers so that evaluation sets, metric definitions, and result storage are created at runtime.

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
| `-runs` | Number of repetitions per evaluation case | `1` |

## Run

```bash
cd trpc-agent-go/examples/evaluation/inmemory
go run . \
  -model "deepseek-chat" \
  -runs 1
```

It prints a case-by-case summary followed by the JSON payload returned from the in-memory result manager.

## Output Log

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
