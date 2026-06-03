# PromptIter SyncRun Sports Recap Example

This example runs PromptIter end to end through `engine.Run` against a single `llmagent` sports recap candidate.

It is intentionally the synchronous single-agent counterpart to the multinode sports recap example. The candidate starts with a weak seed instruction:

```text
生成一篇中文体育战报
```

PromptIter optimizes only the candidate instruction surface:

```text
candidate#instruction
```

## Data Files

The example loads these files from `./data/promptiter-nba-commentary-app/` by default:

```text
nba-commentary-train.evalset.json
nba-commentary-validation.evalset.json
sports-commentary.metrics.json
```

The train and validation evalsets each contain eight sports recap cases. Each case passes one compact structured sports JSON object as `conversation[0].userContent.content` and stores one fixed publishable Chinese recap in `conversation[0].finalResponse.content`. The shared metric file evaluates recap length through the built-in final-response evaluator, plus factual grounding, numeric precision, sport-specific terminology, decisive-sequence coverage, headline quality, and Chinese sports-desk copy quality.

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
| `-model` | Model identifier used by the candidate sports recap agent | `deepseek-v3.2` |
| `-candidate-instruction` | Instruction used by the candidate agent | `生成一篇中文体育战报` |
| `-judge-model` | Model identifier used by the judge agent | `gpt-5.2` |
| `-worker-model` | Model identifier used by the PromptIter worker agents | `gpt-5.2` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `16` |
| `-backward-case-parallelism` | Maximum number of train eval cases processed in parallel during backward; `0` uses `GOMAXPROCS` | `16` |
| `-aggregation-parallelism` | Maximum number of target surfaces aggregated in parallel; `0` uses `GOMAXPROCS` | `16` |
| `-optimizer-parallelism` | Maximum number of target surfaces optimized in parallel; `0` uses `GOMAXPROCS` | `16` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-parallel-backward` | Enable parallel backward processing across train eval cases | `false` |
| `-parallel-aggregation` | Enable parallel aggregation across target surfaces | `true` |
| `-parallel-optimization` | Enable parallel optimization across target surfaces | `true` |
| `-min-score-gain` | Minimum validation score gain required to accept a patch | `0.01` |
| `-max-rounds-without-acceptance` | Maximum consecutive rejected rounds before stopping | `3` |
| `-target-score` | Target validation score that stops optimization when reached | `1.0` |
| `-debug-io` | Log candidate, judge, backwarder, aggregator, and optimizer inputs and outputs | `false` |

When a PromptIter stage parallelism flag is `0` and the corresponding `-parallel-*` flag is enabled, the stage uses `GOMAXPROCS` as the default parallelism.

## Run

```bash
cd examples/evaluation/promptiter/syncrun
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -model "deepseek-v3.2" \
  -judge-model "gpt-5.2" \
  -worker-model "gpt-5.2"
```

For deep troubleshooting, enable component IO logs:

```bash
go run . -debug-io=true
```

## Sample Iteration Result

The local `run.log` captures a `-debug-io=true` run. The raw log includes candidate, judge, backwarder, aggregator, and optimizer IO; the useful final summary is:

```text
Initial instruction: "生成一篇中文体育战报"
Initial validation score: 0.37
Final accepted validation score: 0.85
Rounds executed: 4
Round 1 -> train 0.40, validation 0.85, accepted true, delta 0.48, stop=false (continue optimization)
Round 2 -> train 0.82, validation 0.82, accepted false, delta -0.03, stop=false (continue optimization)
Round 3 -> train 0.86, validation 0.68, accepted false, delta -0.17, stop=false (continue optimization)
Round 4 -> train 0.89, validation 0.79, accepted false, delta -0.06, stop=true (max rounds reached)
```

Round 1 is the only accepted patch in this run. It moves the weak seed prompt into a concrete instruction that requires JSON grounding, numeric and format checks, non-Markdown Chinese output, length control, `recapAngle`/`styleNote` alignment, and sport-specific notation handling. Later rounds improve train score, but their validation scores are lower than the accepted candidate, so PromptIter keeps the round 1 instruction.

<details>
<summary><code>candidate#instruction</code> accepted from <code>run.log</code></summary>

```text
你将收到一份用户提供的比赛信息JSON。请生成一篇可直接发布的专业中文体育战报，并严格遵守以下规则：
1) 严格且仅依据JSON字段写作：不得引入JSON未提供的比赛细节、技战术过程、身份关系、主观评价或因果推断；不得添加排名、晋级轮次、下一轮对手、未来赛程等信息。若JSON中nextStageKnown=false，则不写具体下一轮信息；如需提及，仅可写“晋级下一轮/待定”。
2) 数字与记法必须逐项核对：对比分、分节/局分、盘分、抢七、安打失误、overs等所有数字逐项检查，必要时自行复算，确保无异常值；禁止改变方向或顺序（例如18-21不可写成21-18）。
3) 格式严格保真：比分连字符、抢七括号等按JSON的styleNote或原字段格式原样保留；数字格式如“3-3”不加空格；人名/队名/拼写在全文保持一致。
4) 输出为中文纯文本：不使用任何Markdown或标题符号，不加粗，不用项目符号列表。
5) 篇幅控制：全文约350-850字。
6) 标题与导语：标题与导语必须贴合JSON中的recapAngle与styleNote，突出关键数字与转折节点（如关键回合/关键局分/关键轮次），避免模板化与空泛渲染。
7) 运动专项记法严谨：使用该项目的标准表述；例如板球19.1-19.4属于第20个over的球序，不可混称20.0；no-ball与free hit的得分表述应为该次进攻合计得分，不写成“单球8分”等不严谨说法。
```

</details>

## What It Does

- Loads fixed train and validation sports recap evalsets from local files.
- Evaluates the candidate against structured sports JSON inputs and static Chinese reference recaps.
- Uses the shared sports recap rubric metric from the multinode example plus the built-in final-response text length criterion.
- Enables parallel inference and parallel evaluation across eval cases by default.
- Exposes the same PromptIter stage parallelism controls as the multinode example.
- Optimizes only the single `candidate#instruction` surface.
- Prints the initial instruction, accepted instruction, score changes, and per-round patches.
- Writes raw evaluation results under `./output`.
