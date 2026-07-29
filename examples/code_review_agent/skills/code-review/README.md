# Code Review Skill

基于 tRPC-Agent-Go 框架的自动代码评审技能。

## 快速开始

```bash
# dry-run 模式（不调用 LLM，使用 mock findings）
go run ./cmd/code-review-agent \
  --config code-review-agent.yaml \
  --diff-file testdata/diffs/02-sql-injection.diff \
  --mode dry_run

# live 模式（调用 LLM API）
go run ./cmd/code-review-agent \
  --config code-review-agent.yaml \
  --diff-file testdata/diffs/02-sql-injection.diff \
  --mode live
```

## 目录结构

```
skills/code-review/
├── SKILL.md           # 技能入口定义
├── rules/             # 规则文档 (Markdown)
│   ├── security.md    # 安全规则
│   ├── errors.md      # 错误处理规则
│   ├── sensitive.md   # 敏感信息规则
│   ├── database.md    # 数据库规则
│   └── testing.md     # 测试规则
├── scripts/           # 沙箱执行脚本
│   ├── run_govet.sh
│   ├── run_staticcheck.sh
│   ├── run_tests.sh
│   └── parse_diff.sh
└── README.md
```

## 规则覆盖

| 类别 | 规则数 | 检测方式 |
|------|-------|---------|
| 安全风险 | 3 (SEC-001 ~ SEC-003) | TokenRule 正则 |
| 错误处理 | 3 (ERR-001 ~ ERR-003) | TokenRule 正则 |
| 敏感信息 | 3 (SEN-001 ~ SEN-003) | TokenRule 正则 |
| 数据库 | 3 (DB-001 ~ DB-003) | TokenRule 正则 |
| 测试 | 2 (TEST-001 ~ TEST-002) | TokenRule + 文件检查 |

## 沙箱命令

- `go vet ./...` — Go 官方静态分析
- `staticcheck ./...` — 第三方静态分析
- `go test ./...` — 测试执行 + 覆盖率
- `go build ./...` — 编译检查
