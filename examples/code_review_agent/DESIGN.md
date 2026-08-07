## 方案设计说明

### 1. 整体架构

代码评审 Agent 采用**确定性规则引擎优先 + 沙箱隔离执行**的架构。核心思路是将整个
评审流水线分解为独立可测试的模块：diff 解析、规则匹配、去重、脱敏、沙箱执行、存储、
治理决策和报告生成。每个模块职责单一，组合后形成完整评审链路。

### 2. Skill 设计

`code-review` Skill 包含 SKILL.md（技能定义）、rules.json（可配置规则集）、
docs/rules.md（规则详细文档）和 scripts/checkrunner（沙箱内执行的 Go 检查二进制）。
Skill 通过 FSRepository 加载，Agent 在运行时通过 skill_load 获取规则定义，
通过 skill_run 在沙箱中执行检查命令。

checkrunner 是一个独立编译的 Go 二进制，运行在 Docker 容器内。它通过 setgid/setuid
降权到 UID 65532 后才执行受检代码（go vet / go test），并返回结构化 JSON 结果
（exit_code、stdout、stderr、timed_out、duration_ms）。输出截断 1MB，超时先
SIGTERM 后 SIGKILL。

### 3. 沙箱隔离策略

沙箱默认使用 container runtime（Docker），通过两阶段 Dockerfile 构建：Stage 1
用 golang:1.22-alpine 编译 checkrunner，Stage 2 用 alpine:3.19 安装 Go toolchain
并复用编译产物。镜像中 USER 0:0 配合 CAP_SETUID/SETGID 实现运行时降权。
RuntimeContainer 模式通过 `docker run cr-sandbox:latest -mode vet|test` 执行，
executor 解析 checkrunner 返回的结构化 JSON（exit_code/stdout/stderr/timed_out），
Docker 不可用时不静默 fallback 到 host，而是记录错误继续报告。

sandbox/modproxy.go 提供离线 Go module 代理，将 go mod download 的缓存目录注入
沙箱环境变量（GOMODCACHE、GOPROXY=off），避免沙箱内网络访问。

执行层支持超时控制（30s 默认）、输出截断（1MB 限制）、环境变量白名单。
local runtime 仅作为开发 fallback，需通过 `--sandbox local` 显式启用。
沙箱失败（超时、OOM、exit code != 0）不会导致整个评审任务崩溃，而是记录到
sandbox_runs 表并继续完成报告。

### 4. Filter/Permission 策略

采用 fail-closed 模型。所有沙箱命令在执行前必须经过 governance.Policy.Check()
决策。默认 allowlist 包含 `go` 和 `checkrunner`，denylist 包含 `rm`、`curl`、
`sh`、`sudo` 等高风险命令。Deny 决策不会进入沙箱执行，而是记录到
permission_decisions 表并在报告中标注。TestGovernanceDenyBlocksSandbox
验证了 deny 后 SandboxRuns 为空且 PermissionDecisions 完整记录。

### 5. 数据库 Schema

四张表：review_tasks（任务元信息，含 severity 分布、耗时、异常）、
findings（结构化发现，含 confidence 和 needs_human_review）、
sandbox_runs（沙箱执行记录，含超时和 exit_code）、
permission_decisions（治理决策，含 action 和 reason）。
使用 SQLite（modernc.org/sqlite，纯 Go 无 CGO）作为默认实现，通过
ReviewStore 接口保留切换 SQL 后端的空间。接口包含 CreateTask、SaveFinding、
SaveSandboxRun、SavePermissionDecision、GetTask、GetFindings、
GetSandboxRuns、GetPermissionDecisions、Finalize。Finalize 接受 StatusCompleted、
StatusCompletedWithWarnings 和 StatusFailed 三种终态，拒绝中间态。所有敏感字段
（evidence、stdout/stderr）在落库前经过 redact 模块脱敏。

### 6. 去重和降噪

按 (file, line, category) 三元组去重，重复 findings 取最高 confidence 并合并
source/rule_id。置信度 < 0.5 的 findings 自动降级为 warning 级别并标记
needs_human_review，置信度 0.5-0.65 的标记 needs_human_review 但保持原严重级别。

### 7. 监控字段

每次评审记录：总耗时、沙箱耗时、工具调用次数、Permission 拦截次数、各 severity
分布、异常类型（sandbox_timeout、sandbox_error 等）。这些数据写入 review_tasks
表和 ReportSummary.Monitoring。

### 8. 敏感信息脱敏

redact 模块支持 10 种模式（OpenAI Key、GitHub Token、AWS Key、JWT、Password、
Connection String 等），落库前对所有 evidence/stdout/stderr 调用 redact.String()。
脱敏幂等：String(String(s)) == String(s)。ContainsSecret 使用精确 != 比较而非
EqualFold。TestReviewSecretRedactionDiff 验证报告和数据库中不含明文 secret。
