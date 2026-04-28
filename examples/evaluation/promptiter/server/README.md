# PromptIter Server Example

This example exposes the sports-commentary PromptIter workflow as an HTTP control-plane service.

It serves PromptIter through [server/promptiter](/cbs/workspace/external-trpc-agent-go/promptiter-docs/trpc-agent-go/server/promptiter).
The candidate app behind the server is a single `llmagent` with a deliberately simple summary-style instruction. PromptIter optimizes that one instruction directly.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |
| `CANDIDATE_MODEL_NAME` | Default model used by the candidate agent | `deepseek-chat` |
| `JUDGE_MODEL_NAME` | Default model used by the judge agent | `gpt-5.4` |
| `WORKER_MODEL_NAME` | Default model used by the PromptIter backwarder, aggregator, and optimizer agents | `gpt-5.4` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-addr` | Listen address for the PromptIter server | `:8080` |
| `-base-path` | Base path exposed by the PromptIter server | `/promptiter/v1/apps` |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-model` | Model identifier used by the candidate agent | `$CANDIDATE_MODEL_NAME` or `deepseek-chat` |
| `-candidate-instruction` | Instruction used by the candidate agent | The shared default summary seed |
| `-judge-model` | Model identifier used by the judge agent | `$JUDGE_MODEL_NAME` or `gpt-5.4` |
| `-worker-model` | Model identifier used by the PromptIter backwarder, aggregator, and optimizer agents | `$WORKER_MODEL_NAME` or `gpt-5.4` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |

## Data Files

The server example keeps its own data under `./data/promptiter-nba-commentary-app/`:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The eval sets are generated from the same sports-business source data as the syncrun example, but every case stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as concise length and spoken live-call style are enforced by the shared metric file and optimized instruction instead of being embedded inside each sample. Each eval case also stores one pre-prepared static reference answer in `conversation[0].finalResponse`, so the server runtime evaluates against the same fixed gold answers on every request.

## Run

```bash
cd examples/evaluation/promptiter/server
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
export CANDIDATE_MODEL_NAME="deepseek-chat"
export JUDGE_MODEL_NAME="gpt-5.4"
export WORKER_MODEL_NAME="gpt-5.4"
go run . \
  -addr ":8080" \
  -base-path "/promptiter/v1/apps" \
  -data-dir "./data" \
  -output-dir "./output" \
  -model "deepseek-chat" \
  -judge-model "gpt-5.4" \
  -worker-model "gpt-5.4"
```

Replace the model identifiers when your endpoint exposes different model names.

The default settings enable parallel evaluation for throughput. If your endpoint enforces stricter concurrency limits, lower the parallelism or disable the parallel flags.

The shared metric file combines one reference-aware LLM rubric metric and one deterministic final-response length evaluator. This keeps the concise-output objective while evaluating against fixed reference commentary without double-counting overlapping rubric signals.

The server exposes:

- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/structure`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/runs`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs`
- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}/cancel`

## Minimal Client

The example also includes a minimal Python client at [client.py](/cbs/workspace/external-trpc-agent-go/promptiter-docs/trpc-agent-go/examples/evaluation/promptiter/server/client.py). It uses only the Python standard library, resolves the target instruction surface from `/structure`, submits a run request, prints asynchronous progress and validation scores while polling, and then prints a compact final summary.

The client uses the same run-policy defaults as `syncrun` and `asyncrun`: `max-rounds=4`, `min-score-gain=0.005`, `max-rounds-without-acceptance=5`, `target-score=1.0`, and parallel evaluation enabled with parallelism `8`.

Run the default asynchronous flow:

```bash
cd examples/evaluation/promptiter/server
python3 client.py
```

Override the run policy with the same knobs exposed by `syncrun`:

```bash
python3 client.py \
  --max-rounds 4 \
  --min-score-gain 0.005 \
  --max-rounds-without-acceptance 5 \
  --target-score 1.0
```

Run the blocking endpoint instead:

