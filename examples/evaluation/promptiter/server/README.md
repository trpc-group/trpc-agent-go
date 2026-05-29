# PromptIter Server Sports Recap Example

This example exposes the single-agent sports recap PromptIter workflow as an HTTP control-plane service through `server/promptiter`.

The candidate app behind the server is a single `llmagent` with the same weak seed instruction used by `syncrun` and `asyncrun`:

```text
生成一篇中文体育战报
```

PromptIter optimizes only the candidate instruction surface:

```text
candidate#instruction
```

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |
| `CANDIDATE_MODEL_NAME` | Default model used by the candidate sports recap agent | `deepseek-v3.2` |
| `JUDGE_MODEL_NAME` | Default model used by the judge agent | `gpt-5.2` |
| `WORKER_MODEL_NAME` | Default model used by the PromptIter worker agents | `gpt-5.2` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-addr` | Listen address for the PromptIter server | `:8080` |
| `-base-path` | Base path exposed by the PromptIter server | `/promptiter/v1/apps` |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-model` | Model identifier used by the candidate sports recap agent | `$CANDIDATE_MODEL_NAME` or `deepseek-v3.2` |
| `-candidate-instruction` | Instruction used by the candidate agent | `生成一篇中文体育战报` |
| `-judge-model` | Model identifier used by the judge agent | `$JUDGE_MODEL_NAME` or `gpt-5.2` |
| `-worker-model` | Model identifier used by the PromptIter worker agents | `$WORKER_MODEL_NAME` or `gpt-5.2` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `16` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |

PromptIter run-policy and stage-parallelism settings are supplied by the HTTP run request or by `client.py`, not by server process flags.

## Data Files

The server example keeps its own data under `./data/promptiter-nba-commentary-app/`:

```text
nba-commentary-train.evalset.json
nba-commentary-validation.evalset.json
sports-commentary.metrics.json
```

The train and validation evalsets each contain eight sports recap cases. Each case passes one compact structured sports JSON object as `conversation[0].userContent.content` and stores one fixed publishable Chinese recap in `conversation[0].finalResponse.content`. The shared metric file evaluates recap length through the built-in final-response evaluator, plus factual grounding, numeric precision, sport-specific terminology, decisive-sequence coverage, headline quality, and Chinese sports-desk copy quality.

## Run

```bash
cd examples/evaluation/promptiter/server
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
export CANDIDATE_MODEL_NAME="deepseek-v3.2"
export JUDGE_MODEL_NAME="gpt-5.2"
export WORKER_MODEL_NAME="gpt-5.2"
go run . \
  -addr ":8080" \
  -base-path "/promptiter/v1/apps" \
  -data-dir "./data" \
  -output-dir "./output" \
  -model "deepseek-v3.2" \
  -judge-model "gpt-5.2" \
  -worker-model "gpt-5.2"
```

The server exposes:

- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/structure`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/runs`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs`
- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}/cancel`

## Minimal Client

The example also includes a minimal Python client at [client.py](./client.py). It uses only the Python standard library, resolves the target instruction surface from `/structure`, submits a run request, prints asynchronous progress and validation scores while polling, and then prints a compact final summary.

The client uses the same run-policy defaults as `syncrun` and `asyncrun`: `max-rounds=4`, `min-score-gain=0.01`, `max-rounds-without-acceptance=3`, `target-score=1.0`, eval-case parallelism `16`, backward parallelism disabled by default, and aggregation/optimization parallelism enabled with parallelism `16`.

Run the default asynchronous flow:

```bash
cd examples/evaluation/promptiter/server
python3 client.py
```

Override the run policy and stage controls:

```bash
python3 client.py \
  --max-rounds 4 \
  --min-score-gain 0.01 \
  --max-rounds-without-acceptance 3 \
  --target-score 1.0 \
  --eval-case-parallelism 16 \
  --backward-case-parallelism 16 \
  --aggregation-parallelism 16 \
  --optimizer-parallelism 16 \
  --parallel-aggregation \
  --parallel-optimization
```

Run the blocking endpoint instead:

```bash
python3 client.py --mode blocking
```

## Sample Output

The local `client.log` captures one actual async client run with the default settings. This run improved validation score from `0.42` to `0.84`: round 1 and round 3 were accepted, while round 2 and round 4 were rejected because they did not improve the current accepted candidate enough.

```text
Resolved target surface: candidate#instruction
Started async run: 17712beb-d592-4d4f-a5b7-784dbd323b34
Progress: baseline validation
Progress: round 1 train evaluation
Baseline validation score: 0.42
Progress: round 1 backward pass
Progress: round 1 gradient aggregation
Progress: round 1 optimizer
Progress: round 1 validation evaluation
Progress: round 2 train evaluation
Round 1 validation score: 0.77
...
Round 3 validation score: 0.84
Progress: round 4 validation evaluation
Progress: succeeded
Round 4 validation score: 0.84
Run summary:
  ID: 17712beb-d592-4d4f-a5b7-784dbd323b34
  Status: succeeded
  Baseline validation score: 0.42
  Final accepted validation score: 0.84
  Round 1 -> train 0.59, validation 0.77, accepted True, delta 0.34
  Round 2 -> train 0.86, validation 0.76, accepted False, delta -0.01
  Round 3 -> train 0.78, validation 0.84, accepted True, delta 0.07
  Round 4 -> train 0.84, validation 0.84, accepted False, delta 0.01
```

<details>
<summary><code>candidate#instruction</code> accepted from <code>client.log</code></summary>

