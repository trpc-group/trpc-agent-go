# Redis Session V2 设计方案

## 当前 V1 问题分析

### 数据结构
```
# Session 状态 - Hash
sess:{appName}:{userID} -> { sessionID: JSON(SessionState) }

# 事件列表 - ZSet (score=timestamp, member=完整JSON)
event:{appName}:{userID}:{sessionID} -> ZSet<timestamp, eventJSON>
```

### 问题
1. **事件修改困难**: ZSet 的 member 是完整 JSON，修改需要先删除再添加
2. **事件删除需全量匹配**: ZRem 需要完整的 member 值
3. **无法按维度索引**: 无法按 RequestID、InvocationID 等快速查询
4. **数据冗余**: 每个事件存储完整 JSON，占用空间大
5. **Cluster 兼容性**: 使用 TxPipeline，在 Cluster 下多 key 操作受限

---

## V2 设计方案

### 核心思路
- **事件数据与索引分离**: Hash 存储事件数据，List/ZSet 存储索引
- **使用 Hash Tag**: 确保同一 session 的所有 key 在同一 slot
- **Lua 脚本**: 保证原子性，兼容 Cluster

### Key 设计 (使用 Hash Tag)

```
# 所有 key 使用相同的 hash tag: {appName:userID:sessionID}
# 确保同一 session 的数据落在同一 slot

# 1. Session 元数据 - String
meta:{app:user:sess}
  -> JSON{ id, appName, userID, state, createdAt, updatedAt }

# 2. 事件数据 - Hash (field=eventID, value=eventJSON)
evtdata:{app:user:sess}
  -> { eventID1: JSON, eventID2: JSON, ... }

# 3. 事件时间索引 - ZSet (score=timestamp, member=eventID)
evtidx:time:{app:user:sess}
  -> ZSet<timestamp, eventID>

# 4. RequestID 索引 - Hash (field=requestID, value=eventID列表JSON)
evtidx:req:{app:user:sess}
  -> { requestID1: ["evt1","evt2"], requestID2: ["evt3"] }

# 5. InvocationID 索引 - Hash (可选，按需启用)
evtidx:inv:{app:user:sess}
  -> { invocationID: ["evt1","evt2"] }

# 6. Track 事件 - 复用相同模式
trackdata:{app:user:sess}:{track}
  -> { eventID: JSON }
trackidx:time:{app:user:sess}:{track}
  -> ZSet<timestamp, eventID>
```

### Hash Tag 格式
```go
func hashTag(key session.Key) string {
    return fmt.Sprintf("{%s:%s:%s}", key.AppName, key.UserID, key.SessionID)
}

func metaKey(key session.Key) string {
    return fmt.Sprintf("meta:%s", hashTag(key))
}

func eventDataKey(key session.Key) string {
    return fmt.Sprintf("evtdata:%s", hashTag(key))
}

func eventTimeIndexKey(key session.Key) string {
    return fmt.Sprintf("evtidx:time:%s", hashTag(key))
}

func eventRequestIndexKey(key session.Key) string {
    return fmt.Sprintf("evtidx:req:%s", hashTag(key))
}
```

---

## 核心操作 Lua 脚本

### 1. AppendEvent

```lua
-- KEYS[1] = meta key
-- KEYS[2] = evtdata key
-- KEYS[3] = evtidx:time key
-- KEYS[4] = evtidx:req key
-- ARGV[1] = eventID
-- ARGV[2] = eventJSON
-- ARGV[3] = timestamp (score)
-- ARGV[4] = requestID
-- ARGV[5] = TTL (0=不设置)

-- 更新事件数据
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])

-- 更新时间索引
redis.call('ZADD', KEYS[3], ARGV[3], ARGV[1])

-- 更新 RequestID 索引
if ARGV[4] ~= '' then
    local reqEvents = redis.call('HGET', KEYS[4], ARGV[4])
    local eventList = {}
    if reqEvents then
        eventList = cjson.decode(reqEvents)
    end
    table.insert(eventList, ARGV[1])
    redis.call('HSET', KEYS[4], ARGV[4], cjson.encode(eventList))
end

-- 设置 TTL
local ttl = tonumber(ARGV[5])
if ttl > 0 then
    redis.call('EXPIRE', KEYS[1], ttl)
    redis.call('EXPIRE', KEYS[2], ttl)
    redis.call('EXPIRE', KEYS[3], ttl)
    redis.call('EXPIRE', KEYS[4], ttl)
end

return 1
```

### 2. UpdateEvent

```lua
-- KEYS[1] = evtdata key
-- ARGV[1] = eventID
-- ARGV[2] = newEventJSON

-- 检查事件是否存在
local exists = redis.call('HEXISTS', KEYS[1], ARGV[1])
if exists == 0 then
    return 0
end

-- 直接更新
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
return 1
```

