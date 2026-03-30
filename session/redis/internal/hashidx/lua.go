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

// luaCreateSession atomically creates a session meta key (via SET NX) and registers
// the session ID in the session index Hash.
// KEYS[1] = sessionMeta key, KEYS[2] = session index Hash key
// ARGV[1] = metaJSON, ARGV[2] = sessionID, ARGV[3] = TTL (seconds, 0 = no TTL)
// ARGV[4] = index entry JSON (structured value for the index Hash field)
// Returns: 1 on success, 0 if session already exists
var luaCreateSession = redis.NewScript(`
local metaKey = KEYS[1]
local indexKey = KEYS[2]
local metaJSON = ARGV[1]
local sessionID = ARGV[2]
local ttl = tonumber(ARGV[3])
local indexEntry = ARGV[4]

-- SET NX: only create if not exists
local ok = redis.call('SET', metaKey, metaJSON, 'NX')
if not ok then
    return 0
end

-- Register session ID in the index Hash with structured metadata
redis.call('HSET', indexKey, sessionID, indexEntry)

if ttl > 0 then
    redis.call('EXPIRE', metaKey, ttl)
end

return 1
`)

// luaUpdateSessionState atomically merges a session state patch into session meta.
// KEYS[1] = sessionMeta key
// ARGV[1] = statePatchJSON, ARGV[2] = nilKeysJSON, ARGV[3] = updatedAt RFC3339 string, ARGV[4] = TTL (seconds)
// Returns: 1 on success, 0 if session not found
var luaUpdateSessionState = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local statePatchJSON = ARGV[1]
local nilKeysJSON = ARGV[2]
local updatedAt = ARGV[3]
local ttl = tonumber(ARGV[4])
-- Use a simple placeholder string, then replace its quoted JSON form with null after encoding.
local nilSentinel = "__TRPC_AGENT_GO_NULL__"

local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    return 0
end

local meta = cjson.decode(metaJSON)
if not meta or type(meta) ~= 'table' then
    return redis.error_reply('unmarshal session meta')
end
if not meta.state or type(meta.state) ~= 'table' then
    meta.state = {}
end
local statePatch = cjson.decode(statePatchJSON)
if statePatch and type(statePatch) == 'table' then
    for k, v in pairs(statePatch) do
        meta.state[k] = v
    end
end
local nilKeys = cjson.decode(nilKeysJSON)
if nilKeys and type(nilKeys) == 'table' then
    for _, k in ipairs(nilKeys) do
        meta.state[k] = nilSentinel
    end
end
meta.updatedAt = updatedAt

local encodedMeta = cjson.encode(meta)
encodedMeta = string.gsub(encodedMeta, '"' .. nilSentinel .. '"', 'null')

if ttl > 0 then
    redis.call('SET', sessionMetaKey, encodedMeta, 'EX', ttl)
else
    redis.call('SET', sessionMetaKey, encodedMeta, 'KEEPTTL')
end

return 1
`)

// luaAppendEvent appends an event atomically and applies StateDelta to session state.
// KEYS[1] = sessionMeta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key
// ARGV[1] = eventID, ARGV[2] = eventJSON, ARGV[3] = timestamp, ARGV[4] = TTL (seconds), ARGV[5] = shouldStoreEvent (1 or 0)
// Returns: 1 on success, 0 if session not found
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
if shouldStoreEvent then
    redis.call('HSET', evtDataKey, eventID, eventJSON)
    redis.call('ZADD', evtTimeKey, timestamp, eventID)
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

-- 4. Refresh TTL on event data keys
if ttl > 0 then
    redis.call('EXPIRE', sessionMetaKey, ttl)
    redis.call('EXPIRE', evtDataKey, ttl)
    redis.call('EXPIRE', evtTimeKey, ttl)
end

return 1
`)

// luaLoadEvents loads events by time range.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key
// ARGV[1] = offset, ARGV[2] = limit, ARGV[3] = reverse (1=latest first, 0=oldest first)
var luaLoadEvents = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local offset = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local reverse = tonumber(ARGV[3]) == 1

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

