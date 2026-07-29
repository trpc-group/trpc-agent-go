# PromptIter 回归优化闭环（评测-优化闭环示例）

本示例实现了
[issue #2003](https://github.com/trpc-group/trpc-agent-go/issues/2003) 中描述的
闭环：基线评测 → 失败归因 → PromptIter 优化 → 候选验证 → 接受/拒绝门禁 → 审计
报告。

它在现有 `evaluation/workflow/promptiter/engine` 之上复用优化流程，并新增 issue
要求的**新逻辑**。所有新增逻辑均为确定性、可在无 API key 下做单元测试。

## 文件清单

| 文件 | 作用 |
| --- | --- |
| `main.go` | 命令行入口（`-fake`/`-attribution`/`-key-cases` 等全部参数），并支持 `-config` 读取 JSON 配置。 |
| `agent.go` | 候选 / 评判 / 反向 / 聚合 / 优化 五个 agent（复制自 `promptiter/syncrun`）。 |
| `logging.go` | 可选的 IO 日志 runner 与日志初始化。 |
| `engine_setup.go` | 构建 PromptIter 运行时并运行一次优化（`runEngine`）。 |
| `loop.go` | 闭环主流程：运行引擎 → 归因 → 门禁 → 报告。 |
| `attribution.go` | `classifyFailures` + `Attributor` 接口——将失败归因到粗粒度桶（`response_mismatch`、`tool_call_error`、`tool_param_error`、`route_error`、`format_error`、`knowledge_gap`、`unknown`）。LLM 归因为**批量**调用（N 条失败一次完成），失败还会按 `(类别, 归一化理由)` 去重为 `Clusters`。 |
| `llm_attribution.go` | `llmAttributor`——可选 LLM 增强，同时实现 `Attribute`（逐条）与 `AttributeBatch`（全部失败一次调用），返回 JSON `{category, reason}`，任何错误回退规则。 |
| `insight_aggregate.go` | `ruleInsightAggregator`——**纯确定性**跨 case 聚合：主导失败模式（类别直方图 + 占比 + 代表样例）+ 模板总结。计数永远精确；LLM 的 summary/fix 由 `EnhancedReporter` 产出。 |
| `narrative.go` | `EnhancedReporter`——把跨 case 的 **summary + 修复建议 + 自然语言总结**合并为**一次可选 LLM 调用**（取代原本两次调用）。规则优先：确定性的 `ruleNarrator` 模板作为离线/兜底路径，保证报告始终有内容。 |
| `gate.go` | `decideAcceptance`——确定性接受/拒绝，含回归守卫、关键 case 保护、新增硬失败上限。 |
| `report.go` | `buildReport` / `writeReport`——写出 `optimization_report.json` 与 `optimization_report.md`。 |
| `loop_test.go` | 归因、门禁、逐 case delta、报告生成、聚类、配置加载、LLM 计数，以及基于 fake model 的端到端闭环测试（纯单元测试，无需 API key）。 |
| `data/` | 样例 train/validation evalset、metrics、baseline prompt 等输入。 |
| `pipeline.example.json` | PromptIter 配置样例（evalset / metrics / 门禁 / 成本 等）。 |

## 运行

```bash
cd examples/evaluation/promptiter_regression_loop
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -model "deepseek-v4-flash" \
  -judge-model "deepseek-v4-flash" \
  -worker-model "deepseek-v4-flash"
```

审计报告写入 `./output/optimization_report.json` 与 `./output/optimization_report.md`
（目录下已提交样例产物），同时还有 `candidate_prompt.txt`、`baseline_eval_result.json`、
`candidate_eval_result.json`。

## 失败归因（规则为主 + 可选 LLM）

归因将每条失败的评测指标映射到一个粗粒度类别与可解释理由。它**以规则为主**，可选
**LLM 增强**，且增强层绝不参与接受/拒绝门禁：

- `-attribution=rule`（默认）：完全确定性、零成本、可复现——用于测试、CI 与 fake/离线
  流程。类别来自执行 trace（tool/route 报错）或 judge 理由的关键词启发式。
- `-attribution=llm`：把每条失败（judge 理由 + 执行 trace 摘要）发给模型（默认
  `deepseek-v4-flash`，可用 `-attribution-model` 覆盖），返回 JSON `{category, reason}`。
  理由为更丰富、语义化的自然语言解释；类别仍落在同一封闭枚举内。**所有失败在一次批量
  调用中完成归因**（`AttributeBatch`），把原本 N 次串行调用合并为 1 次，是 LLM 归因在
  大评测集上的主要成本/时延收益。批量失败回退确定性规则（不再额外发起 LLM 调用），
  逐条 LLM 失败也回退规则。
- `-attribution=auto`：有真实 LLM（已设置 key 且非 `-fake`）时用 `llm`，否则 `rule`。

模型通过标准 OpenAI 兼容客户端调用，把 `OPENAI_BASE_URL` / `OPENAI_API_KEY` 指向你的
服务（如 DeepSeek `deepseek-v4-flash`）。

**关键保证：任何 LLM 失败（网络、超时、无法解析）都回退确定性规则**，因此门禁决策始终
可复现、零成本，即使 LLM 不可用。所选方法（`rule`/`llm`）记录在报告的
`attribution.method` 字段。

**规则兜底正确性。** 无关键词命中时，规则归因返回 `unknown`（显式"未归类"桶）而非误标
`response_mismatch`——静默误标比显式桶更误导。执行 trace 的 tool/route 报错（来自 `Step`
ground truth）始终优先于 judge 理由文本，归因因此落在"实际发生了什么"上。

### 跨 case 聚合洞察 + 叙事（合并 LLM 调用）

逐条归因之后：

- **失败模式计数始终由确定性规则计算**（类别直方图 + 占比 + 代表样例理由），即使模型数错
  数字也是正确的。此步骤**纯规则、无 LLM 调用**。
- 在 `-attribution=llm` 下，`EnhancedReporter` 用**一次合并调用**产出 `summary`、
  `suggestedFix` 与 `narrative`，而不是分别调用两次。它接收精确的确定性计数、只写文字，
  保证计数精确。`suggestedFix` 是"可操作建议"的轻量、非侵入形式——**仅记录供开发者参考，
  绝不自动注入优化器**，保留闭环的确定性。
- 任何 LLM 失败都回退到确定性的 `ruleNarrator` 模板，报告始终有内容，门禁决策也不受叙事
  影响。

这些内容分别落到 JSON 的 `attribution.insights`（`summary`/`suggestedFix`/`patterns`）
与 `narrative`，以及 Markdown 的 `## 失败归因` / `## 自然语言总结`。

### 聚类去重 + 可观测性

- **聚类（去重）。** 失败 case 按 `(类别, 归一化理由)` 去重为少量可操作桶
  （`attribution.clusters` / `## 失败归因` → "聚类（去重）"）：每个聚类含类别、计数、代表
  理由与至多 5 个样例 case id。200 行失败清单由此收敛为"format_error ×120、
  tool_call_error ×60……"，让人（或下游优化器）针对模式而非噪声行动。
- **LLM 可观测性。** 报告记录 `llmCalls` / `llmErrors`（`## LLM 增强可观测性`）。这两个计数
  告诉操作者可选增强层实际发起几次调用、失败几次；它们**永远不影响接受/拒绝门禁**。
  在 `-attribution=rule` 下为 `0`，全流程完全确定性运行。

## 离线运行（fake model / 确定性 runner，无需 API key）

三个场景对应 issue #2003 要求的三种情况（优化成功 / 无效 / 验证集退化）：

```bash
go run . -fake -fake-scenario=happy       # 可优化成功 -> 门禁接受
go run . -fake -fake-scenario=no-gain     # 优化无效   -> 拒绝：insufficient_gain
go run . -fake -fake-scenario=regression  # 验证集退化 -> 拒绝：validation_regression（防过拟合）
```

输入也可通过文件提供：`-config pipeline.example.json`（evalset / metrics / promptiter /
门禁 / 成本 配置）与 `-prompt-file prompt.example.txt`（baseline prompt 来源）。数据文件位于
`data/promptiter-nba-commentary-app/`（8 条训练 + 8 条验证 case + `sports-commentary.metrics.json`），
满足"至少 6 条（3 训练 + 3 验证）、且覆盖可优化成功 / 优化无效 / 验证集退化三类情况"
的要求（三类情况由 `-fake-scenario` 驱动演示）。

## 方案设计说明

本示例在现有 `evaluation/workflow/promptiter/engine` 之上构建"评测-归因-优化-回归-门禁-审计"
闭环。**失败归因**以确定性规则为主、可选 LLM 增强为辅：确定性层优先读取每条失败 case 的
执行 trace（ground truth），`tool` 节点报错归为 `tool_call_error`、`agent` 转移节点报错归为
`route_error`；无 trace 信号时回退到 judge 理由文本的关键词规则，归入格式错误、知识召回不足
或回复不匹配；无关键词命中则显式归为 `unknown`；evaluator 未给出理由时由 `explainCategory`
按类别合成可解释说明，保证每条失败 case 至少有一个理由。开启 `-attribution=llm` 时，把失败
case 的 judge 理由与执行 trace 摘要通过 `AttributeBatch` 单次批量调用发给大模型，返回语义级
`category` 与更丰富的自然语言 `reason`（如"回复缺少闭合括号、不是合法 JSON"），提升可解释性；
**任何 LLM 失败都回退确定性规则**，因此 gate 决策始终可复现、零成本。**接受策略**是一个可
配置的确定性 gate，按序检查：验证集总分增益 ≥ 阈值（`-min-score-gain`）、不允许验证集退化、
不允许新增 hard fail、关键 case（`-key-cases`）不得退化、估算成本不得超过预算（`-cost-budget`），
任一不满足即拒绝并输出 `rejectedBy` 代码。**防过拟合**的核心是：优化仅用训练集梯度，gate
决策只看验证集 delta——训练分再高也无法"救回"验证集退化的候选，`regression` 场景端到端验证
了这一点。**PromptIter 接入**通过 `astructure.SurfaceID` 指定优化目标面（`-prompt-type` 支持
system/agent/skill/router，或用 `-target-surface` 显式指定），引擎每轮执行
backward→aggregate→optimize 产出候选 profile。**产物审计**把每轮候选 prompt、train/validation
分数、逐 case delta、gate 决策与理由、成本、耗时及完整配置快照（含 fake 配置）写入
`optimization_report.json/.md`，并另存原始 eval result 与候选 prompt 文本，保证任一次运行都可
从产物完整复现。

## 优化要点

在跑通"规则为主 + LLM 增强"闭环后，针对四个方向做了工程优化：

- **批量 / 合并 LLM 调用**：逐条归因从 N 次串行改为 `AttributeBatch` 单次批量；跨 case 聚合
  与叙事原本两次 LLM 调用，现合并为 `EnhancedReporter` 一次调用返回
  `{summary, suggestedFix, narrative}`。LLM 调用从最多 N+2 次降到 2 次（批量归因 + 合并报告），
  且聚合计数始终确定性产生，LLM 只写文字、不数数。
- **提升测试覆盖**：新增批量归因、聚类去重、配置加载、LLM 调用计数，以及基于 fake model 的
  端到端回归闭环测试（`happy`/`regression`）。包测试覆盖率由 45.5% 提升至 74.7%。
- **规则兜底正确性**：无关键词命中返回 `unknown` 而非误标 `response_mismatch`；执行 trace 的
  tool/route 报错优先于 judge 理由文本；LLM 任意失败（超时、不可解析、批量失败）均回退规则，
  gate 决策始终可复现、零成本。
- **聚类去重 + 可观测性**：失败 case 按 `(类别, 归一化理由)` 去重为少量 `Clusters`，报告展示
  计数、代表理由与样例 case id；新增 `llmCalls`/`llmErrors` 计数，对外暴露"可选增强层"的真实
  调用与失败情况，但不影响接受/拒绝门禁。

## 运行单元测试（无需 API key）

```bash
go test ./...
```

## 门禁语义（关键验收规则）

候选在以下任一情况被**拒绝**：

- 验证集分数退化到基线以下（`validation_regression`）——这正是捕捉"只在训练集过拟合"的 prompt；
- 验证集分数增益低于 `min-score-gain`；
- 指定关键 case 退化；
- 候选引入超过 `MaxNewHardFails` 的新增硬失败。
