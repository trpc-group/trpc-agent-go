---
name: code_review
description: >
  Go 代码自动评审 Skill。分析 Go diff 的安全性、并发安全、
  资源管理、错误处理、测试覆盖等问题，在沙箱中运行 go vet
  等静态分析工具，生成结构化审查结果。
---

# Code Review Skill

## 目的

本 Skill 用于对 Go 语言代码变更（git diff / PR patch）进行自动化评审。
评审覆盖以下维度：

1. **安全风险** — 硬编码密钥、SQL 注入、命令注入、敏感信息泄漏
2. **并发安全** — goroutine 泄漏、context 未传递/未检查、竞态条件
3. **资源管理** — 文件/连接未关闭、HTTP Body 泄漏
4. **错误处理** — 未检查 error、错误信息缺少上下文
5. **测试覆盖** — 新文件缺少对应 _test.go
6. **数据库生命周期** — 连接未 Ping、未 Close
7. **敏感信息** — 信用卡号、私钥、API Key

## 使用方式

### 1. 静态规则扫描

内置 18+ 条正则规则，覆盖 7 个类别。规则定义在 `rules/` 目录。

### 2. 沙箱执行

在隔离沙箱中运行以下工具：
- `go vet ./...` — Go 官方静态分析
- `gofmt -d .` — 格式检查

### 3. 敏感信息脱敏

自动检测并脱敏以下敏感信息：
- API Key (sk-..., github_pat_..., AKIA...)
- 密码 (password=)
- Token
- 私钥 (BEGIN PRIVATE KEY)
- 信用卡号

## 输出格式

### JSON (review_report.json)
```json
{
  "task_id": "cr-...",
  "status": "completed",
  "summary": {
    "total": 5,
    "critical": 1,
    "high": 2,
    "medium": 1,
    "low": 1,
    "warning": 0,
    "duplicates": 1
  },
  "findings": [...]
}
```

### Markdown (review_report.md)
按严重级别分组的可读报告，包含证据、建议和监控摘要。

## 安全说明

- 所有沙箱命令执行前经过 `tool/safety.Scanner` 安全门禁检查
- 高危命令（sudo, rm -rf, sh -c 等）自动拒绝
- 沙箱配置 30 秒超时、1MB 输出限制
- 报告和数据库不含明文敏感信息
