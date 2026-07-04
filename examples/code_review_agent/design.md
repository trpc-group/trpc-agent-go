# 方案设计说明: 自动代码评审 Agent

## 设计目标

面向 Go 项目的自动代码评审 Agent，输入 git diff 或 PR patch，通过
静态规则扫描 + 沙箱静态分析 + 敏感信息检测的三层把关，输出结构化
审查报告（JSON + Markdown），并将全部结果持久化到 SQLite 数据库。

## 失败归因方法

Agent 采用 **多层归因** 策略：
1. **规则层** — 18+ 条正则规则明确绑定 rule_id + category，每条 finding
   可追溯到具体规则和匹配的代码行
2. **沙箱层** — go vet 输出按 file:line:col 格式解析，归因为 specific tool
3. **敏感信息层** — 10 种模式覆盖 API Key、密码、私钥、信用卡号等

## 防过拟合策略

- 规则模式采用精确 regex，不依赖 AI/LLM，避免模型偏差
- 去重机制（SHA256 dedup key）确保同 file+line+category 只报一次
- 置信度标记（低置信度标记为 warning，不入 critical/high）

## 接受策略（Gate）

- 沙箱执行前必须通过 safety.Scanner 安全门禁
- DecisionDeny → 拒绝执行，记录 permission_decisions
- DecisionAsk/NeedsReview → 在非交互模式视为 deny
- 沙箱超时/失败不导致整体审查崩溃（降级为 warning）

## PromptIter 接入方式

本 Agent 不直接调用 LLM。规则引擎完全确定性（regex），支持 dry-run
模式零 API 调用。如需 LLM 增强，可在管线中插入 model 调用阶段，
使用 fake model 或真实 model 做语义审查。

## 产物审计方式

- 每轮审查生成 review_report.json（机器可读）+ review_report.md（人可读）
- SQLite 数据库完整记录 task、findings、sandbox_runs、permission_decisions
- 所有时间戳用 Unix 秒，支持按 task_id 查询全链路
- 数据库 schema 见 schema.sql

## 技术栈

- Go 1.21+
- SQLite (mattn/go-sqlite3)
- 复用 trpc-agent-go: tool/safety.Scanner
- 复用 trpc-agent-go: codeexecutor 接口模式
