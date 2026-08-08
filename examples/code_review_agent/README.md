# Governed Go Code Review Agent

This example turns tRPC-Agent-Go's Skill, workspace execution, permission,
artifact, tracing, and persistence APIs into a runnable Go code-review
pipeline. It accepts a unified diff, a Git working tree, or a bundled fixture;
combines deterministic rules with governed checks and optional model review;
then writes canonical JSON, derived Markdown, and a queryable SQLite record.

## Architecture

```text
CLI input
  -> bounded diff parser and source snapshots
  -> deterministic Go rules
  -> isolated go test / go vet / optional staticcheck
  -> optional two-stage Skill + model assistance
  -> validate, redact, fingerprint, and deduplicate
  -> canonical JSON -> deterministic Markdown
  -> Artifact service + atomic SQLite completion
```

`review_report.json` is the semantic source of truth. Markdown is generated
only from the validated report. Model output is an untrusted candidate source:
the host owns task identity, changed-line validation, confidence routing,
fingerprints, persistence, and publication.

## Run without a model key

The production default is the network-disabled container runtime:

```sh
go run . \
  --fixture security.patch \
  --mode rule-only \
  --runtime container \
  --database review.db \
  --output-dir output
```

The image is built from `docker/Dockerfile`. Docker must be available. The
credential-free fake model exercises Skill loading, `workspace_exec`,
PermissionPolicy, structured output, and degradation behavior:

```sh
go run . --fixture security.patch --mode fake-model --runtime container
```

Local execution is a development fallback and is rejected unless explicitly
enabled:

```sh
go run . --repo-path /path/to/repository \
  --runtime local --allow-local --mode rule-only
```

Use `--runtime e2b` for E2B. Real model mode uses `OPENAI_API_KEY`, with
`--model` and optional `--base-url` for an OpenAI-compatible endpoint. API keys
are never accepted as CLI flags.

Exactly one of these inputs is required:

- `--diff-file change.patch`
- `--repo-path /path/to/git/worktree`
- `--fixture clean.patch`

Repository input reviews separate `HEAD -> index` and `index -> worktree`
layers. A standalone partial patch still runs added-line rules; AST rules run
only where a complete source snapshot is available.

Exit codes are `0` for a completed review without actionable findings, `1`
for an operational failure, `2` for invalid CLI usage, and `3` when the review
completed with actionable findings.

## Governance and isolation

The model sees only Skill read tools and a policy-mode `workspace_exec` tool.
The Filter records the visible surface; the PermissionPolicy evaluates final
arguments and records `allow`, `deny`, or `ask`. Only these exact commands are
eligible:

- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- immutable Skill scripts staged under a digest-addressed path

Dependency installation, environment overrides, shell composition, network
tools, arbitrary interpreters, interactive sessions, and unknown tools fail
closed. Production engines must advertise clean-environment support and no
network capability. Checks receive an include-only environment with
`GOPROXY=off`, `GOSUMDB=off`, and `GOTOOLCHAIN=local`, plus time, output, disk,
CPU, memory, and PID limits. Trusted scripts and review inputs are staged
read-only. Local fallback preserves governance but is not a production sandbox.

Run the opt-in container smoke test with:

```sh
TRPC_REVIEW_CONTAINER_TEST=1 go test -count=1 -run Container ./...
```

## Persistence and observability

SQLite stores tasks, normalized input summaries, sandbox runs, filter and
permission decisions, findings, evidence/publication artifacts, exact metrics,
and report metadata. `GetReview(taskID)` reconstructs the aggregate in one read
transaction. Completion writes findings, metrics, report references, and the
terminal task transition atomically.

OpenTelemetry spans cover the root review and lifecycle phases using only
low-cardinality attributes. Source text, diffs, terminal output, model messages,
credentials, and raw errors are excluded. The persisted metrics include total
and sandbox duration, tool calls, governance blocks, finding and severity
counts, human-review items, and classified errors.

## 方案设计说明（约 300–500 字）

该示例把代码评审拆成宿主控制的确定性流水线，而不是让模型直接生成最终评论。输入层以有界方式读取 unified diff、Git 工作区或内置样例，校验路径并定位新增行；规则层针对 Go 并发与 context 生命周期、资源关闭、错误处理、硬编码密钥、危险命令、事务生命周期和测试缺失产生候选项。沙箱层只执行固定的 `go test`、`go vet` 与可选 `staticcheck`，生产默认使用无网络容器或 E2B，本地执行必须显式开启。Skill 负责提供工作流说明、finding 契约和可信脚本，但仓库内容始终被视为数据，不能安装 Skill 或扩大权限。

治理采用 Filter 与 PermissionPolicy 两道边界：Filter 限制模型可见工具，PermissionPolicy 在参数确定后作最终决策；allow、deny、ask 均写入 SQLite，拒绝和待确认命令不会触发执行。模型评审分为证据收集和无工具结构化输出两阶段，输出必须映射到新增行，再经过闭合枚举校验、敏感信息检测、稳定指纹和跨来源去重。低置信结果进入人工复核，不与高置信 finding 混合。

`review_report.json` 是唯一语义真相，Markdown 由其确定性投影生成。SQLite 事务保存任务、输入摘要、沙箱记录、治理决策、finding、artifact、监控指标和最终报告引用；任务在执行前创建，因此崩溃后仍可查询。所有自由文本在持久化、报告和错误边界统一脱敏，身份字段若含疑似密钥则直接拒绝。OpenTelemetry 仅记录模式、阶段、工具、决策、结果和错误分类等低基数字段，不记录 diff、模型消息、stdout、stderr 或密钥。

## Tests

```sh
go test -race -count=1 ./...
go test -count=1 -run 'Acceptance|Holdout' ./...
go build ./...
go vet ./...
```

The public fixture suite contains clean, security, goroutine/context, resource,
transaction, error-handling, missing-test, duplicate, sandbox-failure, and
redaction scenarios. A separate holdout corpus enforces at least 80% high-risk
recall and at most 15% false positives. See `review_report.json` and
`review_report.md` for generated sample output.
