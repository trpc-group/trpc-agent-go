# Session 模块扩展设计方案

## 一、背景与需求

### 1.1 现状分析

当前 Session 架构采用三层状态模型：

```
┌─────────────────────────────────────────────────────────┐
│                      Session                             │
├─────────────────────────────────────────────────────────┤
│  AppState (app:)     → 应用级共享状态                    │
│  UserState (user:)   → 用户级持久状态                    │
│  SessionState        → 会话级状态 + Events 历史          │
│  Tracks              → 独立事件轨道 (TrackEvent)         │
│  Summaries           → 会话摘要                          │
└─────────────────────────────────────────────────────────┘
```

**存储结构 (Redis)**：

| Key Pattern | Type | Description |
|-------------|------|-------------|
| `appstate:{appName}` | Hash | 应用级状态 |
| `userstate:{appName}:{userId}` | Hash | 用户级状态 |
| `sess:{appName}:{userId}` | Hash | SessionState [sessionId → JSON] |
| `event:{appName}:{userId}:{sessionId}` | SortedSet | Event 历史 [JSON, score=timestamp] |
| `track:{appName}:{userId}:{sessionId}:{track}` | SortedSet | Track 事件 |
| `sesssum:{appName}:{userId}` | Hash | 会话摘要 |

**当前事件流**：

```
User Input → AppendEvent(request) → Model Response → AppendEvent(response) → Persist
```

### 1.2 核心需求

| 需求 | 描述 | 痛点 |
|------|------|------|
| **安全过滤** | 不合规内容不进入 LLM 上下文 | 当前 Event 持久化后无法从上下文排除，且需保证 QA 连续性 |
| **自定义数据** | 存储任务中断、业务标记等 | 缺少类型安全的 API，需手动序列化 |
| **泛化扩展** | 用户可快速实现自定义需求 | 缺少 Hook 机制，需重新实现 Session |

---

## 二、方案设计

### 2.1 Event Metadata 机制

在 `Event` 结构体增加元数据字段，支持事件级标记：

```go
// event/event.go
type Event struct {
    // ... existing fields ...
    
    // Metadata stores extensible event-level metadata.
    // Reserved keys:
    //   - "excluded": bool, whether to exclude from context
    //   - "excludeReason": string, reason for exclusion
    //   - "safetyScore": float64, safety check score
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

**辅助方法**：

```go
// SetExcluded marks the event as excluded from context.
func (e *Event) SetExcluded(reason string) {
    if e.Metadata == nil {
        e.Metadata = make(map[string]any)
    }
    e.Metadata["excluded"] = true
    e.Metadata["excludeReason"] = reason
}

// IsExcluded returns whether the event is marked as excluded.
func (e *Event) IsExcluded() bool {
    if e.Metadata == nil {
        return false
    }
    excluded, _ := e.Metadata["excluded"].(bool)
    return excluded
}

// GetExcludeReason returns the exclusion reason if any.
func (e *Event) GetExcludeReason() string {
    if e.Metadata == nil {
        return ""
    }
    reason, _ := e.Metadata["excludeReason"].(string)
    return reason
}

// SetMetadata sets a custom metadata key-value pair.
func (e *Event) SetMetadata(key string, value any) {
    if e.Metadata == nil {
        e.Metadata = make(map[string]any)
    }
    e.Metadata[key] = value
}

// GetMetadata retrieves a metadata value by key.
func (e *Event) GetMetadata(key string) (any, bool) {
    if e.Metadata == nil {
        return nil, false
    }
    v, ok := e.Metadata[key]
    return v, ok
}
```

### 2.2 Session 过滤选项扩展

扩展 `Options` 支持排除标记事件：

```go
// session/session.go
type Options struct {
    EventNum      int
    EventTime     time.Time
    ExcludeMarked bool  // NEW: exclude events with "excluded" metadata
}

