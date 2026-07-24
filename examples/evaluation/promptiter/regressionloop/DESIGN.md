# 方案设计说明

Pipeline 在 PromptIter 外层构建可审计闭环：读取 prompt、训练/验证集、metrics 和配置，先跑 baseline，再把训练失败归因转成 `LossHint` 驱动 PromptIter 搜索候选；候选产出后必须重跑验证集并计算逐 case delta。

失败归因综合 metric 名、reason、trace、最终回复、工具轨迹、结构化 invocation 和 metrics.json criterion 原文，给出主因与次级标签；结构化证据优先于配置 hint，并在报告中记录冲突。可选 `AttributionJudge` 只在 unknown 或低置信 rubric fallback 时介入，默认 fake/确定性模式无需 API Key。

Gate 按验证集分数提升、hard fail、关键 case 退化、任意降分开关、成本/调用/耗时预算决策，防止训练集提升但验证退化的过拟合候选进入生产。JSON/Markdown 审计每轮 prompt、评测、delta、归因、拒绝理由、seed、模型/fake 配置和成本。
