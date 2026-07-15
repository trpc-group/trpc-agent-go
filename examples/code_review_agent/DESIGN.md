# 方案设计说明

本示例把评审拆成输入、规则、执行、持久化和报告五段。CLI 将 diff、文件列表或 Git 工作区解析为 hunk 和 Go package；`code-review` Skill 通过规则文档和固定脚本加载安全、并发、资源关闭、错误处理、测试缺失和数据库生命周期规则。`review` 可独立开启 sandbox 与 model；动态检查先经过 PermissionPolicy/Filter 白名单，再由默认 container 执行 `go test`、`go vet` 和可选 staticcheck，本地仅作开发 fallback。SQLite 以 task 关联输入摘要、审批、sandbox run、finding、artifact、能力状态、指标和报告，可替换 SQL 后端。结果按文件、行、类别和规则编号去重，高置信问题进入 findings，低置信或 ask 项进入人工复核。执行层限制超时、输出、环境变量及 artifact 数量和大小，落库前脱敏，失败形成审计记录而不终止评审。监控汇总耗时、工具调用、权限拦截、严重级别和异常分布，支持回放与评测。
