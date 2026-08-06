# 方案设计

示例采用可审计的评审流水线。输入层接收有界 diff、文件列表、工作区或样例，生成摘要和变更行。代码评审 Skill 由项目原生 loader 加载；规则输出完整 finding 字段，覆盖凭据和命令注入、goroutine/context 生命周期、资源关闭、错误传播、测试缺失、SQL 参数化及事务回滚。

治理层通过 PermissionPolicy 只允许固定参数的 Go 检查和 Skill 脚本；deny 与 ask 仅记录。生产默认使用断网容器，E2B 为显式选项，本地执行须人工开启。workspace 接收过滤软链、隐藏目录和非 Go 文件后的有界快照，并限制环境、超时、输出及 artifact。失败转人工复核，不伪装成通过。

存储接口隔离 SQLite，以规范化表保存任务、输入、运行、权限/过滤决策、finding、artifact、指标和报告，提供分类查询并在事务中提交。外发和落库前脱敏；去重与低置信路由均留存原因。JSON 与 Markdown 原子发布；dry-run/fake-model 仍走完完整链路，支持无 API Key 测试与回放。