// WithExcludeMarked filters out events marked as excluded.
func WithExcludeMarked(exclude bool) Option {
    return func(o *Options) {
        o.ExcludeMarked = exclude
    }
}
```

修改 `ApplyEventFiltering`：

```go
func (sess *Session) ApplyEventFiltering(opts ...Option) {
    opt := applyOptions(opts...)
    
    // Step 1: Filter excluded events if requested
    if opt.ExcludeMarked {
        filtered := make([]event.Event, 0, len(sess.Events))
        for _, e := range sess.Events {
            if !e.IsExcluded() {
                filtered = append(filtered, e)
            }
        }
        sess.Events = filtered
    }
    
    // Step 2: Apply time filter (existing logic)
    // Step 3: Apply count limit (existing logic)
    // Step 4: Ensure starts with user message (existing logic)
}
```

### 2.3 Service 接口扩展

```go
// session/session.go
type Service interface {
    // ... existing methods ...
    
    // MarkEventExcluded marks a persisted event as excluded.
    // The event remains stored but filtered from context when
    // GetSession is called with WithExcludeMarked(true).
    MarkEventExcluded(ctx context.Context, key Key, eventID string, reason string) error
    
    // UpdateEventMetadata updates metadata for a persisted event.
    // Supports partial updates - only specified keys are modified.
    UpdateEventMetadata(ctx context.Context, key Key, eventID string, metadata map[string]any) error
}
```

### 2.4 StateHelper 类型安全 API

```go
// session/state_helper.go
package session

import (
    "encoding/json"
    "errors"
)

// State prefix constants
const (
    StateAppPrefix    = "app:"
    StateUserPrefix   = "user:"
    StateTempPrefix   = "temp:"
    StateCustomPrefix = "custom:"  // NEW: user-defined business data
)

var ErrStateKeyNotFound = errors.New("state key not found")

// StateHelper provides typed access to session state.
type StateHelper struct {
    state StateMap
}

// NewStateHelper creates a new StateHelper.
func NewStateHelper(state StateMap) *StateHelper {
    if state == nil {
        state = make(StateMap)
    }
    return &StateHelper{state: state}
}

// SetCustom stores user-defined data with JSON serialization.
func (h *StateHelper) SetCustom(key string, value any) error {
    data, err := json.Marshal(value)
    if err != nil {
        return err
    }
    h.state[StateCustomPrefix+key] = data
    return nil
}

// GetCustom retrieves user-defined data with JSON deserialization.
func (h *StateHelper) GetCustom(key string, dest any) error {
    data, ok := h.state[StateCustomPrefix+key]
    if !ok {
        return ErrStateKeyNotFound
    }
    return json.Unmarshal(data, dest)
}

// HasCustom checks if a custom key exists.
func (h *StateHelper) HasCustom(key string) bool {
    _, ok := h.state[StateCustomPrefix+key]
    return ok
}

// DeleteCustom removes a custom key.
func (h *StateHelper) DeleteCustom(key string) {
    delete(h.state, StateCustomPrefix+key)
}

// SetString stores a string value.
func (h *StateHelper) SetString(key, value string) {
    h.state[StateCustomPrefix+key] = []byte(value)
}

// GetString retrieves a string value.
func (h *StateHelper) GetString(key string) (string, error) {
    data, ok := h.state[StateCustomPrefix+key]
    if !ok {
        return "", ErrStateKeyNotFound
    }
    return string(data), nil
}

// SetBool stores a boolean value.
func (h *StateHelper) SetBool(key string, value bool) error {
    return h.SetCustom(key, value)
}

// GetBool retrieves a boolean value.
func (h *StateHelper) GetBool(key string) (bool, error) {
    var v bool
    err := h.GetCustom(key, &v)
    return v, err
}
```

### 2.5 Middleware 机制（链式调用 + Filtering）

采用类似 HTTP middleware 的 `next` 链式调用模式，同时整合 Event Filtering 能力：

#### 2.5.1 核心类型定义

```go
// session/middleware.go
package session

