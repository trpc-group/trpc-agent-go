# Code Review 规则文档

本文档描述 code-review-agent 的所有内置规则和自定义规则机制。

## 内置规则（Token 感知引擎）

所有内置规则基于 `go/scanner` 词法分析，不依赖正则表达式，对不完整的 diff 片段也能工作。

### SEC-AST-001: Token 感知的密钥检测

- **严重级别**: high
- **分类**: security
- **检测内容**: 硬编码密码、API Key、Secret
- **检测方式**: 识别赋值语句中右侧为字符串字面量、左侧标识符包含 `password`/`secret`/`key`/`token`/`credential` 等关键词的模式
- **排除**: 环境变量读取（`os.Getenv`）、配置文件读取、测试文件中的占位符
- **置信度**: 0.95

### SEC-AST-002: Token 感知的敏感信息泄漏

- **严重级别**: high
- **分类**: sensitive_leak
- **检测内容**: AWS Key (`AKIA...`)、GitHub Token (`ghp_...`)、JWT (`eyJ...`)、私钥 (`-----BEGIN`)、数据库连接串
- **检测方式**: 字符串字面量模式匹配
- **排除**: 占位符、示例值、注释中的说明
- **置信度**: 0.99

### GOR-AST-001: Token 感知的 goroutine 泄漏

- **严重级别**: high
- **分类**: resource
- **检测内容**: 无退出机制的 goroutine（`go func()` 缺少 `context.Done()` 或 `return` 信号）
- **检测方式**: 识别 `go` 关键字后的函数字面量，检查是否有 `select`/`ctx.Done()`/`quit` channel
- **排除**: 有 `defer` 或明确退出条件的 goroutine
- **置信度**: 0.85

### RES-AST-001: Token 感知的资源泄漏

- **严重级别**: medium
- **分类**: resource
- **检测内容**: 未关闭的文件（`os.Open`）、HTTP 响应（`http.Get`）、数据库连接
- **检测方式**: 识别资源获取语句，检查对应的 `defer Close()` 或显式关闭
- **排除**: 已有 `defer` 关闭的资源
- **置信度**: 0.85

### ERR-AST-001: Token 感知的错误处理

- **严重级别**: medium
- **分类**: error_handling
- **检测内容**: 忽略 error 返回值、使用 `panic`、使用 `log.Fatal`
- **检测方式**: 识别赋值语句中 error 被 `_` 忽略、函数调用中包含 `panic`/`log.Fatal`
- **排除**: 明确的 error 处理（`if err != nil`）
- **置信度**: 0.80

### TST-AST-001: Token 感知的测试缺失

- **严重级别**: low
- **分类**: testing
- **检测内容**: 新增导出函数无对应测试
- **检测方式**: 比较 diff 中新增的导出函数名与 `_test.go` 文件中的测试函数
- **排除**: `main` 函数、接口方法实现
- **置信度**: 0.75

## YAML 自定义规则

### 规则文件格式

在 `rules/custom/` 目录下创建 `.yaml` 文件：

```yaml
rules:
  - id: MY-001                    # 规则 ID（必填）
    name: "规则名称"              # 规则名称（必填）
    severity: medium              # 严重级别：high / medium / low / info
    category: security            # 分类：security / resource / error_handling / testing / lifecycle / sensitive_leak / concurrency
    description: "规则描述"       # 规则描述（可选）
    match:                        # 匹配条件（至少一个）
      token_facts:                # Token 事实匹配
        - kind: identifier        # token 类型
          value_contains: ["port"] # 值包含
          value_pattern: "^\\d+$"  # 值正则匹配
          value_exact: "main"      # 值精确匹配
      line_contains: ["TODO"]     # 行内容包含
      line_not_contains: ["test"] # 行内容不包含
    exclude:                      # 排除条件（可选）
      line_contains: ["nolint"]
      line_starts_with: ["//"]
    message: "问题描述"           # 问题标题（必填）
    recommendation: "修复建议"    # 修复建议（必填）
    confidence: 0.80              # 置信度 0.0-1.0（可选，默认 0.75）
```

### Token 类型（token_facts[].kind）

| 类型 | 说明 | 示例 |
|------|------|------|
| `identifier` | 标识符 | `password`, `apiKey` |
| `string_literal` | 字符串字面量 | `"secret123"` |
| `assignment` | 赋值语句 | `x := 1` |
| `defer` | defer 语句 | `defer f.Close()` |
| `go` | go 语句 | `go func()` |
| `return` | return 语句 | `return nil` |
| `if` | if 语句 | `if err != nil` |
| `for` | for 语句 | `for range` |

### 加载顺序

1. 程序启动时自动扫描 `rules/custom/` 目录下所有 `.yaml` 文件
2. 也可通过 `--rules-dir` 参数指定自定义规则目录
3. 支持 `LoadDSLRules(dir)` 批量加载、`LoadDSLRulesFromFile(path)` 单文件加载

## 规则引擎工作流程

```
输入 diff 文件
    ↓
diff 解析器提取变更文件和 hunk
    ↓
Token 分析器（go/scanner）提取 token facts
    ↓
规则引擎遍历所有已注册规则
    ↓
每条规则对每个变更文件的每一行执行匹配
    ↓
生成 Finding（含 severity/category/file/line/evidence/confidence）
    ↓
去重引擎（file+line+category+rule_id）
    ↓
分组：confidence >= 0.7 → findings，< 0.7 → warnings
    ↓
输出结构化结果
```

## 扩展规则

### 方式 1: YAML DSL（推荐，不需要写代码）

在 `rules/custom/` 下创建 `.yaml` 文件即可。

### 方式 2: Go 代码

实现 `rules.Rule` 接口：

```go
type Rule interface {
    ID() string
    Name() string
    Severity() findings.Severity
    Category() findings.Category
    Check(file diff.FileDiff) ([]findings.Finding, error)
}
```

然后通过 `engine.Register(myRule)` 注册。
