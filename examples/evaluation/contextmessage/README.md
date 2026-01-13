# Context Message Evaluation Example

This example runs the evaluation pipeline with per-case `contextMessages`. The `contextMessages` are injected into every model request so you can provide extra context (system/user/assistant messages) without persisting them into the Session history.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |
| `JUDGE_MODEL_API_KEY` | API key for the judge model (required) | `` |
| `JUDGE_MODEL_BASE_URL` | Base URL for the judge model API endpoint | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `JUDGE_MODEL_API_KEY` are required for the example to work.

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-model` | Model identifier used by the agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the LLM | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `contextmessage-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |

## Run

```bash
cd examples/evaluation/contextmessage
OPENAI_API_KEY=sk-... \
JUDGE_MODEL_API_KEY=sk-... \
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "contextmessage-basic" \
  -runs 1
```

It prints a case-by-case summary and writes detailed JSON artifacts to `./output/contextmessage-app`.

The example demonstrates how `contextMessages` influence the agent responses without being persisted into the session transcript.

## Data Layout

```shell
data/
└── contextmessage-app/
    ├── contextmessage-basic.evalset.json    # EvalSet file for contextmessage-basic.
    └── contextmessage-basic.metrics.json    # Metric file for contextmessage-basic EvalSet.
```

In `contextmessage-basic.evalset.json`, each `evalCase` can include `contextMessages`, for example:

```json
{
  "evalId": "identity_name",
  "contextMessages": [
    { "role": "system", "content": "Your name is trpc-agent-go bot" }
  ]
}
```

You can add new cases or metrics by editing these JSON files or by creating additional evaluation set IDs under the same directory.

## Output

### EvalResult file

```shell
output/
└── contextmessage-app/
    └── contextmessage-app_contextmessage-basic_ae6d4fd9-87c4-4b58-980b-cf01b963f0df.evalset_result.json    # EvalResult file for contextmessage-basic EvalSet.
```
### Log

```log
✅ Evaluation completed with local storage
App: contextmessage-app
Eval Set: contextmessage-basic
Overall Status: passed
Runs: 1
Case identity_name -> passed
  Metric llm_final_response: score 1.00 (threshold 1.00) => passed
```