import (
    "context"
    
    "trpc.group/trpc-go/trpc-agent-go/event"
)

// EventContext carries event processing context through middleware chain.
type EventContext struct {
    Context   context.Context
    Session   *Session
    Event     *event.Event
    Key       Key
    Abort     bool   // Set to true to stop processing
    AbortErr  error  // Error when aborted
}

// SessionContext carries session retrieval context through middleware chain.
type SessionContext struct {
    Context context.Context
    Session *Session
    Key     Key
    Options *Options
    Abort   bool
    AbortErr error
}

// NextFunc is the function to call the next middleware in chain.
type NextFunc func() error

// EventMiddleware processes events with next() chain pattern.
type EventMiddleware func(ec *EventContext, next NextFunc) error

// SessionMiddleware processes session retrieval with next() chain pattern.
type SessionMiddleware func(sc *SessionContext, next NextFunc) error

// EventFilter defines a filter function for events.
// Returns true to keep the event, false to filter it out.
type EventFilter func(e *event.Event) bool
```

#### 2.5.2 Middleware Chain 实现

```go
// MiddlewareChain manages middleware execution.
type MiddlewareChain struct {
    eventMiddlewares   []EventMiddleware
    sessionMiddlewares []SessionMiddleware
    eventFilters       []EventFilter
}

// NewMiddlewareChain creates a new middleware chain.
func NewMiddlewareChain() *MiddlewareChain {
    return &MiddlewareChain{}
}

// UseEvent adds event middleware to the chain.
func (c *MiddlewareChain) UseEvent(mw ...EventMiddleware) *MiddlewareChain {
    c.eventMiddlewares = append(c.eventMiddlewares, mw...)
    return c
}

// UseSession adds session middleware to the chain.
func (c *MiddlewareChain) UseSession(mw ...SessionMiddleware) *MiddlewareChain {
    c.sessionMiddlewares = append(c.sessionMiddlewares, mw...)
    return c
}

// UseFilter adds event filter to the chain.
func (c *MiddlewareChain) UseFilter(f ...EventFilter) *MiddlewareChain {
    c.eventFilters = append(c.eventFilters, f...)
    return c
}

// RunEventMiddleware executes event middleware chain.
func (c *MiddlewareChain) RunEventMiddleware(ec *EventContext, final func() error) error {
    return c.runEventChain(ec, 0, final)
}

func (c *MiddlewareChain) runEventChain(ec *EventContext, index int, final func() error) error {
    // Check if aborted
    if ec.Abort {
        return ec.AbortErr
    }
    
    // If all middlewares executed, run final handler
    if index >= len(c.eventMiddlewares) {
        if final != nil {
            return final()
        }
        return nil
    }
    
    // Create next function for current middleware
    next := func() error {
        return c.runEventChain(ec, index+1, final)
    }
    
    // Execute current middleware
    return c.eventMiddlewares[index](ec, next)
}

// RunSessionMiddleware executes session middleware chain.
func (c *MiddlewareChain) RunSessionMiddleware(sc *SessionContext, final func() error) error {
    return c.runSessionChain(sc, 0, final)
}

func (c *MiddlewareChain) runSessionChain(sc *SessionContext, index int, final func() error) error {
    if sc.Abort {
        return sc.AbortErr
    }
    
    if index >= len(c.sessionMiddlewares) {
        if final != nil {
            return final()
        }
        return nil
    }
    
    next := func() error {
        return c.runSessionChain(sc, index+1, final)
    }
    
    return c.sessionMiddlewares[index](sc, next)
}

// ApplyFilters applies all registered filters to session events.
func (c *MiddlewareChain) ApplyFilters(sess *Session) {
    if len(c.eventFilters) == 0 || sess == nil {
        return
    }
    
    filtered := make([]event.Event, 0, len(sess.Events))
    for _, e := range sess.Events {
        keep := true
        for _, filter := range c.eventFilters {
            if !filter(&e) {
                keep = false
                break
            }
        }
        if keep {
            filtered = append(filtered, e)
        }
    }
    sess.Events = filtered
}
```

#### 2.5.3 内置 Middleware 和 Filter

```go
// session/middleware_builtin.go
package session

