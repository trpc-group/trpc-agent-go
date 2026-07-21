# 方案设计说明

该原型把 CR 能力拆成 Skill、治理、沙箱、规则、存储和报告六层。`skills/code-review/SKILL.md` 定义工作流入口，规则文档覆盖安全、敏感信息、goroutine/context、资源关闭、错误处理、测试缺失和数据库生命周期；脚本目录只保存可复用检查命令，由 Agent 通过 wrapper 编排。沙箱默认使用 fake deterministic 模式便于无 Key 测试，生产配置应切换到 container、Cube 或 E2B；local 仅作为开发 fallback。每个命令先进入 `PermissionPolicy`，包含破坏性命令、网络下载、shell 包装、提权参数会 deny，`staticcheck` 默认进入 needs_human_review，允许项仍带超时、输出大小限制、环境变量白名单和脱敏。

输入层支持 diff 文件、repo-path 的 git diff 和文件列表，解析 unified diff 后保留文件、hunk、新增行号、上下文和包名。规则引擎只对 Go 新增行做确定性扫描，高置信问题进入 findings，低置信或测试缺失进入 warnings / needs_human_review，并按文件、行、类别去重。存储通过 `Store` 接口隔离，当前 `JSONStore` 离线可跑，`schema.sql` 给出 SQLite 等价表结构，完整保存 task、permission decision、sandbox run、finding、artifact、monitoring 和 final report。监控摘要记录总耗时、沙箱耗时、工具调用数、拦截次数、finding 数、严重级别分布和异常分布。报告写 JSON 与 Markdown，所有 evidence、sandbox output、数据库记录都经过 Redactor，避免 API key、token、password 明文落盘。
