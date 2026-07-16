# Tool 安全防护

`tool/safety` 是一个按需启用、失败时关闭执行通道（fail closed）的工具安全组件。它会在 before-tool 回调完成后扫描最终参数，在执行前拒绝或请求人工批准，并在所有普通回调和 Hook 完成后递归脱敏最终结果；审计只记录元数据。

未配置 Guard 的应用保持原有行为。多层策略固定按 `deny > ask > allow` 合并。

## 覆盖范围

内置规则覆盖危险删除、敏感路径与凭据读取、非白名单网络、Shell 绕过、宿主机或交互执行、依赖变更、资源滥用和密钥泄露。严格 YAML/JSON 策略支持精确域名、显式 `*.example.com` 通配符、工具级命令与环境变量白名单、超时及 stdout/stderr 共用的硬输出上限。

## 在框架中配置

```go
policyData, err := os.ReadFile("tool_safety_policy.yaml")
if err != nil { return err }
policy, err := safety.ParsePolicy(policyData, safety.PolicyFormatAuto)
if err != nil { return err }
sink, err := safety.NewJSONLSink("tool_safety_audit.jsonl")
if err != nil { return err }
defer func() {
    if err := sink.Close(); err != nil {
        log.Printf("close tool safety audit: %v", err)
    }
}()
guard, err := safety.NewGuard(policy, safety.WithAuditSink(sink))
if err != nil { return err }

runOptions := agent.NewRunOptions(
    agent.WithToolPermissionPolicy(guard),
)
```

推荐使用框架级配置来保护所有工具。内置的 `workspaceexec`、`hostexec` 和 `codeexec` 也支持 `WithSafetyGuard(guard)`，可用于直接调用；框架能够发现这个工具本地 Guard，并在工具执行前抑制原始参数日志和遥测。运行时超时/输出 profile 按最严格值合并；`codeexec` 通过 `codeexecutor.ExecutionLimits` 下传限制，本地执行器在运行中执行，wrapper 再限制最终输出与文件内容。

## 策略示例

```yaml
version: 1
default_action: allow
profiles:
  workspace_exec:
    allowed_commands: [go, git]
    allowed_domains: [api.github.com, "*.trusted.example"]
    allowed_env: [CI]
    max_timeout: 2m
    max_output_bytes: 1048576
```

解析器会拒绝未知字段、重复键、尾随文档、非法动作和没有明确单位的时长。重载是原子的：新策略无效时，旧策略继续生效。

## 报告、审计与遥测

每份 `Report` 稳定包含 `decision`、`risk_level`、`rule`、`rule_ids`、`evidence`、`recommendation`、`tool_name`、`command`、`backend` 和 `blocked`。JSONL 审计只记录元数据与请求 SHA-256 摘要，不保存原始参数或结果。OpenTelemetry Span 记录决策、是否阻断、风险、排序且有界的完整规则 ID、后端、摘要和扫描耗时。

审计或最终脱敏失败时会阻止执行或压制结果。输出与错误脱敏屏障位于普通回调、工具结果消息 Hook、Post-result Hook 以及流式最终状态处理之后。

## 安全验证（不执行危险样本）

示例只做扫描：

```bash
go test -v ./tool_safety_guard -run TestAcceptanceMetrics
go run ./tool_safety_guard \
  -policy tool_safety_guard/tool_safety_policy.yaml \
  -output-dir tool_safety_guard/output
```

公开验收语料直接断言：高危检出率不低于 90%，安全样本误报率不高于 10%，凭据读取、受保护路径危险删除、非白名单网络三类拒绝率均为 100%。

## 安全边界

扫描属于纵深防御，不能替代沙箱。生产环境仍应使用最小权限凭据、文件系统与网络隔离、非 root 容器或虚拟机、操作系统资源限制，并对 `ask` 决策进行人工审批。具备能力声明的 workspace 后端无法满足策略依赖时会失败关闭；第三方 CodeExecutor 必须在运行中遵守 context 限制，wrapper 能限制最终结果，但无法强制忽略取消信号的后端停止。