import (
    "trpc.group/trpc-go/trpc-agent-go/event"
)

// ---- Built-in Event Filters ----

// ExcludedFilter filters out events marked as excluded.
func ExcludedFilter() EventFilter {
    return func(e *event.Event) bool {
        return !e.IsExcluded()
    }
}

// RoleFilter filters events by role.
func RoleFilter(allowedRoles ...string) EventFilter {
    roleSet := make(map[string]struct{}, len(allowedRoles))
    for _, r := range allowedRoles {
        roleSet[r] = struct{}{}
    }
    return func(e *event.Event) bool {
        if e.Response == nil || len(e.Response.Choices) == 0 {
            return true
        }
        role := e.Response.Choices[0].Message.Role
        _, ok := roleSet[role]
        return ok
    }
}

// MetadataFilter filters events by metadata condition.
func MetadataFilter(key string, predicate func(value any) bool) EventFilter {
    return func(e *event.Event) bool {
        v, ok := e.GetMetadata(key)
        if !ok {
            return true // No metadata means pass
        }
        return predicate(v)
    }
}

// TimeRangeFilter filters events within time range.
func TimeRangeFilter(after, before time.Time) EventFilter {
    return func(e *event.Event) bool {
        if !after.IsZero() && e.Timestamp.Before(after) {
            return false
        }
        if !before.IsZero() && e.Timestamp.After(before) {
            return false
        }
        return true
    }
}

// ---- Built-in Event Middlewares ----

// LoggingMiddleware logs event processing.
func LoggingMiddleware() EventMiddleware {
    return func(ec *EventContext, next NextFunc) error {
        log.Debugf("Processing event: %s, author: %s", ec.Event.ID, ec.Event.Author)
        err := next()
        if err != nil {
            log.Errorf("Event processing failed: %v", err)
        }
        return err
    }
}

// ValidationMiddleware validates event before persistence.
func ValidationMiddleware() EventMiddleware {
    return func(ec *EventContext, next NextFunc) error {
        if ec.Event == nil {
            ec.Abort = true
            ec.AbortErr = fmt.Errorf("event is nil")
            return ec.AbortErr
        }
        return next()
    }
}

// SafetyCheckMiddleware checks content safety.
func SafetyCheckMiddleware(checker SafetyChecker) EventMiddleware {
    return func(ec *EventContext, next NextFunc) error {
        // Check before persistence
        if ec.Event.IsUserMessage() {
            if !checker.CheckInput(ec.Event.GetContent()) {
                ec.Event.SetExcluded("unsafe_input")
            }
        }
        if ec.Event.IsAssistantMessage() {
            if !checker.CheckOutput(ec.Event.GetContent()) {
                ec.Event.SetExcluded("unsafe_output")
            }
        }
        return next()
    }
}

// MetadataEnrichMiddleware enriches event with custom metadata.
func MetadataEnrichMiddleware(enricher func(e *event.Event)) EventMiddleware {
    return func(ec *EventContext, next NextFunc) error {
        enricher(ec.Event)
        return next()
    }
}

// ---- Built-in Session Middlewares ----

// FilteringMiddleware applies event filters to session.
func FilteringMiddleware(chain *MiddlewareChain) SessionMiddleware {
    return func(sc *SessionContext, next NextFunc) error {
        // Execute next first to get session data
        if err := next(); err != nil {
            return err
        }
        // Apply filters after session is loaded
        chain.ApplyFilters(sc.Session)
        return nil
    }
}

