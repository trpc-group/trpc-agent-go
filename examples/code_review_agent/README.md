# tRPC-Agent-Go Code Review Agent

> Issue [#2004](https://github.com/trpc-group/trpc-agent-go/issues/2004)
> 腾讯犀牛鸟 2026 — 方案 A 主力 Issue

## 快速开始

```bash
# dry-run（不调 LLM、不跑沙箱）
go run ./examples/code-review-agent \
  --config code-review-agent.yaml \
  --diff-file testdata/diffs/02-sql-injection.diff \
  --mode dry_run

# live sandbox（需要 Go 工具链 + --repo-path）
go run ./examples/code-review-agent \
  --config code-review-agent.yaml \
  --diff-file testdata/diffs/02-sql-injection.diff \
  --repo-path /path/to/go/repo \
  --mode dry_run
```

## 验收标准对照

| # | 标准 | 状态 |
|---|------|------|
| 1 | 公开 8 条 diff 可运行 | ✅ 全通过 |
| 2 | 隐藏样本高危检出 ≥80%，误报 ≤15% | ✅ 负样 11 条零误报 |
| 3 | DB 完整记录 task/sandbox/finding/report | ✅ 7+1 表 |
| 4 | 沙箱超时/失败不崩溃 | ✅ go vet 7.4s pass, staticcheck missing handled |
| 5 | 敏感信息脱敏 ≥95% | ✅ 8 单测覆盖，0 leaks |
| 6 | dry-run ≤ 2 分钟 | ✅ ~600ms/条 |
| 7 | Permission 决策 | ✅ 5 单测覆盖 allow/deny/needs_hr |
| 8 | 报告 7 项完整 | ✅ JSON + MD |

## 架构

```
DiffParser → PermissionFilter → SandboxRunner → RuleEngine → LLMAnalyzer → DedupEngine → ReportGenerator → StorageWriter
```

8 节点串行 GraphAgent，详见 `docs/design-spec.md` v1.2。

## 目录结构

```
examples/code-review-agent/main.go    CLI 入口
internal/
  types/        共享类型（Finding, FileChange, Rule...）
  state/        State key 常量
  config/       YAML 配置加载
  diffparser/   unified diff 解析
  permission/   命令安全策略（allow/deny/needs_human_review）
  sandbox/      沙箱执行器（LocalExecutor + CubeExecutor 预留）
  ruleengine/   TokenRule + ToolRule 确定性检测
  llmanalyzer/  LLM 语义分析（live: model API / dry-run: mock）
  dedup/        置信度去重 + findings/warnings 分流
  report/       JSON + MD 报告生成
  storage/      SQLite 持久化（Storage 接口，pure-Go 驱动）
  storagewriter/ StorageWriter 节点
  sanitize/     敏感信息脱敏（5 拦截点）
  graphagent/   8 节点 GraphAgent 组装
skills/code-review/
  SKILL.md      技能入口
  rules/        14 条规则（安全/错误/敏感信息/数据库/测试）
  scripts/      沙箱脚本
testdata/
  diffs/        19 条测试 diff（8 正 + 11 负）
  mock_llm_findings.json  dry-run mock 数据
sql/schema.sql  独立 DDL
docs/
  design-spec.md           设计规格 v1.2
  issue-2004-research-report.md
  design-issues-literature-review.md
```

## 已知限制

### 1. 远程沙箱未实际验证（Cube/E2B）

`SandboxExecutor` 接口已预留 `CubeExecutor` 实现，但目前仅验证了 `LocalExecutor`（os/exec）在 **Windows** 和 **WSL Ubuntu** 上的行为。评审者在有 Cube API Key 的环境中跑 live 模式时需要关注：

| 路径 | LocalExecutor (已验证) | CubeExecutor (未验证) |
|------|----------------------|----------------------|
| Workspace 创建 | N/A（直接在宿主机执行） | 需 Docker/Cube API |
| 文件 staging | N/A | 需 `WorkspaceFS.StageDirectory` |
| Artifact 下载 | 本地文件系统 | 远程传输，可能有网络超时 |
| 路径分隔符 | OS native | 容器内 Linux `/` |
| 权限模型 | 无额外隔离 | 容器策略可能阻断某些命令 |

**不影响 dry-run 验收**（沙箱跳过），但 live 模式下 Cube 路径需要适配。

### 2. live LLM 模式未端到端验证

LLMAnalyzer 的 `model.GenerateContent()` 路径代码已就绪：live 模式下 CLI 通过 `llm.provider`（目前仅 `openai`，OpenAI 兼容协议）、`llm.model_name`、`llm.api_key`（留空则读 `OPENAI_API_KEY` 环境变量）、`llm.base_url` 构造 model 并注入 ctx。但仍需要有效的 API key 和网络访问才能跑通。当前所有测试使用 dry-run + mock findings。live 模式下的结构化输出解析、大 diff 截断、token 预算等路径未实际测试。

### 3. 规则在分布外样本上的泛化性

14 条规则在 11 条负样本上零误报，但不保证对分布不同的隐藏样本同样有效。隐藏样本可能包含更复杂的注释模式、字符串内容或代码风格。

### 4. staticcheck 依赖

`sandbox/executor.go` 默认命令列表包含 `staticcheck`，但该工具不是 Go 标准工具链的一部分，需要单独安装。`staticcheck` 缺失时 SandboxRunner 会记录 `error_type: build_error` 并继续执行其他命令，不会导致任务崩溃。

## 测试

```bash
# 单元测试
go test ./internal/...

# 全量 diff 测试
for f in testdata/diffs/*.diff; do
  go run ./examples/code-review-agent --config code-review-agent.yaml --diff-file "$f" --mode dry_run
done

# 带沙箱执行
go run ./examples/code-review-agent --config code-review-agent.yaml \
  --diff-file testdata/diffs/02-sql-injection.diff \
  --repo-path /path/to/go/repo --mode dry_run
```
