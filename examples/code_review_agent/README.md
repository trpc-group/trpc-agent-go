# Code Review Agent

基于 tRPC-Agent-Go 框架的自动代码审查系统。

## 功能特性

- **Token 感知的规则引擎**：基于 `go/scanner` 词法分析，不是简单正则匹配
- **6 条内置规则**：硬编码密钥、敏感信息泄漏、goroutine 泄漏、资源泄漏、错误处理、测试缺失
- **YAML 规则 DSL**：用户可用 YAML 自定义规则，不需要写 Go 代码
- **风险评分系统**：0-100 分量化评分，带多维度 breakdown
- **SQLite 存储**：审查结果持久化，支持按任务查询
- **安全过滤器**：命令执行前的安全检查 + 审计日志
- **沙箱执行**：基于 trpc-agent-go 的 codeexecutor/local
- **敏感信息脱敏**：自动检测并脱敏 10 种敏感信息类型

## 快速开始

### 安装

```bash
go build -o code-review-agent .
```

### 使用

```bash
# 审查 diff 文件
./code-review-agent --diff-file changes.diff

# 审查 git 仓库变更
./code-review-agent --repo-path /path/to/repo

# dry-run 模式（不写数据库）
./code-review-agent --diff-file changes.diff --dry-run

# 详细输出
./code-review-agent --diff-file changes.diff --verbose

# 使用自定义 YAML 规则
./code-review-agent --diff-file changes.diff --rules-dir ./rules/custom

# 指定输出目录
./code-review-agent --diff-file changes.diff --output ./reports
```

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--diff-file` | - | diff 文件路径 |
| `--repo-path` | - | git 仓库路径 |
| `--rules-dir` | - | 自定义 YAML 规则目录 |
| `--db` | `review.db` | SQLite 数据库路径 |
| `--output` | `.` | 报告输出目录 |
| `--dry-run` | false | 不写数据库 |
| `--verbose` | false | 详细输出 |

## 项目结构

```
code-review-agent/
├── main.go              # CLI 入口
├── analyzer/            # Go AST + Token 分析器
│   ├── analyzer.go      # go/ast 完整文件分析
│   └── token.go         # go/scanner 逐行分析
├── diff/                # unified diff 解析器
│   └── parser.go        # diff 解析 + Go 包名提取
├── findings/            # Finding 结构体 + 去重
│   ├── finding.go       # Finding 定义
│   └── dedup.go         # 去重和分组
├── report/              # 报告生成
│   ├── report.go        # JSON/Markdown 报告
│   └── markdown.go      # Markdown 格式化
├── rules/               # 规则引擎
│   ├── rule.go          # Rule 接口
│   ├── engine.go        # 规则引擎
│   ├── token_rules.go   # 6 条 Token 感知规则
│   ├── dsl.go           # YAML 规则 DSL 加载器
│   └── custom/          # 自定义 YAML 规则示例
├── safety/              # 安全过滤器 + 脱敏
│   ├── filter.go        # 命令安全检查
│   └── mask.go          # 敏感信息脱敏
├── sandbox/             # 沙箱执行
│   ├── sandbox.go       # Sandbox 接口
│   └── local.go         # 本地执行（基于 trpc-agent-go）
├── scoring/             # 风险评分系统
│   └── scoring.go       # 0-100 分 + 维度 breakdown
├── storage/             # SQLite 存储
│   └── storage.go       # 5 张表 + CRUD
└── testdata/            # 8 条测试样例
```

## 内置规则

| 规则 ID | 名称 | 检测内容 |
|---------|------|---------|
| SEC-AST-001 | Token 感知的密钥检测 | 硬编码密码、API Key |
| SEC-AST-002 | Token 感知的敏感信息泄漏 | AWS Key、GitHub Token、私钥、JWT |
| GOR-AST-001 | Token 感知的 goroutine 泄漏 | 无退出机制的 goroutine |
| RES-AST-001 | Token 感知的资源泄漏 | 未关闭的文件、连接、HTTP 响应 |
| ERR-AST-001 | Token 感知的错误处理 | 忽略 error、panic、log.Fatal |
| TST-AST-001 | Token 感知的测试缺失 | 新增导出函数无测试 |

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
    exclude:
      line_contains: ["localhost"]
    message: "疑似硬编码端口号"
    recommendation: "使用配置文件管理端口"
```

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

等级划分：A（0-20）→ B（20-40）→ C（40-60）→ D（60-80）→ F（80-100）

## 技术栈

| 组件 | 技术 |
|------|------|
| 代码分析 | go/ast + go/scanner（标准库） |
| 规则引擎 | 自定义 Token 感知引擎 |
| 规则 DSL | YAML（gopkg.in/yaml.v3） |
| 存储 | SQLite（trpc-agent-go session/sqlite） |
| 沙箱 | trpc-agent-go codeexecutor/local |
| 安全过滤 | 自定义安全策略 + 审计日志 |

## 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包测试
go test ./rules/ -v
go test ./analyzer/ -v

# 运行验收测试
go test ./... -count=1 -timeout 60s
```

## License

Apache License 2.0
