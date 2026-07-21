# code-review Skill

这个 Skill 将确定性的 Go 代码评审工作流封装为 tRPC-Agent 风格的可复用能力。它可以通过 `skill load` 加载，并通过 `skill run` 在隔离 workspace 中执行。

## 目标

评审 unified diff、PR patch 或本地 git 工作区，面向 Go 项目输出结构化 findings。该 Skill 关注真实工程 CR 风险，而不是泛泛的文本点评。

## 输入

- `--diff-file`：unified diff 或 PR patch。
- `--repo-path`：git 工作区。Agent 会读取 `git diff --no-ext-diff -- .`。
- `--files`：没有 diff 时使用的逗号分隔文件路径列表。
- `--fixture`：`testdata/fixtures` 下的本地样例名。

## 工作流

1. 解析 unified diff hunk，收集变更 Go 文件、候选行号、上下文和 package 名。
2. 执行 `rules/go-code-review-rules.md` 中定义的确定性规则。
3. 通过 codeexec wrapper 请求沙箱检查：`go test ./...`、`go vet ./...`，以及可选的 `staticcheck ./...`。
4. 所有高风险命令执行前必须经过 PermissionPolicy。
5. 在写入报告或持久化记录之前执行敏感信息脱敏。
6. 按 `(file, line, category)` 对 findings 去重，并把低置信问题降级到 warnings 或 human review。
7. 持久化 task、sandbox run、permission decision、finding、artifact、monitoring summary 和 final report。

## 安全契约

生产 runtime 应使用 `container`、`cube` 或 `e2b`。本地执行只作为开发 fallback。所有沙箱执行都必须设置超时、输出大小限制、环境变量白名单；可用容器时应禁用网络，并限制 artifact 产物范围和大小。

## 脚本

- `scripts/run_go_checks.sh`：面向 container 或 E2B workspace 的 Go 检查命令集合。
- `scripts/custom_rules_hint.sh`：组织自定义规则 hook 示例。

## 输出

- `review_report.json`：机器可读 findings 和审计数据。
- `review_report.md`：人工可读 CR 摘要。
- 持久化存储：默认 JSON 实现，并提供 `schema.sql` 作为 SQLite 兼容 SQL 存储结构。
