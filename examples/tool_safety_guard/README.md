# Tool Safety Guard (工具执行安全检查器)

`tool/safety` 模块为 `trpc-agent-go` 框架提供了全面而强大的工具执行安全拦截、扫描防范与监控审计能力。

## 核心特性

- **静态扫描与解析**：集成并扩展 `internal/shellsafe`，在命令执行前分析可执行程序名、语法结构与管道逻辑，防止套娃命令（如 `sh -c`、`eval`、`sudo`）绕过限制。
- **动态策略配置**：支持从 JSON/YAML 策略配置文件（如 `tool_safety_policy.yaml` / `tool_safety_policy.json`）中读取黑白名单、禁止路径、域名白名单、耗时与资源限制等。
- **无缝对接 `tool.PermissionPolicy`**：可以直接通过 `agent.WithToolPermissionPolicy(guard)` 作为框架门卫，在工具真正运行前返回 `allow` / `deny` / `ask` 决策。
- **结构化报告与审计日志**：产出符合标准的 `tool_safety_report.json` 与 JSONL 格式的 `tool_safety_audit.jsonl`，全面记录命中规则、证据摘要与处置结果。
- **OpenTelemetry 预留**：自动为当前 Context 的 Span 填充 `tool.safety.decision`、`tool.safety.risk_level`、`tool.safety.rule_id`、`tool.safety.backend` 属性，对接分布式可观测平台。

---

## 机制解析：安全边界与架构关系

### 1. 各安全组件的关系与职责分工

| 组件名称 | 主要职责与定位 | 作用时机 |
|---|---|---|
| **`shellsafe`** | 语法与指令保守解析器 | 执行前（解析 Shell 语法与提取指令） |
| **`SafetyGuard` (本模块)** | 策略匹配、风险决策 (`allow`/`deny`/`ask`)、报告与审计日志 | 执行前（阻断高危风险与触发人工确认） |
| **`PermissionPolicy`** | 框架层统一拦截接口 | 执行前（决定是否真正调度执行） |
| **`workspaceexec`** | 在特定 Workspace 目录内执行命令 | 执行中（约束运行目录与环境变量） |
| **`hostexec`** | 在宿主机执行命令或维持 PTY 会话 | 执行中（长会话交互与本地进程管理） |
| **`codeexecutor` / 沙箱** | 操作系统级/容器级隔离（Docker, E2B, Linux namespaces） | 执行中（内核级强隔离与资源上限限制） |
| **`Telemetry`** | 可观测性与 Trace 追溯 | 贯穿全生命周期 |

---

### 2. 为什么 Safety Guard 不能替代沙箱隔离？

1. **静态扫描与语义解析的局限性**：
   - 静态分析只能检查“表面上”的命令字符串。如果命令试图运行编译好的二进制程序、复杂的混淆脚本或动态调用的可执行代码，静态分析无法预知该二进制文件的真正行为。
2. **纵深防御 (Defense in Depth)**：
   - **Safety Guard (第一道防线)**：在零成本、微秒级延迟内阻断绝大多数已知的高危命令、明文密钥泄露与越权访问。
   - **沙箱隔离 (第二道防线)**：即便有未被检测到的风险代码突破了第一道防线，沙箱隔离（Docker / Container / MicroVM）能从操作系统内核级别限制网络、文件系统写权限与 CPU/内存使用，确保宿主机安全。

---

### 3. `workspaceexec` 与 `hostexec` 的安全边界说明

#### `workspace_exec` (工作区隔离执行)

- **文件系统限制**：强制命令工作目录（Cwd）限制在指定的 Workspace 目录下，禁止越界读取上级目录或系统敏感目录（如 `/etc`, `~/.ssh`）。
- **环境变量隔离**：仅传递白名单允许的环境变量（如 `PATH`, `HOME`），清洗潜在危险的变量配置。
- **进程生命周期**：单个命令运行完即销毁，不支持长连接会话，设有最大超时与输出大小限制。

#### `hostexec` (宿主机/PTY执行)

- **交互与长会话风险**：支持维持 PTY 交互会话或运行长服务（如 `top`），存在进程残留或长久占用资源的风险，需配置 `ask` 人工确认。
- **提权与后台进程**：严格禁止 `sudo` / `su` 提权命令及末尾 `&` 后台脱离进程，防止留下僵尸进程或提权漏洞。

---

## 快速开始与使用示例

### 1. 运行示例程序

```bash
cd examples/tool_safety_guard
go run main.go
```

### 2. 在 Agent 中引入 Safety Guard

```go
// 1. 加载策略并创建 SafetyGuard
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditLogger(auditLogger),
    safety.WithReportPath("tool_safety_report.json"),
)

// 2. 将 SafetyGuard 配置给 Agent 运行选项
runner := runner.NewRunner(
    "my-app",
    myAgent,
    runner.WithSessionService(sessionService),
)

// 在调用 runner.Run 时引入策略拦截
events, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithToolPermissionPolicy(guard),
)
```
