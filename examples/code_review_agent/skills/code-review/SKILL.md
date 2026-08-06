---
name: code-review
description: >
  自动代码评审技能。接收 unified diff 或 git diff，
  通过规则引擎 + LLM 分析双通道检测，去重后生成
  JSON/MD 报告并持久化到 SQLite。
input_spec:
  diff_file: "unified diff 文件路径 (优先级最高)"
  diff_text: "unified diff 文本内容 (stdin 或 API)"
  repo_path: "Git 仓库路径 (预留，InputProvider 接口扩展)"
  base_ref: "对比基线分支 (默认 origin/main)"
triggers:
  - keywords: ["review", "code review", "检查代码", "CR"]
  - file_patterns: ["*.diff", "*.patch"]
---

# Code Review Skill

## 流程概述

```
DiffParser → PermissionFilter → SandboxRunner → RuleEngine → LLMAnalyzer → DedupEngine → ReportGenerator → StorageWriter
```

### 规则覆盖

| 规则类别 | 规则文件 | 检测方式 |
|---------|---------|---------|
| 安全风险 | `rules/security.md` | TokenRule (正则) |
| 错误处理 | `rules/errors.md` | TokenRule (正则) |
| 敏感信息 | `rules/sensitive.md` | TokenRule (正则) |
| 数据库 | `rules/database.md` | TokenRule (正则) |
| 测试覆盖 | `rules/testing.md` | TokenRule + 文件级检查 |

### 沙箱命令

| 命令 | 工具 | 产出 |
|------|------|------|
| `go vet ./...` | Go 官方静态分析 | 行号级告警 |
| `staticcheck ./...` | 第三方静态分析 | 行号级告警 |
| `go test ./...` | 测试执行 | 覆盖率、失败详情 |
| `go build ./...` | 编译检查 | 编译错误 |

### LLM 分析

LLMAnalyzer 的 prompt 和输出 schema 由本 Skill 的 rules/ 定义，推理调用由
框架 model 层直接执行（不经过 Skill Agent loop，减少复杂度）。

### dry-run 模式

dry-run 模式下，LLMAnalyzer 不调用外部 API，改为加载
`testdata/mock_llm_findings.json` 中的预置 findings。