```text
你将根据用户提供的“比赛事实JSON”生成一篇中文体育战报/新闻稿。

硬性规则：
1) 仅基于JSON中明确提供的事实写作：只能复用JSON给出的数据、名称、时间、地点、比分、技术统计、事件与原话口径；不得补充或臆测任何未提供的信息（包括但不限于背景故事、身份/国籍、排名/晋级/赛程与未来对手、预测、主观评价、因果解读、场景与过程细节如主场氛围/逼抢/裁判缘由/门将描写/战术心理等），除非JSON明确给出。
2) 输出必须为纯文本分段；不要使用任何Markdown或类Markdown符号（如#、##、**、---、表格、项目符号/列表等）。
3) 篇幅控制在约350–850字。
4) 必须提供独立标题行（第一行仅标题），第二段为导语。标题与导语必须聚焦JSON明确的主线与关键数字/关键节点（优先使用JSON给定的recapAngle与关键时间点/关键回合/决定性统计）；不得自行拔高为“逆转/最大功臣/晋级/创造历史”等结论，除非JSON明确写明。
5) 比分、记法与口径必须严格按JSON原样呈现：不改写、不合并、不推导；不得把不同阶段合并为“总比分”等。
   - 足球：常规时间/加时/点球需分别写清（如JSON分别给出则分别呈现），不得合并口径。
   - 网球：盘分与抢七按JSON给定的连字符与括号格式呈现（如7-6(6)）；如JSON给出finalSetTiebreak则按其格式呈现（如8-6）。
   - 羽毛球/排球等：局分按JSON给定顺序与格式逐局呈现（如18-21、23-21、21-15），连字符两侧不留空格。
   - 板球：over记法将“19.4”表述为“第20个over第4球/19.4球”，不得写成“第19.4个over”；如JSON要求逐球或关键节点，仅覆盖JSON给出的球与节点，不补写未给出的球。
   - MLB：仅写JSON给出的比分、walk-off、安打/失误、关键球员数据；如有blownSave仅按该字段表述，不扩展为败投等未给信息。
6) 关键回合、时间线与技术统计只能来自JSON字段（如turningPoints、runningScore、keyStats、events、quotes等）；不得自行补过程细节。对未说明原因的失误/未进球等，使用中性表述（如“射失/罚失/未能命中”），不写“被扑出/打偏”等除非JSON明确。

写作要求：结构清晰、信息密度高。先交代比赛结果与关键数字，再按JSON提供的时间线/关键回合/比分变化展开，可通过更充分复述runningScore变化、turningPoints顺序、双方keyStats对照来扩写以满足字数，但不加入解释性原因分析；最后仅用JSON中明确给出的赛后信息（如官方表述/引语/后续安排字段）收束。
```

</details>

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
      "train": [
        {
          "evalSetId": "nba-commentary-train"
        }
      ],
      "validation": [
        {
          "evalSetId": "nba-commentary-validation"
        }
      ],
      "TargetSurfaceIDs": ["candidate#instruction"],
      "EvaluationOptions": {
        "EvalCaseParallelism": 16,
        "EvalCaseParallelInferenceEnabled": true,
        "EvalCaseParallelEvaluationEnabled": true
      },
      "BackwardOptions": {
        "CaseParallelismEnabled": false,
        "CaseParallelism": 16
      },
      "AggregationOptions": {
        "SurfaceParallelismEnabled": true,
        "SurfaceParallelism": 16
      },
      "OptimizerOptions": {
        "SurfaceParallelismEnabled": true,
        "SurfaceParallelism": 16
      },
      "AcceptancePolicy": {
        "MinScoreGain": 0.01
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 3,
        "TargetScore": 1.0
      },
      "MaxRounds": 4
    }
  }'
```

The `runs` endpoint waits until the run reaches a terminal state and then returns the full `run` result directly.

To restrict a run to selected eval cases, add `evalCaseIds` to the corresponding eval set input:

```json
{
  "run": {
    "train": [
      {
        "evalSetId": "nba-commentary-train",
        "evalCaseIds": ["case_1", "case_2"]
      }
    ],
    "validation": [
      {
        "evalSetId": "nba-commentary-validation",
        "evalCaseIds": ["case_3"]
      }
    ]
  }
}
```

Omitting `evalCaseIds` or passing an empty array runs all cases in that eval set. Passing a non-empty array runs only the listed cases. Empty string case IDs are invalid.

Run one asynchronous PromptIter optimization session with the same run payload:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs" \
  -H "Content-Type: application/json" \
  -d '{
    "run": {
      "train": [
        {
          "evalSetId": "nba-commentary-train"
        }
      ],
      "validation": [
        {
          "evalSetId": "nba-commentary-validation"
        }
      ],
      "TargetSurfaceIDs": ["candidate#instruction"],
      "EvaluationOptions": {
        "EvalCaseParallelism": 16,
        "EvalCaseParallelInferenceEnabled": true,
        "EvalCaseParallelEvaluationEnabled": true
      },
      "BackwardOptions": {
        "CaseParallelismEnabled": false,
        "CaseParallelism": 16
      },
      "AggregationOptions": {
        "SurfaceParallelismEnabled": true,
        "SurfaceParallelism": 16
      },
      "OptimizerOptions": {
        "SurfaceParallelismEnabled": true,
        "SurfaceParallelism": 16
      },
      "AcceptancePolicy": {
        "MinScoreGain": 0.01
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 3,
        "TargetScore": 1.0
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
