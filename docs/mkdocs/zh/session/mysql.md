# MySQL 存储

MySQL 存储适用于生产环境和需要复杂查询的应用，MySQL 是广泛使用的关系型数据库。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 支持复杂查询
- ✅ 支持软删除
- ✅ 支持表前缀
- ✅ 支持异步持久化

## 配置选项

### 连接配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithMySQLClientDSN(dsn string)` | `string` | - | MySQL DSN 连接字符串（推荐），格式：`user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local` |
| `WithMySQLInstance(instanceName string)` | `string` | - | 使用预配置的 MySQL 实例（优先级低于 DSN） |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为 MySQL 客户端设置额外选项 |

**优先级**：`WithMySQLClientDSN` > `WithMySQLInstance`

### 会话配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用状态 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户状态 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | TTL 清理间隔，默认 5 分钟（如果配置了 TTL） |
| `WithSoftDelete(enable bool)` | `bool` | `true` | 启用或禁用软删除 |

### 异步持久化配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 启用异步持久化 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步持久化 worker 数量 |

### 摘要配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |

### 表配置

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
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

// Default configuration (minimal)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
)
// Effect:
// - Connect to localhost:3306, database db
// - Each session stores up to 1000 events
// - Data never expires
// - Async persistence disabled by default (enable via WithEnableAsyncPersist)

// Production environment full configuration
sessionService, err := mysql.NewService(
    // Connection configuration
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),

    // Session configuration
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),

    // TTL cleanup configuration
    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),

    // Async persistence configuration
    mysql.WithAsyncPersisterNum(4),
)
// Effect:
// - Sessions expire after 30 minutes of inactivity
// - Expired data cleaned up every 10 minutes (soft delete)
// - 4 async workers handle writes
```

## 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

// Register MySQL instance
mysql.RegisterMySQLInstance("my-mysql-instance",
    mysql.WithClientBuilderDSN("root:password@tcp(localhost:3306)/trpc_sessions?parseTime=true&charset=utf8mb4"),
)

// Use in session service
sessionService, err := sessionmysql.NewService(
    sessionmysql.WithMySQLInstance("my-mysql-instance"),
    sessionmysql.WithSessionEventLimit(500),
)
```

## 表前缀

MySQL 支持表前缀配置，适用于多应用共享数据库的场景：

```go
// Use table prefix
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithTablePrefix("app1_"),  // Table name: app1_session_states
)
```

## 软删除与 TTL 清理

### 软删除配置

```go
// Enable soft delete (default)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(true),
)

// Disable soft delete (physical delete)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(false),
)
```

**删除行为对比：**

| 配置 | 删除操作 | 查询行为 | 数据恢复 |
| --- | --- | --- | --- |
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | 查询附带 `WHERE deleted_at IS NULL` | 可恢复 |
| `softDelete=false` | `DELETE FROM ...` | 查询所有记录 | 不可恢复 |

### TTL 自动清理

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
// - softDelete=true: Expired data marked as deleted_at = NOW()
// - softDelete=false: Expired data physically deleted
// - Queries always include `WHERE deleted_at IS NULL`
```

## 配合摘要使用

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),

    // Summary configuration
    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),
    mysql.WithSummaryQueueSize(100),
)
```

## 存储结构

MySQL 使用以下表结构（使用 `{{PREFIX}}` 表示表前缀）：

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
    UNIQUE KEY `idx_{{PREFIX}}session_summaries_unique_active` (`app_name`,`user_id`,`session_id`,`filter_key`),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

完整的表定义请参考 [session/mysql/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql)

## 版本升级

### 旧版本数据迁移

如果您的数据库是使用旧版本创建的，需要执行以下迁移步骤。

**影响版本**：v1.2.0 之前的版本  
**修复版本**：v1.2.0 及之后

**问题背景**：早期版本的 `session_summaries` 表存在索引设计问题：

- 最初版本使用包含 `deleted_at` 列的唯一索引，但 MySQL 中 `NULL != NULL`，导致多条 `deleted_at = NULL` 的记录无法触发唯一约束
- 后续版本改为普通 lookup 索引（非唯一），同样无法防止重复数据

这两种情况都可能导致重复数据产生。

**迁移步骤**：

```sql
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

-- Step 4: Drop old index (adjust name based on Step 1 results)
DROP INDEX idx_session_summaries_lookup ON session_summaries;
-- Or: DROP INDEX idx_session_summaries_unique_active ON session_summaries;

-- Step 5: Create new unique index (without deleted_at)
CREATE UNIQUE INDEX idx_session_summaries_unique_active 
ON session_summaries(app_name, user_id, session_id, filter_key);

-- Step 6: Verify migration results
SELECT COUNT(*) as duplicate_count FROM (
    SELECT app_name, user_id, session_id, filter_key, COUNT(*) as cnt
    FROM session_summaries
    WHERE deleted_at IS NULL
    GROUP BY app_name, user_id, session_id, filter_key
    HAVING cnt > 1
) t;
-- Expected: duplicate_count = 0
```

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 生产环境 | 配置 TTL、启用软删除 |
| 多应用共享数据库 | 使用表前缀区分 |
| 需要数据恢复 | 启用软删除 |
| 合规审计 | 启用软删除 + 长 TTL |

## 注意事项

1. **连接配置**：确保 MySQL 服务可访问，建议使用连接池
2. **字符集**：使用 utf8mb4 支持完整 Unicode（包括 emoji）
3. **索引优化**：服务会自动创建必要的索引，也可以通过 `WithSkipDBInit(true)` 跳过自动建表
4. **软删除**：默认启用软删除，查询时自动过滤已删除记录
5. **MySQL 版本**：需要 MySQL 5.6.5+ 以支持多个 TIMESTAMP 列的 CURRENT_TIMESTAMP
6. **唯一约束**：MySQL 的 UNIQUE 约束不会阻止多个 NULL 值，应用层处理活跃记录的唯一性
