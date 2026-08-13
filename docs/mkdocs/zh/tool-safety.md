# Tool 执行安全

`tool/safety` 包提供可选启用、偏保守的执行前检查，并提供低敏审计事件与
显式结果处理。它为命令、脚本和 Tool 参数增加策略边界，但不能证明任意代码
安全，也不能替代沙箱。

## 威胁模型

Guard 至少覆盖七类风险：

1. **危险命令与路径**：破坏性命令、递归删除根目录、覆盖系统目录、读取
   `.env`、凭据或私钥。
2. **网络外连**：`curl`、`wget`、`nc`、SSH 系列工具、自定义客户端以及可
   改写目标地址的参数访问非白名单域名。
3. **Shell 绕过**：`-c` 包装器、`eval`、命令替换、变量展开、管道和重定向。
4. **宿主机执行**：PTY/长会话、后台进程、提权、继承状态与进程残留。
5. **依赖与环境变更**：安装依赖以及持久化修改工具链或包管理器配置。
6. **资源滥用**：超时、超大输出、长时间 sleep、明显无限循环和高并发。
7. **敏感信息泄漏**：报告、Tool 输出、错误或审计数据中的 token、password、
   私钥和凭据。

扫描属于启发式判断。混淆代码、自定义解释器、运行时下载、间接导入和远端
数据驱动行为都可能逃过静态检查。“allow”仅表示当前配置的规则没有发现问题。

## 策略与决策

`LoadPolicy` 会严格解析 YAML 或 JSON，并把显式字段覆盖到
`DefaultPolicy`；未知字段或非法决策会失败。

```yaml
allowed_commands: [go, cat, curl]
denied_commands: [dd, mkfs, sudo]
denied_paths: [/, /etc, /root, ~/.ssh, .env, credentials]
network_allowlist: [github.com]
env_allowlist: [LANG, TMPDIR]
review_commands: [go install, npm install]
max_timeout_seconds: 30
max_output_bytes: 1048576
parse_error_action: ask
pipeline_action: needs_human_review
```

这些字段分别配置命令允许/拒绝列表、禁止路径、域名后缀白名单、允许传入的
环境变量名、需要复核的命令，以及请求的超时和输出上限。环境变量只按名称
放行，不按值放行。显式空网络白名单会拒绝已识别的网络目标。默认情况下，
无法解析的 Shell 结构为 deny，管道为 needs_human_review；默认策略还会拒绝
常见提权/系统命令和敏感路径，复核常见依赖安装命令，将超时限制为 300 秒，
将输出限制为 4 MiB。

环境变量白名单不能覆盖与执行相关的安全语义。请求级 `PATH` 和 `PATHEXT`
会改变白名单命令对应的实际可执行文件，因此始终拒绝；`HOME` 会改变 Shell
启动文件和工具全局配置，因此需要人工复核；`BASH_ENV`、`LD_PRELOAD` 等
进程启动注入变量即使写入白名单也会拒绝。

`Guard.Scan` 返回 `allow`、`deny`、`ask` 或 `needs_human_review`，报告包含
风险等级、rule ID、evidence、recommendation、Tool、backend 和 blocked。
deny 会被拦截；两种复核决策都必须进入应用自己控制的审批流程，不能静默放行。

## 保守解析 Shell 结构

Guard 复用 `internal/shellsafe`，只解析一次命令结构，再对参数向量应用命令
策略。Shell 包装器和无法保守推理的结构会被拒绝。解析失败遵循
`parse_error_action`，未引用的管道遵循 `pipeline_action`。只有在其他控制措施
明确承担风险时才应配置为 allow。该解析器不是完整 Shell 解释器，无法解析
所有变量、alias、source 文件或运行时生成的命令。

Permission 扫描还会把解释器 stdin 和 `-f -` stdin 作为可执行内容处理，
检查路径类参数与 Skill 输出 glob，并复核会选择 alias、hook、helper 或外部
程序的 Git 配置。

## Filter 与 Permission 边界

这些机制承担不同责任：

- `agent.WithToolFilter` 决定哪些 Tool 对模型可见；
- `agent.WithToolExecutionFilter` 可把已可见的调用留给外部执行，但不会为
  外部执行提供授权；
- `agent.WithToolPermissionPolicy` 在框架真正执行前检查权限，此时 JSON 修复
  和 before-tool callbacks 已经生成最终参数；`safety.NewPermissionPolicy`
  将 Guard 适配到这个边界。

Permission 可以看到最终参数，把两种复核决策映射为框架 ask，并最多写一条
preflight 审计事件。Filter 阶段尚不存在具体调用参数，因此不能替代参数扫描。
被 ExecutionFilter 延迟到外部执行的调用，需要调用方自己的 Guard wrapper。

## 执行后端边界

### workspaceexec

