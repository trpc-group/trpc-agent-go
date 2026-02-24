# 内存存储（Memory）

内存存储适用于开发环境和小规模应用，无需外部依赖，开箱即用。

## 特点

- ✅ 无需外部依赖
- ✅ 开箱即用
- ✅ 高性能读写
- ❌ 数据不持久化（进程重启后丢失）
- ❌ 不支持分布式

## 配置选项

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话存储的最大事件数量，超过限制时淘汰老的事件 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话状态和事件列表的 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用级状态的 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户级状态的 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | 过期数据自动清理的间隔，如果配置了任何 TTL，默认清理间隔为 5 分钟 |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

## 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// Default configuration (development environment)
sessionService := inmemory.NewSessionService()
// Effect:
// - Each session stores up to 1000 events
// - All data never expires
// - No automatic cleanup

// Production environment configuration
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)
// Effect:
// - Each session stores up to 500 events
// - Sessions expire after 30 minutes of inactivity
// - App state expires after 24 hours
// - User state expires after 7 days
// - Expired data is cleaned up every 10 minutes
```

## 配合摘要使用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Create summarizer
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),
    summary.WithMaxSummaryWords(200),
)

// Create session service with summarizer
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(1000),
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(60*time.Second),
)
```

## 配合 Hook 使用

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // Audit logging before storage
        log.Printf("Appending event for session %s", ctx.Session.ID)
        return next()
    }),
    inmemory.WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
        sess, err := next()
        if err != nil {
            return nil, err
        }
        // Post-processing after retrieval
        log.Printf("Retrieved session %s with %d events", sess.ID, len(sess.Events))
        return sess, nil
    }),
)
```

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 开发测试 | 默认配置即可 |
| 单机小规模应用 | 配置 TTL 和 EventLimit |
| Demo 演示 | 默认配置即可 |
| 单元测试 | 默认配置，每次测试前创建新实例 |

## 注意事项

1. **数据不持久化**：进程重启后所有数据丢失，不适合生产环境
2. **内存占用**：大量会话可能导致内存占用过高，建议配置合理的 EventLimit 和 TTL
3. **不支持分布式**：多实例部署时数据不共享，每个实例有独立的会话数据
4. **并发安全**：内置读写锁，支持并发访问
