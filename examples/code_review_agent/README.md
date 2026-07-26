# GoLens - 基于 trpc-agent-go 的自动代码评审 Agent

## 简介

GoLens 是一个基于 `trpc-agent-go` 框架的自动代码评审系统，面向 Go 项目代码审查场景。

## 核心特性

- ✅ **CR Skill 体系**：使用 `skill.NewFSRepository` 加载代码审查规则
- ✅ **沙箱执行**：支持 `codeexecutor/local`（可扩展到 container/e2b）
- ✅ **PermissionPolicy**：使用框架的 `tool.PermissionPolicyFunc` 控制命令权限
- ✅ **Filter**：使用框架的 `tool.FilterFunc` 过滤工具
- ✅ **数据库存储**：SQLite 持久化审查结果
- ✅ **结构化输出**：JSON/Markdown/Text 格式

## 使用方式

```bash
# 基本用法
go run main.go --diff-file testdata/02_security_issue.diff

# 干运行模式（不执行沙箱检查）
go run main.go --diff-file testdata/02_security_issue.diff --dry-run

# 输出 Markdown 格式
go run main.go --diff-file testdata/02_security_issue.diff --output md

# 输出 JSON 格式
go run main.go --diff-file testdata/02_security_issue.diff --output json
```

## 环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `OPENAI_API_KEY` | AI API 密钥 | 无 |
| `OPENAI_BASE_URL` | AI API 基础 URL | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | AI 模型名称 | `hy3` |

## 使用的 trpc-agent-go 组件

| 组件 | 用途 |
|------|------|
| `skill.NewFSRepository` | 加载 SKILL.md 定义的审查规则 |
| `llmagent.New` | 创建 LLM Agent |
| `runner.NewRunner` | 执行 Agent |
| `codeexecutor/local` | 本地代码执行器 |
| `tool.PermissionPolicyFunc` | 权限策略控制 |
| `function.NewFunctionTool` | 自定义工具 |

## 目录结构

```
code_review_agent/
├── main.go                    # 主程序
├── go.mod
├── README.md
├── input/                     # 输入解析
│   └── diff.go
├── rules/                     # 审查规则
│   └── rules.go
├── sandbox/                   # 沙箱执行
│   └── sandbox.go
├── safety/                    # 安全策略
│   └── safety.go
├── store/                     # 数据库存储
│   └── store.go
├── report/                    # 报告生成
│   └── report.go
├── skills/                    # CR Skill
│   └── code-review/
│       └── SKILL.md
└── testdata/                  # 测试数据
    ├── 01_no_issue.diff
    ├── 02_security_issue.diff
    └── ...
```

## 审查规则

| 规则 ID | 类别 | 说明 |
|---------|------|------|
| SEC001 | security | SQL 注入风险 |
| SEC002 | security | 敏感信息泄漏 |
| GR001 | goroutine | Goroutine 泄漏 |
| GR002 | goroutine | Context 泄漏 |
| RES001 | resource | 资源未关闭 |
| ERR001 | error | 错误处理不当 |
| TEST001 | test | 测试缺失 |
| DB001 | database | 数据库事务问题 |

## 验收标准

- [x] 8 条 diff 样本可运行
- [x] 使用 trpc-agent-go 框架组件
- [x] CR Skill 体系（SKILL.md）
- [x] PermissionPolicy
- [x] 数据库存储（SQLite）
- [x] 结构化输出（JSON/Markdown）
- [x] 沙箱执行（go vet, staticcheck）
- [x] 去重降噪
- [x] 敏感信息脱敏
- [x] 监控审计

## License

Apache-2.0
