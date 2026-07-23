# Design

本示例把 baseline 评测、失败归因、PromptIter 优化、验证回归和审计落盘串成闭环。pipeline 读取 prompt、train/validation evalset、metrics 与 gate 配置后，评测训练集和验证集，并记录每条 case 的分数、pass/fail、hard fail、回复、工具轨迹、trace、rubric 与结构化输出状态。归因采用确定性规则：工具缺失、参数错误、路由错误、格式错误、知识召回不足、最终回复不匹配进入对应类别；仅分数低于阈值时归为 metric_threshold_miss，证据不足则归为 unknown。

PromptIter 通过 optimizer 接口接入；示例用 fake optimizer 生成成功、无效、训练提升但验证退化三类候选。每个候选都重跑验证集并计算逐 case delta。gate 只信验证集：总分提升达标、无新增 hard fail、关键 case 不退化，且调用、成本、延迟不超预算。报告保存每轮 prompt、delta、gate 理由、归因、成本、耗时和 seed。
