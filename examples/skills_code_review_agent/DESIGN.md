# 方案设计说明

## 背景与价值

tRPC-Agent-Go 的 Skill 体系将可复用工作流封装为 SKILL.md、文档和脚本；CodeExecutor 支持 local / container / E2B 沙箱；Session 与 SQL 存储可持久化审查任务与结果。本示例把这些能力串成面向 Go 工程的自动 CR Agent：读取 diff、识别风险、在 Permission 允许后于沙箱执行检查、结构化落库并生成可审计报告。

## 架构（Phase 1 + 2 + 3）

1. **输入层** (`internal/diff`)：解析 unified diff / git 工作区，提取文件、hunk、行号、Go package。
2. **规则层** (`internal/rules`)：对新增行执行 7 类确定性规则（安全、并发、资源、错误、敏感信息、DB 事务、测试缺失）。
3. **LLM 层** (`internal/llmreview`，`--dry-run=false`)：`llmagent` + `code-review` Skill，补充规则未覆盖的发现，与规则结果合并去重。
4. **治理层** (`internal/sandbox`)：PermissionPolicy 拦截高风险命令；仅 allow 的命令进入沙箱。
4. **Skill 层** (`skills/code-review`)：SKILL.md 描述工作流；`scripts/run_checks.sh` 校验 diff；可选 `go vet`。
5. **降噪层** (`internal/findings`)：按 file+line+category 去重；confidence < 0.6 进入 warnings。
6. **安全层** (`internal/redact`)：报告与数据库写入前脱敏 API Key / token / password。
7. **持久层** (`internal/storage`)：SQLite 六张表，Store 接口可替换 PostgreSQL 等后端。
8. **输出层** (`internal/report`)：JSON + Markdown，含 findings、治理拦截、沙箱摘要、监控指标。

## Skill 设计

- **name**: `code-review`
- **docs**: `docs/rules.md` 列出 Rule ID 与类别
- **scripts**: `run_checks.sh` 验证 diff 格式、行数上限、ignored-error 模式
- **编排**: pipeline 通过 `skill_run` 语义调用（Go 实现保证跨平台；container 环境可执行 shell 脚本）

## 沙箱隔离策略

| 控制项 | 实现 |
|--------|------|
| 默认生产 runtime | `container`（`golang:1.24-bookworm`）或 `e2b`；CLI 默认 `local` 便于无 Docker 测试 |
| 超时 | 30s（`RunProgramSpec.Timeout` / context） |
| 输出上限 | 64KB stdout/stderr 截断 |
| 环境变量 | CleanEnv + PATH/LANG 白名单 |
| 失败处理 | 记录 `sandbox_runs.error_type`，不中断 review task |

## Permission / Filter 策略

执行前对每个计划命令调用 PermissionPolicy：

- **deny**: `rm -rf`、`curl|bash`、`git push` 等 — 写入 `permission_decisions`，不进入沙箱
- **allow**: `bash scripts/run_checks.sh`、`go vet`、`go test`
- **ask**: 其他未在白名单的命令 — 记录为需人工审批，不执行

## 监控字段

`review_metrics` 记录：`total_duration_ms`、`sandbox_duration_ms`、`finding_count`、`warning_count`、`tool_call_count`、`permission_deny_count`、`severity_json`、`exception_json`。

## 数据库 Schema

| 表 | 用途 |
|----|------|
| `review_tasks` | 任务状态、输入摘要、耗时 |
| `findings` | 结构化问题 |
| `review_metrics` | 监控摘要 |
| `artifacts` | 报告副本 |
| `sandbox_runs` | 沙箱命令、exit code、error_type |
| `permission_decisions` | 工具名、命令、allow/deny/ask |

## 去重、降噪与安全边界

- 去重键：`file:line:category`
- 低置信度不进 confirmed findings
- 脱敏在最终写入前执行，检出率目标 ≥ 95%
- 沙箱失败不导致任务崩溃（验收：`08_sandbox_fail` fixture）

## dry-run 模式

`--dry-run=true`（默认）时不调用 LLM，完整链路可在无 API Key 环境测试。

`--fake-model` 使用 mock model 跑 Agent + Skill 编排（无需 API Key）。

`--dry-run=false` 调用真实 LLM，需 `OPENAI_API_KEY`。
