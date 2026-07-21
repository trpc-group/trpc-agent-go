# Go 自动代码评审 Agent 示例

这是一个自包含的自动 Go 代码评审 Agent 原型，借鉴 tRPC-Agent 的核心概念：Skill 封装、代码执行沙箱、PermissionPolicy 治理、持久化评审状态、Telemetry 风格监控摘要，以及结构化报告输出。

当前工作区不是上游 `trpc-agent-go` 仓库，因此该示例把集成边界做得很小，并尽量保持无外部依赖。`Store` 接口和沙箱 runner 的形状已经预留出来，后续可以替换为真实 tRPC-Agent 应用里的 `session/sqlite`、`tool/codeexec`、`codeexecutor/container`、`codeexecutor/e2b` 和 telemetry 适配器。

## 运行方式

```powershell
go test ./...
go run . --fixture security_sql --out-dir out/security_sql --store-path out/security_sql/store.json
go run . --fixture redaction --out-dir out/redaction --store-path out/redaction/store.json
go run . --fixture sandbox_failure --force-sandbox-failure --out-dir out/sandbox_failure --store-path out/sandbox_failure/store.json
```

输入参数：

- `--diff-file path/to/change.diff`：读取 unified diff 或 PR patch。
- `--repo-path path/to/git/repo`：读取本地 git 工作区变更。
- `--files internal/a.go,internal/b.go`：只有文件列表时使用。
- `--fixture clean|security_sql|goroutine_leak|resource_unclosed|db_lifecycle|missing_tests|duplicate_finding|sandbox_failure|redaction`：运行内置测试样例。

运行时参数：

- `--runtime fake`：确定性的 dry-run 沙箱，默认用于本地测试。
- `--runtime container`：可用 Docker 时使用容器执行，工作区只读挂载且禁用网络。
- `--runtime e2b` 或 `cube`：预留远程沙箱集成接口。
- `--runtime local`：仅作为开发 fallback，不应作为生产默认方案。

输出文件：

- `review_report.json`：机器可读结构化报告。
- `review_report.md`：人工阅读报告。
- 默认持久化存储：`out/review_store.json`。
- SQLite 兼容 schema：`schema.sql`。

## Golang 开发者需要学习什么

要把这个原型完整接入开源版 tRPC-Agent，一个 Go 开发者建议重点学习以下内容：

1. tRPC-Agent 基础抽象：Agent 生命周期、tools、`tool/skill`、`skill load`、`skill run`、artifact、session/memory、filter、PermissionPolicy、telemetry hooks。
2. 沙箱执行体系：`tool/workspaceexec`、`tool/hostexec`、`tool/codeexec`、container runtime、E2B/Cube runtime、超时与取消、输出大小限制、只读挂载、环境变量白名单、artifact 白名单。
3. Go 代码评审领域知识：unified diff 解析、hunk 行号映射、package 发现、`go test`、`go vet`、可选 `staticcheck`、context 传播、goroutine 生命周期、资源关闭、错误包装、SQL 事务生命周期、密钥扫描。
4. 持久化设计：SQLite schema、task/run/finding/report 关系、幂等 migration、按 task id 查询、脱敏 evidence、报告 artifact。
5. 治理与可观测性：permission decision、deny/ask/needs-human-review 状态、去重、confidence 阈值、严重级别统计、延迟、异常分布、可回放审计记录。
6. 可测试性：deterministic rule-only 模式、fake model 模式、fixture diff、沙箱失败测试、适合隐藏样本的规则设计，以及无 API Key 的完整链路测试。

## Fixture 矩阵

| Fixture | 目的 |
| --- | --- |
| `clean` | 无阻塞问题，并包含测试更新 |
| `security_sql` | SQL 拼接风险与 rows 生命周期风险 |
| `goroutine_leak` | goroutine 取消路径与 `time.Tick` 泄漏 |
| `resource_unclosed` | 文件打开后缺少关闭路径 |
| `db_lifecycle` | transaction 缺少 commit/rollback |
| `missing_tests` | 生产行为变更但没有测试更新 |
| `duplicate_finding` | finding 去重与密钥脱敏 |
| `sandbox_failure` | 沙箱失败不应导致评审崩溃 |
| `redaction` | 报告和持久化存储不能包含明文 password/token |

## 生产集成建议

- 用 `database/sql` 实现替换 `JSONStore`，后端可接 `session/sqlite` 或其他 SQL 存储。当前 `schema.sql` 给出了最小关系模型。
- 用 `tool/codeexec` 加 `codeexecutor/container` 或 E2B/Cube runtime 替换当前 `SandboxRunner`。`local` 模式应始终保持为开发专用开关。
- 将 `PermissionPolicy.Decide` 接入 tRPC-Agent filter/permission hooks，并为每次工具调用和规则扫描发出 telemetry span。
- 如果后续加入 LLM review，应保留确定性规则作为第一道扫描，只让低置信或需要解释的 case 进入模型；模型证据单独存储，并且所有边界都必须执行密钥脱敏。
