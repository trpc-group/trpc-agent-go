# Code Review Agent Design / Code Review Agent 方案设计

## 1. Scope and Architecture / 范围与架构

English:
The code review agent is an executable Go example that demonstrates how a repository-local review workflow can combine a Codex-style Skill, deterministic rule checks, sandboxed command execution, structured findings, durable storage, and report generation. It is intentionally placed under `examples/code_review_agent/` so the implementation is isolated from the root framework while still being able to review Go code in a real repository checkout.

The agent accepts review input from fixture directories, unified diff files, file lists, or a Git workspace path. It parses the input into changed files, hunks, candidate line numbers, and package metadata; runs deterministic review rules; optionally asks an OpenAI-compatible planner for review planning; gates high-risk commands through a safety wrapper; executes allowed checks in a sandbox runtime; stores the full task trail; and emits both machine-readable and human-readable reports.

中文：
Code Review Agent 是一个可运行的 Go 示例，用来展示如何把仓库内的 code-review Skill、确定性规则检查、沙箱命令执行、结构化 findings、持久化存储和报告生成串成一条完整链路。它放在 `examples/code_review_agent/` 下，避免影响根模块核心框架，同时仍然可以在真实仓库工作区内审查 Go 代码。

Agent 支持从 fixture 目录、unified diff 文件、文件列表或 Git 工作区读取输入。输入会被解析为变更文件、hunk、候选行号和 Go package 信息；随后执行确定性规则；在非 fake 模式下可调用 OpenAI-compatible planner 生成检查计划；高风险命令会先经过安全 wrapper 决策；允许执行的命令会进入沙箱 runtime；最终将任务全链路落库，并输出 JSON 与 Markdown 两种报告。

## 2. Skill Design / Skill 设计

English:
The review Skill lives in `skills/code-review/` and is deliberately structured as a portable review package rather than only a prompt. `SKILL.md` describes the review workflow, expected inputs, required output fields, risk categories, and execution rules. `docs/rules.md` records the rule taxonomy and reviewer expectations. The `scripts/` directory contains helper commands that can be run inside the sandbox, including diff summarization and Go check orchestration.

This structure keeps three concerns separate:

- `SKILL.md` defines how the agent should reason and report.
- Rule documentation defines what categories are in scope and how evidence should be interpreted.
- Scripts provide executable checks that can be called by the orchestrator after permission filtering.

The current rule set covers more than the minimum required categories: secret leakage, goroutine/context lifecycle issues, resource closing, ignored errors, missing tests, and database transaction or connection lifecycle risks. Rule findings are normalized into a common schema before report generation.

中文：
审查 Skill 位于 `skills/code-review/`，它不是单纯的提示词，而是一个可移植的审查包。`SKILL.md` 描述审查流程、输入形态、输出字段、风险类别和执行规则；`docs/rules.md` 记录规则分类和 reviewer 判断标准；`scripts/` 目录提供可在沙箱内执行的辅助脚本，例如 diff 摘要和 Go 检查编排。

这种结构把三类职责拆开：

- `SKILL.md` 定义 Agent 如何审查和输出。
- 规则文档定义覆盖范围和证据判断标准。
- 脚本提供可执行检查，由 orchestrator 在 permission/filter 通过后调用。

当前规则覆盖范围超过最低要求，包含敏感信息泄漏、goroutine/context 生命周期、资源关闭、错误处理、测试缺失、数据库事务或连接生命周期问题。规则命中的结果会先标准化为统一 finding schema，再进入报告生成阶段。

## 3. Input Parsing / 输入解析

English:
The agent supports four input modes:

- `--fixture-dir`: loads a directory of test diffs and expected review scenarios.
- `--diff-file`: reads one unified diff file directly; it may be combined with `--repo-path` to associate sandbox validation with the patched checkout.
- `--repo-path`: inspects a Git workspace, derives current changes, and runs sandbox commands in that workspace.
- `--file-list`: reads a newline-delimited list of file paths for planning and sandbox context; it may be combined with `--repo-path` to identify the repository that owns those paths. Without a repository, sandbox validation is skipped. Content-based deterministic rules require diff input.

Unified diffs are parsed into files, hunks, added/deleted/context lines, and candidate line numbers. Go package information is extracted when available so checks can be scoped to the right module or package. Fixture mode is primarily used for deterministic acceptance tests, while repo and diff modes are closer to real review usage.

中文：
Agent 支持四种输入模式：

