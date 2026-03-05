# ClickHouse 存储

ClickHouse 存储适用于生产环境和海量数据场景，利用 ClickHouse 强大的写入吞吐量和数据压缩能力。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 海量数据存储
- ✅ 高写入吞吐量
- ✅ 数据压缩
- ✅ 支持批量写入
- ✅ 支持表前缀

## 配置选项

### 连接配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithClickHouseDSN(dsn string)` | `string` | - | ClickHouse DSN 连接字符串（推荐），格式：`clickhouse://user:password@host:port/database?dial_timeout=10s` |
| `WithClickHouseInstance(name string)` | `string` | - | 使用预配置的 ClickHouse 实例（优先级低于 DSN） |
| `WithExtraOptions(opts ...any)` | `[]any` | `nil` | 为 ClickHouse 客户端设置额外选项 |

**优先级**：`WithClickHouseDSN` > `WithClickHouseInstance`

### 会话配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用状态 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户状态 TTL |
| `WithDeletedRetention(retention time.Duration)` | `time.Duration` | `0`（禁用） | 软删除数据保留时间，启用后通过 `ALTER TABLE DELETE` 定期清理。**生产环境不建议开启**，建议优先使用 ClickHouse 表级 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | 清理任务间隔 |

### 异步持久化配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 启用应用层异步持久化 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步 worker 数量 |
| `WithBatchSize(size int)` | `int` | `100` | 批量写入大小 |
| `WithBatchTimeout(timeout time.Duration)` | `time.Duration` | `100ms` | 批量写入超时 |

### 摘要配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |

### Schema 配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithTablePrefix(prefix string)` | `string` | `""` | 表名前缀 |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | 跳过自动建表 |

### Hook 配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

## 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"

// 默认配置（最简）
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
)
```

## 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
    sessionch "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

// 注册 ClickHouse 实例
clickhouse.RegisterClickHouseInstance("my-clickhouse",
    clickhouse.WithClientBuilderDSN("clickhouse://localhost:9000/default"),
)

// 在会话服务中使用
sessionService, err := sessionch.NewService(
    sessionch.WithClickHouseInstance("my-clickhouse"),
)
```

## 批量写入配置

ClickHouse 适合批量写入，可以配置批量写入参数以优化性能：

```go
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://localhost:9000/default"),
    clickhouse.WithEnableAsyncPersist(true),
    clickhouse.WithAsyncPersisterNum(10),
    clickhouse.WithBatchSize(100),
    clickhouse.WithBatchTimeout(100*time.Millisecond),
)
```

## 配合摘要使用

```go
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
    clickhouse.WithSummarizer(summarizer),
    clickhouse.WithAsyncSummaryNum(2),
)
```

## 存储结构

ClickHouse 实现使用了 `ReplacingMergeTree` 引擎来处理数据更新和去重。

**关键特性：**

1. **ReplacingMergeTree**：利用 `updated_at` 字段，ClickHouse 会在后台自动合并相同主键的记录，保留最新版本
2. **FINAL 查询**：所有读取操作都使用 `FINAL` 关键字（如 `SELECT ... FINAL`），确保在查询时合并所有数据部分，保证读取一致性
3. **Soft Delete**：删除操作通过插入一条带有 `deleted_at` 时间戳的新记录实现，查询时过滤 `deleted_at IS NULL`

### session_states

```sql
CREATE TABLE IF NOT EXISTS session_states (
    app_name    String,
    user_id     String,
    session_id  String,
    state       JSON COMMENT 'Session state in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (app_name, user_id, session_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session states table';
```

### session_events

```sql
CREATE TABLE IF NOT EXISTS session_events (
    app_name    String,
    user_id     String,
    session_id  String,
    event_id    String,
    event       JSON COMMENT 'Event data in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (app_name, user_id, session_id, event_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session events table';
```

### session_summaries

```sql
CREATE TABLE IF NOT EXISTS session_summaries (
    app_name    String,
    user_id     String,
    session_id  String,
    filter_key  String COMMENT 'Filter key for multiple summaries per session',
    summary     JSON COMMENT 'Summary data in JSON format',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (app_name, user_id, session_id, filter_key)
SETTINGS allow_nullable_key = 1
COMMENT 'Session summaries table';
```

### app_states

```sql
CREATE TABLE IF NOT EXISTS app_states (
    app_name    String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY app_name
ORDER BY (app_name, key)
SETTINGS allow_nullable_key = 1
COMMENT 'Application states table';
```

### user_states

```sql
CREATE TABLE IF NOT EXISTS user_states (
    app_name    String,
    user_id     String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (app_name, user_id, key)
SETTINGS allow_nullable_key = 1
COMMENT 'User states table';
```

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 海量日志存储 | 启用批量写入、配置合理的 BatchSize |
| 高并发写入 | 启用异步持久化、增加 worker 数量 |
| 数据分析 | 使用 ClickHouse 原生查询能力 |
| 长期数据保留 | 使用 ClickHouse 表级 TTL |

## 注意事项

1. **ClickHouse 版本**：需要 ClickHouse 22.3+ 以支持 JSON 类型
2. **ReplacingMergeTree**：数据更新通过插入新记录实现，后台自动合并去重
3. **FINAL 查询**：读取时使用 FINAL 确保一致性，但可能影响性能
4. **软删除清理**：`WithDeletedRetention` 使用 `ALTER TABLE DELETE`，对大数据集可能有性能影响，建议使用 ClickHouse Native TTL
5. **批量写入**：ClickHouse 适合批量写入，避免频繁小批量写入
6. **分区策略**：默认按 `app_name` 和 `user_id` 哈希分区，适合大多数场景
