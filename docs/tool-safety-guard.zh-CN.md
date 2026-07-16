# 工具安全护栏

`tool/safety` 提供一个可选启用、失败时关闭执行通道的模型工具安全护栏。它在工具执行前扫描最终输入，返回结构化权限决策，写入仅含元数据的审计事件，并在所有回调完成后递归移除最终结果中的秘密。

应用没有显式配置 Guard 时，既有默认行为保持不变。

## 安全契约

Guard 处理七类执行风险：破坏性命令、敏感路径、网络访问、Shell 绕过、宿主机/交互执行、依赖变更和资源滥用。秘密暴露属于始终开启的输入/输出保护，策略不能关闭它。

执行顺序如下：

1. before-tool 回调完成参数修改；
2. 权限 Guard 扫描真正要执行的参数；
3. 只有 `allow` 能进入执行器；
4. 插件和 after-tool 回调完成结果修改；
5. 最终净化器递归脱敏真正要返回的结果；
6. 只有净化后的结果能进入事件、模型消息和工具结果遥测。

决策强度固定为 `deny > ask > allow`。非法策略、审计失败或最终净化失败都不会静默降级；系统会返回错误并抑制未净化结果。

## 创建 Guard

```go
policyData, err := os.ReadFile("policy.yaml")
if err != nil {
    return err
}
policy, err := safety.ParsePolicy(policyData, safety.PolicyFormatAuto)
if err != nil {
    return err
}
sink, err := safety.NewJSONLSink("tool-audit.jsonl")
if err != nil {
    return err
}
defer func() {
    if err := sink.Close(); err != nil {
        log.Printf("close tool safety audit: %v", err)
    }
}()
guard, err := safety.NewGuard(policy, safety.WithAuditSink(sink))
if err != nil {
    return err
}
```

`NewDefaultGuard` 会启用全部内置检测并以 `allow` 为默认动作。`Reload` 严格解析并原子替换完整策略；非法重载不会影响原有快照。

## 框架级接入

把 Guard 作为本次运行的权限策略：

```go
runOptions := agent.NewRunOptions(
    agent.WithToolPermissionPolicy(guard),
)
```

这条路径覆盖标准函数调用处理器和 Graph 执行，包括包装工具及回调产生的自定义结果；Guard 同时承担最终结果净化器职责。

## 内置工具直接接入

直接调用内置工具的应用可在构造时启用：

```go
workspaceTool := workspaceexec.NewExecTool(exec,
    workspaceexec.WithSafetyGuard(guard),
)

hostTools := hostexec.NewToolSet(
    hostexec.WithSafetyGuard(guard),
)

codeTool := codeexec.NewTool(exec,
    codeexec.WithSafetyGuard(guard),
)
```

同一调用链建议只选择框架级或直接接入之一。两处同时配置不会放宽决策，但会产生两次扫描和两条审计事件。

`codeexec` 会在灵活输入解码和语言校验后，扫描执行器真正收到的代码。wrapper 会合并 direct / Invocation Guard 中最严格的超时和输出上限，通过 `codeexecutor.ExecutionLimits` 下传执行中限制，并对最终输出与文件内容使用同一总预算；本地执行器会在子进程运行时执行硬输出上限。交互写入工具继续允许空轮询，而非空输入或仅提交换行默认需要批准。

## 策略

策略有版本并采用严格解码。未知字段、重复键、多份 YAML 文档、尾随 JSON 值、非法动作、非法域名和负数限制都会被拒绝。

```yaml
version: 1
default_action: allow
profiles:
  workspace_exec:
    allowed_commands: [go, git]
    denied_commands: [curl]
    allowed_domains: [api.github.com, "*.corp.example"]
    forbidden_paths: [/etc, ~/.ssh]
    allowed_env: [CI]
    max_timeout: 2m
    max_output_bytes: 1048576
    allow_host: false
    allow_background: false
    allow_pty: false
```

域名按精确规则匹配。`*.corp.example` 能匹配 `build.corp.example`，但不能匹配 `corp.example` 或 `evilcorp.example`。代理、目的地址重映射、外部 curl/SSH 配置和 SSH 转发可能改变实际目标，因此需要更强的处置。

策略中的超时和输出值是上限，不是请求默认值。声明支持硬限制的运行时必须接收并执行这些上限；不透明后端无法保证时，文档和代码都不能假装限制已经生效。

## 报告、审计与遥测

`Report` 包含决策、已脱敏发现、请求摘要、扫描耗时、风险等级、建议、后端和阻断/脱敏状态。关联重复活动不需要保存原始请求。

`JSONLSink` 会串行化并发写入，在 POSIX 系统上创建或收紧为仅所有者可读写的文件，拒绝目录和符号链接，并支持幂等 `Close`。每一行都是独立 `AuditEvent`，只记录元数据和摘要，不记录参数或结果。

Guard 写入数量有界且已净化的 OpenTelemetry 属性，包括决策、阻断状态、风险等级、规则 ID、请求摘要和扫描耗时。`tool.safety.request_sha256` 是用于请求关联、可能具有高基数的属性。应用还可以通过遥测 span 属性策略丢弃框架的原始工具参数/结果属性。

## 脱敏

最终结果脱敏递归处理字符串、字节切片、map、slice、array 和可 JSON 序列化 struct。`api_key`、`password`、`private_key`、`session_token` 等敏感键即使对应短值也会脱敏；常见访问令牌、Bearer 凭据、云密钥、赋值表达式和跨行私钥也会按值识别。

脱敏不能替代授权。含秘密的输入会被拒绝；输出脱敏只是工具意外返回秘密时的最后一道 containment 层。

## 验证

在仓库根目录运行：

```bash
go test ./tool/safety ./tool/codeexec ./tool/hostexec ./tool/workspaceexec
go test ./internal/flow/processor ./graph ./telemetry/trace
go test -race ./tool/safety
go test -run '^$' -bench BenchmarkGuardScan500 ./tool/safety
```

在 `examples` 目录运行公开的仅扫描验收集：

```bash
go test ./tool_safety_guard
go run ./tool_safety_guard -policy ./tool_safety_guard/tool_safety_policy.yaml -output-dir ./tool_safety_guard/output
```

执行器的系统相关测试应在原生平台运行；仓库中依赖 Unix 命令的集成测试建议使用 Linux 容器或 CI。

## 局限

- 静态扫描是策略边界，不是完整 Shell 解释器或沙箱；隔离运行时和最小权限仍应作为独立防线。
- `ask` 表示“暂不执行”，用户批准交互由宿主应用负责，批准后才能重新发起。
- 第三方 CodeExecutor 会收到 deadline 和 `codeexecutor.ExecutionLimits`，并应在运行中遵守；wrapper 仍会限制其最终返回结果，但无法让忽略 context 取消的执行器事后停止工作。
- 审计文件通过严格权限保护本地完整性和机密性，保留、轮换和集中传输仍由应用负责。
