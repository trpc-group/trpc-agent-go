# 方案设计说明

示例采用“确定性规则 + 沙箱验证”。CLI 支持 diff、fixture、文件列表和 git 工作区输入，解析 unified diff，校验 hunk、quoted path、路径穿越、绝对路径、盘符路径与 NUL，并提取新增行和 Go package。`skills/code-review` 含 SKILL.md、规则和脚本，覆盖安全、并发/context、资源关闭、错误处理、数据库生命周期和测试缺失。沙箱默认 container，E2B 可选，fake 用于 CI，local 需授权；命令先经 PermissionPolicy，高风险命令 deny/ask，非 allow 不执行，并使用超时、输出上限、clean env、环境白名单和脱敏。SQLite 事务保存 task、sandbox、permission、finding、artifact、report、metrics 与 diff hash alias。finding 按文件、行号、类别、rule id 去重；低置信进人工复核。监控记录耗时、拦截、级别和异常；`--eval-labels` 输出可复验指标。
