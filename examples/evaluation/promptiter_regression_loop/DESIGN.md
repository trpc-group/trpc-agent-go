# PromptIter Evaluation Regression Loop 方案

方案复用现有 Evaluation Service 与 PromptIter Engine，不修改核心 API。流程分别评测训练集和验证集，保存每条 case 的 metric、状态、原因与 trace。归因以 metric 类型为主，trace 作为错误证据；无 metric 失败时再归因 trace，避免多信号互相覆盖。

每轮重跑两组评测并生成逐 case、metric delta。gate 检查增益、新增失败、关键 case 与验证预算；失败运行禁止回写。确定性 optimizer 只在收到失败梯度后应用配置候选，并审计失败 case。报告保存 prompt、评测、delta、决策、成本、耗时、种子、配置哈希和 fake 版本。fake model 严格匹配自然问题及完整 prompt，固定工具 trace 保证无 API Key 复现。