```bash
python3 client.py --mode blocking
```

## Sample Output

A representative asynchronous run with the default client settings produced the following summary:

```text
Resolved target surface: candidate#instruction
Started async run: <run_id>
Progress: baseline validation
Progress: round 1 train evaluation
Baseline validation score: 0.35
Progress: round 1 backward pass
Progress: round 1 gradient aggregation
Progress: round 1 optimizer
Progress: round 1 validation evaluation
Progress: round 2 train evaluation
Round 1 validation score: 0.92
Progress: round 2 backward pass
Progress: round 2 gradient aggregation
Progress: round 2 optimizer
Progress: round 2 validation evaluation
Progress: round 3 train evaluation
Round 2 validation score: 0.90
Progress: round 3 backward pass
Progress: round 3 gradient aggregation
Progress: round 3 optimizer
Progress: round 3 validation evaluation
Progress: round 4 train evaluation
Round 3 validation score: 0.90
Progress: round 4 backward pass
Progress: round 4 gradient aggregation
Progress: round 4 optimizer
Progress: round 4 validation evaluation
Progress: succeeded
Round 4 validation score: 0.93
Run summary:
  Status: succeeded
  Baseline validation score: 0.35
  Final accepted validation score: 0.93
  Round 1 -> train 0.30, validation 0.92, accepted True, delta 0.58
  Round 2 -> train 0.88, validation 0.90, accepted False, delta -0.03
  Round 3 -> train 0.97, validation 0.90, accepted False, delta -0.02
  Round 4 -> train 0.93, validation 0.93, accepted True, delta 0.01
```

This output format comes from polling the asynchronous run resource, so it reflects observable run snapshots rather than a guaranteed event-by-event trace. Short transient phases can be skipped between polling ticks.

## Example Requests

Fetch the current editable structure:

```bash
curl "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/structure"
```

Use the returned structure to find the editable `candidate` instruction surface. The example currently resolves to `candidate#instruction`, but callers should treat that value as structure-derived instead of hard-coding the name.

Run one PromptIter optimization session:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/runs" \
  -H "Content-Type: application/json" \
  -d '{
    "run": {
      "TrainEvalSetIDs": ["nba-commentary-train"],
      "ValidationEvalSetIDs": ["nba-commentary-validation"],
      "TargetSurfaceIDs": ["candidate#instruction"],
        "EvaluationOptions": {
          "EvalCaseParallelism": 8,
          "EvalCaseParallelInferenceEnabled": true,
          "EvalCaseParallelEvaluationEnabled": true
        },
      "AcceptancePolicy": {
        "MinScoreGain": 0.005
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 5,
        "TargetScore": 1
      },
      "MaxRounds": 4
    }
  }'
```

The `runs` endpoint waits until the run reaches a terminal state and then returns the full `run` result directly.

Run one asynchronous PromptIter optimization session:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs" \
  -H "Content-Type: application/json" \
  -d '{
    "run": {
      "TrainEvalSetIDs": ["nba-commentary-train"],
      "ValidationEvalSetIDs": ["nba-commentary-validation"],
      "TargetSurfaceIDs": ["candidate#instruction"],
      "EvaluationOptions": {
          "EvalCaseParallelism": 8,
          "EvalCaseParallelInferenceEnabled": true,
          "EvalCaseParallelEvaluationEnabled": true
        },
      "AcceptancePolicy": {
        "MinScoreGain": 0.005
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 5,
        "TargetScore": 1
      },
      "MaxRounds": 4
    }
  }'
```

The asynchronous endpoint immediately returns a persisted asynchronous `run` view. Use the returned `run.ID` to query lifecycle state and round details:

```bash
curl "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/<run_id>"
```

The run detail response contains:

- run status and current round
- baseline validation result and score
- per-round train and validation scores
- per-round losses, backward results, aggregation, patches, output profiles, acceptance, and stop decisions
- final accepted profile when the run succeeds

Cancel one asynchronous run:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/<run_id>/cancel"
```
