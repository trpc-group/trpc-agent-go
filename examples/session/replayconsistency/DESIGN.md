# Design

框架以统一 Snapshot 表示 Event、State、Memory、Summary 与 Track，但 Backend 不再直接保存最终快照：它把输入拆成 CreateSession、AppendEvent、UpdateSessionState、AddMemory、CreateSessionSummary 与 AppendTrackEvent 操作，分别驱动真实 InMemory 和 SQLite Session/Memory 服务，再通过服务读取结果生成 Snapshot。比较前清除自动时间戳与生成型 ID，按 seq、memory id、summary filter-key/id 和 track name 稳定排序，JSON map 通过编码规范化；相似度保留三位、耗时取整，避免浮点和调度噪声。稳定业务 ID 不删除，因此报告仍能定位 memory id、summary id 和 session；Event 使用索引与 seq 定位。

Summary 使用确定性 summarizer 通过真实 CreateSessionSummary 路径生成，并同时检查 session 归属、filter-key、boundary 版本、文本和覆盖关系；截断场景把历史摘要、保留事件与后续事件作为整体回放。Track 通过 TrackService 写入，保留 name、type、invocation、错误，忽略时间戳并归一化耗时。只有后端明确不支持的分页、TTL、Track 或 Memory 查询能力可登记 allowed_diff，并必须附原因；summary 丢失、覆盖、归属或 filter-key 错误永不允许。轻量模式使用本地临时 SQLite，无需外部服务；Redis、Postgres、MySQL、ClickHouse 可按相同接口通过环境变量启用。十个故障注入分别破坏内容、顺序、参数、state、memory、summary、track 与幂等性，测试要求全部检出且正常回放零差异。
