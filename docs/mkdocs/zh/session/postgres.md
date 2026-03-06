# PostgreSQL 存储

PostgreSQL 存储适用于生产环境和需要复杂查询的应用，提供关系型数据库的完整能力。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 支持复杂查询
- ✅ 支持软删除
- ✅ 支持 Schema 和表前缀
- ✅ 支持异步持久化

## 配置选项

### 连接配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithPostgresClientDSN(dsn string)` | `string` | - | PostgreSQL DSN，格式：`postgres://user:password@localhost:5432/dbname?sslmode=disable`（优先级最高） |
| `WithHost(host string)` | `string` | `localhost` | PostgreSQL 服务器地址 |
| `WithPort(port int)` | `int` | `5432` | PostgreSQL 服务器端口 |
| `WithUser(user string)` | `string` | `""` | 数据库用户名 |
| `WithPassword(password string)` | `string` | `""` | 数据库密码 |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-pgsession` | 数据库名称 |
| `WithSSLMode(sslMode string)` | `string` | `disable` | SSL 模式，可选：`disable`、`require`、`verify-ca`、`verify-full` |
| `WithPostgresInstance(name string)` | `string` | - | 使用预配置的 PostgreSQL 实例（优先级最低） |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为 PostgreSQL 客户端设置额外选项 |

**优先级**：`WithPostgresClientDSN` > `WithHost/Port/User/Password/Database` > `WithPostgresInstance`

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

### Schema 和表配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSchema(schema string)` | `string` | `""`（默认 schema） | 指定 schema 名称 |
| `WithTablePrefix(prefix string)` | `string` | `""` | 表名前缀 |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | 跳过自动建表 |

### Hook 配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

## 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// 默认配置（最简）
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
)

// 生产环境完整配置
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/trpc_sessions?sslmode=require"),

    // 会话配置
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),

    // TTL 清理配置
    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),

    // 异步持久化配置
    postgres.WithAsyncPersisterNum(4),
)
// 效果：
// - 使用 SSL 加密连接
// - 会话最后一次写入后 30 分钟过期
// - 每 10 分钟清理过期数据（软删除）
// - 4 个异步 worker 处理写入
```

## 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// 注册 PostgreSQL 实例
postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString("postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable"),
)

// 在会话服务中使用
sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithSessionEventLimit(500),
)
```

## Schema 与表前缀

PostgreSQL 支持 schema 和表前缀配置，适用于多租户和多环境场景：

```go
// 使用 schema
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithDatabase("mydb"),
    postgres.WithSchema("my_schema"),  // 表名：my_schema.session_states
)

// 使用表前缀
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithTablePrefix("app1_"),  // 表名：app1_session_states
)

// 组合使用
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSchema("tenant_a"),
    postgres.WithTablePrefix("app1_"),  // 表名：tenant_a.app1_session_states
)
```

**表命名规则：**

| Schema | Prefix | 最终表名 |
| --- | --- | --- |
| （无） | （无） | `session_states` |
| （无） | `app1_` | `app1_session_states` |
| `my_schema` | （无） | `my_schema.session_states` |
| `my_schema` | `app1_` | `my_schema.app1_session_states` |

## 软删除与 TTL 清理

### 软删除配置

```go
// 启用软删除（默认）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(true),
)

// 禁用软删除（物理删除）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(false),
)
```

**删除行为对比：**

| 配置 | 删除操作 | 查询行为 | 数据恢复 |
| --- | --- | --- | --- |
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | 查询附带 `WHERE deleted_at IS NULL` | 可恢复 |
| `softDelete=false` | `DELETE FROM ...` | 查询所有记录 | 不可恢复 |

### TTL 自动清理

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),
    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),
)
// 清理行为：
// - softDelete=true：过期数据标记为 deleted_at = NOW()
// - softDelete=false：过期数据物理删除
// - 查询始终包含 `WHERE deleted_at IS NULL`
```

## 配合摘要使用

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),

    // 摘要配置
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)
```

## 存储结构

PostgreSQL 使用以下表结构：

### session_states

存储会话元数据和状态。

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

-- Partial unique index for non-deleted records
CREATE UNIQUE INDEX IF NOT EXISTS idx_session_states_unique_active
ON session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;
```

### session_events

存储会话事件。

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

存储会话摘要。

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

存储 Track 事件。

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

存储应用级状态。

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

存储用户级状态。

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

完整的表定义请参考 [session/postgres/init.go](https://github.com/trpc-group/trpc-agent-go/blob/main/session/postgres/init.go)

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 生产环境 | 配置 TTL、启用软删除 |
| 多租户 | 使用 Schema 隔离 |
| 多环境 | 使用表前缀区分 |
| 需要数据恢复 | 启用软删除 |
| 合规审计 | 启用软删除 + 长 TTL |

## 注意事项

1. **连接配置**：确保 PostgreSQL 服务可访问，建议使用连接池
2. **索引优化**：服务会自动创建必要的索引，也可以通过 `WithSkipDBInit(true)` 跳过自动建表
3. **软删除**：默认启用软删除，查询时自动过滤已删除记录
4. **Schema 权限**：使用自定义 Schema 时，确保用户有相应权限
5. **SSL 模式**：生产环境建议使用 `require` 或更高级别的 SSL 模式
