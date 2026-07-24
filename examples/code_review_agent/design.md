# 代码审查 Agent 设计方案

**Skill 设计**：code-review skill 独立成包，入口为 `Review(ctx, Input) (Report, error)`，内部拆分为 `diffparse`（解析 unified diff，提取文件/hunk/行号）、`rules`（规则集与匹配引擎）、`review`（编排 LLM 与工具调用流程）、`report`（产出 Markdown/JSON 报告）。通过 SkillRegistry 注册，由 Runner 在沙箱内统一调度。

**沙箱隔离**：复用 `codeexecutor.Engine`，container / e2b / local 三选一，经 `--executor` 切换，默认 container。采用 fail-closed 策略：后端不可用或镜像校验失败即拒绝执行，不降级到本地。`--unsafe-local` 显式开启本地执行，仅用于离线测试。源码只读挂载，工作目录可写但隔离。

**Permission 策略**：工具调用采用 token 化精确匹配——将命令拆为可执行 token 与参数 token，逐条与白名单比对。未命中即 deny，不进行模糊或前缀放宽。每条规则声明所需权限，运行前聚合为最小权限集。

**监控字段**：记录总耗时、沙箱耗时、工具调用次数、拦截次数、finding 总数及 severity 分布（critical / high / medium / low / info），经 telemetry 包统一上报，便于回归与告警。

**SQLite Schema**：7 张表——runs、findings、rule_executions、tool_calls、sandbox_sessions、permissions、artifacts，均经 run_id 外键关联。建连时执行 `PRAGMA foreign_keys=ON` 与 `PRAGMA journal_mode=WAL`，保证引用完整性并支持并发读。

**去重**：finding 以 `sha256(task_id + rule_id + file + line + category)` 作为唯一键，写入前 upsert，相同键仅保留首次记录，避免重复告警淹没审查结果。

**安全边界**：输入侧校验 diff/file-list 大小与编码；路径解析后拒绝穿越 repo 根的相对路径；禁止跟随符号链接；沙箱只读挂载源码；收到 os.Interrupt 信号时清理容器与临时目录，确保不留残留进程。
