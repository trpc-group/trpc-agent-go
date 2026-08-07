# Go Code Review Agent

面向 Go 项目的可运行代码评审示例。它把 unified diff、文件列表或 Git
工作区变更统一为 diff，通过 `code-review` Skill 运行确定性规则；可选在
container 沙箱中执行 `go test`、`go vet`，并把 findings、权限决策、沙箱运行、
监控和最终报告写入 SQLite。容器保持 `NetworkMode=none`，因此 Go module / workspace
仓库必须提前 vendor 依赖（`vendor/modules.txt`）；否则沙箱会返回明确的
`unsupported` 能力审计，而不是尝试联网下载。`staticcheck` 仅支持
`local-fallback` runtime，container 启动时会直接拒绝该组合。

## Run

```bash
cd examples/code_review_agent
go run ./cmd/review-agent --diff-file testdata/fixtures/secret.diff \
  --runtime local-fallback --output-dir /tmp/cr-report
```

生产形态使用 container sandbox：

```bash
go run ./cmd/review-agent --repo-path /path/to/go-repo \
  --sandbox --runtime container --output-dir /tmp/cr-report
```

如果目标仓库依赖 Go modules，请先在仓库根目录准备 `vendor/modules.txt`
（例如由 `go mod vendor` 或 `go work vendor` 生成），否则 container runtime
只会记录 `unsupported` 的 Go 检查能力状态。

`review` 是正式模式；`--sandbox` 和 `--model-enabled` 可独立组合。默认 fake
model 不联网，真实 Provider 必须显式配置。`dry-run` 只验证 Skill 加载与审计链路。

## Included Fixtures

`testdata/fixtures/` 包含无问题、安全/敏感信息、goroutine/context、资源关闭、
数据库生命周期、测试缺失、重复 finding 和 sandbox 失败等样本。
`testdata/holdout/` 提供独立的期望矩阵与变体样例，用于检查规则没有只记住公开 fixtures。

## Outputs

每次评审输出 `review_report.json`、`review_report.md`、`review_report.zh.md`、
`review_diagnostics.json` 和默认 `review.db`。示例报告见
`review_report.json`、`review_report.md`、`review_report.zh.md`。

## Verify

```bash
go test ./...
go vet ./...
```

容器、网络、权限、超时、输出大小、环境变量白名单和 artifact 限制由执行层统一
约束；命令在进入沙箱前经过 PermissionPolicy，失败会记录为审计或人工复核项，
不会让整个评审任务崩溃。
