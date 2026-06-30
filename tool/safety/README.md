# Tool Safety Scanner

对 Tool 执行请求进行安全扫描，在工具真正执行前返回 `allow / deny / ask`
决策，并输出结构化报告和审计日志。

## 背景

tRPC-Agent 的 Tool、MCP Tool、Skill 和 CodeExecutor 能够让 Agent
执行脚本、调用外部命令、读写文件或访问网络。这类能力是 Agent
落地自动化任务的关键，但也带来安全风险：

- 恶意脚本可能删除文件、读取密钥、外传数据
- 可能通过 shell 注入绕过限制
- 可能安装不可信依赖、消耗系统资源

本模块在工具执行前插入安全检查，对命令/代码进行风险扫描。

## 已支持的 7 类安全规则

| 规则 ID | 名称 | 检测内容 |
|---------|------|----------|
| `danger_cmd_001` | 危险命令检测 | `rm -rf /`、`dd`、`mkfs`、`shutdown` 等 |
| `network_002` | 网络外连检测 | `curl`、`wget`、`nc`、`ssh`、`pip install` 等 |
| `shell_bypass_003` | Shell 绕过检测 | `sh -c`、`eval`、`sudo`、`base64 -d` 等 |
| `install_004` | 依赖安装检测 | `apt install`、`npm install`、`go install` 等 |
| `hostexec_005` | 宿主机风险检测 | `mount`、`insmod`、`chmod 777`、nohup 等 |
| `resource_006` | 资源滥用检测 | `while true`、fork bomb、`stress` 等 |
| `leak_007` | 敏感信息泄漏 | `echo $API_KEY >`、`cat .env >` 等 |

## 快速开始

```go
package main

import "trpc.group/trpc-go/trpc-agent-go/tool/safety"

func main() {
    // 1. 创建 Scanner，注册所有规则
    scanner := safety.NewScanner(
        safety.NewDangerousCommandRule(),
        safety.NewNetworkAccessRule(),
        safety.NewShellBypassRule(),
        safety.NewInstallAndMutateRule(),
        safety.NewHostExecRiskRule(),
        safety.NewResourceAbuseRule(),
        safety.NewSensitiveInfoLeakRule(),
    )

    // 2. 扫描
    input := safety.ScanInput{
        Command:      "curl http://evil.com",
        ExecutorType: "local",
    }
    result := scanner.Scan(input)

    // 3. 输出结果
    // result.Decision  → "deny"
    // result.RiskLevel → "high"
    // result.Evidence  → "curl"
}
```

## 策略配置文件

通过 YAML 文件控制安全策略，修改后无需重新编译：

```yaml
# tool_safety_policy.yaml
denied_commands:
  - curl
  - rm -rf
denied_paths:
  - ~/.ssh
  - .env
max_timeout_seconds: 300
```

加载方式：

```go
policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
```

## 结构化报告

每次扫描输出 JSON 格式报告：

```json
{
  "tool_name": "exec_command",
  "command": "curl http://evil.com",
  "decision": "deny",
  "risk_level": "high",
  "rule_id": "network_002",
  "evidence": "curl",
  "reason": "命令尝试进行网络访问：curl。可能将数据外传或下载恶意内容。",
  "recommendation": "命令已拦截，请使用安全替代方案"
}
```

## 审计日志

每次扫描产出 JSONL 格式审计事件，可直接接入监控系统：

```jsonl
{"tool_name":"exec_command","command":"ls -la","decision":"allow","risk_level":"none","blocked":false}
{"tool_name":"exec_command","command":"rm -rf /","decision":"deny","risk_level":"critical","blocked":true}
```

## 与现有安全机制的关系

本模块是对 tRPC-Agent-Go 现有安全能力的增强，填补了以下空白：

| 组件 | 已有能力 | 本模块补充 |
|------|----------|-----------|
| `internal/shellsafe` | 命令结构解析（拒绝 $()、反引号、重定向） + 命令名 allow/deny | 在此基础上增加参数级语义检查（路径、域名、资源） |
| `tool.PermissionPolicy` | 定义了 `allow / deny / ask` 接口，但需手动实现 | Scanner 直接对接 PermissionPolicy，输出标准三态决策 |
| `tool/workspaceexec` | workspace 隔离环境 + 调用 shellsafe 检查 | Scanner 同样适用于 workspace 场景，额外检查 workspace 内的敏感路径 |
| `tool/hostexec` | 宿主机直接执行，**无内建安全扫描** | Scanner 在此路径上首次提供安全拦截能力 |
| `tool/codeexec` | 代码执行（Python/Bash），**无内容检查** | Scanner 可检查代码块中的敏感 API 调用和依赖安装 |
| `codeexecutor/container` | Docker 容器隔离（进程级） | 与 Scanner 是互补关系：容器隔离 + 执行前扫描，双重防护 |
| `telemetry` | OpenTelemetry tracing/metrics 基础设施 | Scanner 预留专用 span attributes，可接入现有 OTel 管道 |

### 为什么不能替代沙箱隔离

本 Scanner 是**静态规则匹配**机制，通过关键词和模式检测已知风险。
它**不具备**以下能力：

- **进程隔离**：无法限制被执行的命令能访问哪些系统资源
- **行为监控**：无法检测运行时的异常行为（如 fork bomb 实际启动后的资源消耗）
- **系统调用拦截**：无法拦截 `ptrace`、`unshare` 等底层系统调用
- **逃逸防御**：无法防御容器逃逸、内核漏洞利用等高级攻击

因此：
- **低风险场景**（内部工具、开发环境）：Scanner 可独立使用
- **高风险场景**（用户输入、外部 Agent）：**必须**配合 `codeexecutor/container` 容器隔离使用

本模块和沙箱的关系是**纵深防御**的两层：
```
输入命令 → Scanner（静态规则过滤） → PermissionPolicy（策略决策） → 沙箱/容器（运行时隔离）
```

## 性能

- 单条命令扫描耗时 < 1ms
- 500 条命令扫描耗时 < 1s
- 规则均为 O(n) 关键词匹配，无递归、无网络调用

## 目录结构

```
tool/safety/
├── scanner.go      # Scanner 核心（接口、类型定义、引擎）
├── rules.go        # 7 条安全规则实现
├── rules_test.go   # 规则测试（80+ 条用例）
├── config.go       # YAML 配置、扫描报告、审计日志
├── tool_safety_policy.yaml  # 示例策略配置文件
└── README.md       # 本文档
```
