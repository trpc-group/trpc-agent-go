# 方案设计说明

本示例把 code-review Skill、沙箱、治理和 SQLite 串成可回放的 Go 代码评审流水线。CLI 有界读取 diff、文件、Git 工作区或 fixture，解析 hunk、行号和 package；规则覆盖密钥、动态 SQL、并发/context、资源、错误、事务及测试。fake model 无需密钥即可验证 Agent 链路，真实模型只接收脱敏内容，失败自动降级。

外部命令先经 PermissionPolicy；默认 managed，另支持断网容器和 E2B，local 仅开发使用。命令有超时、输出上限、洁净环境和失败分类；自定义 Skill 默认转人工，授权后记录摘要。结果按文件、行、规则和类别去重，并按置信度分桶。

schema v2 原子保存任务、输入摘要、三个 finding 分桶、沙箱运行、权限/过滤决策、产物、指标和最终结论，并可从 v1 幂等升级。统一 sanitizer 在报告和落库前遍历所有外部字符串；JSON、Markdown 与校验清单原子发布。指标记录总耗时、沙箱/模型耗时、真实工具调用、拦截、严重级别和异常分布，支持 OTLP 审计。