### 3. DeleteEvent

```lua
-- KEYS[1] = evtdata key
-- KEYS[2] = evtidx:time key
-- KEYS[3] = evtidx:req key
-- ARGV[1] = eventID
-- ARGV[2] = requestID

-- 删除事件数据
redis.call('HDEL', KEYS[1], ARGV[1])

-- 删除时间索引
redis.call('ZREM', KEYS[2], ARGV[1])

-- 更新 RequestID 索引
if ARGV[2] ~= '' then
    local reqEvents = redis.call('HGET', KEYS[3], ARGV[2])
    if reqEvents then
        local eventList = cjson.decode(reqEvents)
        local newList = {}
        for _, eid in ipairs(eventList) do
            if eid ~= ARGV[1] then
                table.insert(newList, eid)
            end
        end
        if #newList > 0 then
            redis.call('HSET', KEYS[3], ARGV[2], cjson.encode(newList))
        else
            redis.call('HDEL', KEYS[3], ARGV[2])
        end
    end
end

return 1
```

### 4. TrimConversations

```lua
-- KEYS[1] = evtdata key
-- KEYS[2] = evtidx:time key
-- KEYS[3] = evtidx:req key
-- ARGV[1] = count (要删除的会话数)

local count = tonumber(ARGV[1])
local targetReqIDs = {}
local toDelete = {}
local offset = 0
local batchSize = 100

-- 从最新事件开始扫描
while #targetReqIDs < count do
    local eventIDs = redis.call('ZREVRANGE', KEYS[2], offset, offset + batchSize - 1)
    if #eventIDs == 0 then
        break
    end

    for _, eventID in ipairs(eventIDs) do
        local eventJSON = redis.call('HGET', KEYS[1], eventID)
        if eventJSON then
            local event = cjson.decode(eventJSON)
            local reqID = event.requestID or ''

            if reqID ~= '' then
                if not targetReqIDs[reqID] then
                    if #toDelete > 0 and #targetReqIDs >= count then
                        break
                    end
                    targetReqIDs[reqID] = true
                end

                if targetReqIDs[reqID] then
                    table.insert(toDelete, {id = eventID, reqID = reqID, json = eventJSON})
                end
            end
        end
    end

    offset = offset + batchSize
end

-- 执行删除
local deleted = {}
for _, item in ipairs(toDelete) do
    redis.call('HDEL', KEYS[1], item.id)
    redis.call('ZREM', KEYS[2], item.id)
    table.insert(deleted, item.json)

    -- 更新 RequestID 索引
    -- (简化: 直接删除整个 requestID 的索引)
end

-- 清理 RequestID 索引
for reqID, _ in pairs(targetReqIDs) do
    redis.call('HDEL', KEYS[3], reqID)
end

return deleted
```

### 5. LoadEvents (分页)

```lua
-- KEYS[1] = evtdata key
-- KEYS[2] = evtidx:time key
-- ARGV[1] = offset
-- ARGV[2] = limit

local eventIDs = redis.call('ZRANGE', KEYS[2], ARGV[1], ARGV[1] + ARGV[2] - 1)
local events = {}

for _, eventID in ipairs(eventIDs) do
    local eventJSON = redis.call('HGET', KEYS[1], eventID)
    if eventJSON then
        table.insert(events, eventJSON)
    end
end

return events
```


---

## 索引抽象设计 (internal)

所有索引相关类型均为 internal，不对外暴露。

```go
// eventIndex 定义事件索引的抽象接口 (internal)
type eventIndex interface {
    // name 返回索引名称，用于生成 Redis key
    name() string

    // extractKey 从事件中提取索引 key
    // 返回空字符串表示该事件不需要此索引
    extractKey(evt *event.Event) string

    // buildRedisKey 构建完整的 Redis key
    buildRedisKey(sessionHashTag string) string
}

// requestIDIndex RequestID 索引实现 (internal)
type requestIDIndex struct{}

func (i *requestIDIndex) name() string { return "req" }

func (i *requestIDIndex) extractKey(evt *event.Event) string {
    return evt.RequestID
}

func (i *requestIDIndex) buildRedisKey(tag string) string {
    return fmt.Sprintf("evtidx:req:%s", tag)
}

// 默认启用的索引 (internal)
var defaultIndexes = []eventIndex{
    &requestIDIndex{},
}
```

### Lua 脚本中的索引处理

