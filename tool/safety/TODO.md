# Tool Safety Guard — 代码审查 TODO & 设计建议

## 已修复的问题

| # | 问题 | 文件 | 修复方式 |
|---|------|------|----------|
| 1 | `containsNetworkCommand` 用 substring 匹配，"http" 会误匹配 `http.server` | checker_network.go | 改为 token-based 匹配，对 "http"/"host" 加 `-m` 上下文守卫 |
| 2 | `inferCommandRuleID` 依赖 shellsafe 错误消息字符串 | checker_command.go | 使用更精确的子串 (`"denied by built-in policy"` 而非 `"built-in"`)，加注释说明依赖 |
| 3 | Scanner 检查器列表硬编码，不能通过策略文件调整 | scanner.go + policy.go | Policy 新增 `checkers` 字段，`NewScanner` 支持过滤 |
| 4 | `defaultRequestMapper` 仅支持 `{"command": "..."}` 格式 | adapter.go | 新增 "cmd"、"script" 备用键名 |
| 5 | `equalOrSuffixMatch` 仅支持 `*_TOKEN` 前缀通配 | checker_env.go | 新增 `TOKEN_*` 后缀通配 |
| 6 | `maskSecret` 泄露过多字符 (keep=4) | checker_secret_cmd.go | 降至 keep=2~3，短于 7 字符全掩码 |
| 7 | `result()` 方法用 `strings.Contains` 在 pattern 上做 RuleID 分类，可能与文本内容混淆 | checker_path.go | 重构为 `classifyPathRule()`，仅基于 pattern 字符串本身分类 |
| 8 | `NewSafetyPermissionPolicy` 无意义的 `fmt.Sprintf` | adapter.go | 改为直接 `panic("message")`，移除未使用的 `fmt` import |
| 9 | `NewJSONLAuditLogger` / `SecretRegexps` nil receiver panic | audit.go + policy.go | 加 nil 检查 |
| 10 | 弱断言：`TestCommand_NotAllowed_RuleID` 等 | coverage_test.go | 改为精确匹配具体 RuleID |
| 11 | 缺失测试：checker 过滤、网络误匹配、env 后缀通配、cmd/script key | coverage_test.go | 新增 16 条测试 |

## 测试概览

- **总测试数**: 95 条（原 79 条 + 16 条新增）
- **覆盖率**: 91.5%（原 89.8%）
- **全部通过** ✅

## 剩余设计建议（非阻塞，供后续 PR 参考）

### 1. shellsafe 错误类型的依赖（checker_command.go）

**风险等级**: 低
**描述**: `inferCommandRuleID` 仍然依赖 shellsafe 错误消息字符串。虽然已使用更精确的子串，但如果 shellsafe 重写错误格式，RuleID 会退化为 `CMD_REJECTED`。

**建议**: 在 shellsafe 包中导出 sentinel error 类型（如 `ErrImplicitDeny`、`ErrDeniedCommand`、`ErrNotAllowed`），让 safety 包可以用 `errors.Is` 做类型判断。

### 2. checker 运行顺序不可配置

**风险等级**: 低
**描述**: 虽然现在可以通过 `policy.checkers` 禁用 checker，但顺序仍固定为 `command → secret_cmd → env → network → path → host → resource`。某些场景可能需要调整顺序（如先检查 secret 再检查 command）。

**建议**: 未来可扩展为 `checkers: [{name: "command", priority: 1}, ...]` 格式，支持自定义顺序。

### 3. Cwd 路径检测仅限 checker_path

**风险等级**: 中
**描述**: `Cwd` 的敏感路径检测仅在 `checker_path` 中进行。对于 hostexec backend，如果攻击者通过其他方式设置 `Cwd`（如修改工具参数而非命令本身），可能需要 host checker 中也检查。

**建议**: 在 `checker_host` 中也加入 `Cwd` 检测，或创建一个统一的 pre-check 阶段。

### 4. 策略文件中的 allowlist 过于宽松

**风险等级**: 高
**描述**: 测试用 YAML 策略的 `commands.allowed` 包含了 `nc`、`sleep`、`tmux`、`disown`、`screen`、`ssh` 等。这些命令在生产环境中应该更严格限制。

**建议**: 
- 创建单独的生产示例策略文件 `tool_safety_policy_production.yaml`
- 在 README 中明确标注测试策略仅供测试使用
- 添加 `allow_reason` 字段要求对高危命令的 allow 提供理由

### 5. MaxOutputMB 配置未完全集成

**风险等级**: 低
**描述**: `RESOURCE_OUTPUT_LIMIT` 规则已检测 `yes`、`cat /dev/urandom`、`dd` 等无界输出命令。但 `max_output_mb` 数值配置尚未用于运行时输出大小截断——此限制由 CodeExecutor 层实施（workspaceexec/hostexec 各自的 max_output_bytes / max_lines），Safety Guard 只做预检提醒。

**建议**: 在 README 中明确说明 `max_output_mb` 与 CodeExecutor 层限制的分工关系。

### 6. audit.go: TraceIDKey 是字符串常量但应该用自定义类型

**风险等级**: 低
**描述**: `TraceIDKey` 定义为 `const TraceIDKey = "trace_id"`（字符串），不是自定义类型。这可能导致 context key 冲突。

**建议**: 改为 `type contextKey string; const TraceIDKey contextKey = "trace_id"`，遵循 Go 最佳实践。

### 7. checker_secret_output.go 的 Check() 总是返回 nil

**风险等级**: 低
**描述**: `secretOutputChecker` 实现了 `Checker` 接口但 `Check()` 总是返回 nil。如果它被意外加入 checker 列表，不会有任何效果但会浪费一次扫描。

**建议**: 要么让 `Check()` 返回一个错误提示不应被调用，要么不实现 `Checker` 接口，将其作为独立的 `Desensitize` 工具函数。

### 8. 性能：每次 Scan 都创建新的 CheckerOutcome slice

**风险等级**: 低
**描述**: `Scanner.Scan()` 每次调用都创建新的 `[]CheckerOutcome`（`make([]CheckerOutcome, 0)`）。对于高频扫描场景，可以考虑预分配容量或使用 sync.Pool。

**建议**: 当前 500 条命令只需 3.6ms，性能充足。若未来需要更高吞吐，可考虑 sync.Pool。

## 安全性评估

### 防御深度 ✅

7 层检查器形成有效纵深防御：
1. shellsafe 解析器阻止 shell 注入
2. 命令 allow/deny 策略控制可执行文件
3. 密钥检测防止凭证泄露
4. 网络白名单/黑名单控制出口
5. 路径 glob 匹配保护敏感文件
6. Host checker 防止特权提升和会话残留
7. 资源检查器限制超时和并发

### 已知绕过路径（已在 README 中明确）

- Safety Guard 是**预执行静态检查**，不是沙箱
- 如果 shell 解析器接受了某种语法，后续 checker 可能漏检
- `containsNetworkCommand` 的 token 匹配可能被特殊编码绕过（如 `curl`）

### 建议添加的黑名单命令

以下命令在默认策略中缺失，建议加入 denied 或 allowed（需显式审计）:
- `chmod`、`chown` — 权限修改
- `mount`、`umount` — 文件系统操作
- `iptables`、`nft` — 防火墙修改
- `crontab` — 持久化任务
- `git clone` — 代码拉取（已有 `denied_install` 但 `git clone` 不在其中）
