---
name: code-review
description: |
  Go 代码审查 Skill，提供全面的代码审查指导。
  覆盖安全风险、goroutine/context 泄漏、资源关闭、错误处理、
  测试缺失、敏感信息泄漏、数据库事务等问题。
  使用场景：审查 PR、代码变更、建立审查标准、指导开发者。
allowed-tools:
  - Read
  - Grep
  - Glob
  - Bash      # 运行 go vet/test/build 命令
  - WebFetch  # 查阅最新文档和最佳实践
version: "1.0.0"
author: GoLens
tags: [go, code-review, static-analysis, security]
---

# Go Code Review Skill

将代码审查从"把关"转变为"知识分享"，通过建设性反馈、系统分析和协作改进来提升代码质量。

## 何时使用此 Skill

- 审查 Pull Request 和代码变更
- 为团队建立代码审查标准
- 通过审查指导初级开发者
- 进行架构审查
- 创建审查清单和指南
- 提升团队协作
- 减少代码审查周期时间
- 维护代码质量标准

## 核心原则

### 1. 审查心态

**代码审查的目标：**
- 发现 bug 和边界情况
- 确保代码可维护性
- 在团队中分享知识
- 强制执行编码标准
- 改进设计和架构
- 建立团队文化

**不是目标：**
- 炫耀知识
- 纠结格式（使用 linter）
- 不必要地阻碍进度
- 按个人偏好重写

### 2. 有效反馈

**好的反馈是：**
- 具体且可操作
- 教育性而非评判性
- 关注代码而非个人
- 平衡（也要表扬好的工作）
- 有优先级（关键 vs 锦上添花）

```markdown
❌ 不好："这是错的。"
✅ 好："这里可能导致竞态条件，当多个用户同时访问时。
       建议在这里使用 mutex。"

❌ 不好："为什么不用 X 模式？"
✅ 好："考虑使用 Repository 模式，这样更容易测试。
       这里有一个示例：[链接]"

❌ 不好："重命名这个变量。"
✅ 好："[nit] 考虑用 `userCount` 代替 `uc`，更清晰。
       如果你更喜欢保持原样也可以。"
```

### 3. 审查范围

**应该审查：**
- 逻辑正确性和边界情况
- 安全漏洞
- 性能影响
- 测试覆盖率和质量
- 错误处理
- 文档和注释
- API 设计和命名
- 架构适配性

**不应该手动审查：**
- 代码格式（使用 gofmt）
- import 组织
- Lint 违规
- 简单的拼写错误

## 审查流程

### 阶段 1：上下文收集（2-3 分钟）

在深入代码之前，先了解：
1. 阅读 PR 描述和关联的 issue
2. 检查 PR 大小（>400 行？建议拆分）
3. 验证 CI/CD 状态（测试通过？）
4. 理解业务需求

### 阶段 2：整体扫描（5 分钟）

快速浏览变更：
- 哪些文件被修改了？
- 变更的总体方向对吗？
- 有没有明显的架构问题？
- 测试是否足够？

### 阶段 3：深入审查（15-30 分钟）

逐文件审查，关注：
- 逻辑正确性
- 边界情况
- 安全问题
- 性能问题
- 错误处理

### 阶段 4：反馈整理（5 分钟）

- 按严重程度分类
- 提供具体的修复建议
- 表扬好的代码
- 提出建设性问题

## 严重级别标签

| 标签 | 含义 | 行动 |
|------|------|------|
| 🔴 `[blocking]` | 必须修复 | 阻止合并 |
| 🟡 `[important]` | 应该修复 | 讨论是否同意 |
| 🟢 `[nit]` | 锦上添花 | 非阻塞 |
| 💡 `[suggestion]` | 替代方案 | 考虑采用 |
| 📚 `[learning]` | 教育性评论 | 无需行动 |
| 🎉 `[praise]` | 好的工作 | 表扬！ |

## Go 特定审查要点

### 必查项
- [ ] 错误是否正确处理（不忽略、有上下文）
- [ ] goroutine 是否有退出机制（避免泄漏）
- [ ] context 是否正确传递和取消
- [ ] 接收器类型选择是否合理（值/指针）
- [ ] 是否使用 `gofmt` 格式化代码

### 高频问题
- [ ] 循环变量捕获问题（Go < 1.22）
- [ ] nil 检查是否完整
- [ ] map 是否初始化后使用
- [ ] defer 在循环中的使用
- [ ] 变量遮蔽（shadowing）

## 使用方式

### 审查 diff 文件

```bash
skill run code-review --diff-file changes.diff
```

### 审查 git 工作区变更

```bash
skill run code-review --repo-path /path/to/repo
```

### 干运行模式（不执行沙箱）

```bash
skill run code-review --diff-file changes.diff --dry-run
```

### 使用 fake model 测试

```bash
skill run code-review --diff-file changes.diff --fake-model
```

## 输出示例

```markdown
## Findings

### 1. [blocking] SQL Injection Risk

- **Severity:** critical
- **Category:** security
- **File:** db.go:10
- **Rule ID:** SEC001

**Evidence:**
```go
query := fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", username)
```

**Recommendation:** 使用参数化查询：
```go
rows, err := db.Query("SELECT * FROM users WHERE name = ?", username)
```

### 2. [important] Resource Leak

- **Severity:** high
- **Category:** resource
- **File:** file.go:15
- **Rule ID:** RES001

**Evidence:**
```go
f, err := os.Open("file.txt")
// 缺少 defer f.Close()
```

**Recommendation:** 添加 `defer f.Close()`

### 3. [nit] Missing Test

- **Severity:** low
- **Category:** test
- **File:** math.go:4
- **Rule ID:** TEST001

**Recommendation:** 为 `Add` 函数添加测试
```