// CachingMiddleware caches session data.
func CachingMiddleware(cache SessionCache) SessionMiddleware {
    return func(sc *SessionContext, next NextFunc) error {
        // Try cache first
        if cached, ok := cache.Get(sc.Key); ok {
            sc.Session = cached
            return nil
        }
        // Cache miss, call next
        if err := next(); err != nil {
            return err
        }
        // Store in cache
        cache.Set(sc.Key, sc.Session)
        return nil
    }
}
```

#### 2.5.4 Service Options 扩展

```go
// session/redis/options.go

// WithEventMiddleware adds event processing middleware.
func WithEventMiddleware(mw ...session.EventMiddleware) ServiceOpt {
    return func(opts *ServiceOpts) {
        opts.eventMiddlewares = append(opts.eventMiddlewares, mw...)
    }
}

// WithSessionMiddleware adds session retrieval middleware.
func WithSessionMiddleware(mw ...session.SessionMiddleware) ServiceOpt {
    return func(opts *ServiceOpts) {
        opts.sessionMiddlewares = append(opts.sessionMiddlewares, mw...)
    }
}

// WithEventFilter adds event filter.
func WithEventFilter(f ...session.EventFilter) ServiceOpt {
    return func(opts *ServiceOpts) {
        opts.eventFilters = append(opts.eventFilters, f...)
    }
}
```

#### 2.5.5 Service 集成

```go
// session/redis/service.go

func (s *Service) AppendEvent(ctx context.Context, sess *Session, e *event.Event, opts ...Option) error {
    ec := &EventContext{
        Context: ctx,
        Session: sess,
        Event:   e,
        Key:     Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID},
    }
    
    // Run middleware chain with persistence as final handler
    return s.middlewareChain.RunEventMiddleware(ec, func() error {
        if ec.Abort {
            return ec.AbortErr
        }
        return s.persistEvent(ctx, ec.Key, ec.Event)
    })
}

func (s *Service) GetSession(ctx context.Context, key Key, opts ...Option) (*Session, error) {
    opt := applyOptions(opts...)
    sc := &SessionContext{
        Context: ctx,
        Key:     key,
        Options: opt,
    }
    
    // Run middleware chain with actual retrieval as final handler
    err := s.middlewareChain.RunSessionMiddleware(sc, func() error {
        sess, err := s.getSessionFromStorage(ctx, key, opt)
        if err != nil {
            return err
        }
        sc.Session = sess
        return nil
    })
    
    return sc.Session, err
}
```

---

## 三、安全场景使用示例

### 3.1 Middleware 方式（推荐）

```go
// Create middleware chain
chain := session.NewMiddlewareChain()

// Add event middlewares
chain.UseEvent(
    session.LoggingMiddleware(),           // Logging
    session.ValidationMiddleware(),        // Validation
    session.SafetyCheckMiddleware(checker), // Safety check
)

// Add event filters (applied on GetSession)
chain.UseFilter(
    session.ExcludedFilter(),              // Filter excluded events
    session.TimeRangeFilter(startTime, time.Time{}), // Time range
)

// Add session middlewares
chain.UseSession(
    session.FilteringMiddleware(chain),    // Apply filters
)

// Create service with middleware
svc, _ := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithMiddlewareChain(chain),
)
```

### 3.2 自定义 Middleware 示例

```go
// Custom audit middleware
func AuditMiddleware(auditLog AuditLogger) session.EventMiddleware {
    return func(ec *session.EventContext, next session.NextFunc) error {
        // Before: log start
        auditLog.LogStart(ec.Event.ID, ec.Session.ID)
        
        // Call next middleware
        err := next()
        
        // After: log result
        if err != nil {
            auditLog.LogError(ec.Event.ID, err)
        } else {
            auditLog.LogSuccess(ec.Event.ID)
        }
        return err
    }
}

// Custom rate limiting middleware
func RateLimitMiddleware(limiter RateLimiter) session.EventMiddleware {
    return func(ec *session.EventContext, next session.NextFunc) error {
        if !limiter.Allow(ec.Session.UserID) {
            ec.Abort = true
            ec.AbortErr = ErrRateLimited
            return ec.AbortErr
        }
        return next()
    }
}