`workspaceexec` 将路径限制在配置的 workspace root 内，并验证工作目录。
`CleanEnv` 可以减少继承环境暴露，调用方还应限制超时和输出。Workspace 路径
隔离不会限制 CPU、内存、进程创建或网络，也无法阻止进程利用宿主内核、挂载
socket、凭据或过宽的文件系统挂载。符号链接与挂载边界仍应由沙箱层处理。

### hostexec

`hostexec` 直接运行宿主 Shell。PTY、较长的默认会话超时、后台进程、继承环境、
提权命令和子进程都会扩大信任边界。Guard 会复核 PTY、拒绝请求的后台执行并
检查有效超时，但宿主仍必须负责进程组、取消、子进程清理、环境构造和输出
捕获。不要在缺乏强隔离时把 hostexec 暴露给不可信输入。

### codeexec 与 CodeExecutor

`codeexec` 解析 code blocks 后委托 CodeExecutor。Guard 会扫描每个 block，
将 Shell 语言交给命令扫描，并保守识别常见语言中的进程/网络桥接和资源滥用；
没有保守扫描器的代码语言需要人工复核。
`codeexecutor/local` 具有本机权限；`codeexecutor/container` 只有在限制挂载、
用户、capability 与网络时才提供容器隔离；E2B 是远端沙箱，同时需要独立管理
身份、网络和留存策略。Guard 不会把这些后端变成同一安全等级，也不会替调用方
配置后端。

### MCP Tool 与 Skill

MCP Tool 参数是远端契约下的开放 JSON。Guard 会递归查找可执行字符串和网络
目标，但不可能理解所有服务端字段，也不知道远端服务收到参数后的行为。未知
MCP Tool 和缺失 annotations 应保守处理。

Skill 可能跨多个 turn 创建持久会话或逐步生成命令。应复核整个会话生命周期、
后端和清理行为，并对每次最终执行重新检查；一次批准不能视为无限期会话授权。

## 审计与 Telemetry

`NewJSONLAuditSink` 以并发安全方式每行写入一个 JSON 事件。preflight
`AuditEvent` 包含 schema/timestamp/scan 关联、stage、tool、backend、decision、
risk、rule、duration、redacted 与 intercepted，刻意不包含命令、参数、evidence、
环境变量值和结果。审计文件还需要受限权限、轮转、保留策略和访问控制。

`AuditBestEffort` 在 sink 失败时保留扫描决策；`AuditRequired` 会让原本 allow
的 Permission 检查失败关闭，已经拦截的决策仍保持拦截。应单独监控 sink 故障，
避免 best-effort 掩盖审计中断。Permission 集成只设置以下五个 OTel span
attributes：

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`
- `tool.safety.blocked`

这些属性应保持低基数，不能加入命令、路径或密钥。

## 执行结果处理

`ResultProcessor` 是显式组件，不会自动注入 callbacks。对于能保留 `Report`
的直接执行 wrapper，应先完成 Tool 的正常执行与正常 callbacks，再调用
`ResultProcessor.Process(ctx, report, result, err)`。它会通过 JSON 复制结果，
递归脱敏字段名和值，并在 JSON 将字节切片编码为 Base64 前检查其内容；文本中的
敏感信息会被脱敏，二进制内容会替换为省略标记。随后它会把执行错误变成单行并
脱敏，再按完整序列化后的 `ProcessedResult` 执行 `max_output_bytes` 上限。若字节
表示无法在不误改无关值的前提下可靠关联，它会安全失败；处理成功后才会写入
关联的 `post_execute` 事件。

框架 Permission 适配器只返回权限决策，不会暴露内部报告。需要关联执行后处理
时，应用必须在自己的直接执行 wrapper 中保留对应报告，不能声称 callbacks 会
自动集成。执行后使用 `AuditRequired` 时，即使同时得到了安全处理后的值，也应
把审计失败视为运维故障。

## 纵深防御

静态策略发生在执行前，无法阻止已放行二进制随后改变行为，无法强制内核资源
限制，无法撤回网络包，也不能可靠清理所有子进程。生产环境仍需要：

- 非 root 身份、受限挂载/capability 的内核、虚拟机或容器隔离；
- 独立网络出口、DNS 与代理策略；
- 最小化、短期凭据和干净环境；
- CPU、内存、进程、超时和总输出配额；
- 进程组清理、artifact 限制和审计留存；
- 对模糊、高影响或持久会话操作进行人工复核。

Guard 用于提前拒绝明显危险请求并让决策可观测；沙箱和运行环境负责约束仍然
获准执行的行为。

可运行的[纯扫描示例][tool-safety-example]提供 12 个场景
以及确定性的报告与审计 fixture。

[tool-safety-example]: https://github.com/trpc-group/trpc-agent-go/tree/main/examples/tool_safety_guard
