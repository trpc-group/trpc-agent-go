# Evaluation Callbacks Example

This example demonstrates evaluation lifecycle callbacks (hooks). It registers callbacks for every evaluation stage and prints the callback arguments so you can understand what the framework provides at each point.

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
cd trpc-agent-go/examples/evaluation/callbacks
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "math-basic" \
  -runs 1
```

It prints callback logs for each stage and then prints a summary. Results are written to `./output/math-eval-app`.

```log
[callback BeforeInferenceSet] args={"Request":{"appName":"math-eval-app","evalSetId":"math-basic"}}
[callback BeforeInferenceCase] args={"Request":{...},"EvalCaseID":"calc_add","SessionID":"da25bf57-2551-4702-8583-eda9d490261c"}
[callback AfterInferenceCase] args={"Request":{...},"Result":{...},"Error":null}
[callback AfterInferenceSet] args={"Request":{...},"Results":[...],"Error":null}
[callback BeforeEvaluateSet] args={"Request":{...}}
[callback BeforeEvaluateCase] args={"Request":{...},"EvalCaseID":"calc_add"}
[callback AfterEvaluateCase] args={"Request":{...},"InferenceResult":{...},"Result":{...},"Error":null}
[callback AfterEvaluateSet] args={"Request":{...},"Result":{...},"Error":null}
✅ Evaluation completed with callbacks
App: math-eval-app
Eval Set: math-basic
Overall Status: passed
Runs: 1
Case calc_add -> passed
  Metric tool_trajectory_avg_score: score 1.00 (threshold 1.00) => passed

Results saved under: ./output
```