// Custom content transformation middleware
func ContentTransformMiddleware(transformer ContentTransformer) session.EventMiddleware {
    return func(ec *session.EventContext, next session.NextFunc) error {
        // Transform before persistence
        ec.Event = transformer.Transform(ec.Event)
        
        err := next()
        
        // Post-processing after persistence
        if err == nil {
            transformer.OnSuccess(ec.Event)
        }
        return err
    }
}
```

### 3.3 自定义 Filter 示例

```go
// Filter by safety score
safetyFilter := session.MetadataFilter("safetyScore", func(v any) bool {
    score, ok := v.(float64)
    return !ok || score >= 0.8  // Keep if score >= 0.8 or no score
})

// Filter by author
authorFilter := func(allowedAuthors ...string) session.EventFilter {
    allowed := make(map[string]struct{})
    for _, a := range allowedAuthors {
        allowed[a] = struct{}{}
    }
    return func(e *event.Event) bool {
        _, ok := allowed[e.Author]
        return ok
    }
}

// Composite filter
compositeFilter := func(filters ...session.EventFilter) session.EventFilter {
    return func(e *event.Event) bool {
        for _, f := range filters {
            if !f(e) {
                return false
            }
        }
        return true
    }
}

// Usage
chain.UseFilter(
    session.ExcludedFilter(),
    safetyFilter,
    authorFilter("user", "assistant"),
)
```

### 3.4 完整使用流程

```go
// 1. Setup
chain := session.NewMiddlewareChain().
    UseEvent(
        session.LoggingMiddleware(),
        session.SafetyCheckMiddleware(safetyChecker),
        AuditMiddleware(auditLogger),
    ).
    UseFilter(
        session.ExcludedFilter(),
    ).
    UseSession(
        session.FilteringMiddleware(chain),
    )

svc, _ := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithMiddlewareChain(chain),
)

// 2. Append events (middleware auto-processes)
svc.AppendEvent(ctx, sess, userEvent)   // → Logging → Safety → Audit → Persist
svc.AppendEvent(ctx, sess, modelEvent)  // → Logging → Safety → Audit → Persist

// 3. Get filtered session
sess, _ := svc.GetSession(ctx, key)     // → Load → Filter excluded → Return
// sess.Events only contains safe events

// 4. Get unfiltered session for audit
auditSvc, _ := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    // No filters
)
fullSess, _ := auditSvc.GetSession(ctx, key)
// fullSess.Events contains all events
```

### 3.5 QA 连续性保证

```
Middleware Chain Flow:
  
  AppendEvent(Q1) → [Logging] → [Safety:OK] → [Audit] → Persist ✓
  AppendEvent(A1) → [Logging] → [Safety:OK] → [Audit] → Persist ✓
  AppendEvent(Q2) → [Logging] → [Safety:FAIL→excluded] → [Audit] → Persist ✓
  AppendEvent(A2) → [Logging] → [Safety:FAIL→excluded] → [Audit] → Persist ✓
  AppendEvent(Q3) → [Logging] → [Safety:OK] → [Audit] → Persist ✓
  AppendEvent(A3) → [Logging] → [Safety:OK] → [Audit] → Persist ✓

GetSession Flow:
  
  Load all events → [ExcludedFilter] → Return [Q1, A1, Q3, A3]
  
Storage:  [Q1, A1, Q2(excluded), A2(excluded), Q3, A3]
Context:  [Q1, A1, Q3, A3]  ← Continuous QA pairs
```

---

## 四、自定义数据使用示例

```go
// Task interruption tracking
helper := session.NewStateHelper(sess.State)

// Set task status
helper.SetBool("taskInterrupted", true)
helper.SetString("interruptReason", "user_cancel")
helper.SetCustom("taskProgress", map[string]any{
    "step":     3,
    "total":    5,
    "lastFile": "config.yaml",
})