return result
`)

// luaSummarySetIfNewer atomically merges one filterKey summary into the stored
// JSON map (String key) only if the incoming UpdatedAt is newer-or-equal.
//
// KEYS[1] = summaryKey (String containing JSON map of all filterKey summaries)
// ARGV[1] = filterKey
// ARGV[2] = newSummaryJSON (single Summary, e.g. {"summary":"...","updated_at":"..."})
// ARGV[3] = TTL (seconds, 0 = no TTL)
//
// Returns 1 if updated, 0 if skipped (existing is newer).
var luaSummarySetIfNewer = redis.NewScript(`
local sumKey = KEYS[1]
local fk = ARGV[1]
local newSum = cjson.decode(ARGV[2])
local ttl = tonumber(ARGV[3])

local cur = redis.call('GET', sumKey)
if not cur or cur == '' then
    local m = {}
    m[fk] = newSum
    if ttl > 0 then
        redis.call('SET', sumKey, cjson.encode(m), 'EX', ttl)
    else
        redis.call('SET', sumKey, cjson.encode(m))
    end
    return 1
end

local map = cjson.decode(cur)
local old = map[fk]

local old_ts = old and old['updated_at'] or nil
local new_ts = newSum and newSum['updated_at'] or nil

if not old or (old_ts and new_ts and old_ts <= new_ts) then
    map[fk] = newSum
    redis.call('SET', sumKey, cjson.encode(map), 'KEEPTTL')
    return 1
end
return 0
`)

// luaLoadSessionData loads core session data in a single Lua call (except appState and tracks).
// Tracks are loaded separately via pipeline (RT2) to avoid cjson empty-array quirks.
//
// KEYS layout (all {userID}-scoped, same Redis Cluster slot):
//
//	KEYS[1] = evtdata key (HASH)
//	KEYS[2] = evtidx:time key (ZSET)
//	KEYS[3] = sessionMeta key (STRING)
//	KEYS[4] = summaryKey (STRING, JSON map of filterKey -> Summary)
//	KEYS[5] = userStateKey (HASH)
//
// Returns: cjson-encoded table:
//
//	{
//	  "events": [eventJSON, ...],                       -- all events in chronological order
//	  "summary": "..." or nil,                          -- raw summary JSON string (entire map)
//	  "userState": {"key": "value", ...} or nil,        -- user state map
//	}
var luaLoadSessionData = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local summaryKey = KEYS[4]
local userStateKey = KEYS[5]

local result = {}

-- 1. Load events (chronological order)
local eventIDs = redis.call('ZRANGE', evtTimeKey, 0, -1)
local events = {}
if #eventIDs > 0 then
    local dataList = redis.call('HMGET', evtDataKey, unpack(eventIDs))
    for _, data in ipairs(dataList) do
        if data then table.insert(events, data) end
    end
end
result['events'] = events

-- 2. Load summary (String key containing entire JSON map)
local sumData = redis.call('GET', summaryKey)
if sumData then
    result['summary'] = sumData
end

-- 3. Load user state
local userState = redis.call('HGETALL', userStateKey)
if #userState > 0 then
    local us = {}
    for i = 1, #userState, 2 do
        us[userState[i]] = userState[i + 1]
    end
    result['userState'] = us
end

return cjson.encode(result)
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

// luaDeleteSessionLegacy deletes all session data keys without index awareness.
// KEYS[1..N] = all keys to delete (meta, evtdata, evtidx:time, summary, track keys...)
var luaDeleteSessionLegacy = redis.NewScript(`
if #KEYS > 0 then
    redis.call('DEL', unpack(KEYS))
