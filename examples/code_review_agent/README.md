# Code Review Agent

自动化的 Go 代码评审工具。输入 git diff / PR patch，通过静态规则扫描、
沙箱执行和敏感信息检测，生成结构化审查报告。

## 快速开始

```bash
# 编译
go build .

# Dry-run 模式（无需 API Key）
./code_review_agent --diff-file=testdata/security_issue/diff.patch --dry-run

# 连接真实仓库运行 go vet
./code_review_agent --diff-file=changes.patch --repo-path=/path/to/repo --db-path=review.db

# 使用自定义数据库和输出目录
./code_review_agent --diff-file=changes.patch --db-path=/tmp/cr.db --output-dir=./reports
```

## 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--diff-file` | diff/patch 文件路径 | - |
| `--diff` | diff 文本内容 | - |
| `--repo-path` | 仓库路径（用于 go vet） | - |
| `--db-path` | SQLite 数据库路径 | review.db |
| `--output-dir` | 报告输出目录 | . |
| `--dry-run` | 跳过沙箱真执行 | false |
| `--fake-model` | 用确定性 fake model 做离线语义审查（无网络，耗时 < 2min） | false |
| `--pr-title` | PR 标题（报告中使用） | - |
| `--author` | 作者 | - |
| `--branch` | 分支名 | - |

## 审查维度

| 类别 | 规则数 | 说明 |
|------|--------|------|
| security | 3 | 硬编码密钥、SQL 注入、命令注入 |
| goroutine_context | 2 | goroutine 泄漏、context 未检查 |
| resource_cleanup | 2 | 文件未关闭、HTTP Body 泄漏 |
| error_handling | 2 | 未检查 error、错误缺上下文 |
| test_coverage | 1 | 新文件缺少测试 |
| db_lifecycle | 2 | 连接未 Ping、未 Close |
| sensitive_info | 2 | 信用卡号、私钥 |

## 输出

- `review_report.json` — 机器可读的完整审查结果
- `review_report.md` — 按严重级别分组的人可读报告
- SQLite 数据库 — 持久化 task、findings、sandbox_runs、permission_decisions、model_calls、monitoring_summary；报告路径（report_json_path / report_md_path）与监控异常分布（沙箱超时 / 失败计数）随 task 落库，可按 task id 查询

## 测试与评测

```bash
# 单元测试 + CLI 二进制 e2e + 管线级测试
go test ./... -v

# 检出率 / 误报率评测（验收标准 2：recall >= 0.8, fp <= 0.15）
go test ./benchmark/ -v
go run ./benchmark --json=result.json

# 端到端测试（dry-run）
go run . --diff-file=testdata/security_issue/diff.patch --dry-run --output-dir=/tmp
cat /tmp/review_report.json | python3 -m json.tool
```

## 目录结构

```
examples/code_review_agent/
├── main.go              # CLI 入口（flag 解析 -> internal.RunReview）
├── cli_test.go          # CLI 二进制 e2e 测试
├── internal/            # 核心实现
│   ├── pipeline.go      # 完整审查管线（ParseDiff -> Scan -> Gate/Sandbox -> Dedup -> Mask -> DB -> Report）
│   ├── diff.go          # Unified diff 解析
│   ├── finding.go       # Finding 模型 + 去重 + 置信度降噪
│   ├── scanner.go       # 规则扫描器 + 文件级二次验证
│   ├── storage.go       # SQLite 存储 + schema 迁移
│   ├── sandbox.go       # 沙箱执行 + 安全门禁 + env 白名单
│   ├── security.go      # 敏感信息脱敏（注册表驱动，检出/脱敏一致）
│   ├── model.go         # fake model 离线语义摘要
│   └── reporter.go      # JSON + Markdown 报告
├── benchmark/           # 检出率 / 误报率评测（12 正 + 12 反样本）
│   ├── eval.go
│   ├── main.go
│   └── samples/         # 24 个 diff 样本 + expect.json
├── skills/code-review/  # CR Skill 定义
├── testdata/            # 9 个测试 fixture
├── schema.sql           # 数据库 schema
├── design.md            # 方案设计说明
└── README.md            # 本文件
```
