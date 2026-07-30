# Code Review Agent 方案设计

基于 `trpc-agent-go` 实现 code review agent：Agent 借助 code-review Skill 理解变更，必要时在沙箱中取证，最后把结论结构化落库，输出 `review_report.json` 和 `review_report.md`

## SKILL 设计

CR 流程中区分两种输入模式：patch-only（只传 `--diff-file`，不涉及仓库）与 repo-backed（传 `--repo-path`，可与 diff 组合）。两种模式均先审查变更 hunk，仅在确认行为、数据流、生命周期等问题时才扩读周围代码，结论只允许锚定本次变更或实际观察到的关联失败

结果分为 findings（证据充分且置信度达标）、warnings（已观察但影响不足以定为缺陷）、needs_human_review（上下文缺失、执行被拒或需人工决策）。规则的判定条件、rule-id 与豁免场景维护在 skill 的 `references/rules.md`，渐进式披露给 Agent

## 沙箱隔离策略

执行环境默认使用 `codeexecutor/container`， 设置 `NetworkMode: "none"`，每个 review task 对应一个沙箱实例，结束即清理；`codeexecutor/local` 仅作显式启用的 fallback，不自动降级

## Permission 策略

通过 `PermissionPolicy` 在执行前判断高风险调用并返回 `allow`/`deny`/`ask`：命中 `run-go-checks.sh` 或 `Metadata.Destructive == true` 判定为高风险并 `ask`；参数违反环境变量白名单、超时上限等约束直接 `deny`。放行后的实际执行由 `Callbacks` 脱敏、截断并分类状态，写入 `sandbox_runs`，单次失败不终止整个 review。框架也支持 `workspace_exec` 用 `WithAllowedCommands`/`WithDeniedCommands` 做黑白名单，但拦截发生在策略放行之后且只返回工具调用错误， 无法被直接记录为 `permission_decision`，且无法与高风险工具调用的记录在同一处收敛

## 监控字段

每个 task 终态写入 `monitoring_summary_json`：总耗时、沙箱耗时、Tool 调用次数、Permission 拦截次数、finding 数量及结果分档/严重级别/异常类型分布，均从已落库的 permission decision、`sandbox_runs`、`review_results` 及 task 起止时间构造

## 数据库 Schema

Code Review 业务记录依赖 Review Store 接口，而非直接依赖 SQLite 实现，与储存后端解耦。

现存表：

- `review_tasks`：task 生命周期与最终结论
- `permission_decisions`：Tool 执行前的治理决策及理由
- `sandbox_runs`：实际进入沙箱的执行事实
- `review_results`：结构化的审查结果
- `artifact_versions`：框架 Artifact

## 去重降噪

语义层面由 Agent 参照 skill 判断：同一根因、同一受影响行为、同一最小修复只保留一条

确定性折叠放在 `submit_review_results`。Agent 一次性提交完整结果，以 `(file, line, rule_id)` 为身份键合并同一处的重复条目，同一身份下问题分类或结果等级矛盾则工具调用失败，由 Agent 重试。数据库唯一索引只作为兜底

使用 `submit_review_results` 工具显式提交而非使用框架 `WithStructuredOutputJSON`，是为了输入校验失败时能返回给 Agent 修正，`WithStructuredOutputJSON` 反序列化 JSON 失败即静默丢弃且不重试

## 安全边界

`PermissionPolicy` 在审批前校验环境变量白名单、超时上限与参数合法性；Tool Callbacks 限制回传输出并记录截断/超时/失败；Artifact Service 限制对象大小。输入准备阶段还会在模型或沙箱访问前将 diff 文件和每个 Git 输出流限制为 64 MiB、单个快照文件限制为 64 MiB、完整快照限制为 512 MiB。这些约束不可被绕过，`sandbox_runs` 会记录所有实际执行的命令状态

review 输入 diff 在进入模型前，Tool 参数、执行结果与审查结论在回传或落库前统一经 `Sanitizer` 脱敏；SQLite Session Service 通过 `WithAppendEventHook` 注入同一个 `Sanitizer`，作为持久化前的兜底
