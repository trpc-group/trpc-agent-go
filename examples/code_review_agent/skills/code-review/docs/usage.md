# Code Review Skill 使用说明

## 概述

Code Review Skill 是一个用于自动审查 Go 代码变更的 Skill。它基于规则引擎和沙箱执行，
能够检测代码中的安全风险、资源泄漏、错误处理等问题。

## 快速开始

### 1. 加载 Skill

```bash
# 使用 skill load 命令加载
skill load code-review

# 或者通过环境变量指定 skills 目录
export SKILLS_ROOT=/path/to/skills
```

### 2. 运行审查

```bash
# 审查 diff 文件
skill run code-review --diff-file changes.diff

# 审查 git 工作区变更
skill run code-review --repo-path /path/to/repo

# 使用指定的执行器
skill run code-review --diff-file changes.diff --executor container
```

### 3. 查看结果

审查完成后，会生成以下文件：
- `review_report.json` - JSON 格式的结构化报告
- `review_report.md` - Markdown 格式的可读报告

## 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--diff-file` | Diff 文件路径 | 必需（与 --repo-path 二选一） |
| `--repo-path` | 仓库路径 | `.` |
| `--output` | 输出格式 | `json` |
| `--db` | 数据库路径 | `golens.db` |
| `--dry-run` | 干运行模式 | `false` |
| `--fake-model` | 使用 fake model | `false` |
| `--executor` | 执行器类型 | `local` |
| `--model` | LLM 模型名称 | 从环境变量读取 |

## 执行器类型

### local（默认，仅作开发 fallback）

本地执行，直接在当前环境运行命令。

```bash
skill run code-review --diff-file changes.diff --executor local
```

**注意**：本地执行器仅作为开发 fallback，不建议在生产环境使用。

### container（推荐生产方案）

使用 Docker 容器执行，提供隔离的执行环境。

```bash
skill run code-review --diff-file changes.diff --executor container
```

**要求**：
- Docker 已安装并运行
- 有权限访问 Docker daemon

**安全特性**：
- 网络隔离（`--network none`）
- 只读挂载工作目录
- 资源限制

### e2b（高级沙箱）

使用 E2B 沙箱执行，提供最高级别的隔离。

```bash
export E2B_API_KEY=your-api-key
skill run code-review --diff-file changes.diff --executor e2b
```

**要求**：
- E2B API Key

## 审查规则

### 安全风险

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| SEC001 | SQL 注入风险 | critical |
| SEC002 | 敏感信息泄漏 | critical |

### Goroutine 泄漏

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| GR001 | 无限循环 goroutine | high |
| GR002 | Context 泄漏 | medium |

### 资源泄漏

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| RES001 | 文件/连接未关闭 | high |

### 错误处理

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| ERR001 | 错误未检查 | medium |

### 测试缺失

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| TEST001 | 导出函数无测试 | low |

### 数据库事务

| 规则 ID | 说明 | 严重级别 |
|---------|------|----------|
| DB001 | 事务未提交/回滚 | high |

## 输出格式

### JSON 格式

```json
{
  "task_id": "task_20260726-120000-abc123",
  "timestamp": "2026-07-26T12:00:00Z",
  "summary": {
    "total_findings": 2,
    "critical_count": 1,
    "high_count": 1,
    "overall_risk": "critical"
  },
  "findings": [
    {
      "severity": "critical",
      "category": "security",
      "file": "db.go",
      "line": 10,
      "title": "SQL Injection Risk",
      "evidence": "fmt.Sprintf(\"SELECT * FROM users WHERE name = '%s'\", username)",
      "recommendation": "Use parameterized queries",
      "confidence": 0.95,
      "source": "rule",
      "rule_id": "SEC001"
    }
  ]
}
```

### Markdown 格式

```markdown
# GoLens Code Review Report

**Task ID:** task_20260726-120000-abc123

## Summary

- **Total Findings:** 2
- **Critical:** 1
- **High:** 1
- **Overall Risk:** critical

## Findings

### 1. SQL Injection Risk

- **Severity:** critical
- **Category:** security
- **File:** db.go:10
- **Rule ID:** SEC001

**Evidence:**
```
fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", username)
```

**Recommendation:** Use parameterized queries
```

## 数据库查询

审查结果会持久化到 SQLite 数据库，支持以下查询：

```sql
-- 查询任务状态
SELECT * FROM review_tasks WHERE task_id = 'task_xxx';

-- 查询 findings
SELECT * FROM findings WHERE task_id = 'task_xxx' ORDER BY severity;

-- 查询沙箱执行记录
SELECT * FROM sandbox_runs WHERE task_id = 'task_xxx';

-- 查询权限决策
SELECT * FROM permission_decisions WHERE task_id = 'task_xxx';

-- 查询监控摘要
SELECT * FROM monitoring_summaries WHERE task_id = 'task_xxx';

-- 查询 artifact
SELECT * FROM artifacts WHERE task_id = 'task_xxx';

-- 查询报告
SELECT * FROM review_reports WHERE task_id = 'task_xxx';
```

## 测试模式

### dry-run 模式

只运行规则引擎，不执行沙箱检查。

```bash
skill run code-review --diff-file changes.diff --dry-run
```

### fake-model 模式

使用确定性 mock 模型，不需要真实 LLM API。

```bash
skill run code-review --diff-file changes.diff --fake-model
```

### rule-only 模式

等同于 dry-run，只使用规则引擎。

```bash
skill run code-review --diff-file changes.diff --dry-run
```

## 环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `OPENAI_API_KEY` | AI API 密钥 | 无 |
| `OPENAI_BASE_URL` | AI API 基础 URL | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | AI 模型名称 | `hy3` |
| `E2B_API_KEY` | E2B API Key | 无 |
| `SKILLS_ROOT` | Skills 目录路径 | `./skills` |
