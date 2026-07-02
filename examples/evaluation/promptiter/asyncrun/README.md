# PromptIter AsyncRun Sports Recap Example

This example runs PromptIter end to end through `manager.Start` and `manager.Get` against a single `llmagent` sports recap candidate. It uses the same candidate shape and local eval data as `syncrun`, but demonstrates asynchronous run lifecycle management and progress polling.

The candidate starts with a weak seed instruction:

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
| `-poll-interval` | Polling interval used to wait for asynchronous run completion | `1s` |

When a PromptIter stage parallelism flag is `0` and the corresponding `-parallel-*` flag is enabled, the stage uses `GOMAXPROCS` as the default parallelism.

## Run

```bash
cd examples/evaluation/promptiter/asyncrun
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

Async runs print progress snapshots from `manager.Get` while the run is active, followed by the same final summary printed by `syncrun`. Fast transient phases can be skipped between polling ticks.

The local `run.log` captures one actual async run with the default flags. This run improved validation score from `0.49` to `0.90`: round 1 and round 2 were accepted, while round 3 and round 4 were rejected because their validation scores dropped below the current accepted candidate.

```text
Data directory: ./data
Result directory: ./output
Target node: candidate
Target surface ID: candidate#instruction
Initial instruction: "生成一篇中文体育战报"
Initial validation score: 0.49
Final accepted validation score: 0.90
Rounds executed: 4
Round 1 -> train 0.35, validation 0.85, accepted true, delta 0.36, stop=false (continue optimization)
Round 2 -> train 0.83, validation 0.90, accepted true, delta 0.05, stop=false (continue optimization)
Round 3 -> train 0.87, validation 0.81, accepted false, delta -0.09, stop=false (continue optimization)
Round 4 -> train 0.89, validation 0.80, accepted false, delta -0.10, stop=true (max rounds reached)
```

<details>
<summary><code>candidate#instruction</code> accepted from <code>run.log</code></summary>

```text
仅基于用户输入的JSON中“明确提供”的事实与数据，撰写一篇可发布的中文体育战报（纯文本新闻体）。

硬性要求：
1) 禁止任何无依据的扩写/推断/补充：不得模拟引语；不得添加来源/报道时间；不得自行补充日期地点、联赛/积分榜、未来赛程与展望、球员/球队身份标签与国籍归属、主客场信息、战术过程、因果归因、心理/状态描写、射门方向等JSON未给出的内容。若JSON出现nextStageKnown=false等不确定字段，不得写“晋级/出局/挺进下一轮”等结论，只能陈述JSON已给赛果与比分。
2) 关键数字与格式必须与JSON完全一致并逐项核对，不得改写或自相矛盾：比分/局分/盘分/小分/目标分、时间点（如90+4必须原样）、各类统计（如2-for-4、打数安打、三振、失分、wickets等）均按JSON原样表达，不得自行换算或改写口径。所有比分统一用连字符且不加空格（如“3-2”“23-21”“3-6”“2-2”）。如JSON涉及特定项目记分，必须保留其规范写法且与JSON一致：网球抢七/盘分括号写法（如“7-6(6)”或finalSetTiebreak“8-6”）；ace写作“ace”；板球overs/balls remaining按JSON原样呈现（如“19.4 overs”不得当作小数回合随意解释），并确保wickets含义不被误写。
3) 输出不得包含任何Markdown或排版符号：不使用#、##、---、表格、加粗、项目符号/编号列表等。
4) 篇幅控制：默认约350-850字，且不超过850字（除非JSON另有明确要求）。
5) 结构：必须包含标题与导语。标题与导语需在开头直接突出recapAngle（若JSON提供）与决定性关键数字/关键制胜信息（如点球5-4、第六轮决胜、118分钟被扳平后点球取胜等，均以JSON为准）。正文严格按JSON给定的时间线/turningPoints/keyMoments组织叙述，完整保留已提供的关键事实链条与数据，避免加入未给背景或结论。
```

</details>

## What It Does

- Builds the same single-agent PromptIter runtime shape as `syncrun`.
- Starts an asynchronous run through `manager.Start`.
- Polls run state through `manager.Get` until the run reaches a terminal state.
- Prints asynchronous progress updates such as the current round and stage while polling.
- Uses the shared sports recap rubric metric from the multinode example plus the built-in final-response text length criterion.
- Exposes the same PromptIter stage parallelism controls as the multinode and syncrun examples.
- Optimizes only the single `candidate#instruction` surface.
- Prints the initial instruction, accepted instruction, score changes, and per-round patches.
- Writes raw evaluation results under `./output`.
