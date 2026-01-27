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

// luaAppendEvent appends an event and updates indexes.
// KEYS[1] = meta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key, KEYS[4] = evtidx:req key
// ARGV[1] = eventID, ARGV[2] = eventJSON, ARGV[3] = timestamp, ARGV[4] = requestID
// ARGV[5] = TTL (0=no expire), ARGV[6] = maxEvents (0=no limit), ARGV[7] = evictBatchSize
var luaAppendEvent = redis.NewScript(`
local metaKey = KEYS[1]
local evtDataKey = KEYS[2]
local evtTimeKey = KEYS[3]
local evtReqKey = KEYS[4]

local eventID = ARGV[1]
local eventJSON = ARGV[2]
local timestamp = tonumber(ARGV[3])
local requestID = ARGV[4]
local ttl = tonumber(ARGV[5])
local maxEvents = tonumber(ARGV[6])
local evictBatch = tonumber(ARGV[7])

redis.call('HSET', evtDataKey, eventID, eventJSON)
redis.call('ZADD', evtTimeKey, timestamp, eventID)

if requestID ~= '' then
    local reqEvents = redis.call('HGET', evtReqKey, requestID)
    local eventList = {}
    if reqEvents then
        eventList = cjson.decode(reqEvents)
    end
    table.insert(eventList, eventID)
    redis.call('HSET', evtReqKey, requestID, cjson.encode(eventList))
end

if maxEvents > 0 then
    local count = redis.call('ZCARD', evtTimeKey)
    if count > maxEvents then
        local toEvict = redis.call('ZRANGE', evtTimeKey, 0, evictBatch - 1)
        for _, oldEventID in ipairs(toEvict) do
            local oldEventJSON = redis.call('HGET', evtDataKey, oldEventID)
            if oldEventJSON then
                local oldEvent = cjson.decode(oldEventJSON)
                local oldReqID = oldEvent.requestID or ''
                if oldReqID ~= '' then
                    local oldReqEvents = redis.call('HGET', evtReqKey, oldReqID)
                    if oldReqEvents then
                        local oldList = cjson.decode(oldReqEvents)
                        local newList = {}
                        for _, eid in ipairs(oldList) do
                            if eid ~= oldEventID then
                                table.insert(newList, eid)
                            end
                        end
                        if #newList > 0 then
                            redis.call('HSET', evtReqKey, oldReqID, cjson.encode(newList))
                        else
                            redis.call('HDEL', evtReqKey, oldReqID)
                        end
                    end
                end
                redis.call('HDEL', evtDataKey, oldEventID)
            end
            redis.call('ZREM', evtTimeKey, oldEventID)
        end
    end
end

if ttl > 0 then
    redis.call('EXPIRE', metaKey, ttl)
    redis.call('EXPIRE', evtDataKey, ttl)
    redis.call('EXPIRE', evtTimeKey, ttl)
    redis.call('EXPIRE', evtReqKey, ttl)
end

return 1
`)

// luaLoadEvents loads events by time range.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key
// ARGV[1] = offset, ARGV[2] = limit (-1 for all)
var luaLoadEvents = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local offset = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])

local endIdx
if limit < 0 then
    endIdx = -1
else
    endIdx = offset + limit - 1
end

local eventIDs = redis.call('ZRANGE', evtTimeKey, offset, endIdx)
local events = {}

for _, eventID in ipairs(eventIDs) do
    local eventJSON = redis.call('HGET', evtDataKey, eventID)
    if eventJSON then
        table.insert(events, eventJSON)
    end
end

return events
`)

// luaDeleteEvent deletes an event and updates indexes.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = evtidx:req key
// ARGV[1] = eventID, ARGV[2] = requestID
var luaDeleteEvent = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local evtReqKey = KEYS[3]
local eventID = ARGV[1]
local requestID = ARGV[2]

redis.call('HDEL', evtDataKey, eventID)
redis.call('ZREM', evtTimeKey, eventID)

if requestID ~= '' then
    local reqEvents = redis.call('HGET', evtReqKey, requestID)
    if reqEvents then
        local eventList = cjson.decode(reqEvents)
        local newList = {}
        for _, eid in ipairs(eventList) do
            if eid ~= eventID then
                table.insert(newList, eid)
            end
        end
        if #newList > 0 then
            redis.call('HSET', evtReqKey, requestID, cjson.encode(newList))
        else
            redis.call('HDEL', evtReqKey, requestID)
        end
    end
end

return 1
`)

// luaTrimConversations trims the most recent N conversations.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key, KEYS[3] = evtidx:req key
// ARGV[1] = count (number of conversations to trim)
var luaTrimConversations = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local evtReqKey = KEYS[3]
local count = tonumber(ARGV[1])

local targetReqIDs = {}
local targetReqCount = 0
local toDelete = {}
local offset = 0
local batchSize = 100

while targetReqCount < count do
    local eventIDs = redis.call('ZREVRANGE', evtTimeKey, offset, offset + batchSize - 1)
    if #eventIDs == 0 then break end

    for _, eventID in ipairs(eventIDs) do
        local eventJSON = redis.call('HGET', evtDataKey, eventID)
        if eventJSON then
            local evt = cjson.decode(eventJSON)
            local reqID = evt.requestID or ''
            if reqID ~= '' then
                if not targetReqIDs[reqID] then
                    if targetReqCount >= count then break end
                    targetReqIDs[reqID] = true
                    targetReqCount = targetReqCount + 1
                end
                if targetReqIDs[reqID] then
                    table.insert(toDelete, {id = eventID, json = eventJSON})
                end
            end
        end
    end
    if targetReqCount >= count then break end
    offset = offset + batchSize
end

local deleted = {}
for _, item in ipairs(toDelete) do
    redis.call('HDEL', evtDataKey, item.id)
    redis.call('ZREM', evtTimeKey, item.id)
    table.insert(deleted, item.json)
end

for reqID, _ in pairs(targetReqIDs) do
    redis.call('HDEL', evtReqKey, reqID)
end

local result = {}
for i = #deleted, 1, -1 do
    table.insert(result, deleted[i])
end
return result
`)

// luaGetEventsByRequestID gets all events for a RequestID.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:req key
// ARGV[1] = requestID
var luaGetEventsByRequestID = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtReqKey = KEYS[2]
local requestID = ARGV[1]

local reqEvents = redis.call('HGET', evtReqKey, requestID)
if not reqEvents then return {} end

local eventIDs = cjson.decode(reqEvents)
local events = {}
for _, eventID in ipairs(eventIDs) do
    local eventJSON = redis.call('HGET', evtDataKey, eventID)
    if eventJSON then
        table.insert(events, eventJSON)
    end
end
return events
`)

// luaDeleteSession deletes all session data.
// KEYS[1] = meta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key
// KEYS[4] = evtidx:req key, KEYS[5] = summary key
var luaDeleteSession = redis.NewScript(`
redis.call('DEL', KEYS[1], KEYS[2], KEYS[3], KEYS[4], KEYS[5])
return 1
`)
