# 方案设计说明

## Skill 设计

本系统使用确定性规则引擎代替 LLM 进行代码评审。`code-review` Skill
封装了 SKILL.md（使用说明）、RULES.md（规则文档）和 scripts/（沙箱
执行脚本），但实际规则检测逻辑由 Go 代码实现，保证 dry-run 模式下
无需 API Key 即可运行完整评审链路。

规则引擎包含 13 条规则，覆盖 7 类问题：安全风险（SQL 注入、命令注入、
硬编码密钥）、goroutine/context 泄漏、资源未关闭、错误处理（忽略 error、
panic）、数据库连接生命周期、敏感信息泄漏和测试缺失。每条规则返回 0-N
个 Finding，包含 severity、category、file、line、evidence、recommendation、
confidence、source 和 rule_id 字段。

## 沙箱隔离策略

生产沙箱使用框架的 `codeexecutor/container` workspace runtime，仓库会复制到独立 workspace，容器默认关闭网络且以非特权模式运行。本地 `os/exec` 仅作为显式开发 fallback。执行链提供三层隔离：

1. **权限策略**：所有命令先经过 PermissionPolicy.Decide() 判断。
   高风险命令（rm、curl、wget）直接 deny；需审查命令（docker、git push）
   进入 needs_human_review；安全命令（go test、go vet）才 allow。
2. **超时控制**：默认 30 秒超时，通过 context.WithTimeout 实现。
   超时不会导致评审崩溃，而是记录 timeout 状态后继续。
3. **环境变量白名单**：只传递 PATH、HOME、GOROOT、GOPATH，防止
   敏感环境变量泄漏到沙箱进程。
4. **输出大小限制**：stdout/stderr 各限制 1MB，超长自动截断。

## Permission/Filter 策略

PermissionPolicy 使用三级决策模型：allow → deny → needs_human_review → ask。
deny 和 needs_human_review 的命令不进入沙箱执行，但记录到
permission_decisions 表。shell 管道、重定向等 metacharacter 自动
触发 ask 决策。

## 监控字段

MonitoringSummary 记录：总耗时、沙箱执行耗时、工具调用次数、
Permission 拦截次数、finding 总数、各 severity 分布（critical/high/
medium/low）、warning 数量和异常类型分布。

## 数据库 Schema

SQLite 数据库包含 7 张表：review_tasks（任务元数据和状态）、
sandbox_runs（沙箱执行记录含 permission_decision）、permission_decisions
（权限决策审计）、findings（结构化发现）、artifacts（产物引用）、
review_reports（JSON 和 Markdown 报告）、monitoring_summary（监控指标）。
所有表通过 task_id 外键关联，支持按 task_id 查询完整评审链路。

## 去重降噪

去重基于 (file, line, category) 三元组，相同 key 只保留 confidence
最高的 finding。confidence < 0.5 的 finding 进入 warnings 列表而非
主 findings，避免低置信度噪声混入结果。

## 安全边界

敏感信息脱敏使用 8 个正则模式覆盖 API Key、token、password、secret、
Bearer token、AWS key、private key 和连接字符串。脱敏在规则引擎输出
和报告生成两个阶段执行，确保证据字段和报告中均不出现明文密钥。
