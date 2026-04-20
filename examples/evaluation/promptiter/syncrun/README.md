# PromptIter SyncRun Example

This example runs PromptIter end to end on a real sports live-commentary generation task through `engine.Run`.

The candidate agent is a single `llmagent` with a deliberately simple instruction. PromptIter directly optimizes that one instruction so the example highlights instruction tuning instead of relying on a heavy graph scaffold. The initial seed should stay simple on purpose, because a strong hand-written starting instruction weakens the demonstration value of PromptIter itself.

## Data Files

The example loads these files from `./data/promptiter-nba-commentary-app/` by default:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The train and validation sets are generated directly from a real sports-business `jsonl` file, but each case now stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as live-call style and concise length are enforced by the shared metric file and optimized instruction rather than being embedded inside every eval-case input. Each eval case also stores one pre-prepared static reference answer in `conversation[0].finalResponse`, so repeated runs evaluate against the same fixed gold answers.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where evaluation results will be stored | `./output` |
| `-model` | Model identifier used by the candidate agent | `deepseek-chat` |
| `-judge-model` | Model identifier used by the judge agent | `gpt-5.4` |
| `-worker-model` | Model identifier used by the PromptIter backwarder, aggregator, and optimizer agents | `gpt-5.4` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-min-score-gain` | Minimum validation score gain required to accept a patch | `0.005` |
| `-max-rounds-without-acceptance` | Maximum consecutive rejected rounds before stopping | `5` |
| `-target-score` | Target validation score that stops optimization when reached | `1.0` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-debug-io` | Log candidate, judge, backwarder, aggregator, and optimizer inputs and outputs | `false` |

## Run

```bash
cd examples/evaluation/promptiter/syncrun
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -model "deepseek-chat" \
  -judge-model "gpt-5.4" \
  -worker-model "gpt-5.4"
```

Replace the model identifiers when your endpoint exposes different model names.

For deep troubleshooting, enable component IO logs:

```bash
go run . -debug-io=true
```

The syncrun example uses:

- `candidate=deepseek-chat`
- `judge=gpt-5.4`
- `worker=gpt-5.4`

## What It Does

- Loads train and validation eval sets from local files.
- Evaluates the candidate on structured sports game-state JSON inputs.
- Enables parallel inference and parallel evaluation across eval cases by default for faster end-to-end runs.
- Uses static gold commentary references stored directly in the eval sets.
- Uses one reference-aware LLM rubric metric and one deterministic final-response length evaluator.
- Keeps the default `backwarder`, `aggregator`, and `optimizer` prompts generic; the example does not hard-code sports-specific worker prompts.
- Directly targets the known `candidate#instruction` surface in `TargetSurfaceIDs`.
- Optimizes only that single candidate `instruction` surface via `TargetSurfaceIDs`.
- Prints the initial instruction, accepted instruction, and score changes.
- Writes raw evaluation results under `./output`.

## Sample Result

A representative run with the default example settings produced the following console summary:

```text
✅ PromptIter syncrun sports commentary example completed
Data directory: ./data
Result directory: ./output
Target node: candidate
Target surface ID: candidate#instruction
Initial instruction: "Write one Chinese sentence that summarizes the JSON input. Output only the text."
Accepted instruction: "Write one concise, natural, energetic Chinese live-play commentary sentence based on the JSON input. Center it on the latest play in current_event and recent_events, and explicitly state the decisive actor, action, and outcome of that play. Preserve exact event-specific details from the input; do not generalize into a broad game summary or recap. Include at least one exact, grounded live detail from the input when available, preferably the game clock, exact score, or lead/deficit situation. Do not add unverified embellishment or infer details not explicitly supported by current_event or recent_events. Output only the sentence."
Initial validation score: 0.35
Final accepted validation score: 0.92
Rounds executed: 4
Round 1 -> train 0.25, validation 0.81, accepted true, delta 0.46, stop=false (continue optimization)
  Instruction patch [candidate]: "Write one concise, natural, energetic Chinese live-play commentary sentence based on the JSON input. Center it on the latest play in current_event and recent_events, and explicitly state the decisive actor, action, and outcome of that play. Preserve exact event-specific details from the input; do not generalize into a broad game summary or recap. If clearly salient, include one concrete supporting detail such as the score, time, or game situation. Output only the sentence."
  Patch reason: Refocuses the instruction from generic JSON summary to one-sentence Chinese live commentary grounded in the latest play, requiring actor/action/outcome and allowing one salient supporting detail.
Round 2 -> train 0.88, validation 0.92, accepted true, delta 0.12, stop=false (continue optimization)
  Instruction patch [candidate]: "Write one concise, natural, energetic Chinese live-play commentary sentence based on the JSON input. Center it on the latest play in current_event and recent_events, and explicitly state the decisive actor, action, and outcome of that play. Preserve exact event-specific details from the input; do not generalize into a broad game summary or recap. Include at least one exact, grounded live detail from the input when available, preferably the game clock, exact score, or lead/deficit situation. Do not add unverified embellishment or infer details not explicitly supported by current_event or recent_events. Output only the sentence."
  Patch reason: Tightens grounding by requiring one exact live detail when available and explicitly forbids unsupported embellishment, while preserving the existing concise latest-play focus.
Round 3 -> train 1.00, validation 0.90, accepted false, delta -0.02, stop=false (continue optimization)
Round 4 -> train 1.00, validation 0.91, accepted false, delta -0.01, stop=true (max rounds reached)
```

This sample shows the intended optimization pattern: PromptIter starts from a deliberately weak seed, accepts two instruction patches that materially improve validation quality, and then rejects later over-optimization rounds.
