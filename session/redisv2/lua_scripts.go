//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redisv2

import "github.com/redis/go-redis/v9"

// luaAppendEvent appends an event and updates custom indexes.
// KEYS[1] = sessionMeta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key, KEYS[4] = evtidx:custom key
// ARGV[1] = eventID, ARGV[2] = eventJSON, ARGV[3] = timestamp
// ARGV[4] = TTL (seconds), ARGV[5] = maxEvents, ARGV[6] = evictBatchSize, ARGV[7] = indexJSON
var luaAppendEvent = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local evtDataKey = KEYS[2]
local evtTimeKey = KEYS[3]
local evtCustomIdxKey = KEYS[4]

local eventID = ARGV[1]
local eventJSON = ARGV[2]
local timestamp = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local maxEvents = tonumber(ARGV[5])
local evictBatch = tonumber(ARGV[6])
local indexJSON = ARGV[7]

-- Helper: remove eventID from custom index
local function removeFromCustomIndex(eid)
    local fields = redis.call('HKEYS', evtCustomIdxKey)
    for _, field in ipairs(fields) do
        local idsJSON = redis.call('HGET', evtCustomIdxKey, field)
        if idsJSON then
            local ids = cjson.decode(idsJSON)
            local newIds = {}
            for _, id in ipairs(ids) do
                if id ~= eid then table.insert(newIds, id) end
            end
            if #newIds == 0 then
                redis.call('HDEL', evtCustomIdxKey, field)
            elseif #newIds < #ids then
                redis.call('HSET', evtCustomIdxKey, field, cjson.encode(newIds))
            end
        end
    end
end

-- 1. Store event data
redis.call('HSET', evtDataKey, eventID, eventJSON)

-- 2. Update time index
redis.call('ZADD', evtTimeKey, timestamp, eventID)

-- 3. Update custom indexes
if indexJSON and indexJSON ~= '' then
    local indexes = cjson.decode(indexJSON)
    for name, value in pairs(indexes) do
        local field = name .. ":" .. value
        local current = redis.call('HGET', evtCustomIdxKey, field)
        local ids = current and cjson.decode(current) or {}
        table.insert(ids, eventID)
        redis.call('HSET', evtCustomIdxKey, field, cjson.encode(ids))
    end
end

-- 4. Eviction (with custom index cleanup)
if maxEvents > 0 then
    local count = redis.call('ZCARD', evtTimeKey)
    if count > maxEvents then
        local toEvict = redis.call('ZRANGE', evtTimeKey, 0, evictBatch - 1)
        for _, oldID in ipairs(toEvict) do
            redis.call('HDEL', evtDataKey, oldID)
            redis.call('ZREM', evtTimeKey, oldID)
            removeFromCustomIndex(oldID)
        end
    end
end

-- 5. Unified TTL refresh
if ttl > 0 then
    redis.call('EXPIRE', sessionMetaKey, ttl)
    redis.call('EXPIRE', evtDataKey, ttl)
    redis.call('EXPIRE', evtTimeKey, ttl)
    redis.call('EXPIRE', evtCustomIdxKey, ttl)
end

return 1
`)

// luaLoadEvents loads events by time range and refreshes TTL.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = sessionMeta key, KEYS[4] = evtidx:custom key
// ARGV[1] = offset, ARGV[2] = limit, ARGV[3] = TTL, ARGV[4] = reverse (1=latest first, 0=oldest first)
var luaLoadEvents = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local evtCustomIdxKey = KEYS[4]
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
    redis.call('EXPIRE', evtCustomIdxKey, ttl)
end

return result
`)

// luaDeleteEvent deletes an event and its basic indexes.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = evtidx:custom key
// ARGV[1] = eventID
var luaDeleteEvent = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local evtCustomIdxKey = KEYS[3]
local eventID = ARGV[1]

redis.call('HDEL', evtDataKey, eventID)
redis.call('ZREM', evtTimeKey, eventID)

-- Clean up custom index
local fields = redis.call('HKEYS', evtCustomIdxKey)
for _, field in ipairs(fields) do
    local idsJSON = redis.call('HGET', evtCustomIdxKey, field)
    if idsJSON then
        local ids = cjson.decode(idsJSON)
        local newIds = {}
        for _, id in ipairs(ids) do
            if id ~= eventID then table.insert(newIds, id) end
        end
        if #newIds == 0 then
            redis.call('HDEL', evtCustomIdxKey, field)
        elseif #newIds < #ids then
            redis.call('HSET', evtCustomIdxKey, field, cjson.encode(newIds))
        end
    end
end

return 1
`)

// luaTrimConversations trims the most recent N conversations (by RequestID).
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = evtidx:custom key
var luaTrimConversations = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local evtCustomIdxKey = KEYS[3]
local count = tonumber(ARGV[1])

-- Helper: remove eventID from custom index
local function removeFromCustomIndex(eid)
    local fields = redis.call('HKEYS', evtCustomIdxKey)
    for _, field in ipairs(fields) do
        local idsJSON = redis.call('HGET', evtCustomIdxKey, field)
        if idsJSON then
            local ids = cjson.decode(idsJSON)
            local newIds = {}
            for _, id in ipairs(ids) do
                if id ~= eid then table.insert(newIds, id) end
            end
            if #newIds == 0 then
                redis.call('HDEL', evtCustomIdxKey, field)
            elseif #newIds < #ids then
                redis.call('HSET', evtCustomIdxKey, field, cjson.encode(newIds))
            end
        end
    end
end

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
    removeFromCustomIndex(eid)
end

local reversed = {}
for i = #result, 1, -1 do table.insert(reversed, result[i]) end
return reversed
`)

// luaDeleteSession deletes all session data.
// KEYS[1] = sessionMeta, KEYS[2] = evtdata, KEYS[3] = evtidx:time, KEYS[4] = summary, KEYS[5] = evtidx:custom
var luaDeleteSession = redis.NewScript(`
redis.call('DEL', KEYS[1], KEYS[2], KEYS[3], KEYS[4], KEYS[5])
return 1
`)