- `--fixture-dir`：读取测试 fixture 目录中的 diff 和审查场景。
- `--diff-file`：直接读取一个 unified diff 文件；可以与 `--repo-path` 组合，将 sandbox 校验关联到补丁所属 checkout。
- `--repo-path`：检查 Git 工作区、提取当前变更，并在该工作区运行沙箱命令。
- `--file-list`：读取按行分隔的文件路径列表，用于 planning 和 sandbox 上下文；可以与 `--repo-path` 组合以明确路径所属仓库；未提供仓库时跳过 sandbox 校验；基于内容的确定性规则需要 diff 输入。

Unified diff 会被解析为文件、hunk、新增/删除/上下文行以及候选行号。对于 Go 代码，会尽量提取 package 信息，便于把检查限定到正确的 module 或 package。fixture 模式主要用于确定性验收测试，repo 和 diff 模式更接近真实审查场景。

## 4. Review Rules and Findings / 审查规则与 Findings

English:
Every finding is represented with a structured schema so downstream tools can consume reports without scraping prose. The required fields are:

- `severity`: risk level such as critical, high, medium, or low.
- `category`: review domain such as security, concurrency, resources, errors, tests, or database.
- `file` and `line`: source location.
- `title`: concise issue summary.
- `evidence`: the code or behavior that triggered the finding.
- `recommendation`: actionable fix guidance.
- `confidence`: reviewer confidence used for filtering and escalation.
- `source`: rule, sandbox, model, or safety component that produced the finding.
- `rule_id`: stable rule identifier.

The implementation also tracks status and fingerprint metadata. High-confidence findings are reported as findings. Lower-confidence or ambiguous issues are routed to warnings or human-review sections so the report does not overstate uncertain results.

中文：
每个 finding 都使用结构化 schema 表示，方便下游工具消费报告，而不需要解析自然语言。必要字段包括：

- `severity`：风险级别，例如 critical、high、medium、low。
- `category`：审查领域，例如 security、concurrency、resources、errors、tests、database。
- `file` 和 `line`：源码位置。
- `title`：简洁的问题标题。
- `evidence`：触发 finding 的代码或行为证据。
- `recommendation`：可执行的修复建议。
- `confidence`：置信度，用于过滤、降噪和升级人工复核。
- `source`：产生 finding 的来源，例如 rule、sandbox、model 或 safety 组件。
- `rule_id`：稳定规则标识。

实现中还会记录 status 和 fingerprint。高置信问题进入正式 findings；低置信或需要判断的问题进入 warnings 或 human-review 区域，避免把不确定结果包装成确定性结论。

## 5. Sandbox Isolation Strategy / 沙箱隔离策略

English:
For the container runtime, the orchestrator stages the selected workspace into an isolated workspace. Fixture inputs run commands from the example module directory so the sample's review scripts and rules are available. Standalone diff and file-list inputs without `--repo-path` skip sandbox validation; associated `--repo-path` inputs run commands from the selected repository root, and the command allowlist is limited to checks that are valid in an arbitrary Go repository, such as `go test ./...` and `go vet ./...`. The container is unprivileged, auto-removed, resource-limited, and only the reviewed workspace is mounted. Network mode is explicitly set to `none`; the image/runtime must provide dependencies for `GOMODCACHE=/go/pkg/mod` because the host-wide module cache is never mounted. The runtime also sets container-safe Go paths such as `HOME=/tmp`, `GOPATH=/go`, and `GOCACHE=/tmp/go-build`.

For E2B, the upload boundary uses a temporary review snapshot built from `git ls-files --cached --others --exclude-standard`. Git metadata, ignored files, environment files, and local report/store artifacts are excluded before `StageDirectory` uploads the snapshot.


Execution is bounded by command timeouts and output size limits. Stdout and stderr are redacted before they are stored or reported. Sandbox failures, command failures, timeouts, and truncated output are recorded as sandbox run records instead of crashing the whole review task.

中文：
在 container runtime 中，orchestrator 会把选中的工作区 staging 到隔离 workspace。fixture 输入会从示例模块目录执行命令，便于使用示例自带的 review scripts 和 rules。未提供 `--repo-path` 的单独 diff 和 file-list 输入会跳过 sandbox 校验；关联 `--repo-path` 的输入会从用户选择的仓库根目录执行命令，并将命令 allowlist 限制为适用于任意 Go 仓库的检查，例如 `go test ./...` 和 `go vet ./...`。容器以非特权方式运行，执行结束后自动删除，并设置资源上限；只挂载被审查的工作区。网络模式显式设置为 `none`；沙箱命令不会通过网络下载 Go module，镜像或 runtime 必须自行提供 `GOMODCACHE=/go/pkg/mod` 所需依赖，绝不挂载宿主机全局 module cache。runtime 还会设置容器内安全的 Go 路径，例如 `HOME=/tmp`、`GOPATH=/go` 和 `GOCACHE=/tmp/go-build`。

