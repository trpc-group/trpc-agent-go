# PostgreSQL Storage

PostgreSQL storage is suitable for production environments and applications requiring complex queries, providing the full capabilities of a relational database.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ Complex query support
- ✅ Soft delete support
- ✅ Schema and table prefix support
- ✅ Async persistence support

## Configuration Options

### Connection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithPostgresClientDSN(dsn string)` | `string` | - | PostgreSQL DSN, format: `postgres://user:password@localhost:5432/dbname?sslmode=disable` (highest priority) |
| `WithHost(host string)` | `string` | `localhost` | PostgreSQL server address |
| `WithPort(port int)` | `int` | `5432` | PostgreSQL server port |
| `WithUser(user string)` | `string` | `""` | Database username |
| `WithPassword(password string)` | `string` | `""` | Database password |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-pgsession` | Database name |
| `WithSSLMode(sslMode string)` | `string` | `disable` | SSL mode: `disable`, `require`, `verify-ca`, `verify-full` |
| `WithPostgresInstance(name string)` | `string` | - | Use a pre-configured PostgreSQL instance (lowest priority) |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for the PostgreSQL client |

**Priority**: `WithPostgresClientDSN` > `WithHost/Port/User/Password/Database` > `WithPostgresInstance`

### Session Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | Session TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | App state TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | User state TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | TTL cleanup interval; defaults to 5 minutes if TTL is configured |
| `WithSoftDelete(enable bool)` | `bool` | `true` | Enable or disable soft delete |

### Async Persistence Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable async persistence |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async persistence workers |

### Summary Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |

### Schema and Table Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSchema(schema string)` | `string` | `""` (default schema) | Specify schema name |
| `WithTablePrefix(prefix string)` | `string` | `""` | Table name prefix |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | Skip automatic table creation |

### Hook Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// Minimal configuration
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
)

// Full production configuration
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/trpc_sessions?sslmode=require"),

    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),

    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),

    postgres.WithAsyncPersisterNum(4),
)
```

## Instance Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString("postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable"),
)

sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithSessionEventLimit(500),
)
```

## Schema and Table Prefix

PostgreSQL supports schema and table prefix configuration for multi-tenant and multi-environment scenarios:

```go
// Using schema
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithDatabase("mydb"),
    postgres.WithSchema("my_schema"),
)

// Using table prefix
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithTablePrefix("app1_"),
)

// Combined
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSchema("tenant_a"),
    postgres.WithTablePrefix("app1_"),
)
```

**Table naming rules:**

| Schema | Prefix | Final Table Name |
| --- | --- | --- |
| (none) | (none) | `session_states` |
| (none) | `app1_` | `app1_session_states` |
| `my_schema` | (none) | `my_schema.session_states` |
| `my_schema` | `app1_` | `my_schema.app1_session_states` |

## Soft Delete and TTL Cleanup

### Soft Delete Configuration

```go
// Enable soft delete (default)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(true),
)

// Disable soft delete (hard delete)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(false),
)
```

**Delete behavior comparison:**

| Config | Delete Operation | Query Behavior | Data Recovery |
| --- | --- | --- | --- |
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | Queries include `WHERE deleted_at IS NULL` | Recoverable |
| `softDelete=false` | `DELETE FROM ...` | Queries all records | Not recoverable |

### TTL Auto Cleanup

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),
    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),
)
// Cleanup behavior:
// - softDelete=true: expired data marked as deleted_at = NOW()
// - softDelete=false: expired data physically deleted
// - Queries always include `WHERE deleted_at IS NULL`
```

## With Summary

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),

    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)
```

## Storage Structure

PostgreSQL uses the following table structure:

### session_states

```sql
CREATE TABLE IF NOT EXISTS session_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  state JSONB DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_states_unique_active
ON session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;
```

### session_events

```sql
CREATE TABLE IF NOT EXISTS session_events (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  event JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);
```

### session_summaries

```sql
CREATE TABLE IF NOT EXISTS session_summaries (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  filter_key VARCHAR(255) NOT NULL DEFAULT '',
  summary JSONB DEFAULT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);
```

### session_track_events

```sql
CREATE TABLE IF NOT EXISTS session_track_events (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  track VARCHAR(255) NOT NULL,
  event JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);
```

### app_states

```sql
CREATE TABLE IF NOT EXISTS app_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  key VARCHAR(255) NOT NULL,
  value TEXT DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_app_states_unique_active
ON app_states(app_name, key)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_app_states_expires
ON app_states(expires_at)
WHERE expires_at IS NOT NULL;
```

### user_states

```sql
CREATE TABLE IF NOT EXISTS user_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  key VARCHAR(255) NOT NULL,
  value TEXT DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_states_unique_active
ON user_states(app_name, user_id, key)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_states_expires
ON user_states(expires_at)
WHERE expires_at IS NOT NULL;
```

See [session/postgres/init.go](https://github.com/trpc-group/trpc-agent-go/blob/main/session/postgres/init.go) for full table definitions.

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Production | Configure TTL, enable soft delete |
| Multi-tenant | Use Schema isolation |
| Multi-environment | Use table prefix |
| Data recovery needed | Enable soft delete |
| Compliance audit | Enable soft delete + long TTL |

## Notes

1. **Connection**: Ensure PostgreSQL service is accessible; use connection pooling
2. **Index optimization**: The service automatically creates necessary indexes; use `WithSkipDBInit(true)` to skip auto table creation
3. **Soft delete**: Enabled by default; queries automatically filter deleted records
4. **Schema permissions**: Ensure the user has appropriate permissions when using custom schemas
5. **SSL mode**: Use `require` or higher SSL mode for production
