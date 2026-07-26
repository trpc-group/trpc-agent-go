# Design

框架以统一 Snapshot 表示 Event、State、Memory、Summary 与 Track，但 Backend 不再直接保存最终快照：它把输入拆成 CreateSession、AppendEvent、UpdateSessionState、AddMemory、CreateSessionSummary 与 AppendTrackEvent 操作，分别驱动真实 InMemory 和 SQLite Session/Memory 服务，再通过服务读取结果生成 Snapshot。比较前清除自动时间戳，memory、summary 与 track 按稳定定位字段排序，Event 则保留服务返回的时间顺序；JSON map 通过编码规范化，相似度保留三位、耗时取整，避免浮点和调度噪声。跨后端比较保留并校验服务生成的规范 memory ID，因此报告仍能定位 memory id、summary id 和 session；Event 使用索引与 seq 定位。

Summary 使用确定性 summarizer 通过真实 CreateSessionSummary 路径生成，并同时检查 session 归属、filter-key、boundary 版本、文本和覆盖关系；截断场景先回放 1-9 并建立边界，再追加保留事件 10-11。Track 通过 TrackService 写入，保留 name、type、invocation、错误，忽略时间戳并归一化耗时。只有后端明确不支持的分页、TTL、Track 或 Memory 查询能力可登记 allowed_diff，并必须附原因；任意 map key 在 capability path 中按 JSON Pointer 转义，summary 丢失、覆盖、归属或 filter-key 错误永不允许。轻量模式使用本地临时 SQLite，无需外部服务；Redis、Postgres、MySQL、ClickHouse 可按相同接口通过环境变量启用。十个故障注入分别破坏内容、顺序、参数、state、memory、summary、track 与幂等性，测试要求全部检出且正常回放零差异。