```lua
-- AppendEvent 时，动态处理多个索引
-- KEYS[n+1..n+m] = 各索引的 key
-- ARGV[n+1..n+m] = 各索引的 extractKey 值

for i = 1, indexCount do
    local indexKey = KEYS[baseIdx + i]
    local indexValue = ARGV[baseArgIdx + i]
    if indexValue ~= '' then
        local existing = redis.call('HGET', indexKey, indexValue)
        local eventList = existing and cjson.decode(existing) or {}
        table.insert(eventList, eventID)
        redis.call('HSET', indexKey, indexValue, cjson.encode(eventList))
    end
end
```

---

## 事件自动淘汰

### 策略
在 AppendEvent 时检查事件数量，超过阈值则自动淘汰最旧的事件。

### 配置项

```go
type ServiceV2Options struct {
    // MaxEventsPerSession 每个 session 最大事件数，0 表示不限制
    MaxEventsPerSession int

    // EvictionBatchSize 每次淘汰的事件数量，默认 10
    EvictionBatchSize int
}
```

### Lua 脚本 (AppendEvent 扩展)

```lua
-- 追加事件后检查数量
local count = redis.call('ZCARD', KEYS[3])  -- evtidx:time
local maxEvents = tonumber(ARGV[6])
local evictBatch = tonumber(ARGV[7])

if maxEvents > 0 and count > maxEvents then
    -- 获取最旧的 N 个事件 ID
    local toEvict = redis.call('ZRANGE', KEYS[3], 0, evictBatch - 1)

    for _, oldEventID in ipairs(toEvict) do
        -- 获取事件数据用于清理索引
        local oldEventJSON = redis.call('HGET', KEYS[2], oldEventID)
        if oldEventJSON then
            local oldEvent = cjson.decode(oldEventJSON)

            -- 清理各索引 (简化示意)
            local reqID = oldEvent.requestID or ''
            if reqID ~= '' then
                -- 从 RequestID 索引中移除
                local reqEvents = redis.call('HGET', KEYS[4], reqID)
                if reqEvents then
                    local eventList = cjson.decode(reqEvents)
                    local newList = {}
                    for _, eid in ipairs(eventList) do
                        if eid ~= oldEventID then
                            table.insert(newList, eid)
                        end
                    end
                    if #newList > 0 then
                        redis.call('HSET', KEYS[4], reqID, cjson.encode(newList))
                    else
                        redis.call('HDEL', KEYS[4], reqID)
                    end
                end
            end

            -- 删除事件数据
            redis.call('HDEL', KEYS[2], oldEventID)
        end

        -- 删除时间索引
        redis.call('ZREM', KEYS[3], oldEventID)
    end
end
```

---

## 设计决策

### 1. 索引一致性保障

**建议：不需要定期 repair 任务**

理由：
- Lua 脚本在 Redis 中是原子执行的，要么全部成功，要么全部失败
- 不存在"执行到一半失败"的情况
- 唯一的风险是 Redis 宕机导致数据丢失，但这属于持久化层面的问题，与索引一致性无关

如果确实担心，可以提供一个手动 repair 工具：
```go
// RepairIndexes 重建指定 session 的所有索引
func (s *ServiceV2) RepairIndexes(ctx context.Context, key session.Key) error
```

### 2. App/User State 设计

**建议：保持现有设计，不重构**

理由：
- App/User State 是简单的 KV 结构，当前 Hash 设计已经够用
- 不存在"修改困难"的问题
- 保持稳定，减少迁移成本

### 3. Track Event 设计

**建议：复用 V2 模式**

理由：
- Track Event 的痛点与普通 Event 相同（删除/修改困难）
- 统一架构，减少维护成本
- 可能也需要按维度索引（如按 Track 名称快速查询）

结构：
```
trackdata:{app:user:sess}:{track}   -> Hash<eventID, JSON>
trackidx:time:{app:user:sess}:{track} -> ZSet<timestamp, eventID>
```

---

## API 设计

V2 实现现有 `session.Service` 接口，不新增接口方法。

扩展能力（如 `TrimConversations`、按 RequestID 查询等）作为 `*ServiceV2` 的具体方法暴露，不改变接口定义。

---

## 迁移策略

1. **V2 作为独立实现**: 不影响 V1
2. **Key 前缀区分**: V2 使用 `v2:` 前缀或不同的 key 模式
3. **可选迁移工具**: 提供 V1 -> V2 的数据迁移脚本

---

## 优势总结

| 特性 | V1 | V2 |
|------|----|----|
| 事件修改 | 需删除+添加完整JSON | 直接 HSET 更新 |
| 事件删除 | 需完整 member 匹配 | 按 eventID 删除 |
| RequestID 查询 | 全量扫描 | O(1) 索引查询 |
| 存储效率 | JSON 冗余 | 数据与索引分离 |
| Cluster 兼容 | TxPipeline 受限 | Hash Tag + Lua |
| 扩展索引 | 困难 | 新增 Hash 即可 |