对于 E2B，上传边界使用由 `git ls-files --cached --others --exclude-standard` 构建的临时 review snapshot。在 `StageDirectory` 上传前会排除 Git 元数据、ignored 文件、环境文件以及本地 report/store 产物。


命令执行受 timeout 和输出大小限制约束。stdout 和 stderr 在落库或写入报告前会先做脱敏处理。沙箱初始化失败、命令失败、超时、输出截断都会记录为 sandbox run，而不是让整个 review task 崩溃。

## 6. Permission and Filter Strategy / Permission 与 Filter 策略

English:
All planned commands pass through the safety wrapper before execution. The permission decision can allow execution, deny execution, request human review, or ask for additional confirmation. Commands denied by the policy are not sent to the sandbox. Instead, the agent records a permission decision and a skipped sandbox run so the audit trail remains complete.

The wrapper is intended to protect the boundary between review planning and command execution. Review planning may propose commands, but execution only happens after the command is classified as safe for the selected runtime. High-risk commands are blocked or escalated before sandbox initialization, preventing dangerous behavior from reaching the execution layer.

中文：
所有计划执行的命令都会先经过 safety wrapper。Permission 决策可以是允许执行、拒绝执行、需要人工复核或需要进一步确认。被策略拒绝的命令不会进入沙箱；Agent 会记录 permission decision 和 skipped sandbox run，保证审计链路完整。

这个 wrapper 的核心作用是保护“审查计划”和“命令执行”之间的边界。Planner 可以提出命令，但只有被分类为对当前 runtime 安全的命令才会真正执行。高风险命令会在沙箱初始化前被拦截或升级，避免危险行为进入执行层。

## 7. Persistence and Database Schema / 持久化与数据库 Schema

English:
The example uses a durable JSON-backed store for portability and ships a SQLite-compatible schema so the storage layer can be migrated to SQL backends. The storage interface keeps the implementation replaceable: SQLite is the default target for a small local deployment, while the schema design avoids coupling review logic to a single persistence engine.

In the current example implementation, `review_agent.db` is a JSON-backed equivalent persistent store, not a physical SQLite database file. It can be loaded by the example store implementation and queried by task through the Go store API, but it is not expected to work with `sqlite3 review_agent.db ".tables"` or direct SQL queries. `internal/store/schema.sql` documents the SQL-compatible target schema for teams that want to replace the portable JSON store with a strict SQLite or other SQL backend.

The schema stores these entities:

- `review_tasks`: task id, status, conclusion, timestamps, input mode, and overall summary.
- `input_summaries`: normalized diff or file-list summary for the task.
- `sandbox_runs`: runtime, command, status, exit code, duration, stdout/stderr, truncation, and error type.
- `permission_decisions`: command-level allow/deny/ask/human-review decisions and reasons.
- `findings`: structured review issues with severity, category, location, evidence, recommendation, confidence, source, rule id, status, and fingerprint.
- `artifacts`: generated files such as reports or intermediate artifacts.
- `reports`: final JSON/Markdown report locations and conclusion.
- `metrics`: review timing, counters, severity distribution, error distribution, and redaction counts.

This data model supports querying every review by task id and reconstructing the full lifecycle from input parsing through final report generation.

中文：
示例当前使用 JSON-backed durable store 来保证可移植性，同时提供 SQLite-compatible schema，便于后续切换到 SQL 后端。存储层通过接口隔离，SQLite 可以作为小型本地部署的默认目标，但审查逻辑不会绑定到单一持久化实现。

当前示例实现里的 `review_agent.db` 是 JSON-backed 的等价持久化文件，不是物理 SQLite 数据库文件。它可以被示例里的 store 实现加载，并通过 Go store API 按 task 查询，但不适合用 `sqlite3 review_agent.db ".tables"` 或直接 SQL 查询打开。`internal/store/schema.sql` 记录的是 SQL-compatible 目标 schema，方便后续把便携 JSON store 替换成真正 SQLite 或其他 SQL 后端。

Schema 保存以下实体：

