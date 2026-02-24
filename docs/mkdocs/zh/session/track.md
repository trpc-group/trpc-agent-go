# Track 事件

## 概述

Track 事件是 Session 中独立于主对话事件的轨迹存储机制。它允许你在会话中记录特定类型的事件，而不影响主对话流程。Track 事件适用于记录用户行为、系统状态变化、调试信息等场景。

## 核心概念

### Track

Track 是一个逻辑轨道，用于存储特定类型的事件：

```go
type Track string
```

### TrackEvent

TrackEvent 表示一个轨迹事件：

```go
type TrackEvent struct {
    Track     Track           `json:"track"`     // Track name
    Payload   json.RawMessage `json:"payload"`   // Event payload (JSON)
    Timestamp time.Time       `json:"timestamp"` // Event timestamp
}
```

### TrackEvents

TrackEvents 包含某个 Track 下的所有事件：

```go
type TrackEvents struct {
    Track  Track        `json:"track"`  // Track name
    Events []TrackEvent `json:"events"` // Event list
}
```

## TrackService 接口

所有支持 Track 的存储后端都实现了 `TrackService` 接口：

```go
type TrackService interface {
    AppendTrackEvent(ctx context.Context, sess *Session, event *TrackEvent, opts ...Option) error
}
```

**支持的存储后端**：

- ✅ 内存存储（inmemory）
- ✅ Redis 存储
- ✅ PostgreSQL 存储
- ✅ MySQL 存储
- ❌ ClickHouse 存储（不支持）

## 使用方法

### 追加 Track 事件

```go
import (
    "context"
    "encoding/json"
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

// Create track event
payload, _ := json.Marshal(map[string]any{
    "action": "button_click",
    "button": "submit",
})

trackEvent := &session.TrackEvent{
    Track:     "user-actions",
    Payload:   payload,
    Timestamp: time.Now(),
}

// Append to session
err := sessionService.AppendTrackEvent(ctx, sess, trackEvent)
if err != nil {
    log.Printf("Failed to append track event: %v", err)
}
```

### 获取 Track 事件

```go
// Get track events from session
trackEvents, err := sess.GetTrackEvents("user-actions")
if err != nil {
    log.Printf("Failed to get track events: %v", err)
    return
}

for _, event := range trackEvents.Events {
    fmt.Printf("Track: %s, Timestamp: %v, Payload: %s\n",
        event.Track, event.Timestamp, string(event.Payload))
}
```

### 从 Session State 获取 Track 列表

Track 索引存储在 Session State 中，可以通过 `TracksFromState` 函数获取：

```go
// Get all tracks from session state
tracks, err := session.TracksFromState(sess.State)
if err != nil {
    log.Printf("Failed to get tracks: %v", err)
    return
}

for _, track := range tracks {
    fmt.Printf("Track: %s\n", track)
}
```

## 存储结构

### 内存存储

Track 事件存储在 Session 的 `Tracks` 字段中：

```go
type Session struct {
    // ...
    Tracks   map[Track]*TrackEvents `json:"tracks,omitempty"`
    TracksMu sync.RWMutex           `json:"-"`
    // ...
}
```

### MySQL 存储

MySQL 使用独立的 `session_track_events` 表存储 Track 事件：

```sql
CREATE TABLE IF NOT EXISTS `session_track_events` (
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
    KEY `idx_session_track_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_session_track_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### Redis 存储

Redis 将 Track 事件存储在 Session 数据的 JSON 结构中。

### PostgreSQL 存储

PostgreSQL 的实现类似 MySQL，使用独立的 `session_track_events` 表存储 Track 事件：

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

## 使用场景

| 场景 | 说明 |
| --- | --- |
| 用户行为追踪 | 记录用户点击、页面访问等行为 |
| 调试日志 | 记录调试信息，不影响主对话 |
| 状态变化 | 记录系统状态变化历史 |
| A/B 测试 | 记录实验分组和用户行为 |
| 审计日志 | 记录重要操作的审计信息 |

## 示例

### 记录用户行为

```go
// Record user click event
payload, _ := json.Marshal(map[string]any{
    "action":    "click",
    "element":   "buy_button",
    "page":      "/product/123",
    "timestamp": time.Now().Unix(),
})

err := sessionService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
    Track:     "user-behavior",
    Payload:   payload,
    Timestamp: time.Now(),
})
```

### 记录调试信息

```go
// Record debug info
debugInfo, _ := json.Marshal(map[string]any{
    "function":  "processOrder",
    "input":     orderID,
    "duration":  processingTime.Milliseconds(),
    "status":    "success",
})

err := sessionService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
    Track:     "debug",
    Payload:   debugInfo,
    Timestamp: time.Now(),
})
```

### 记录 A/B 测试数据

```go
// Record A/B test assignment
abData, _ := json.Marshal(map[string]any{
    "experiment": "checkout_flow_v2",
    "variant":    "treatment",
    "user_id":    userID,
})

err := sessionService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
    Track:     "ab-test",
    Payload:   abData,
    Timestamp: time.Now(),
})
```

## 注意事项

1. **Track 名称**：Track 名称是字符串类型，建议使用有意义的命名，如 `user-behavior`、`debug`、`audit` 等
2. **Payload 格式**：Payload 是 `json.RawMessage` 类型，必须是有效的 JSON 数据
3. **并发安全**：Track 操作使用读写锁保护，支持并发访问
4. **存储限制**：Track 事件会占用存储空间，建议配置合理的 TTL 或定期清理
5. **索引存储**：Track 索引存储在 Session State 的 `tracks` 键中，用于快速查找已有的 Track
