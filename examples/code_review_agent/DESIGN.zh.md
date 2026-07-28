# Code Review Agent 方案设计

基于 `trpc-agent-go` 实现 code review agent：Agent 借助 code-review Skill 理解变更，必要时在沙箱中取证，最后把结论结构化落库，输出 `review_report.json` 和 `review_report.md`

## SKILL 设计

code-review Skill 区分两种模式：

- patch-only：只传 `--diff-file`（可加 `--paths` / `--paths-file` 限定），不传 `--repo-path`；或传 `--fixture`，且该 fixture 只有 diff、没有 repo
- repo-backed：传 `--repo-path`（可单独使用，也可与 `--diff-file` 组合，并可加 paths）；或传 `--fixture`，且该 fixture 带 repo

两种模式均先审查变更 hunk，仅在确认行为、数据流、所有权、生命周期、可达性或规则豁免时扩读周围代码；结论只锚定本次变更或实际观察到的关联失败。repo-backed 对每个受影响且含 `go.mod` 的 Go module 调用 `run-go-checks.sh` 取证

结果按证据强度分为 findings（已确认、可行动）、warnings（已观察但不足以确认为缺陷）和 needs_human_review（已有合理危害路径，但缺少关键事实或需要人工决策）。检查失败、超时或执行被拒本身只作为执行事实

`SKILL.md` 定义审查方法和结果门槛，`references/rules.md` 维护规则、信号与豁免，`run-go-checks.sh` 执行单个 Go module 的基线检查；Review Store 仅保存不透明的 `rule_id`

## 沙箱隔离策略

执行环境默认选用 `codeexecutor/container`，创建时设置 `NetworkMode: "none"`。`codeexecutor/local` 只作为需要显式启用的 fallback：container 起不来时不会自动降级 local。每一次 review task 对应一个沙箱实例，task 结束即清理

Permission Policy 放行之后的命令的实际执行，由我们的 `Callbacks` 对执行结果进行脱敏、截断和状态分类，将超时、非零退出等事实写入 `sandbox_runs`，单次执行失败不会终止整个 review

## Permission 策略

通过 `PermissionPolicy` 判断高风险调用并记录 `allow`、`deny` 或 `ask`，目前在 example 中只将 `workspace_exec` 执行中命令字符串带有 `run-go-checks.sh` 和 `Metadata.Destructive == true` 的普通工具调用判定为高风险返回 `AskPermission`，`workspace_exec` 参数违反环境变量白名单、五分钟超时上限或基本结构约束时直接 `DenyPermission`，避免引入对于 example 来说过多的复杂性

对返回 `ask` 的工具调用，Agent 需要通过 `request_tool_permission` 提交目标工具、完整参数和理由，人工批准后重试原调用

高风险门禁和可查询的 permission decision 都放在这套 `PermissionPolicy` 里完成，如果改为为 `workspace_exec` 配置 `WithAllowedCommands` / `WithDeniedCommands` 黑白名单来拦截高风险命令，拦截会发生在策略已经放行之后，返回的是工具调用错误，既落不成可查询的 decision，也进不了拦截次数统计

## 监控字段

每个 task 在终态写入 `monitoring_summary_json`，内容包括总耗时、sandbox 耗时、Tool 调用次数、Permission 拦截次数、finding 数量、severity 分布和异常类型分布，这部分数据从已经落库的 permission decision、`sandbox_runs`、`review_results` 以及 task 起止时间构造

## 数据库 schema

Code Review 相关业务记录直接依赖 Review Store 接口，便于底层更换储存后端，目前使用 SQLite

Review Store 保存可按 `task_id` 查询的业务事实；框架 Artifact 保存脱敏 diff、报告等大对象，Review Store 只保存其名称与版本。`task_id` 在 review 开始前生成，同时作为 session id，并与 `app_name`、`user_id` 组成 Session 和 Artifact 的完整查询键

现存表：

- `review_tasks`：保存 task 生命周期、输入摘要、监控摘要、Artifact 引用、最终结论及终止错误
- `permission_decisions`：保存 Tool 执行前的治理决策，包括调用对象、操作摘要、Allow、Deny 或 Ask 结果及理由
- `sandbox_runs`：保存实际进入沙箱的执行事实，包括执行后端、命令、状态、退出码、超时、截断输出、脱敏计数、耗时和错误分类
- `review_results`：保存 findings、warnings 和 needs_human_review 的结构化结果及证据、位置、置信度和 `rule_id`
- `artifact_versions`：按完整 Session 键、名称和版本保存受大小限制的 Artifact 内容及元数据

区分 `permission_decisions` 与 `sandbox_runs` 两张表是因为前者记录执行前门禁的 permission decision，后者记录放行后实际执行的数据

## 去重降噪

同一文件、同一行、同一类问题不应重复出现；低置信内容不能混进 findings

语义上是否同一问题，由 Agent 参照 skill 判断：同一根因、同一受影响行为或资源、同一最小修复，只保留一条。确定性折叠则放在 `submit_review_results`：Agent 一次提交完整的 findings、warnings、needs_human_review 和 conclusion，提交边界做规范化与按位置、`rule_id` 等身份的归并；身份冲突时拒绝整次提交。数据库唯一索引只防止最终投影出现完全相同的规则与位置行，并不解释自然语言是否语义等价

使用 `submit_review_results` 工具显式提交而非使用框架 `WithStructuredOutputJSON`，是为了校验失败时能返回 Agent 修正，并保证库、监控和报告共用同一份已被接受的结果快照

## 安全边界

执行侧沿用前述强制边界：`PermissionPolicy` 在审批前校验环境变量白名单、timeout 上限和参数合法性；Tool Callbacks 限制回传输出并记录截断、超时和执行失败；Artifact Service 限制单个对象的大小。强制约束不能被人工审批绕过，未实际执行的调用不会写入 `sandbox_runs`

输入 diff 在进入模型和保存 Artifact 前由统一的 Sanitizer 脱敏；Tool 参数摘要、执行结果、错误信息和审查结果也在返回 Agent、写入 Review Store 或生成报告前使用同一套规则清洗

SQLite Session Service 通过 `WithAppendEventHook` 注入使用同一 Sanitizer 的 `AppendEventHook`，在 Event 更新 Session 内存状态和写入 SQLite 前执行脱敏；无法安全处理时拒绝写入，作为用户消息、Agent 回复和 Tool 事件的持久化保底。该 Hook 不替代上游脱敏，避免明文在到达 Session 前已经进入模型或其他输出面

`Sanitizer` 只覆盖了部分常见密钥、令牌、凭证形态，避免为 example 内注入过多主题无关的复杂度
