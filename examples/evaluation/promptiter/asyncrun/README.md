# PromptIter AsyncRun Example

This example runs PromptIter end-to-end through `manager.Start` and `manager.Get`.

The candidate agent is a single `llmagent` with a deliberately simple instruction. PromptIter directly optimizes that one instruction so the example highlights asynchronous run lifecycle management instead of HTTP transport.

## Data Files

By default, `-data-dir` is `./data`, and this example reads files from `./data/promptiter-nba-commentary-app/`:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The train and validation sets are generated directly from a real sports-business `jsonl` file, but each case stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as live-call style and concise length are enforced by the shared metric file and optimized instruction rather than being embedded inside every eval-case input. Each eval case also stores one pre-prepared static reference answer in `conversation[0].finalResponse`, so asynchronous runs evaluate against the same fixed gold answers every time.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Base directory containing `promptiter-nba-commentary-app/` | `./data` |
| `-output-dir` | Directory where evaluation results will be stored | `./output` |
| `-model` | Model identifier used by the candidate agent | `deepseek-v4-flash` |
| `-judge-model` | Model identifier used by the judge agent | `gpt-5.4` |
| `-worker-model` | Model identifier used by the PromptIter backwarder, aggregator, and optimizer agents | `gpt-5.4` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-min-score-gain` | Minimum validation score gain required to accept a patch | `0.005` |
| `-max-rounds-without-acceptance` | Maximum consecutive rejected rounds before stopping | `5` |
| `-target-score` | Target validation score that stops optimization when reached | `1.0` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-poll-interval` | Polling interval used to wait for asynchronous run completion | `1s` |

## Run

```bash
cd examples/evaluation/promptiter/asyncrun
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -model "deepseek-v4-flash" \
  -judge-model "gpt-5.4" \
  -worker-model "gpt-5.4"
```

Replace the model identifiers when your endpoint exposes different model names.

The default settings enable parallel evaluation for throughput. If your endpoint enforces stricter concurrency limits, lower the parallelism or disable the parallel flags.

## What It Does

- Builds the same PromptIter runtime as `syncrun`.
- Starts an asynchronous run through `manager.Start`.
- Polls run state through `manager.Get` until the run reaches a terminal state.
- Prints asynchronous progress updates such as the current round and stage while polling.
- Prints the baseline validation score and each round validation score as soon as they become available.
- Prints the run ID, accepted instruction, and score changes after completion.
- Writes raw evaluation results under `./output`.

## Sample Output

A representative run with the default example settings produced the following console summary:

```text
Started async run: 55048e70-ca1b-47fb-8a4a-34c5fd15441c
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: queued
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: baseline validation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 1 train evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c baseline validation score: 0.35
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 1 backward pass
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 1 gradient aggregation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 1 optimizer
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 1 validation evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 2 train evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c round 1 validation score: 0.79
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 2 backward pass
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 2 gradient aggregation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 2 optimizer
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 2 validation evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 3 train evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c round 2 validation score: 0.90
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 3 terminal loss extraction
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 4 train evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c round 3 validation score: 0.89
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 4 backward pass
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 4 gradient aggregation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 4 optimizer
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: round 4 validation evaluation
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c progress: succeeded
Run 55048e70-ca1b-47fb-8a4a-34c5fd15441c round 4 validation score: 0.89
✅ PromptIter asyncrun sports commentary example completed
Data directory: ./data
Result directory: ./output
Structure ID: struct_eaa7b0231861010fad5a7ec8dba6ffbd3c0f171a7cecd04fe2adeb9bcc3d8e76
Target node: candidate
Target surface ID: candidate#instruction
Initial instruction: "Write one Chinese sentence that summarizes the JSON input. Output only the text."
Accepted instruction: "Write exactly one Chinese sentence of text-only, broadcast-ready live basketball commentary based only on the provided JSON input. Focus first on the latest current_event/current play, not a whole-game recap. Preserve the key player(s), action, and immediate result of that play, and include the most salient supported live context from the input—especially the game clock and score from current_event or score_at_event when available, plus any immediate game-state implication if explicitly supported. If the base play description is very short, add one more concrete supported live detail such as the player name, make/miss/foul/rebound/free throw outcome, clock, or score. Keep the sentence punchy, energetic, natural, spoken, and in present tense with live-call feel, avoiding filler, generic stat-summary language, or flat report-like phrasing. Do not invent or infer play mechanics, positioning, shot location, opponent involvement, or any other detail not explicitly supported by current_event or recent_events."
Initial validation score: 0.35
Final accepted validation score: 0.90
Rounds executed: 4
Round 1 -> train 0.30, validation 0.79, accepted true, delta 0.44, stop=false (continue optimization)
Round 2 -> train 0.72, validation 0.90, accepted true, delta 0.11, stop=false (continue optimization)
Round 3 -> train 1.00, validation 0.89, accepted false, delta -0.00, stop=false (continue optimization)
Round 4 -> train 0.87, validation 0.89, accepted false, delta -0.01, stop=true (max rounds reached)
```

Because progress is reported by polling `manager.Get`, the output shows observable stage snapshots instead of a guaranteed event-by-event trace. Fast transient phases can be skipped between polling ticks.