- `review_tasks`：task id、状态、结论、时间戳、输入模式和整体摘要。
- `input_summaries`：标准化后的 diff 或文件列表摘要。
- `sandbox_runs`：runtime、命令、状态、exit code、耗时、stdout/stderr、是否截断和错误类型。
- `permission_decisions`：命令级 allow/deny/ask/human-review 决策与原因。
- `findings`：结构化审查问题，包括严重级别、类别、位置、证据、建议、置信度、来源、规则 id、状态和 fingerprint。
- `artifacts`：生成文件，例如报告或中间产物。
- `reports`：最终 JSON/Markdown 报告路径和结论。
- `metrics`：审查耗时、计数器、严重级别分布、错误类型分布和脱敏次数。

这个数据模型支持按 task id 查询每次审查，并能从输入解析到最终报告完整还原任务生命周期。

## 8. Monitoring and Audit Metrics / 监控与审计指标

English:
The report metrics are designed for both operational visibility and acceptance validation. The current monitoring summary includes:

- `total_duration_ms`: end-to-end review duration.
- `sandbox_duration_ms`: total time spent inside sandbox executions.
- `tool_call_count`: number of executed or attempted sandbox/tool commands.
- `permission_blocked_count`: number of commands blocked or escalated by safety policy.
- `finding_count`: total number of findings.
- `severity_distribution`: count by severity.
- `error_distribution`: count by sandbox or orchestration error type.
- `redaction_count`: number of detected/redacted sensitive values.

These fields appear in the generated JSON report and are persisted so a failed or partially completed review can still be audited. Sandbox run records also keep command-level duration and error detail, which makes it possible to distinguish model/network failures, command failures, timeouts, and permission blocks.

中文：
报告中的 metrics 同时服务于运维可观测和验收验证。当前监控摘要包括：

- `total_duration_ms`：端到端审查耗时。
- `sandbox_duration_ms`：沙箱执行总耗时。
- `tool_call_count`：执行或尝试执行的 sandbox/tool 命令数量。
- `permission_blocked_count`：被安全策略拦截或升级的命令数量。
- `finding_count`：finding 总数。
- `severity_distribution`：按 severity 统计的问题分布。
- `error_distribution`：按沙箱或编排错误类型统计的错误分布。
- `redaction_count`：检测并脱敏的敏感值数量。

这些字段会写入 JSON 报告并持久化，因此即便 review 失败或部分完成，也可以审计。sandbox run 还保留命令级 duration 和错误详情，用于区分模型/网络失败、命令失败、超时和 permission 拦截。

## 9. Deduplication and Noise Reduction / 去重与降噪

English:
Deduplication is based on stable finding fingerprints. A finding fingerprint includes the task scope plus normalized rule identity, file, line, and category. This prevents repeated rule passes or overlapping sources from emitting duplicate issues for the same file, line, and problem type.

Noise reduction is handled in two ways. First, low-confidence findings are not promoted into high-confidence findings; they are routed to warnings or human-review sections. Second, ambiguous high-risk issues can produce a `needs_human_review` conclusion, making uncertainty visible without hiding potentially important evidence.

The goal is not to suppress all uncertain information. The goal is to keep the main findings list actionable while still preserving lower-confidence evidence in clearly labeled report sections.

中文：
去重基于稳定 finding fingerprint。fingerprint 会包含 task scope、规范化后的 rule identity、文件、行号和类别。这样可以避免多轮规则扫描或多个来源对同一文件、同一行、同一类问题重复报出。

降噪主要通过两层机制实现。第一，低置信问题不会被提升为高置信 finding，而是进入 warnings 或 human-review 区域。第二，模糊但高风险的问题可以让最终结论变为 `needs_human_review`，让不确定性显式可见，同时不丢失重要证据。

目标不是压掉所有不确定信息，而是让主 findings 列表保持可执行，同时把低置信证据保存在清晰标注的报告区域中。

## 10. Security Boundary / 安全边界

English:
The security boundary is enforced across input handling, command execution, environment propagation, output capture, and artifact persistence.

Key controls include:

- Runtime separation: production-oriented execution uses `container` or `e2b`; `local` is disabled for untrusted input and requires an explicit trusted-input opt-in.
- Permission gate: planned commands must pass safety decisions before execution.
- Timeout control: sandbox commands are bounded and failures are recorded.
- Output cap: large stdout/stderr streams are truncated and marked.
- Environment allowlist: only selected variables such as Go proxy/toolchain/cache settings are propagated.
- Secret redaction: API keys, tokens, passwords, and similar sensitive values are masked before reports or store writes.
- Artifact control: reports and database/store files are written to the requested output directory, and generated artifacts are recorded.
- Failure isolation: sandbox failure does not crash the review pipeline; it becomes an auditable run record and can influence the final conclusion.

This boundary is especially important because code review agents operate on untrusted diffs. The design assumes reviewed code may contain malicious content or suspicious scripts, so execution is explicit, filtered, time-limited, and recorded.

