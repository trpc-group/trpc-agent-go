# Tool Safety Guard

Tool Safety Guard 是面向命令类工具的执行前安全层。它会在
`workspace_exec`、`hostexec` 或 `codeexec` 真正执行前扫描 tool call，
返回 `allow`、`deny` 或 `ask` 决策，并输出结构化报告、JSONL 审计事件和
OpenTelemetry 属性。

当 Agent 可以执行 shell 命令、脚本、代码块、依赖安装器，或调用可能读文件、
访问网络的工具时，可以把它作为纵深防御的一层。

## 检查内容

扫描器由策略文件驱动，默认采用保守判断，覆盖以下风险类型：

- 危险命令，例如递归删除或写入受保护路径。
- 敏感路径，例如 SSH 密钥、`.env`、凭据文件和系统目录。
- 网络外连，例如 `curl`、`wget`、`nc`、`ssh` 或访问不在白名单内的 URL。
- Shell 绕过，例如 `sh -c`、`bash -c`、`eval`、命令替换、环境变量展开、
  管道和重定向。
- 宿主机执行风险，例如 PTY 会话、后台进程、提权命令和进程生命周期问题。
- 依赖和环境变更，例如 `go install`、`npm install`、`pip install` 和包管理器安装。
- 资源滥用，例如长时间 sleep、过大 timeout、无界输出和并发 fan-out 信号。
- 敏感信息泄漏，例如命令、stdin、环境变量、日志、输出、审计事件或 artifact
  中出现 token、password、私钥等内容。

扫描器也会检查通过 `stdin` 传给 `sh`、`bash`、`python -` 这类解释器的脚本。
对于未知工具或开放世界工具，即使 `unknown_tool_action` 配置为 `allow`，
也会扫描 raw JSON 参数中的 secret、URL、敏感路径和 command/script/code 字段。

## PermissionPolicy 集成

推荐通过 `tool.PermissionPolicy` 集成。Permission 检查发生在模型已经请求工具、
参数已经确定之后，并且在工具真正执行之前。

```go
policy, err := safety.LoadPolicyStrict("tool_safety_policy.yaml")
if err != nil {
    return err
}

guard := safety.NewPermissionPolicy(
    safety.WithPolicy(policy),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
    safety.WithTelemetry(true),
)

events, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolPermissionPolicy(guard),
)
```

如果希望在构造 PermissionPolicy 时直接加载并校验策略文件，可以使用
`WithStrictPolicyFile("tool_safety_policy.yaml")`。

`PermissionPolicy` 会解析常见执行工具的参数：

- `workspace_exec`：command、cwd、env、stdin、timeout、TTY 和 background。
- `workspace_write_stdin`：写入运行中 workspace 会话的 chars。
- `hostexec` / `exec_command`：command、workdir、env、stdin、timeout、PTY 和 background。
- `write_stdin`：写入运行中 hostexec 会话的 chars。
- `execute_code`：Bash/Shell/Python 以及其他代码块。
- `skill_run`、`skill_exec` 和 `skill_write_stdin`：旧版 skill 执行工具，
  按 workspace 风格解析 command、env、timeout 和 stdin。
- 常见 MCP 命令包装工具，例如 `mcp_shell`、`mcp_exec`、`mcp_command`：
  命令形态参数会结构化解析，空命令或非标准载荷会回退到 raw JSON 扫描。
- 未知工具：即使未知工具策略是 `allow`，也会扫描 raw JSON 字符串。

应用自定义的工具名可以通过
`safety.WithToolBackend("custom_shell", safety.BackendWorkspaceExec)` 映射到对应 backend。

`PermissionPolicy` 扫描的是模型可见的原始参数。对于 `hostexec`，
`WithBaseDir` 这类工具本地配置只有内置工具自身知道；如果策略判断需要使用
解析后的宿主机工作目录，而不是原始 `workdir` 参数，应同时启用
`hostexec.WithSafetyScanner`。

`deny` 会阻止执行，并向模型返回结构化的 denied 工具结果。`ask` 也会阻止执行，
并返回 approval-required 工具结果；如果应用有审批 UI，应在 policy 内完成审批，
并只在审批通过后返回 `allow`。

## 策略文件

策略文件支持 YAML 或 JSON。生产和 CI 环境建议使用 `LoadPolicyStrict`，
这样未知字段、非法 decision 和负数资源限制都会快速失败。

