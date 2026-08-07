# Design

该示例以确定性流水线为结果真源。输入先解析为规范化差异，仅检查新增行；仓库模式把已跟踪普通文件复制到临时快照，并同时覆盖暂存与未暂存修改，快照后用同一文件选择重新采集 diff，不一致则 fail closed，避免旧 diff 配新快照。bundled Skill 由独立 Repository 提供，Agent 模式通过真实 llmagent 工具循环先调用 skill_load，再调用绑定计划摘要的工作区工具，被审仓库中的同名 Skill 不进入搜索根。规则与脚本摘要在授权后、暂存前和运行前重新计算。

每条执行计划绑定 runtime、Skill、快照、脚本、固定参数、目录、环境白名单、命令及任务超时、输出和 artifact 限额。Permission 是执行硬门，决定先写入 SQLite，deny 或 ask 不创建容器、不暂存、不运行；Filter 只负责工具可见性，不能替代授权。容器关闭网络并限制内存、CPU、进程数和能力，fake 只用于确定性验收，local 不冒充隔离环境。计划器按 snapshot 中的 go.mod 和变更 Go 文件生成 module/package 级 cwd 与参数，避免多模块仓库漏跑或扩大到无关包。bundled 检查脚本在命令执行时把 stdout/stderr 捕获到临时文件，只向 runtime 回放限定字节和 `output_truncated:*` marker；报告同时记录 truncated 与 truncation_reason。底层公共容器 API 仍返回字符串结果，但进入该 API 的脚本输出已经被 wrapper 限界。

静态结果按文件、行号和类别去重，保留更高严重度与置信度；低置信项进入人工复核，避免噪声污染主结论。统一 Redactor 在报告和数据库等持久化出口前处理证据、路径、治理原因及沙箱输出。SQLite 用任务、运行、决定、发现、artifact、metrics、报告七类实体保存审计链，早期输入/解析/快照失败也写 failed report 并可按 task id 重载。JSON 和 Markdown 从同一 DTO 派生，另生成 `acceptance_manifest.json` 作为机器可读验收入口，包含状态、输入、stats、metrics、artifact sha256/size/content type 和 sandbox/redaction/governance checks。metrics 记录耗时、调用、拦截、严重度、失败类型、依赖缺失和 skip 原因。隐藏样本召回率/误报率只能由活动评测集确认；本仓库提供公开 fixtures、holdout 风格单测和 ad-hoc CLI 探针作为可复现证据，不声称私有集通过。
