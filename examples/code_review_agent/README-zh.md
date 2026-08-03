## Code Review Agent 示例

该示例实现了一个确定性的自动代码评审 Agent 原型。它会加载
`code-review` Skill，解析 unified diff 或仓库变更，通过
PermissionPolicy 风格的包装器对沙箱命令做门控，记录沙箱与治理审计数据，
写入 SQLite 状态，并输出 JSON/Markdown 报告。

请在 `examples` 模块下运行：

```bash
go test ./code_review_agent/...
go test ./code_review_agent/... -coverprofile=./code_review_agent.cover.out
go run ./code_review_agent --fixture clean --runtime=fake --dry-run
go run ./code_review_agent --fixture all --runtime=fake --dry-run
go run ./code_review_agent --eval-labels code_review_agent/testdata/eval_labels.json --runtime=fake --dry-run
```

CLI 参数：

- `--diff-file`：unified diff 或 PR patch 文件路径。
- `--repo-path`：Git 仓库路径；Agent 会使用 `git ls-files -z` 做路径校验，并使用 `git diff --no-ext-diff` 作为输入。
- `--file-list`：逗号分隔的变更文件列表，用于解析和门控测试。
- `--fixture`：fixture 名称，或使用 `all` 运行全部 fixture。
- `--fixture-dir`：fixture 目录，默认使用示例内置测试数据目录。
- `--out-dir`：报告和数据库输出目录，默认 `code_review_agent_out`。
- `--db-path`：SQLite 数据库路径，默认 `<out-dir>/review_agent.db`。
- `--runtime`：`container`、`e2b`、`fake` 或 `local`，默认 `container`。
- `--allow-trusted-local`：只有显式设置后，`--runtime=local` 才允许执行。
- `--dry-run`：只记录允许执行的沙箱命令，不真正运行。
- `--sandbox-timeout`：单条命令的硬超时，默认 `30s`。
- `--output-limit`：沙箱输出字节上限，默认 `10485760`。
- `--max-diff-lines` 与 `--max-files`：大小门控阈值；超限时会跳过沙箱执行并请求人工复核。
- `--skills-root`：可选的 Skill 根目录，用于加载 `code-review`。
- `--eval-labels`：带标签的 fixture 清单，用于计算可度量的召回率、误报率和脱敏率。

`container` 模式是面向生产形态的默认运行方式，会构建
`code_review_agent/sandbox/Dockerfile`。除非显式设置
`--allow-trusted-local`，否则本地执行默认被阻止。`fake` runtime
主要用于 CI 和 fixture 验证，适用于 Docker、E2B 或模型凭证不可用的场景。

默认输出文件：

- `code_review_agent_out/review_report.json`
- `code_review_agent_out/review_report.md`
- `code_review_agent_out/review_agent.db`
- 使用 `--eval-labels` 时输出 `code_review_agent_out/eval_report.json`
- 使用 `--eval-labels` 时输出 `code_review_agent_out/eval_report.md`

SQLite 数据库会记录 task、input、sandbox run、permission decision、
finding、artifact、report、metrics 和 diff-hash alias。测试覆盖了这些
记录按 task id 的加载路径。

所有指标、检出结果和持久化结论都必须通过测试或 fixture 运行证明。
`--eval-labels` 接收一个包含 fixture 标签和 secret 探针的 JSON 清单，并输出
实测的 recall、high-risk recall、false-positive rate 和 redaction rate。
如果没有使用隐藏标签执行明确的测试或评测命令，不应声明隐藏样本上的 precision、
recall、false-positive rate 或 secret-redaction rate。
