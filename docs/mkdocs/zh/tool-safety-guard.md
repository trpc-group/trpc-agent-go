# Tool Safety Guard

Tool Safety Guard 在工具真正执行前扫描执行请求，适用于能够执行命令、脚本、代码块、MCP Tool 或 Skill 的场景。

扫描结果会给出 allow、deny 或 ask 决策，并输出结构化报告。报告包含 decision、risk level、rule id、evidence、recommendation、tool name、command、backend 和是否拦截。系统也可以写出 JSONL 审计事件，并预留 `tool.safety.decision`、`tool.safety.risk_level`、`tool.safety.rule_id`、`tool.safety.backend` 等 telemetry 属性。

## 与现有机制的关系

`internal/shellsafe` 负责保守解析 shell 命令。它会拒绝 shell wrapper、命令替换、环境变量展开、重定向、子 shell、后台执行等可能绕过命令策略的结构。Tool Safety Guard 使用该解析能力，并把无法安全解析的命令转换为 deny 或 ask，而不是默认 allow。

`tool.PermissionPolicy` 是执行前拦截点。它在工具参数确定之后、工具执行之前运行。`tool.FilterFunc` 控制工具是否对模型可见，`PermissionPolicy` 控制已经被请求的工具调用是否允许执行。

`workspace_exec` 在 codeexecutor workspace 中执行，边界包括工作区路径、输出限制、命令策略和环境隔离。`hostexec` 直接通过宿主机 shell 执行，风险更高，PTY 会话、后台进程、提权命令和进程清理都需要更严格的复核。`codeexecutor` 和 sandbox backend 提供运行时隔离，但仍然需要执行前扫描和审计。

## 不能替代沙箱

Tool Safety Guard 不是沙箱。它本身不提供文件系统隔离、进程隔离、网络隔离、CPU/内存限制或进程清理。生产环境应将该机制与容器或沙箱策略、网络限制、干净环境、输出大小限制和明确的进程生命周期管理一起使用。
