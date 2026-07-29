# PromptIter Regression Loop

An end-to-end evaluation + optimization closed loop built on top of the
PromptIter engine (`evaluation/workflow/promptiter`): baseline evaluation,
causal failure attribution, prompt optimization, per-case validation
regression, a two-stage acceptance gate, and audited reporting.

The pipeline answers one question: **is the optimized prompt actually worth
accepting?** It refuses candidates that overfit the train set, regress
protected validation cases, introduce new hard failures, or blow the budget —
even when the aggregate score goes up.

## Layout

A single flat `package main`, matching the other `examples/evaluation`
directories:

- `main.go` — CLI entry, flags, and fake/real component assembly.
- `agent.go` — the candidate order assistant with two deterministic tools.
- `fake.go` — deterministic scripted model and PromptIter workers (no API key).
- `config.go` — `promptiter.json` loading, defaulting, and validation.
- `pipeline.go` — S1–S6 orchestration.
- `promote.go` — the `-promote` action installing accepted artifacts as the
  new baseline.
- `evaluate.go` — case snapshots, metric locator, and cost tracking.
- `attribution.go` — the failure attribution rule engine.
- `gate.go` — per-case delta computation and the acceptance gate.
- `report.go` — report generation and the audit trail writer.
- `data/promptiter-regression-app/` — inputs:
  - `train.evalset.json` / `validation.evalset.json` — 4 + 3 cases
  - `trace.evalset.json` — 2 recorded trace-mode cases (see Trace mode below)
  - `metrics.json` — deterministic metrics (final response exact + tool trajectory)
  - `baseline_prompt.txt` — the prompt under optimization (source of truth)
  - `promptiter.json` — pipeline configuration (strict gate preset)
- `output/` — committed sample reports (`optimization_report.json` / `.md`)
  produced by one fake-mode run. Rerunning regenerates them plus the full
  audit trail and per-case eval results.

## Run (fake mode, no API key)

```bash
go run . -mode fake
```

Finishes in well under a second and produces:

- `output/optimization_report.json` — machine-readable report: baseline,
  candidate, per-case delta, gate decision, attribution stats, cost summary.
- `output/optimization_report.md` — the human verdict, first section readable
  by non-engineers.
- `output/candidate_prompt.txt` + `output/candidate_profile.json` — only when
  the gate accepts: the instruction text plus the full **effective** profile —
  the accepted overrides merged onto any previously written-back baseline
  profile, so the artifact always describes the complete behavior that passed
  the gate (e.g. an improved tool description inherited from an earlier
  acceptance), not just this run's patches. The reports and the candidate
  artifacts are published as **one transaction**: a rejecting run removes any
  stale accepted candidate in the same unit that writes its rejection report,
  an acceptance that changes no instruction (only e.g. a tool description)
  removes the prompt artifact of an earlier acceptance in the same output dir
  instead of leaving unevaluated text on the stable prompt path, and a failure
  anywhere in the publication rolls everything back, so the stable paths never
  show reports that disagree with the candidate state next to them. The
  transaction also holds a lock file (`.promptiter-publish.lock`) in every
  directory it touches, so a concurrent run or `-promote` in another process
  waits instead of interleaving its own snapshot and writes.
- With `-write-back`, acceptance also updates the on-disk baseline:
  `baseline_prompt.txt` receives the instruction text, and
  `baseline_profile.json` (next to it) receives the **merged effective
  profile** — the accepted overrides layered onto any previously written-back
  profile, so consecutive write-backs never drop inherited overrides such as
  the improved tool description. The next run reloads this profile as its
  baseline: the instruction still comes from `baseline_prompt.txt`, and tool
  descriptions are restored into the agent.
- Without `-write-back`, the accepted artifacts are promoted later, after
  review, by the same binary:

  ```bash
  go run . -promote -config ./data/promptiter-regression-app/promptiter.json -output-dir ./output
  ```

  This is what the report recommends. It validates that
  `candidate_prompt.txt` and `candidate_profile.json` agree on the
  instruction, then installs both in one rollback unit. Two chained `cp`
  commands would not be failure-atomic: a failure on the second leaves
  `baseline_prompt.txt` updated against an old `baseline_profile.json` — a
  baseline combination that never passed the gate. Paths reach the command as
  flag arguments, so whitespace and shell metacharacters in them are inert.
- `output/audit/<runID>/` — run meta (seed, mode, config snapshot), every
  engine event per round, per-round cost/latency, attributions, candidates,
  and the gate decision, isolated per execution so reruns never mix audit
  artifacts. Events are flushed as they happen, so an interrupted run keeps a
  complete partial trail.

Exit code is `0` for both accept and reject — a rejection is a normal
business outcome; only pipeline execution errors exit non-zero.

## Run (real mode)

```bash
export OPENAI_API_KEY="..."
export OPENAI_BASE_URL="..."   # optional, defaults to the OpenAI endpoint
go run . -mode real            # every role defaults to gpt-5.2
```

