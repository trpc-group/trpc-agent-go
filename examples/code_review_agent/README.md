# Code Review Agent

基于 Skills + 沙箱 + 数据库存储的自动代码评审 Agent，面向 Go 项目代码评审场景。

## 概述

本工具读取 git diff，通过确定性规则引擎识别代码风险（SQL 注入、goroutine
泄漏、资源未关闭等），可选在沙箱中运行 go test/go vet，将发现的问题结构化
输出并写入 SQLite 数据库，同时生成 JSON 和 Markdown 报告。

支持 dry-run 模式：仅运行规则引擎，无需 API Key 即可测试完整链路。

## 快速开始

```bash
# 进入示例目录
cd examples/code_review_agent

# 下载依赖（需要 CGO 用于 SQLite）
CGO_ENABLED=1 go mod tidy

# dry-run 模式运行（推荐，无需 API Key）
go run . --diff-file fixtures/02_security.diff --dry-run

# 指定输出目录和数据库路径
go run . --diff-file fixtures/03_goroutine_leak.diff \
    --db-path ./reviews.db \
    --output-dir ./reports

# 运行全部测试
CGO_ENABLED=1 go test ./...
```

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--diff-file` | "" | unified diff 文件；与 `--repo-path` 至少提供一个 |
| `--repo-path` | "" | Git 工作区输入及沙箱检查使用的仓库路径 |
| `--files` | "" | 逗号分隔的仓库相对路径过滤器 |
| `--executor` | container | 沙箱执行器：container\|local（local 仅供开发） |
| `--dockerfile` | 自动探测 | 自定义容器 Dockerfile 所在目录 |
| `--dry-run` | true | 仅运行规则，跳过沙箱执行 |
| `--db-path` | review.db | SQLite 数据库路径 |
| `--output-dir` | . | 报告输出目录 |
| `--timeout` | 30 | 沙箱超时秒数 |

## 测试样例

`fixtures/` 目录包含 8 条 diff 样本：

| 文件 | 测试场景 |
|------|----------|
| 01_clean.diff | 无问题代码 |
| 02_security.diff | SQL 注入 + 硬编码密钥 |
| 03_goroutine_leak.diff | goroutine/context 泄漏 |
| 04_resource_unclosed.diff | 资源未关闭（os.Open、HTTP body） |
| 05_db_lifecycle.diff | 数据库连接生命周期问题 |
| 06_test_missing.diff | 新增导出函数无测试 |
| 07_duplicate.diff | 重复 finding 去重测试 |
| 08_sensitive_info.diff | 敏感信息脱敏测试 |

## 规则覆盖

| 类别 | 规则 ID | 严重级别 | 说明 |
|------|---------|----------|------|
| 安全 | SQL_INJECTION | Critical | SQL 字符串拼接 |
| 安全 | CMD_INJECTION | Critical/High | 命令注入 |
| 安全 | HARDCODED_SECRET | Critical/High | 硬编码密钥 |
| goroutine | GOROUTINE_LEAK | High/Medium | goroutine 无 context |
| goroutine | CONTEXT_NOT_PASSED | Medium/Low | context 未传递 |
| 资源 | UNCLOSED_RESOURCE | High | 资源未关闭 |
| 资源 | HTTP_BODY_NOT_CLOSED | High/Medium | HTTP body 未关闭 |
| 错误处理 | IGNORED_ERROR | High/Medium | 忽略 error 返回值 |
| 错误处理 | PANIC_IN_GOROUTINE | Critical/Medium | goroutine 中 panic |
| DB | DB_CONNECTION_LEAK | High/Medium | DB 连接泄漏 |
| DB | MISSING_TX_ROLLBACK | High | 事务无 Rollback |
| 敏感信息 | SENSITIVE_INFO_IN_LOG | High | 日志中的敏感信息 |
| 测试 | TEST_MISSING | Low | 导出函数无测试 |

## 数据库 Schema

| 表名 | 说明 |
|------|------|
| review_tasks | 评审任务元数据和状态 |
| sandbox_runs | 沙箱执行记录（含 permission decision） |
| permission_decisions | 权限决策审计记录 |
| findings | 结构化发现结果 |
| artifacts | 产物引用 |
| review_reports | JSON 和 Markdown 报告 |
| monitoring_summary | 监控指标摘要 |

## 输出示例

运行后会生成两个文件：

- `review_report.json`：结构化 JSON 报告，包含所有 findings、warnings、
  permission decisions、sandbox runs 和 monitoring 指标
- `review_report.md`：人类可读的 Markdown 报告，包含 findings 摘要、
  严重级别统计、人工复核项、治理拦截摘要、监控指标和修复建议

## 安全特性

- 高风险命令（rm、curl、wget）被 PermissionPolicy 拒绝
- 需审查命令（docker、git push）进入 needs_human_review
- 默认使用无网络、非特权的 `codeexecutor/container` 隔离 workspace；本地执行仅为显式 fallback
- 沙箱超时 30 秒、输出限制 1MB，并限制 CPU、内存、PID 和 workspace 大小
- 环境变量白名单（仅 PATH、HOME、GOROOT、GOPATH）
- 敏感信息自动脱敏（API Key、token、password 等）
