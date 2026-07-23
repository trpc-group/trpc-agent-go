# 方案设计说明

**失败归因方法。** 对每条 `Status=failed` 的指标，`regloop.Attribute` 依据
`metricName` 与 `reason` 文本把失败分到六类：工具调用错误、工具参数错误、路由错误
（由 `tool_trajectory` 指标名 + reason 关键词判定）、格式错误、知识召回不足
（由 reason 关键词判定）、最终回复不匹配（final_response / rouge 指标），其余归入
other。每个失败 case 都携带原始 `reason`（为空时生成稳定 fallback）作为依据，并按
P0–P3 汇总训练集终端损失的严重度分布。

**接受策略。** 采用两层门禁。引擎层沿用 PromptIter 的 `AcceptancePolicy.MinScoreGain`，
控制候选是否被 commit 进下一轮；harness 层再叠加可配置的 `ReleaseGate` 决定候选是否
值得回写生产：验证集总增益 ≥ 阈值、不得新增 hard fail、关键 case 不得退化、轮数不超预算。
两者的默认阈值来自 `promptiter.json`（`minScoreGain` 与 `gate` 块），示例的 overfit
场景显式下调引擎阈值以演示"引擎接受、门禁拒绝"的分歧。任一条不满足即拒绝发布，理由逐条落盘。

**防过拟合策略。** 训练集只用于生成梯度，验证集独立把关：候选必须在验证集重评，并与
baseline 做逐 case delta，区分新增通过 / 新增失败 / 涨分 / 掉分。门禁对"新增失败"零容忍
——即使验证集总分上升，只要某个 case 由通过退化为失败（典型过拟合信号），gate 立即拒绝
候选，防止引擎仅凭总分增益接受一个牺牲了其它 case 的 prompt。这正是 harness 层相对引擎
裸 `MinScoreGain` 的核心价值。

**PromptIter 接入方式。** 复用 `evaluation/workflow/promptiter/engine` 的
`engine.Run(RunRequest) → RunResult`，注入 Teacher / Judge / backwarder / aggregator /
optimizer 五个 runner，指定 `TargetSurfaceIDs` 优化 instruction surface。示例用确定性
fake 模型驱动全链路，无需 API Key，保证可复现与 CI 友好。

**产物审计方式。** `RunResult → regloop.Analyze` 生成 `optimization_report.json`
（机器真相源：baseline/candidate 分数、逐 case delta、gate 决策与逐条理由、失败归因、
接受候选的 surface 投影、每轮候选 surface 与完整 validation 逐 case 投影及 delta、
接受/拒绝理由，以及成本：耗时、按角色模型调用数、evaluated case 数）与
`optimization_report.md`。确定性配置（`deterministic`、
`randomSeed`、fake 模型摘要）随报告落盘。
