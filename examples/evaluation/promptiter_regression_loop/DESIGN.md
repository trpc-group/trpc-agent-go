# 方案设计说明

闭环以 Evaluation Service 结果为事实来源，分别评测训练集和验证集。结果摘要保留 case、metric、final response、工具轨迹和 trace route；失败归因按执行、路由、工具、参数、格式、知识召回、最终回复和兜底类别的固定优先级，为 PromptIter 生成 case + metric hint。

外层循环每次只让 PromptIter 产生一个候选，再重新运行训练集和验证集。候选必须满足验证集最小提升、hard metric 不新增失败、critical case 不退化、单 metric 降幅和调用/token 预算。被拒绝候选不会晋升，后续轮次仍以最近接受的 prompt 为基线；第一个通过外部门禁的候选结束循环。这样可以明确拒绝“训练提升但验证退化”的过拟合结果。

fake runtime 运行真实 PromptIter engine 的 evaluate、backward、aggregate、optimizer、profile patch 和 validation stage，只把各 stage runner 换成确定性实现，无 API key 也能复现无效、过拟合和成功三轮。runner 精确统计调用数，trace 补充 token 与延迟证据。最终同一报告对象生成 JSON 和 Markdown，记录 prompt、输入哈希、seed、runtime、变化的 case/metric delta、归因、门禁理由和每轮成本；失败 baseline 保存必要 evidence，候选不重复展开相同轨迹。系统只建议是否回写，不修改源 prompt。
