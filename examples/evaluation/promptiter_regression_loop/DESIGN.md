# Design

该示例把 PromptIter 的候选生成与生产回写之间补成可审计闭环。Pipeline 首先用同一 metrics 配置分别评估训练集和独立验证集，保留每条 case 的最终回复、工具轨迹、参数、route、格式、召回、成本和延迟。失败归因采用确定性优先级：运行异常、route、工具选择、工具参数、结构化格式、知识召回、最终回复，确保每个失败 case 至少有一个可解释原因，也便于替换成 Evaluation Service trace 或 LLM rubric 信号。

每个候选 prompt 对训练集和验证集重新评估。验证结果按 case ID 与不可变 baseline 对齐，计算分数变化、新增通过和新增失败。Gate 同时检查验证总分最小增益、hard fail、关键 case 退化、成本增量和工具调用预算；任何条件失败都拒绝候选。这样即使候选在训练集满分，只要验证集下降或关键 case 退化，仍会被识别为过拟合并拒绝。

示例的 fake trace engine 固定随机种子且无需 API Key，可在三分钟内稳定回放“有效优化、无效优化、训练提升但验证退化”三类轮次。接口可对接真实 `evaluation/workflow/promptiter`：将每轮 surface patch、EvaluationResult 和 trace 映射到相同审计结构，再复用 delta 与 gate。JSON 报告保存 baseline、每轮完整 prompt、评测、归因、delta、决策理由、模型或 fake engine 配置、成本、耗时和种子；Markdown 报告供人工审批。源 prompt 不自动回写，只有通过验证门禁的最佳候选才被标记为可考虑回写。
