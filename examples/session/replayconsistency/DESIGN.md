# Design

框架以统一 Snapshot 表示 Event、State、Memory、Summary 与 Track，再由 Backend 接口写入 InMemory 和本地持久化 JSON 后端。比较前清除自动时间戳与生成型 ID，按 seq、memory id、summary filter-key/id 和 track name 稳定排序，JSON map 通过编码规范化；相似度保留三位、耗时取整，避免浮点和调度噪声。稳定业务 ID 不删除，因此报告仍能定位 memory id、summary id 和 session；Event 使用索引与 seq 定位。

Summary 比较同时检查 session 归属、filter-key、版本、文本和覆盖关系，截断场景把历史摘要、保留事件与后续事件作为整体回放。Track 保留 name、type、invocation、错误，忽略时间戳并归一化耗时。只有后端明确不支持的分页、TTL、Track 或 Memory 查询能力可登记 allowed_diff，并必须附原因；summary 丢失、覆盖、归属或 filter-key 错误永不允许。轻量模式使用 JSON 持久化，Redis、Postgres、MySQL、ClickHouse 可按相同接口通过环境变量启用。十个故障注入分别破坏内容、顺序、参数、state、memory、summary、track 与幂等性，测试要求全部检出且正常回放零差异。
