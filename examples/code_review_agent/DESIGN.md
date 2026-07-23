# 自动代码评审 Agent 实现方案

> 模块：`examples/code_review_agent`  
> 框架：tRPC-Agent-Go（Skill / CodeExecutor / Permission / Artifact）  
> 状态：已实现，可通过 `go test ./...` 验收

## 1. 设计目标

本示例不是让 LLM 自由点评代码，而是把 **Skills、沙箱执行、治理策略、确定性审查规则、结构化结果、SQLite 落库与监控审计** 串成可验证流水线。验收主链路为 `rule-only`（无 API Key）；`--mode=llm` 支持 `--llm=fake|openai|auto`：默认 fake 演示 `skill_load` + `workspace_exec`，也可通过 `OPENAI_API_KEY`（及可选 `OPENAI_BASE_URL`）走真实 OpenAI 兼容模型。最终 findings 仍以规则引擎为准。

## 2. Skill 设计

`skills/code-review/` 提供 `SKILL.md`、`docs/rules.md`、`docs/usage.md` 与脚本目录。规则覆盖：安全（SQL 拼接 / TLS）、敏感信息、goroutine/context、资源关闭、数据库连接/事务、错误处理、测试缺失。脚本包括 `run_checks.sh`、`run_go_vet.sh`、`run_go_test.sh`、`run_staticcheck.sh`（后两者可选）。宿主 Orchestrator 加载 Skill 工作副本路径，在 Permission 允许后于沙箱执行。

## 3. 沙箱隔离策略

默认 `--executor=container`（真实 `codeexecutor/container`）；可选 `e2b`（Cube 兼容 API）。`local` 仅开发 fallback，且 **`--allow-local-fallback` 默认关闭**，避免静默降级。执行经 `CodeExecutor.ExecuteCode(bash)`，带超时（默认 60s）、stdout/stderr 1MiB 截断、环境变量白名单；失败/超时记 `sandbox_run`，任务可 `partial` 但不崩溃。

## 4. Permission / Filter 策略

命令白名单（go/git/rg/bash/staticcheck 等）+ `PermissionPolicy`：`curl/sudo/rm -rf/docker` 等 **deny**；宽泛 `go test ./...` 为 **ask/needs_human_review**。deny/ask **不得**进入沙箱。决策写入 `permission_decision` 并出现在报告 Governance。LLM 模式将同一 Gate 挂到 `WithToolPermissionPolicy`，且 **沿用 `AllowLocalFallback`，不再强制 true**。fixture / 演示路径可注入 deny+ask 样例命令；真实 `--diff-file` / `--repo-path` / `--files` 默认不注入。

## 5. 去重降噪与安全边界

去重键：`file|line|rule_id`，保留更高置信度。confidence ≥0.75 进 findings；[0.40,0.75) 进 warnings/人工复核；更低丢弃。落库与报告前统一脱敏（API Key/token/password/AKIA/Bearer）。Artifact 限制：最多 32 个、单文件 2MiB、合计 8MiB，超额丢弃并记异常。

## 6. 数据库 schema 与监控

SQLite 默认实现 `ReviewStore`（接口可换 SQL 后端）。表：`review_task`、`review_input`、`sandbox_run`、`permission_decision`、`finding`、`artifact`、`metrics_summary`、`report`；支持按 task id 全量查询。监控字段：总耗时、沙箱耗时、工具调用次数、Permission 拦截次数、finding/warning 数量、severity 分布、异常类型分布。

## 7. 输入输出

输入：`--diff-file`、`--repo-path`、`--files`（路径列表/`@listfile`）、`--fixture`。输出：`review_report.json`、`review_report.md` 与 SQLite。公开 9 类 fixture（含 `expected.json`）与隐藏评测集（检出≥80%、误报≤15%）。

## 8. 方案摘要（300–500 字）

本原型在 `examples/code_review_agent` 把 tRPC-Agent-Go 的 Skill 体系、CodeExecutor
沙箱（默认 container，可选 e2b；local 仅显式 fallback）、Permission 治理与独立
审查持久化层组合成可验证的自动代码评审系统，面向 Go 工程 diff 而非通用文本点评。
Orchestrator 支持 unified diff、git 工作区、`--files` 路径列表与 fixture，解析 hunk、
行号与 package 后加载 `skills/code-review`（SKILL.md、规则文档与检查脚本）。高风险
命令先经白名单与 PermissionPolicy：deny / ask / needs_human_review 不得进入沙箱，
决策落库并可在报告中审计。沙箱执行具备超时、输出截断、环境变量白名单与失败记录；
确定性规则覆盖安全、敏感信息、goroutine/context、资源关闭、数据库生命周期、错误
处理与测试缺失，输出含 severity、category、file、line、title、evidence、
recommendation、confidence、source、rule_id 的 findings。结果经 `file|line|rule_id`
去重与置信度分桶（低置信进 warnings），统一脱敏后写入 `review_report.json`/
`.md` 与 SQLite（`ReviewStore` 接口可换后端）。库表记录 task、input、sandbox run、
permission、finding、artifact、metrics 与 report，支持按 task id 查询回放。监控含
总耗时、沙箱耗时、工具调用、Permission 拦截、finding 数量及 severity/异常分布。
`rule-only`、`dry-run` 与 fake model 模式无需真实 API Key，便于 CI 与无密钥环境回归；
公开/隐藏样本与单元测试覆盖解析、去重、脱敏、落库查询与失败不崩溃路径，满足题面交付物与全部验收标准要求。

