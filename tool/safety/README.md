# tool/safety — Tool 执行安全检查器

预执行安全扫描器：对 Agent 即将执行的脚本/命令做**静态风险分析**，在真正执行前返回 `allow / deny / ask` 决策，并产出结构化扫描报告、审计 JSONL 与 OTel 遥测。可接入 `tool.PermissionPolicy`（workspaceexec / hostexec / codeexec 复用）。

## 快速使用

```go
scanner := safety.NewScanner(nil) // nil → DefaultPolicy()
report := scanner.Scan(ctx, safety.ScanRequest{
    ToolName: "workspace_exec",
    Command:  "rm -rf /",
    Backend:  "workspaceexec",
})
if report.Decision == safety.DecisionDeny {
    // 拒绝执行，report.Recommendation 给出原因
}
```

作为 PermissionPolicy 接入：

```go
var _ tool.PermissionPolicy = safety.NewScanner(safety.DefaultPolicy())
// 之后框架会在每次工具执行前调用 CheckToolPermission。
```

从 YAML 加载策略（**严格模式**：未知字段直接报错，防 typo 悄悄削弱策略）：

```go
policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
scanner := safety.NewScanner(policy)
```

## 覆盖的风险类型（issue 验收对照）

| 风险类型 | 实现 | 证据 |
|---|---|---|
| 1. 危险命令（rm -rf、覆盖系统目录、~/.ssh、.env、凭据文件） | `dangerous_cmd_001/002`、`secrets_001`、DeniedCommands、ForbiddenPaths | `policy.go` / `scanner.go` step 1、4 |
| 2. 网络外连（curl/wget/nc/ssh 非白名单域名） | `network_egress_001` + host allowlist（`extractHostTarget`，flag 值不误判） | `policy.go` / `scanner.go` step 6 |
| 3. Shell 绕过（sh -c、bash -c、eval、反引号、$()、管道） | `shell_bypass_001/002` + **shellsafe 保守解析 fail-closed**（命令替换/反引号/重定向/未闭合引号 → deny） | `scanner.go` step 1.5 + `parseShellCommands` |
| 4. 宿主机执行（PTY 长会话、交互命令） | `hostexec_risk_001/002` + hostexec 交互/长会话检测（top/htop/tail -f/编辑器 → ask） | `scanner.go` step 8.8 + `matchesHostExecRisk` |
| 5. 依赖和环境变更（go/npm/pip/apt install） | `dependency_install_001` + EnvAllowlist | `policy.go` / `scanner.go` step 7 |
| 6. 资源滥用（超时、超大输出、长时间 sleep、并发） | `resource_abuse_001` + sleep 时长 vs `MaxTimeoutSec` + 输出洪水（/dev/zero、yes → deny）+ 并发标志 | `scanner.go` step 8.5–8.7 |
| 7. 敏感信息泄漏（命令/日志中的 API Key、token、私钥） | `sensitive_leak_001` + `redactSecrets` 对报告 command/evidence 脱敏 + 审计只存命令哈希 | `scanner.go` + `redactSecrets` |

**ShellSafe 保守解析**：所有命令（多行脚本逐行）先经 `internal/shellsafe` 结构化解析；`$()`、反引号、输出重定向、未闭合引号等无法安全解析的结构 → **fail-closed deny**，绝不默认 allow。`wget | sh` 这类"管道下载→解释器"绕过会被多段 allowlist 裁决捕获。

## workspace 与 hostexec 的安全边界

- **workspace_exec（隔离工作区）**：命令在受限 workspace 内执行，资源有上限、环境变量白名单过滤；扫描重点在命令本身的危险性。
- **hostexec（宿主 shell）**：直接跑 host shell，风险更高。交互式/长会话命令（`top`、`tail -f`、`vim`、`ssh -t` 等）默认 `ask`；可配置 `hostexec_requires_ask: true` 让所有 hostexec 命令都需复核。host shell 建议配合进程清理、输出上限、环境隔离一起使用。

## 交付物

- 扫描报告：`ScanReport`（decision/risk_level/rule_id/evidence/recommendation/tool_name/command/backend/intercepted），示例见 `examples/tool_safety_report.json`。
- 审计：`Auditor` 有界缓冲（默认 10000，满丢最旧）+ JSONL `Flush`（0600），命令只存 SHA-256 哈希并标 `Desensitized`，示例见 `examples/tool_safety_audit.jsonl`。
- 遥测：`otel.go` 输出 `tool.safety.*` span attributes（decision/risk_level/rule_id/intercepted）。
- 策略：`tool_safety_policy.yaml`（可配置 allowed/denied commands、forbidden paths、网络白名单、超时/输出上限、env 白名单、规则）。

## 与沙箱 / Filter / Permission / Telemetry 的关系

`tool/safety` 是**执行前**的静态决策层，不能替代运行时沙箱（隔离）、Filter（参数治理）或 Telemetry（链路监控），而是与它们互补：先静态扫描给决策，再交给沙箱执行，全程埋点。
