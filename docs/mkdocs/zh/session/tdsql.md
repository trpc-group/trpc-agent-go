# TDSQL 分布式存储

TDSQL 模式基于 [MySQL Session 后端](mysql.md)，通过 `WithTDSQLSharding(true)` 启用。用法与 MySQL Session **完全一致**，所有接口、配置选项均通用，仅建表语句和分片策略有所不同。

## 特点

- ✅ 基于 [MySQL Session 后端](mysql.md)，API 完全兼容
- ✅ 自动使用 TDSQL 分片表 DDL（`shardkey=user_id`）
- ✅ 内部自动处理 DML 分片路由、TTL 清理等 TDSQL 兼容逻辑
- ✅ 支持 MySQL Session 的全部功能：软删除、表前缀、异步持久化、摘要、Hook 等

## 配置选项

TDSQL 模式仅新增一个配置项：

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithTDSQLSharding(enable bool)` | `bool` | `false` | 启用 TDSQL 分布式模式 |

其余所有配置选项（连接、会话、异步持久化、摘要、表前缀、Hook 等）与 [MySQL 存储](mysql.md) 完全一致，请参考 MySQL 文档。

## 快速开始

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(tdsql-proxy:3306)/db?parseTime=true&charset=utf8mb4"),
    mysql.WithTDSQLSharding(true),  // 启用 TDSQL 分布式模式
    mysql.WithTablePrefix("trpc_"),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithSoftDelete(true),
)
```

唯一的区别是多了一个 `WithTDSQLSharding(true)`，其余配置选项（`WithSessionEventLimit`、`WithSummarizer`、`WithEnableAsyncPersist` 等）与 [MySQL 存储](mysql.md) 完全一致。

### 运行示例

```bash
export TDSQL_HOST=your-tdsql-proxy-host
export TDSQL_PORT=3306
export TDSQL_USER=your_user
export TDSQL_PASSWORD='your_password'
export TDSQL_DATABASE=your_database
```

```bash
go run ./examples/session/simple/ -session=tdsql
```

## 分片策略

TDSQL 要求每张分片表指定一个 `shardkey`，该列必须出现在 PRIMARY KEY 和 UNIQUE KEY 中。

Session 的所有读写路径天然携带 `user_id`，因此直接使用 `user_id` 作为分片键，同一用户的所有数据落在同一分片：

| 表 | shardkey | 说明 |
| --- | --- | --- |
| `session_states` | `user_id` | 同一用户的所有 session 落在同一分片 |
| `session_events` | `user_id` | 事件与 session 同分片 |
| `session_track_events` | `user_id` | track 事件与 session 同分片 |
| `session_summaries` | `user_id` | 摘要与 session 同分片 |
| `user_states` | `user_id` | 用户状态与 session 同分片 |
| `app_states` | `noshardkey_allset` | 广播表，每个节点全量复制 |

`app_states` 是应用级配置数据，数据量小、写入频率低，使用广播表可在任意节点本地读取，无需跨分片查询。

## 表结构

- **TDSQL schema**：[`session/mysql/schema_tdsql.sql`](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema_tdsql.sql)
- **MySQL schema**（对比参考）：[`session/mysql/schema.sql`](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql)

## 已知限制

1. **`session_summaries` 列长度**：`app_name`、`session_id`、`filter_key` 限制为 128 字符（受 InnoDB 索引长度约束）
2. **`user_id` 字符集**：建议使用 ASCII 字符串，非 ASCII 值可能导致 TDSQL Proxy 路由不稳定