中文：
安全边界覆盖输入处理、命令执行、环境变量传递、输出捕获和 artifact 持久化。

关键控制包括：

- Runtime 隔离：面向生产的执行使用 `container` 或 `e2b`；对不可信输入禁用 `local`，只有显式 trusted-input opt-in 才能启用。
- Permission gate：计划命令必须先通过安全决策才能执行。
- Timeout 控制：沙箱命令有执行时间边界，失败会被记录。
- 输出限制：过大的 stdout/stderr 会被截断并标记。
- 环境变量白名单：只传递 Go proxy、toolchain、cache 等被允许的变量。
- 敏感信息脱敏：API key、token、password 等敏感值在写报告或落库前被 mask。
- Artifact 控制：报告和数据库/存储文件写入指定 out-dir，并记录生成产物。
- 失败隔离：沙箱失败不会导致审查流程崩溃，而是成为可审计的 run record，并影响最终结论。

这个边界尤其重要，因为 code review agent 面对的是不可信 diff。设计上假设被审查代码可能包含恶意内容或可疑脚本，因此所有执行都必须显式、可过滤、有时间限制并且可追踪。

## 11. Report Artifacts / 报告产物

English:
Each run writes two task-specific primary reports:

- `review_report_<task-id>.json`: structured report for automated checks, CI integration, and database comparison.
- `review_report_<task-id>.md`: human-readable report with summaries, severity distribution, review findings, human-review items, governance decisions, sandbox summaries, metrics, and fix recommendations.

The output directory also contains the durable review store, currently `review_agent.db`. The store is intended to support task-level queries and historical audit. A review can be accepted only when the reports and store agree on task status, sandbox runs, permission decisions, findings, metrics, artifacts, and final conclusion.

中文：
每次运行会写出两个主要报告：

- `review_report_<task-id>.json`：结构化报告，供自动化检查、CI 集成和数据库比对使用。
- `review_report_<task-id>.md`：面向人的报告，包含摘要、severity 分布、findings、人工复核项、治理决策、沙箱摘要、监控指标和修复建议。

输出目录还会包含持久化 review store，目前为 `review_agent.db`。该 store 用于支持按 task 查询和历史审计。只有当报告和 store 中的 task 状态、sandbox run、permission decision、finding、metrics、artifact 和最终结论一致时，才算完整可验收。

## 12. Acceptance and Test Strategy / 验收与测试策略

English:
The deterministic acceptance path is:

```powershell
cd E:\trpc-agent-go\examples\code_review_agent
go test -count=1 ./...
go run . -fixture-dir testdata\fixtures -out-dir .\out -runtime fake
go run . -diff-file testdata\fixtures\security_secret.diff -out-dir .\out-diff -runtime fake
```

The fake runtime validates diff parsing, rule-only review, sandbox-run recording, storage, redaction, report generation, and deterministic fixtures without requiring a real model API key. Container or E2B validation should be used when checking production sandbox behavior:

```powershell
go run . -fixture-dir testdata\fixtures -out-dir .\out-real -model $env:MODEL -runtime container
```

For a passing container sandbox run, `sandbox_runs` should show `runtime=container`, command status should be recorded, duration should be greater than zero for real execution, and `metrics.sandbox_duration_ms` should reflect the sum of sandbox execution time. A review may still conclude `needs_human_review` when the fixture intentionally contains high-risk or ambiguous findings; that conclusion is expected and different from infrastructure failure.

中文：
确定性验收路径如下：

```powershell
cd E:\trpc-agent-go\examples\code_review_agent
go test -count=1 ./...
go run . -fixture-dir testdata\fixtures -out-dir .\out -runtime fake
go run . -diff-file testdata\fixtures\security_secret.diff -out-dir .\out-diff -runtime fake
```

fake runtime 可以在没有真实模型 API Key 的情况下验证 diff 解析、规则审查、sandbox run 记录、落库、脱敏、报告生成和确定性 fixture。检查生产沙箱行为时，应使用 container 或 E2B：

```powershell
go run . -fixture-dir testdata\fixtures -out-dir .\out-real -model $env:MODEL -runtime container
```

如果 container 沙箱真正运行成功，`sandbox_runs` 应显示 `runtime=container`，命令状态应完整记录，真实执行的 `duration_ms` 应大于 0，`metrics.sandbox_duration_ms` 应体现沙箱执行耗时之和。当 fixture 本身包含高风险或模糊 finding 时，最终结论仍可能是 `needs_human_review`；这是预期的审查结论，不等同于基础设施失败。
