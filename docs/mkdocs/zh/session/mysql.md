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

// 默认配置（最简）
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
)
// 效果：
// - 连接 localhost:3306，数据库 db
// - 每个会话最多存储 1000 个事件
// - 数据永不过期
// - 默认不启用异步持久化（通过 WithEnableAsyncPersist 启用）

// 生产环境完整配置
sessionService, err := mysql.NewService(
    // 连接配置
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),

    // 会话配置
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),

    // TTL 清理配置
    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),

    // 异步持久化配置
    mysql.WithAsyncPersisterNum(4),
)
// 效果：
// - 会话最后一次写入后 30 分钟过期
// - 每 10 分钟清理过期数据（软删除）
// - 4 个异步 worker 处理写入
```

## 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

// 注册 MySQL 实例
mysql.RegisterMySQLInstance("my-mysql-instance",
    mysql.WithClientBuilderDSN("root:password@tcp(localhost:3306)/trpc_sessions?parseTime=true&charset=utf8mb4"),
)

// 在会话服务中使用
sessionService, err := sessionmysql.NewService(
    sessionmysql.WithMySQLInstance("my-mysql-instance"),
    sessionmysql.WithSessionEventLimit(500),
)
```

## 表前缀

MySQL 支持表前缀配置，适用于多应用共享数据库的场景：

```go
// 使用表前缀
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithTablePrefix("app1_"),  // 表名：app1_session_states
)
```

## 软删除与 TTL 清理

### 软删除配置

```go
// 启用软删除（默认）
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(true),
)

// 禁用软删除（物理删除）
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
// 清理行为：
// - softDelete=true：过期数据标记为 deleted_at = NOW()
// - softDelete=false：过期数据物理删除
// - 查询始终包含 `WHERE deleted_at IS NULL`
```

## 配合摘要使用

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),

    // 摘要配置
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
    UNIQUE KEY `idx_{{PREFIX}}session_summaries_unique_active` (`app_name`(191),`user_id`(191),`session_id`(191),`filter_key`(191)),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### app_states

存储应用级状态。

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

存储用户级状态。

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

**旧版索引**（以下两种之一）：

- `idx_*_session_summaries_unique_active(app_name, user_id, session_id, filter_key, deleted_at)` — 唯一索引但包含 deleted_at
- `idx_*_session_summaries_lookup(app_name, user_id, session_id, deleted_at)` — 普通索引

**新版索引**：`idx_*_session_summaries_unique_active(app_name(191), user_id(191), session_id(191), filter_key(191))` — 唯一索引，不包含 deleted_at（使用前缀索引以避免 Error 1071）

**迁移步骤**：

```sql
-- ============================================================================
-- 迁移脚本：修复 session_summaries 唯一索引问题
-- 执行前请备份数据！
-- ============================================================================

-- Step 1: 查看当前索引，确认旧索引名称
SHOW INDEX FROM session_summaries;

-- Step 2: 清理重复数据（保留最新记录）
-- 如果存在多条 deleted_at = NULL 的重复记录，保留 id 最大的那条。
DELETE t1 FROM session_summaries t1
INNER JOIN session_summaries t2
WHERE t1.app_name = t2.app_name
  AND t1.user_id = t2.user_id
  AND t1.session_id = t2.session_id
  AND t1.filter_key = t2.filter_key
  AND t1.deleted_at IS NULL
  AND t2.deleted_at IS NULL
  AND t1.id < t2.id;

-- Step 3: 硬删除软删除记录（summary 数据可再生，无需保留）
-- 如果需要保留软删除记录，可跳过此步骤，但需要在 Step 5 之前手动处理冲突。
DELETE FROM session_summaries WHERE deleted_at IS NOT NULL;

-- Step 4: 删除旧索引（根据 Step 1 的结果选择正确的索引名）
-- 注意：索引名称可能带有表前缀，请根据实际情况调整。
-- 如果是 lookup 索引：
DROP INDEX idx_session_summaries_lookup ON session_summaries;
-- 如果是旧的 unique_active 索引（包含 deleted_at）：
-- DROP INDEX idx_session_summaries_unique_active ON session_summaries;

-- Step 5: 创建新的唯一索引（不包含 deleted_at）
-- 注意：索引名称可能带有表前缀，请根据实际情况调整。
CREATE UNIQUE INDEX idx_session_summaries_unique_active 
ON session_summaries(app_name(191), user_id(191), session_id(191), filter_key(191));

-- Step 6: 验证迁移结果
SELECT COUNT(*) as duplicate_count FROM (
    SELECT app_name, user_id, session_id, filter_key, COUNT(*) as cnt
    FROM session_summaries
    WHERE deleted_at IS NULL
    GROUP BY app_name, user_id, session_id, filter_key
    HAVING cnt > 1
) t;
-- 期望结果：duplicate_count = 0

-- Step 7: 验证索引是否创建成功
SHOW INDEX FROM session_summaries WHERE Key_name = 'idx_session_summaries_unique_active';
-- 期望结果：显示新创建的唯一索引，且不包含 deleted_at 列
```

**注意事项**：

1. 如果使用了 `WithTablePrefix("trpc_")` 配置，表名和索引名会带有前缀：
   - 表名：`trpc_session_summaries`
   - 旧索引名：`idx_trpc_session_summaries_lookup` 或 `idx_trpc_session_summaries_unique_active`
   - 新索引名：`idx_trpc_session_summaries_unique_active`
   - 请根据实际配置调整上述 SQL 中的表名和索引名。

2. 新索引不包含 `deleted_at` 列，这意味着软删除的 summary 记录会阻止相同业务键的新记录插入。由于 summary 数据可再生，迁移时建议硬删除软删除记录（Step 3）。如果跳过此步骤，需手动处理冲突。

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