Real mode assembles OpenAI-compatible models for the candidate (`-model`),
the PromptIter workers (`-worker-model`, backwarder/aggregator/optimizer),
and the judge used by `llmJudge` metrics (`-judge-model`). **All roles
resolve against the single `OPENAI_BASE_URL` endpoint, so that endpoint must
serve every configured model name.** All three default to `gpt-5.2`, which
the default endpoint (`api.openai.com`, used when `OPENAI_BASE_URL` is unset)
serves, so the command above runs as written. Point the flags at your own
model names when the endpoint serves another provider:

```bash
go run . -mode real \
  -model deepseek-v3.2 \
  -worker-model deepseek-v3.2 \
  -judge-model deepseek-v3.2
```

To add an LLM-judged metric, append to `metrics.json`:

```jsonc
{
  "metricName": "llm_rubric_critic",
  "threshold": 1.0,
  "criterion": {
    "llmJudge": {
      "rubrics": [
        {
          "id": "no_fabrication",
          "type": "FINAL_RESPONSE_QUALITY",
          "content": { "text": "回复只能使用工具返回的数据，编造订单信息判不通过。" }
        },
        {
          "id": "plain_text",
          "type": "FORMAT_COMPLIANCE",
          "content": { "text": "回复必须是纯文本，出现 Markdown 语法判不通过。" }
        }
      ]
    }
  }
}
```

Attribution automatically classifies all-`FORMAT_*` rubric metrics as
`format_error` and all-`KNOWLEDGE_*` ones as `knowledge_recall_gap`.

## The seven sample cases

| Case | Baseline | After optimization | Scenario |
|---|---|---|---|
| `train_01_response_completeness` | fail (final_response_mismatch) | pass | optimizable success |
| `train_02_wrong_tool_choice` | fail (tool_call_error) | still fail | optimization ineffective |
| `train_03_stable_tool_pass` | pass | pass | stable |
| `train_04_wrong_tool_argument` | fail (tool_argument_error) | still fail | argument error, optimization ineffective |
| `val_01_generalize_tool_and_format` | fail (missing tool call + vague reply) | pass | generalizes |
| `val_02_protected_format` | pass | **fail** (over-formatting regression) | validation regression → gate rejects |
| `val_03_stable_pass` | pass | pass | stable |

The candidate agent's tools (`query_order`, `query_logistics`) return canned
deterministic data, so no toolmock layer is needed: both the "wrong tool" and
the "wrong argument" paths execute reproducibly (`train_04` queries existing
order ORD-1070 instead of ORD-1007, so the tool succeeds on the wrong record
and attribution must split argument errors from tool-choice errors via the
trajectory diff).

## Gate presets

**Strict (committed `promptiter.json`) — demonstrates overfitting rejection.**
The round-1 candidate raises validation from 0.6667 to 0.8333, so the
engine's inner score gate accepts it. The outer gate still rejects:
`val_02_protected_format` flips pass→fail, tripping `max_regressed_cases`
and `protected_cases`. The report states: 训练集 +0.1250 但验证集 case
val_02_protected_format 由 pass 转 fail，判定为过拟合。

**Relaxed — demonstrates the accept path.** Edit the `gate` section:

```jsonc
"gate": {
  "minValidationScoreGain": 0.02,
  "maxNewHardFails": 1,
  "maxRegressedCases": 1,
  "protectedCases": [],
  "maxModelCalls": 200,
  "maxWallClock": "3m"
}
```

The same candidate now passes every rule; the run ends `accepted`
(recommendation `accept_pending_canary`) and writes
`output/candidate_prompt.txt`. Both paths are locked by integration tests.

## Trace mode (evaluating recorded behavior, zero inference)

Besides the scripted fake model, the pipeline supports the evaluation
service's trace mode: an eval case with `"evalMode": "trace"` carries the
recorded behavior in `actualConversation`, and the service scores it against
`conversation` **without invoking the candidate model at all**. This is the
deterministic path for samples that cannot be scripted in advance — hidden
evaluation sets, canary recordings (`evaluation/evalset/recorder` with trace
mode enabled emits exactly this shape), or hand-authored regression anchors.

`data/promptiter-regression-app/trace.evalset.json` ships two recorded cases:
a passing one and one whose recorded trajectory queried `ORD-1070` instead of
`ORD-1007`. `TestTraceModeEvalSetRunsWithoutInference` locks the contract:
zero runner/model calls, and the recorded tool trajectory flows through the
same attribution engine (`tool_argument_error` with the final-response
mismatch folded under it).

Because the recording is frozen, trace mode is **historical scoring and
attribution**, not candidate gating: baseline evaluation and every candidate
round replay the same `actualConversation`, so per-case results never change
across rounds. Pointing `evalsets.validation` at a trace-only set therefore
cannot expose a candidate regression — every delta comes out `unchanged`, the
default positive-gain rule rejects every candidate, and lowering the gain
threshold to zero would accept without validating the candidate's behavior at
all. Use trace sets to deterministically score and explain recorded (e.g.
canary) behavior; to gate a live candidate against those scenarios, turn them
into ordinary eval cases the candidate actually runs (drop `evalMode:
"trace"` and keep the expected `conversation`).

