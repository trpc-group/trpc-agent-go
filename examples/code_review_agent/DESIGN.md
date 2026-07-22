# 方案设计

流程包含输入解析、确定性分析、治理、沙箱、落库和报告。`code-review` Skill 规定适用边界与阶段顺序，规则清单声明 Go 检查，规则文档定义证据与降噪，runner 只接受 `go-test`、`go-vet`。模型不能提供命令、环境变量或 artifact 路径。

生产默认使用 `codeexecutor/container` workspace runtime。创建 workspace 后，Filter 校验真实路径、固定 argv、runner、超时、环境白名单和 artifact，`PermissionPolicy` 再作 allow、deny 或 ask 决策。决策均先落库，只有 allow 才能只读 staging 并执行。容器禁网、禁特权并限制资源、输出和 artifact；fake 不执行代码，local 仅供显式启用的开发场景。

SQLite 通过 `Store` 接口接入，以 task 为聚合根，关联输入摘要、sandbox run、Filter/Permission decision、finding、metrics、artifact 和最终报告，可按 task ID 查询。监控记录总耗时、沙箱耗时、工具调用数、Permission 拦截数、finding 数、severity 分布和异常类型分布，不采集源码或原始 diff。

finding 按文件、行号和类别去重，低置信证据进入 warnings 或人工复核。沙箱输出、错误、数据库、报告、CLI 和 artifact 统一脱敏，源码及 diff 不持久化。沙箱失败或超时会记录并降级，不使流程崩溃。命令受 context 与 runner 双重超时控制；local 不具备容器隔离。共享 runtime 的 `New/Close` 不接受 context，记录该边界，不修改公共模块。
