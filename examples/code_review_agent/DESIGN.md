# 方案设计

`code-review` Skill 定义阶段、失败语义和报告契约；规则清单绑定 ID 与 AST/patch 实现，runner 仅接受 `go-test`、`go-vet`，模型不能生成命令、环境或 artifact 路径。

生产默认使用 `codeexecutor/container` workspace runtime。Filter 校验 argv、runner、超时、环境白名单、选中 module roots、依赖摘要和临时 result artifact，PermissionPolicy 再决策；结果先落库，只有 allow 才能 copy-only 只读 staging，待审仓库不会预先 bind mount。repository snapshot 仅包含 tracked 与非 ignored untracked 文件，排除 `.git` 文件/目录；显式文件列表可 opt in ignored 文件。宿主 snapshot 根保持私有，staging 后 UID 65532 可读并保留执行位，但不可写。容器禁网、禁特权，并限制内存、进程、tmpfs、输出和 result artifact。所有 changed old/new path 决定数量受限的 module roots，确定性源码分析仍只处理 Go 文件；runner 关闭 `GOWORK`，在每个选中 module 执行固定检查并共享总超时和输出上限。Go 依赖聚合这些 module 的 `go.sum`，验证 zip、`go.mod` 的 h1 和展开量，生成只读 `file://` proxy；缓存缺失记录为 `dependency_cache`。fake 不执行代码，local 仅是开发 fallback。

SQLite 以 task 为聚合根，关联输入摘要、sandbox run、Filter/Permission decision、finding、metrics、durable artifact 和版本化报告；`GetReview` 使用单个只读事务返回一致快照。workspace 内 result artifact 会在 cleanup 时删除，不会伪装成 durable artifact；其验证后的 SHA-256 与大小记录在 sandbox run。监控记录总耗时、沙箱耗时、工具调用数、Permission 拦截数、finding 数、severity 与异常类型分布，不保存源码或原始 diff。

finding 按文件、行号、类别去重，保留最高置信证据，仅合并规则和来源；低置信项进入 warning 或人工复核。持久化、CLI、报告和 artifact 统一脱敏，Markdown 中和结构字符。报告以临时文件和 no-clobber hard link 原子发布，输出目录须独占。沙箱超时或失败会记录并降级，caller cancellation 保持失败。共享 runtime 提供 `NewWithContext`/`CloseWithContext`，旧 `New`/`Close` 保持 background context 兼容语义；示例使用独立有界 cleanup context。