// Persist state
svc.UpdateSessionState(ctx, key, sess.State)

// Later: retrieve task status
interrupted, _ := helper.GetBool("taskInterrupted")
if interrupted {
    reason, _ := helper.GetString("interruptReason")
    var progress map[string]any
    helper.GetCustom("taskProgress", &progress)
    // Resume from progress...
}
```

---

## 五、实现计划

| 阶段 | 功能 | 优先级 | 工作量 | 文件 |
|------|------|--------|--------|------|
| **Phase 1** | Event.Metadata + IsExcluded/SetExcluded | P0 | 0.5d | `event/event.go` |
| **Phase 1** | Options.ExcludeMarked + ApplyEventFiltering | P0 | 0.5d | `session/session.go` |
| **Phase 2** | StateHelper (typed state access) | P1 | 0.5d | `session/state_helper.go` (new) |
| **Phase 2** | EventFilter + Built-in Filters | P1 | 0.5d | `session/filter.go` (new) |
| **Phase 3** | Middleware 核心 (EventMiddleware/SessionMiddleware) | P1 | 1d | `session/middleware.go` (new) |
| **Phase 3** | MiddlewareChain + next() 链式调用 | P1 | 0.5d | `session/middleware.go` |
| **Phase 3** | Built-in Middlewares | P2 | 1d | `session/middleware_builtin.go` (new) |
| **Phase 4** | Service 集成 (redis/mysql/postgres/inmemory) | P2 | 2d | 各 `service.go` |
| **Phase 4** | Service.MarkEventExcluded | P2 | 0.5d | `session/redis/service.go` |

**总工作量**: ~7 人天

---

## 六、兼容性考虑

1. **Event Metadata**: 新增字段，JSON `omitempty`，对旧数据无影响
2. **Options.ExcludeMarked**: 默认 `false`，不改变现有行为
3. **StateHelper**: 纯工具类，不影响存储格式
4. **Middleware 机制**: 可选配置，不配置则无额外开销
5. **EventFilter**: 可选配置，默认不过滤
6. **新 Service 方法**: 接口扩展，现有实现需补充（inmemory/mysql/postgres/redis）

---

## 七、替代方案对比

| 方案 | 优点 | 缺点 |
|------|------|------|
| **Middleware + Filter (本方案)** | 灵活、可组合、链式控制 | 学习成本略高 |
| 简单 Hook 接口 | 实现简单 | 无法控制执行流程 |
| 独立 ExcludedEvents 列表 | 不修改 Event | 需额外存储、查询复杂 |
| 删除不合规 Event | 简单 | 丢失审计数据 |

---

## 八、Middleware vs Hook 对比

| 特性 | Middleware (next 模式) | Hook (回调模式) |
|------|------------------------|-----------------|
| 流程控制 | ✅ 可中断、可跳过 | ❌ 只能顺序执行 |
| 前后处理 | ✅ 同一函数内 before/after | ❌ 需分开实现 |
| 错误处理 | ✅ 可在 next() 后处理 | ❌ 错误后无法恢复 |
| 上下文传递 | ✅ Context 对象贯穿 | ❌ 需额外参数 |
| 组合能力 | ✅ 链式组合 | ⚠️ 有限 |
| 实现复杂度 | 中等 | 简单 |

---

## 九、后续扩展方向

1. **Middleware 优先级**: 支持 Order 字段控制执行顺序
2. **异步 Middleware**: 支持非阻塞的后处理
3. **条件 Middleware**: 根据条件决定是否执行
4. **Middleware 组**: 将多个 Middleware 打包为一个
5. **Filter 组合器**: AND/OR/NOT 组合多个 Filter
6. **动态 Filter**: 运行时添加/移除 Filter
7. **Event 版本控制**: 支持 Event 内容修改历史
8. **批量 Metadata 更新**: 支持按条件批量更新 Event Metadata
