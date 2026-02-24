//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import "github.com/redis/go-redis/v9"

// luaAppendEvent appends an event atomically and applies StateDelta to session state.
// KEYS[1] = sessionMeta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key
// ARGV[1] = eventID, ARGV[2] = eventJSON, ARGV[3] = timestamp, ARGV[4] = TTL (seconds), ARGV[5] = shouldStoreEvent (1 or 0)
// Returns: 1 on success, 0 if session not found
//
// Note on TTL: Only evtdata and evtidx:time keys have TTL set here (they may be newly created
// by HSET/ZADD and need an initial TTL). sessionMeta TTL is managed by GetSession/CreateSession.
var luaAppendEvent = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local evtDataKey = KEYS[2]
local evtTimeKey = KEYS[3]

local eventID = ARGV[1]
local eventJSON = ARGV[2]
local timestamp = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local shouldStoreEvent = tonumber(ARGV[5]) == 1

-- 1. Check session meta exists first to avoid orphan events
local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    return 0
end

-- 2. Store event data only if shouldStoreEvent is true
-- This matches zset behavior: only store events with Response != nil && !IsPartial && IsValidContent()
if shouldStoreEvent then
    redis.call('HSET', evtDataKey, eventID, eventJSON)
    redis.call('ZADD', evtTimeKey, timestamp, eventID)
    -- Set TTL for event keys (they may be newly created and need an initial TTL)
    if ttl > 0 then
        redis.call('EXPIRE', evtDataKey, ttl)
        redis.call('EXPIRE', evtTimeKey, ttl)
    end
end

-- 3. Apply StateDelta to session meta's state (always, regardless of shouldStoreEvent)
local evt = cjson.decode(eventJSON)
local stateDelta = evt.stateDelta
if stateDelta and next(stateDelta) ~= nil then
    local meta = cjson.decode(metaJSON)
    if not meta.state or type(meta.state) ~= 'table' then
        meta.state = {}
    end
    for k, v in pairs(stateDelta) do
        meta.state[k] = v
    end
    redis.call('SET', sessionMetaKey, cjson.encode(meta), 'KEEPTTL')
end

return 1
`)

// luaLoadEvents loads events by time range and refreshes TTL.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = sessionMeta key
// ARGV[1] = offset, ARGV[2] = limit, ARGV[3] = TTL, ARGV[4] = reverse (1=latest first, 0=oldest first)
var luaLoadEvents = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local offset = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local reverse = tonumber(ARGV[4]) == 1

local endIdx = limit < 0 and -1 or offset + limit - 1
local eventIDs
if reverse then
    eventIDs = redis.call('ZREVRANGE', evtTimeKey, offset, endIdx)
else
    eventIDs = redis.call('ZRANGE', evtTimeKey, offset, endIdx)
end

local result = {}
if #eventIDs > 0 then
    local dataList = redis.call('HMGET', evtDataKey, unpack(eventIDs))
    for _, data in ipairs(dataList) do
        if data then table.insert(result, data) end
    end
end

-- Refresh TTL
if ttl > 0 then
    redis.call('EXPIRE', sessionMetaKey, ttl)
    redis.call('EXPIRE', evtDataKey, ttl)
    redis.call('EXPIRE', evtTimeKey, ttl)
end

return result
`)

// luaDeleteEvent deletes an event and its indexes.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key
// ARGV[1] = eventID
var luaDeleteEvent = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local eventID = ARGV[1]

redis.call('HDEL', evtDataKey, eventID)
redis.call('ZREM', evtTimeKey, eventID)

return 1
`)

// luaTrimConversations trims the most recent N conversations (by RequestID).
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key
// ARGV[1] = count
var luaTrimConversations = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local count = tonumber(ARGV[1])

local targetReqIDs = {}
local targetReqCount = 0
local toDelete = {}
local offset = 0
local batchSize = 100

while targetReqCount < count do
    local eventIDs = redis.call('ZREVRANGE', evtTimeKey, offset, offset + batchSize - 1)
    if #eventIDs == 0 then break end

    for _, eid in ipairs(eventIDs) do
        local data = redis.call('HGET', evtDataKey, eid)
        if data then
            local evt = cjson.decode(data)
            local rid = evt.requestID or ''
            if rid ~= '' then
                if not targetReqIDs[rid] then
                    if targetReqCount >= count then break end
                    targetReqIDs[rid] = true
                    targetReqCount = targetReqCount + 1
                end
                if targetReqIDs[rid] then table.insert(toDelete, eid) end
            end
        end
    end
    if targetReqCount >= count then break end
    offset = offset + batchSize
end

local result = {}
for _, eid in ipairs(toDelete) do
    local data = redis.call('HGET', evtDataKey, eid)
    table.insert(result, data)
    redis.call('HDEL', evtDataKey, eid)
    redis.call('ZREM', evtTimeKey, eid)
end

local reversed = {}
for i = #result, 1, -1 do table.insert(reversed, result[i]) end
return reversed
`)

// luaDeleteSession deletes all session data including any track keys.
// KEYS[1..N] = all keys to delete (meta, evtdata, evtidx:time, summary, track keys...)
var luaDeleteSession = redis.NewScript(`
if #KEYS > 0 then
    redis.call('DEL', unpack(KEYS))
end
return 1
`)
