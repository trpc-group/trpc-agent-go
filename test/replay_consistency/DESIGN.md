# Replay Consistency 设计

`Run` 要求显式传入非空且无首尾空白的 run namespace；同一次后端比较复用该 namespace，每次重跑则生成新的 namespace。namespace 与 case name 共同派生 app、user 和 session 身份，隔离 session state、memory、summary 与 track；执行后保留持久化数据，不自动清理。`allowed_diff` 中的 quoted key 只有在解码后包含非 `*` 字面内容时才提供具体性，纯星号 quoted key 不能放行整个分区。

`session/replaytest` 负责执行、归一化、比较和报告；`test` 保留后端装配与故障注入。Summary 用单调 `EventPrefix` 表达事件间的执行顺序。Track 分别比较 map key、外层容器和内嵌 event；payload 覆盖完整 JSON 值域，并以标签区分 nil、空字节、JSON、UTF-8 和二进制。Backend 名称必须非空且无首尾空白，并提供显式的 `ReadAllMemories` callback；alias 与最终 snapshot 共用该 callback，只有后端 adapter 能证明读取完整时才返回 `complete=true`。nil memory entry、nil `Memory` payload 和 event normalize 失败均作为 runner error 返回，不会静默归一化。`allowed_diff` 路径必须在声明的 section 根之后包含具体字段、quoted key 或固定索引，不能用 section 根通配符放行整个分区。既有 state scope、事件时间、精确 memory query 与 retry 语义保持不变。
