# Tool Safety Guard

Tool Safety Guard 在工具真正执行前扫描执行请求，适用于能够执行命令、脚本、代码块、MCP Tool 或 Skill 的场景。

扫描结果会给出 allow、deny 或 ask 决策，并输出结构化报告。报告包含 decision、risk level、rule id、evidence、recommendation、tool name、command、backend 和是否拦截。系统也可以写出 JSONL 审计事件，并预留 `tool.safety.decision`、`tool.safety.risk_level`、`tool.safety.rule_id`、`tool.safety.backend` 等 telemetry 属性。

## 与现有机制的关系

`internal/shellsafe` 负责保守解析 shell 命令。它会拒绝 shell wrapper、命令替换、环境变量展开、重定向、子 shell、后台执行等可能绕过命令策略的结构。Tool Safety Guard 使用该解析能力，并复用 shellsafe 不可覆盖的 wrapper 集合，因此 `env`、`xargs`、`timeout`、`nohup` 等进程包装器不能隐藏内层命令。白名单中的裸命令只匹配裸可执行文件，`git` 不会隐式允许 `./git` 或 `/tmp/git`。无法安全解析的命令会转换为 deny 或 ask，而不是默认 allow。

`tool.PermissionPolicy` 是执行前拦截点。它在工具参数确定之后、工具执行之前运行。`tool.FilterFunc` 控制工具是否对模型可见，`PermissionPolicy` 控制已经被请求的工具调用是否允许执行。

`workspace_exec` 在 codeexecutor workspace 中执行，边界包括工作区路径、输出限制、命令策略和环境隔离。`hostexec` 直接通过宿主机 shell 执行，风险更高；包括普通前台命令在内的所有请求都会应用 `backend_rules.hostexec.default_action`，PTY 会话、后台进程、提权命令和进程清理还会触发更具体的规则。`codeexecutor` 和 sandbox backend 提供运行时隔离，但仍然需要执行前扫描和审计；代码块语言会在执行前匹配 `backend_rules.codeexec.allowed_languages`。

策略文件采用严格解析：未知 JSON/YAML 字段和尾随的第二个文档都会报错，避免拼写错误导致安全配置被静默忽略。启用 `audit.enabled` 后，`NewScanner` 会打开 `audit.path` 并为每次扫描写入一条 JSONL 事件；`audit.fail_closed` 为 true 时，审计文件创建或写入失败会阻止执行。Scanner 不再使用时应调用 `Scanner.Close`。`redaction.enabled` 默认开启，并保留显式配置的 false。

workspace、host 和 code execution 适配器会对返回给调用方的输出执行脱敏，并应用 `resource_limits.max_output_bytes`。上限按单次工具响应计算，长会话的每次 poll 分别受限。

## 不能替代沙箱

Tool Safety Guard 不是沙箱。它本身不提供文件系统隔离、进程隔离、网络隔离、CPU/内存限制或进程清理。生产环境应将该机制与容器或沙箱策略、网络限制、干净环境、输出大小限制和明确的进程生命周期管理一起使用。
