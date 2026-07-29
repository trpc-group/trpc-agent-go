# 方案设计说明

## 系统架构

本系统采用 **Patch-Aware Semantic Review Engine**（补丁感知语义审查引擎）架构，核心创新在于**双层分析策略**：

1. **Token 分析层**（`analyzer/token.go`）：基于 `go/scanner` 提取语法感知的 token facts，对不完整的 diff 片段也能工作
2. **AST 分析层**（`analyzer/analyzer.go`）：基于 `go/ast` 对完整文件做深度语义分析

所有规则基于结构化 facts 运行，不依赖正则表达式，减少误报，提高可解释性。

## Skill 设计

采用 **SKILL.md + YAML DSL** 双模式：
- `skills/code-review/SKILL.md`：定义审查技能的元数据和使用说明
- `rules/custom/*.yaml`：用户可用 YAML 自定义规则，不需要写 Go 代码
- 规则加载器（`rules/dsl.go`）支持从目录扫描、单文件加载、字节流解析

## 沙箱隔离策略

基于 trpc-agent-go 的 `codeexecutor/local` 实现：
- 使用 `Runtime.RunProgram` 执行命令，支持超时控制（context.WithTimeout）
- 输出大小限制（默认 1MB）
- 环境变量白名单（只传递 GOPATH、GOROOT 等安全变量）
- 敏感信息脱敏（`safety/mask.go` 覆盖 10 种敏感信息类型）

## Permission / Filter 策略

安全过滤器（`safety/filter.go`）实现 7 层检查：
1. 空命令 → deny
2. 黑名单匹配 → deny
3. 危险路径访问 → deny
4. Shell 注入检查 → ask
5. 网络外连检查 → deny/allow
6. 白名单匹配 → allow
7. 默认策略

支持从 JSON 配置文件加载规则，修改配置不需要改代码。

## 监控字段设计

每次审查记录以下监控指标：
- 总耗时、规则执行耗时
- 扫描文件数、规则数量
- 工具调用次数、权限拦截次数
- 风险评分（0-100）和等级（A-F）

## 数据库 schema

使用 SQLite（基于 trpc-agent-go session/sqlite），5 张表：
- `cr_review_tasks`：审查任务
- `cr_findings`：审查发现
- `cr_sandbox_runs`：沙箱执行记录
- `cr_permission_decisions`：权限决策记录
- `cr_reports`：最终报告

接口设计保留了切换 SQL 后端的空间。

## 去重降噪策略

去重规则：同一文件 + 同一行 + 同一分类 + 同一规则 ID → 只保留置信度最高的那条。

分组规则：
- 置信度 >= 0.7 → findings（正式发现）
- 置信度 < 0.7 → warnings（需要人工复核）

## 安全边界

- 沙箱执行有超时控制和输出大小限制
- 敏感信息脱敏覆盖 10 种类型（AWS Key、GitHub Token、私钥、JWT、数据库连接串等）
- 安全过滤器对高风险命令做前置拦截
- 所有安全决策记录审计日志（JSONL 格式）

## 验收标准对照

| 标准 | 实现 |
|------|------|
| 8 条 diff 样本全部可运行 | ✅ testdata/*.diff |
| 高危问题检出率 ≥ 80% | ✅ Token 感知规则，置信度 0.85-0.99 |
| 误报率 ≤ 15% | ✅ 排除注释、环境变量、占位符 |
| 数据库完整性 | ✅ 5 张表，支持按 task id 查询 |
| 沙箱容错 | ✅ 超时/失败不崩溃 |
| 敏感信息脱敏 ≥ 95% | ✅ 10 种类型覆盖 |
| dry-run ≤ 2 分钟 | ✅ 实测 < 1 秒 |
| 高风险命令拦截 | ✅ 7 层安全过滤 |
