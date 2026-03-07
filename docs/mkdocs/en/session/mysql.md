# MySQL Storage

MySQL storage is suitable for production environments and applications requiring complex queries. MySQL is a widely used relational database.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ Complex query support
- ✅ Soft delete support
- ✅ Table prefix support
- ✅ Async persistence support

## Configuration Options

### Connection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithMySQLClientDSN(dsn string)` | `string` | - | MySQL DSN (recommended), format: `user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local` |
| `WithMySQLInstance(instanceName string)` | `string` | - | Use a pre-configured MySQL instance (lower priority than DSN) |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for the MySQL client |

**Priority**: `WithMySQLClientDSN` > `WithMySQLInstance`

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

### Table Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithTablePrefix(prefix string)` | `string` | `""` | Table name prefix |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | Skip automatic table creation |

### Hook Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

// Minimal configuration
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
)

// Full production configuration
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),

    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),

    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),

    mysql.WithAsyncPersisterNum(4),
)
```

## Instance Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

mysql.RegisterMySQLInstance("my-mysql-instance",
    mysql.WithClientBuilderDSN("root:password@tcp(localhost:3306)/trpc_sessions?parseTime=true&charset=utf8mb4"),
)

sessionService, err := sessionmysql.NewService(
    sessionmysql.WithMySQLInstance("my-mysql-instance"),
    sessionmysql.WithSessionEventLimit(500),
)
```

## Table Prefix

MySQL supports table prefix configuration for multi-application shared database scenarios:

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithTablePrefix("app1_"),
)
```

## Soft Delete and TTL Cleanup

### Soft Delete Configuration

```go
// Enable soft delete (default)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(true),
)

// Disable soft delete (hard delete)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(false),
)
```

**Delete behavior comparison:**

| Config | Delete Operation | Query Behavior | Data Recovery |
| --- | --- | --- | --- |
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | Queries include `WHERE deleted_at IS NULL` | Recoverable |
| `softDelete=false` | `DELETE FROM ...` | Queries all records | Not recoverable |

### TTL Auto Cleanup

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),
    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),
)
// Cleanup behavior:
// - softDelete=true: expired data marked as deleted_at = NOW()
// - softDelete=false: expired data physically deleted
// - Queries always include `WHERE deleted_at IS NULL`
```

## With Summary

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),

    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),
    mysql.WithSummaryQueueSize(100),
)
```

## Storage Structure

MySQL uses the following table structure (`{{PREFIX}}` represents the table prefix):

### session_states

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `state` JSON DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}session_states_unique_active` (`app_name`,`user_id`,`session_id`,`deleted_at`),
    KEY `idx_{{PREFIX}}session_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### session_events

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_events` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `event` JSON NOT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### session_track_events

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_track_events` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `track` VARCHAR(255) NOT NULL,
    `event` JSON NOT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_track_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_track_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### session_summaries

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_summaries` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `filter_key` VARCHAR(255) NOT NULL DEFAULT '',
    `summary` JSON DEFAULT NULL,
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}session_summaries_unique_active` (`app_name`(191),`user_id`(191),`session_id`(191),`filter_key`(191)),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### app_states

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}app_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}app_states_unique_active` (`app_name`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}app_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### user_states

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}user_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}user_states_unique_active` (`app_name`,`user_id`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}user_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

See [session/mysql/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql) for full table definitions.

## Version Upgrade

### Legacy Data Migration

If your database was created with an older version, follow these migration steps.

**Affected versions**: Before v1.2.0
**Fixed in**: v1.2.0 and later

**Background**: Early versions of the `session_summaries` table had index design issues:

- The initial version used a unique index that included the `deleted_at` column, but in MySQL `NULL != NULL`, so multiple records with `deleted_at = NULL` would not trigger the unique constraint
- A later version changed to a regular lookup index (non-unique), which also could not prevent duplicate data

Both cases could lead to duplicate data.

**Old indexes** (one of the following):

- `idx_*_session_summaries_unique_active(app_name, user_id, session_id, filter_key, deleted_at)` — unique index including deleted_at
- `idx_*_session_summaries_lookup(app_name, user_id, session_id, deleted_at)` — regular index

**New index**: `idx_*_session_summaries_unique_active(app_name(191), user_id(191), session_id(191), filter_key(191))` — unique index without deleted_at (uses prefix index to avoid Error 1071)

**Migration steps**:

```sql
-- ============================================================================
-- Migration script: Fix session_summaries unique index issue
-- Back up your data before executing!
-- ============================================================================

-- Step 1: Check current indexes
SHOW INDEX FROM session_summaries;

-- Step 2: Clean up duplicate data (keep newest record)
DELETE t1 FROM session_summaries t1
INNER JOIN session_summaries t2
WHERE t1.app_name = t2.app_name
  AND t1.user_id = t2.user_id
  AND t1.session_id = t2.session_id
  AND t1.filter_key = t2.filter_key
  AND t1.deleted_at IS NULL
  AND t2.deleted_at IS NULL
  AND t1.id < t2.id;

-- Step 3: Hard delete soft-deleted records (summary data is regenerable)
DELETE FROM session_summaries WHERE deleted_at IS NOT NULL;

-- Step 4: Drop old index (choose based on Step 1 results)
DROP INDEX idx_session_summaries_lookup ON session_summaries;
-- Or if it's the old unique_active index (with deleted_at):
-- DROP INDEX idx_session_summaries_unique_active ON session_summaries;

-- Step 5: Create new unique index (without deleted_at)
CREATE UNIQUE INDEX idx_session_summaries_unique_active 
ON session_summaries(app_name(191), user_id(191), session_id(191), filter_key(191));

-- Step 6: Verify migration
SELECT COUNT(*) as duplicate_count FROM (
    SELECT app_name, user_id, session_id, filter_key, COUNT(*) as cnt
    FROM session_summaries
    WHERE deleted_at IS NULL
    GROUP BY app_name, user_id, session_id, filter_key
    HAVING cnt > 1
) t;
-- Expected: duplicate_count = 0

-- Step 7: Verify index creation
SHOW INDEX FROM session_summaries WHERE Key_name = 'idx_session_summaries_unique_active';
```

**Notes**:

1. If you configured `WithTablePrefix("trpc_")`, table and index names will have the prefix:
   - Table: `trpc_session_summaries`
   - Old index: `idx_trpc_session_summaries_lookup` or `idx_trpc_session_summaries_unique_active`
   - New index: `idx_trpc_session_summaries_unique_active`
   - Adjust the SQL above according to your actual configuration.

2. The new index does not include `deleted_at`, meaning soft-deleted summary records will block new records with the same business key. Since summary data is regenerable, it is recommended to hard delete soft-deleted records during migration (Step 3).

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Production | Configure TTL, enable soft delete |
| Multi-app shared database | Use table prefix |
| Data recovery needed | Enable soft delete |
| Compliance audit | Enable soft delete + long TTL |

## Notes

1. **Connection**: Ensure MySQL service is accessible; use connection pooling
2. **Character set**: Use utf8mb4 for full Unicode support (including emoji)
3. **Index optimization**: The service automatically creates necessary indexes; use `WithSkipDBInit(true)` to skip auto table creation
4. **Soft delete**: Enabled by default; queries automatically filter deleted records
5. **MySQL version**: Requires MySQL 5.6.5+ for multiple TIMESTAMP columns with CURRENT_TIMESTAMP
6. **Unique constraint**: MySQL's UNIQUE constraint does not prevent multiple NULL values; the application layer handles active record uniqueness