end
return 1
`)

// luaDeleteSession deletes all session data including any track keys,
// and removes the session from the session index Hash.
// KEYS[1..N-1] = keys to delete (meta, evtdata, evtidx:time, summary, track keys...)
// KEYS[N] = session index Hash key
// ARGV[1] = sessionID (field to remove from index Hash)
var luaDeleteSession = redis.NewScript(`
local indexKey = KEYS[#KEYS]
local sessionID = ARGV[1]

-- Delete all data keys (everything except the last key which is the index)
if #KEYS > 1 then
    local dataKeys = {}
    for i = 1, #KEYS - 1 do
        table.insert(dataKeys, KEYS[i])
    end
    redis.call('DEL', unpack(dataKeys))
end

-- Remove session from the index Hash
if sessionID and sessionID ~= '' then
    redis.call('HDEL', indexKey, sessionID)
end

return 1
`)

// luaAppendTrackEvent atomically generates an ID, stores a track event, updates the time index,
// and registers the track name in session meta's state.tracks.
// The sequence counter is stored as a reserved field "_seq" inside the data Hash,
// eliminating the need for a separate key.
// KEYS[1] = trkdata key (Hash, field=eventID value=TrackEvent JSON, field="_seq" = counter)
// KEYS[2] = trkidx:time key (ZSet, member=eventID, score=timestamp)
// KEYS[3] = sessionMeta key (String, for existence check and track registration)
// ARGV[1] = TrackEvent JSON
// ARGV[2] = timestamp (UnixNano)
// ARGV[3] = TTL (seconds, 0 = no TTL)
// ARGV[4] = updated tracks value (base64-encoded JSON array, to set as state.tracks)
// Returns: generated eventID (integer) on success, 0 if session not found.
var luaAppendTrackEvent = redis.NewScript(`
local dataKey = KEYS[1]
local idxKey = KEYS[2]
local metaKey = KEYS[3]

local payload = ARGV[1]
local ts = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local tracksVal = ARGV[4]

-- Check session exists and read meta
local metaJSON = redis.call('GET', metaKey)
if not metaJSON then
    return 0
end

-- Generate auto-increment ID via reserved "_seq" field in the data Hash
local id = redis.call('HINCRBY', dataKey, '_seq', 1)

-- Store event data and time index
redis.call('HSET', dataKey, id, payload)
redis.call('ZADD', idxKey, ts, id)

-- Update session meta's state.tracks with the Go-provided value
local meta = cjson.decode(metaJSON)
if not meta.state or type(meta.state) ~= 'table' then
    meta.state = {}
end
meta.state['tracks'] = tracksVal
redis.call('SET', metaKey, cjson.encode(meta), 'KEEPTTL')

-- Refresh TTL for track data keys
if ttl > 0 then
    redis.call('EXPIRE', dataKey, ttl)
    redis.call('EXPIRE', idxKey, ttl)
end

return id
`)

// luaLoadTrackEvents loads track events by time range from Hash+ZSet structure.
// KEYS[1] = trkdata key (Hash)
// KEYS[2] = trkidx:time key (ZSet)
// ARGV[1] = minScore (afterTime UnixNano, use "-inf" for no lower bound)
// ARGV[2] = maxScore (use "+inf" for no upper bound)
// ARGV[3] = limit (0 = no limit)
// Returns: list of TrackEvent JSON strings in chronological order.
var luaLoadTrackEvents = redis.NewScript(`
local dataKey = KEYS[1]
local idxKey = KEYS[2]

local minScore = ARGV[1]
local maxScore = ARGV[2]
local limit = tonumber(ARGV[3])

-- Get event IDs in chronological order (ascending score)
local eventIDs
if limit > 0 then
    -- Get the latest N by reversing, then we reverse the result
    eventIDs = redis.call('ZREVRANGEBYSCORE', idxKey, maxScore, minScore, 'LIMIT', 0, limit)
    -- Reverse to chronological order
    local reversed = {}
    for i = #eventIDs, 1, -1 do
        table.insert(reversed, eventIDs[i])
    end
    eventIDs = reversed
else
    eventIDs = redis.call('ZRANGEBYSCORE', idxKey, minScore, maxScore)
end

local result = {}
if #eventIDs > 0 then
    local dataList = redis.call('HMGET', dataKey, unpack(eventIDs))
    for _, data in ipairs(dataList) do
        if data then
            table.insert(result, data)
        end
    end
end

return result
`)
