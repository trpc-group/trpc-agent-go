---
name: code-review
description: 自动代码审查技能，基于 Token 感知的语义分析引擎
version: 1.0.0
author: code-review-agent
---

# Code Review Skill

自动代码审查技能，输入 git diff 或 PR patch，输出结构化审查报告。

## 功能特性

- **Token 感知分析**：基于 `go/scanner` 词法分析，不依赖正则表达式
- **6 条内置规则**：覆盖安全、资源、错误处理、测试 4 大类
- **YAML 规则 DSL**：用户可自定义规则，不需要写 Go 代码
- **风险评分**：0-100 分量化评分，带多维度 breakdown
- **沙箱执行**：在隔离环境中执行 go vet / go test
- **安全过滤**：7 层命令安全检查 + 审计日志

## 使用方法

### 命令行

```bash
# 审查 diff 文件
code-review-agent --diff-file changes.diff

# 审查 git 仓库变更
code-review-agent --repo-path /path/to/repo

# dry-run 模式（不写数据库）
code-review-agent --diff-file changes.diff --dry-run

# 使用自定义规则
code-review-agent --diff-file changes.diff --rules-dir ./rules/custom

# 详细输出
code-review-agent --diff-file changes.diff --verbose
```

### 程序调用

```go
import (
    "code-review-agent/diff"
    "code-review-agent/rules"
    "code-review-agent/findings"
)

// 1. 解析 diff
files, _ := diff.ReadFromFile("changes.diff")

// 2. 创建规则引擎并注册规则
engine := rules.NewEngine()
engine.Register(rules.NewTokenSecretRule())
engine.Register(rules.NewTokenLeakRule())
engine.Register(rules.NewTokenGoroutineRule())
engine.Register(rules.NewTokenResourceRule())
engine.Register(rules.NewTokenErrorRule())
engine.Register(rules.NewTokenMissingTestRule())

// 3. 执行审查
allFindings, _ := engine.Run(files)

// 4. 去重
result := findings.Deduplicate(allFindings)

// 5. 输出
fmt.Printf("高置信度发现: %d 个\n", len(result.Findings))
fmt.Printf("低置信度警告: %d 个\n", len(result.Warnings))
```

## 内置规则

| 规则 ID | 名称 | 检测内容 | 严重级别 |
|---------|------|---------|---------|
| SEC-AST-001 | Token 感知的密钥检测 | 硬编码密码、API Key | high |
| SEC-AST-002 | Token 感知的敏感信息泄漏 | AWS Key、GitHub Token、私钥、JWT | high |
| GOR-AST-001 | Token 感知的 goroutine 泄漏 | 无退出机制的 goroutine | high |
| RES-AST-001 | Token 感知的资源泄漏 | 未关闭的文件、连接、HTTP 响应 | medium |
| ERR-AST-001 | Token 感知的错误处理 | 忽略 error、panic、log.Fatal | medium |
| TST-AST-001 | Token 感知的测试缺失 | 新增导出函数无测试 | low |

## YAML 自定义规则

在 `rules/custom/` 目录下创建 `.yaml` 文件：

```yaml
rules:
  - id: MY-001
    name: "检测硬编码端口"
    severity: medium
    category: security
    match:
      token_facts:
        - kind: identifier
          value_contains: ["port"]
        - kind: string_literal
          value_pattern: "^\\d{4,5}$"
    exclude:
      line_contains: ["localhost", "127.0.0.1"]
    message: "疑似硬编码端口号"
    recommendation: "使用配置文件管理端口"
    confidence: 0.75
```

### 匹配条件

| 条件 | 说明 |
|------|------|
| `token_facts[].kind` | token 类型：identifier, string_literal, assignment, defer, go, return, if, for |
| `token_facts[].value_exact` | 精确匹配值 |
| `token_facts[].value_contains` | 包含任一 |
| `token_facts[].value_pattern` | 正则匹配 |
| `line_contains` | 行内容包含 |
| `line_not_contains` | 行内容不包含 |
| `file_extension` | 文件扩展名过滤 |

### 排除条件

| 条件 | 说明 |
|------|------|
| `exclude.line_contains` | 行包含则排除 |
| `exclude.line_starts_with` | 行以指定前缀开头则排除 |

## 沙箱执行

支持三种后端：

| 后端 | 说明 | 安全性 |
|------|------|--------|
| `local` | 本地执行（开发 fallback） | 低 |
| `container` | Docker 容器执行（生产方案） | 高 |
| `e2b` | E2B 云沙箱 | 高 |

沙箱执行的安全边界：
- 超时控制（默认 30 秒）
- 输出大小限制（默认 1MB）
- 环境变量白名单
- 敏感信息自动脱敏

## 安全过滤器

命令执行前经过 7 层安全检查：

1. 空命令 → deny
2. 黑名单匹配 → deny
3. 危险路径访问 → deny
4. Shell 注入检查 → ask
5. 网络外连检查 → deny/allow
6. 白名单匹配 → allow
7. 默认策略

所有安全决策记录审计日志（JSONL 格式）。

## 风险评分

评分维度（0-100 分）：

| 维度 | 权重 | 说明 |
|------|------|------|
| 安全问题 | 30% | 密钥泄漏、注入风险 |
| 敏感信息 | 15% | AWS Key、Token 泄漏 |
| 资源泄漏 | 20% | goroutine、文件、连接 |
| 错误处理 | 15% | 忽略 error、panic |
| 测试覆盖 | 15% | 缺少测试 |
| 并发问题 | 5% | 数据竞争 |

等级：A（0-20）→ B（20-40）→ C（40-60）→ D（60-80）→ F（80-100）

## 输出格式

### JSON 报告

```json
{
  "task_id": "task-20260729-120000",
  "summary": {
    "total_findings": 3,
    "total_warnings": 1,
    "by_severity": {"high": 2, "medium": 1}
  },
  "findings": [...],
  "warnings": [...],
  "monitor": {
    "total_duration": "1.2s",
    "risk_score": 45,
    "risk_grade": "C"
  }
}
```

### Markdown 报告

包含以下章节：
1. 基本信息
2. 审查摘要
3. 严重级别分布
4. 问题分类分布
5. 审查发现详情
6. 需人工复核项
7. 治理拦截摘要
8. 沙箱执行摘要
9. 监控指标
10. 修复建议汇总
