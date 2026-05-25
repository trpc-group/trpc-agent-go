# TDSQL Distributed Storage

TDSQL mode builds on the [MySQL Session backend](mysql.md). Enable it with `WithTDSQLSharding(true)`. The API is **fully compatible** with MySQL Session — all interfaces and configuration options are shared. Only the DDL and sharding strategy differ.

## Features

- ✅ Built on [MySQL Session backend](mysql.md), fully API-compatible
- ✅ Automatic TDSQL sharded table DDL (`shardkey=user_id`)
- ✅ Internally handles DML shard routing, TTL cleanup, and other TDSQL compatibility logic
- ✅ Supports all MySQL Session features: soft delete, table prefix, async persistence, summarizer, hooks, etc.

## Configuration Options

TDSQL mode adds only one configuration option:

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithTDSQLSharding(enable bool)` | `bool` | `false` | Enable TDSQL distributed mode |

All other configuration options (connection, session, async persistence, summarizer, table prefix, hooks, etc.) are identical to [MySQL Storage](mysql.md). Please refer to the MySQL documentation.

## Quick Start

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(tdsql-proxy:3306)/db?parseTime=true&charset=utf8mb4"),
    mysql.WithTDSQLSharding(true),  // Enable TDSQL distributed mode
    mysql.WithTablePrefix("trpc_"),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithSoftDelete(true),
)
```

The only difference is the addition of `WithTDSQLSharding(true)`. All other options (`WithSessionEventLimit`, `WithSummarizer`, `WithEnableAsyncPersist`, etc.) are identical to [MySQL Storage](mysql.md).

### Running the Example

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

## Sharding Strategy

TDSQL requires each sharded table to specify a `shardkey`. This column must appear in all PRIMARY KEYs and UNIQUE KEYs.

Since all Session read/write paths naturally carry `user_id`, it is used directly as the shard key. All data for the same user lands on the same shard:

| Table | shardkey | Description |
| --- | --- | --- |
| `session_states` | `user_id` | All sessions for the same user land on the same shard |
| `session_events` | `user_id` | Events co-located with sessions |
| `session_track_events` | `user_id` | Track events co-located with sessions |
| `session_summaries` | `user_id` | Summaries co-located with sessions |
| `user_states` | `user_id` | User states co-located with sessions |
| `app_states` | `noshardkey_allset` | Broadcast table, full copy on every node |

`app_states` stores app-level configuration — small dataset, infrequent writes. As a broadcast table, it can be read locally on any node without cross-shard queries.

## Table Schema

- **TDSQL schema**: [`session/mysql/schema_tdsql.sql`](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema_tdsql.sql)
- **MySQL schema** (for comparison): [`session/mysql/schema.sql`](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql)

## Known Limitations

1. **`session_summaries` column length**: `app_name`, `session_id`, `filter_key` limited to 128 characters (due to InnoDB index length constraints)
2. **`user_id` character set**: ASCII strings recommended; non-ASCII values may cause TDSQL Proxy routing instability