```yaml
allowed_commands:
  - go
  - git
  - ls

denied_commands:
  - rm
  - sudo
  - chmod

denied_paths:
  - ~/.ssh
  - .env
  - /etc/passwd

allowed_domains:
  - github.com
  - golang.org

env_allowlist:
  - HOME

max_timeout_sec: 30
max_output_bytes: 1048576
parse_error_action: deny
shell_bypass_action: deny
dependency_install_action: ask
hostexec_tty_action: ask
unknown_tool_action: ask
audit_failure_mode: fail_closed
redact_sensitive_evidence: true
redact_sensitive_paths: true
```

包内默认值保持 opt-in 场景的向后兼容；生产策略通常应把
`unknown_tool_action` 设置为 `ask` 或 `deny`，并在缺失审计记录必须阻止执行时使用
`audit_failure_mode: fail_closed`。`safety.ProductionPolicy()` 提供更严格的
生产默认值：未知工具进入复核、不支持的 backend fail closed、审计失败 fail closed，
并启用敏感路径脱敏。

允许命令、拒绝命令、网络域名白名单、禁止路径、timeout、输出上限和环境变量白名单
都可以通过修改策略文件调整，不需要改 Go 代码。`PATH`、`BASH_ENV`、`LD_PRELOAD`
这类 shell 启动、动态链接器和命令搜索路径变量会始终拒绝，因为它们可能在允许命令
执行前改写实际运行目标。

## 报告、审计和遥测

每次扫描都会生成结构化报告，包含：

- `decision`
- `risk_level`
- `rule_id`
- `evidence`
- `recommendation`
- `tool_name`
- `command`
- `backend`
- `blocked`

配置 audit 文件或 writer 后，guard 会写入 JSONL 审计事件，方便监控系统或 SIEM
消费。审计投影包含工具名、决策、风险等级、主规则 id、扫描耗时、是否脱敏和是否拦截执行。

审计写入失败默认是 best-effort。需要“审计失败即拒绝执行”时，可以配置
`audit_failure_mode: fail_closed`，或使用
`WithAuditFailureMode(safety.AuditFailClosed)`。

启用 OpenTelemetry 后，guard 会在当前 span 上记录：

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

配置 `WithSafetyScanner` 后，内置 `workspaceexec`、`hostexec` 和 `codeexec`
工具会在返回给模型前扫描返回输出，`codeexec` 还会扫描返回的 output file content。
自定义工具或独立的日志、输出、artifact 持久化/导出路径仍应显式调用
`Scanner.ScanOutput`。`PermissionPolicy` 只在执行前运行。

## 执行边界

`internal/shellsafe` 用于保守解析 shell 命令和检查命令结构。Tool Safety Guard
在此基础上增加策略、路径、网络、资源、宿主机、依赖、secret 和审计规则。无法安全解析的命令
应该被拒绝或进入人工复核，而不是默认放行。

`workspace_exec` 在 executor workspace 中执行。guard 会在请求进入 workspace
前检查，但 workspace 隔离、干净环境、timeout、输出限制、artifact 处理和进程清理
仍由 workspace executor 负责。

`hostexec` 在宿主机上执行命令。guard 会把宿主机 PTY、后台任务、提权命令和长会话
视为更高风险，因为它们可能保留状态、留下子进程或影响宿主机。运行时清理由
`hostexec` 和宿主机环境负责。

`codeexec` 和 `codeexecutor` 后端会在本地、容器或外部运行时执行代码块。guard
会在执行前扫描代码，但容器、沙箱、E2B、文件系统、网络、timeout 和输出控制仍然是运行时边界。

## 不能替代沙箱

Tool Safety Guard 是静态的执行前分析。它可能漏掉混淆命令、动态生成脚本、运行时数据流、
间接下载和恶意依赖。配置 `WithSafetyScanner` 后，内置工具可以扫描返回输出；
但自定义日志、输出和 artifact 持久化路径仍需要接入 `ScanOutput`。它也可能拦截
看起来危险但实际合理的命令。

生产环境应把它和沙箱、容器或远程 executor 隔离、干净环境、workspace 权限、网络策略、
timeout、输出上限、进程清理、artifact 控制、permission policy 和 telemetry 监控结合使用。

## 示例

`examples/tool_safety_guard` 提供了可运行 demo、示例策略、12 条验收样例、
确定性的 `tool_safety_report.json` 和 `tool_safety_audit.jsonl`。