## Canary follow-up (S7, extension)

Offline acceptance is deliberately labeled `accept_pending_canary`: offline
eval does not equal online success. The repo already has the hook for the
next stage — `evaluation/evalset/recorder` (see
`examples/evaluation/evalsetrecorder`) records live sessions into standard
evalset files:

1. Ship the candidate prompt to a small traffic slice (shadow or canary).
2. Record sampled sessions into `canary.evalset.json` with the recorder.
3. Point `evalsets.validation` at the canary set and rerun this pipeline for
   a second gate pass; a regression there means rollback.

No new code path is needed — the gate consumes any evalset with the same
metrics.

## Tests

```bash
go test ./...
```

Covers the four required domains plus the end-to-end paths: gate rules and
candidate selection (`gate_test.go`), per-case delta classification
(`gate_test.go`), attribution — six categories, causal folding, hint
overrides (`attribution_test.go`), report generation for both verdicts
(`report_test.go`), promotion — including its failure atomicity and paths
containing whitespace or shell metacharacters (`promote_test.go`), and both
end-to-end gate outcomes over the shipped data (`pipeline_test.go`).

## 方案设计说明

**PromptIter 接入**：pipeline 以组合方式复用
`evaluation/workflow/promptiter/engine`，不改库代码。候选 agent 与
backwarder/aggregator/optimizer 按接口注入（fake 模式为确定性脚本，real
模式为 LLM worker）；baseline prompt 从源文件读入，经 InitialProfile surface
override 注入引擎；引擎每轮事件由 Observer 流式落盘。

**失败归因**：确定性规则引擎输出因果链而非扁平标签。工具类失败对实际/期望
轨迹做结构化 diff，区分错调、漏调与参数错误；format/knowledge 由 criterion
结构与 rubric 类型推导，metric 名映射仅作补充。配置的 metricCategoryHints
在加载后与实际 metric 名集合比对，未知 metric 名直接报错（fail closed）：
报告中的前后对比只在 validation 上进行——候选侧归因来自验证集逐 case
delta，把 train baseline 失败混进去会得到"baseline 4 例 vs 候选 1 例"这类
跨数据集的假象；train baseline 归因单列，仅作优化输入的解释。
拼错的 hint 若被静默忽略，对应 metric 会回落到 final_response_mismatch，
可能让本应触发 hard-fail 红线的失败漏过门禁。多信号按 route→tool→response
传播序折叠：下游症状标记 derivedFrom，仅根因转为 LossHints（P0–P2）反哺引擎。

**接受策略**：安全门对每个候选执行硬规则——验证集增益阈值、新增 hard fail
上限、退化 case 上限、关键 case 保护、调用与墙钟预算，逐条输出实测值与理由；
过闸候选中取验证集分数最高者。质量红线永不参与 tradeoff。新增 hard fail
规则同时覆盖两种形态：pass 转 fail 且候选侧根因属 hard 类别（或无归因，
保守计入），以及 baseline 已失败的 case 在候选下根因迁移进 baseline 归因中
不存在的 hard 类别——后者 pass 状态与分数都可能不变，仅靠 delta 规则不可见。

**防过拟合**：外层 gate 基于逐 case delta 而非聚合分。样例内置该场景：候选使
验证集总分上升（引擎内层接受），但受保护 case 由 pass 转 fail，安全门确定性
拒绝并给出"判定为过拟合"的可解释理由；离线接受仅给 accept_pending_canary，
建议经线上回灌二次门禁。

**产物审计**：`audit/<runID>/` 按执行隔离地留存 run_meta（seed、模式、配置
快照）、每轮全部引擎事件、per-round 成本耗时、归因、候选与 gate 决策，重跑
不会混入历史轮次；报告汇总 baseline/candidate 分数、双集逐 case delta 与规则
明细，报告中的外部数据（case ID、理由、模型证据、候选 prompt）经 Markdown
转义与动态围栏渲染，防止模型输出注入报告结构。报告与可部署候选产物（含
回写基线）在同一发布单元内原子生效：接受即一并写入，拒绝即一并清除历史
候选，仅接受非指令 override 时同样清除历史 prompt 产物，任一步失败整体
回滚，稳定路径上不会出现报告与候选状态不一致的中间态。人工晋升同样由
`-promote` 子命令完成：先校验 prompt 与 effective profile 一致，再在一个
回滚单元内回写基线，避免"prompt 已更新、profile 仍是旧版"这种从未过闸的
基线组合；路径以参数传入，不经 shell 拼接。发布期对涉及的每个目录持有
文件锁，跨进程串行，避免两次发布交错出后写覆盖前写的混合态。指令产物统一
按 trim 后的规范文本落盘（与 baseline prompt 的加载方式一致），只含空白的
指令 override 直接拒绝发布，不会写出下轮自己都判为空的基线。fake 模式下评测分数、归因与 gate 决策完全确定：同输入必得同结论；
runId、时间戳、耗时等审计字段随每次运行变化。
