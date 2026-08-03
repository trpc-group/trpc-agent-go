# 方案设计说明

本示例基于 tRPC-Agent-Go 的 Skills、沙箱与数据库存储能力构建自动代码评审
Agent，评审主链路保持确定性，无需模型凭证即可对样本 diff 产出稳定报告。

`code-review` Skill（SKILL.md + 规则文档 + 脚本）描述评审流程与规则目录，
Go CLI 负责编排：解析 unified diff、文件列表或 `git diff` 工作区输入，
提取变更文件、hunk 与行号后执行规则检测，并将结果落库与出报告。规则命中
按置信度分桶：高置信度进入 findings，中间区间进入 needs_human_review，
低置信度进入 warnings；同文件、同行、同规则、同类别的重复结果按最高
severity/confidence 去重，每条结果都会记录可审计的 filter decision。

模型辅助评审通过 `agent/llmagent` + `runner` 驱动，`fake-model` 模式使用
离线确定性模型覆盖完整链路，`llm` 模式对接 OpenAI 兼容模型；仅发送脱敏后
的 diff，模型结果经严格 JSON 契约解析后并入统一去重降噪流水线，失败时
降级为纯规则结果并记录异常。

沙箱执行默认使用 `codeexecutor/sandbox`（managed OS 沙箱），同时支持
`container`（Docker）与 `e2b` 云沙箱；Skill 脚本经框架 `skill_load` /
`skill_run` 工具在同样的沙箱选择上运行，`local-dev` 仅作为开发降级。
所有外部命令执行前都经过实现了框架 `tool.PermissionPolicy` 接口的命令
治理策略：白名单仅限 Go 静态检查与评审 Skill 脚本，高危网络、提权、
破坏性命令被拒绝或转人工审查，全部决策落库审计。

存储层定义了精简的 `store.Store` 接口，默认由 SQLite 实现最小 schema
（任务、findings、沙箱执行、权限决策、过滤决策、报告、产物），可替换为
其他 SQL 后端。脱敏在报告与持久化前统一执行，防止密钥、令牌等敏感信息
泄漏。监控指标覆盖总耗时、沙箱耗时、工具调用次数、权限拦截数、severity
分布与异常分布，支持通过 OTLP 上报用于审计与回放。
