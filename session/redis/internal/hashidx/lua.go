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
// KEYS[1] = sessionMeta key, KEYS[2] = private revision metadata key
// ARGV[1] = statePatchJSON, ARGV[2] = nilKeysJSON,
// ARGV[3] = updatedAt RFC3339 string, ARGV[4] = TTL (seconds),
// ARGV[5] = has expected generation, ARGV[6] = expected generation,
// ARGV[7] = explicit turn hazard, ARGV[8] = request ID.
// Returns: 1 on success, 0 if session not found, -1 if stale.
var luaUpdateSessionState = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local revisionKey = KEYS[2]
local statePatchJSON = ARGV[1]
local nilKeysJSON = ARGV[2]
local updatedAt = ARGV[3]
local ttl = tonumber(ARGV[4])
local hasExpectedGeneration = tonumber(ARGV[5]) == 1
local expectedGeneration = tonumber(ARGV[6])
local explicitHazard = tonumber(ARGV[7]) == 1
local requestID = ARGV[8]
-- Use a simple placeholder string, then replace its quoted JSON form with null after encoding.
local nilSentinel = "__TRPC_AGENT_GO_NULL__"

local function setPreserveTTL(key, value)
    local ttlMs = redis.call('PTTL', key)
    redis.call('SET', key, value)
    if ttlMs >= 0 then
        redis.call('PEXPIRE', key, ttlMs)
    end
end

local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    return 0
end
local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then
    revision = cjson.decode(revisionJSON)
end
if hasExpectedGeneration and tonumber(revision.generation or 0) ~= expectedGeneration then
    return -1
end
revision.head = tonumber(revision.head or 0) + 1
if revision.checkpoint and (not hasExpectedGeneration or explicitHazard or
    requestID == '' or requestID ~= revision.checkpoint.requestID) then
    revision.checkpoint.hazard = true
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
    setPreserveTTL(sessionMetaKey, encodedMeta)
end
if ttl > 0 then
    redis.call('SET', revisionKey, cjson.encode(revision), 'EX', ttl)
else
    setPreserveTTL(revisionKey, cjson.encode(revision))
end
return 1
`)

// luaAppendEvent appends an event atomically and applies StateDelta to session state.
// KEYS[1] = sessionMeta key, KEYS[2] = evtdata key, KEYS[3] = evtidx:time key,
// KEYS[4] = private revision metadata key.
// ARGV[1] = eventID, ARGV[2] = eventJSON, ARGV[3] = timestamp,
// ARGV[4] = TTL (seconds), ARGV[5] = shouldStoreEvent (1 or 0),
// ARGV[6] = has expected generation, ARGV[7] = expected generation,
// ARGV[8] = turn-start request ID, ARGV[9] = turn-start invocation ID,
// ARGV[10] = pre-turn projection boundary, ARGV[11] = runner completion,
// ARGV[12] = has expected head, ARGV[13] = expected head,
// ARGV[14] = has prepared projection, ARGV[15] = prepared projection JSON,
// ARGV[16] = boundary requires a retained summary carrier,
// ARGV[17] = explicit turn hazard.
// (empty JSON clears the projection).
// Returns: 1 on success, 0 if session not found, -1 for a stale generation,
// -2 for a stale turn-start projection.
var luaAppendEvent = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local evtDataKey = KEYS[2]
local evtTimeKey = KEYS[3]
local revisionKey = KEYS[4]
local summaryKey = KEYS[5]

local eventID = ARGV[1]
local eventJSON = ARGV[2]
local timestamp = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local shouldStoreEvent = tonumber(ARGV[5]) == 1
local hasExpectedGeneration = tonumber(ARGV[6]) == 1
local expectedGeneration = tonumber(ARGV[7])
local startRequestID = ARGV[8]
local startInvocationID = ARGV[9]
local boundaryBase64 = ARGV[10]
local runnerCompletion = tonumber(ARGV[11]) == 1
local hasExpectedHead = tonumber(ARGV[12]) == 1
local expectedHead = tonumber(ARGV[13])
local hasPreparedProjection = tonumber(ARGV[14]) == 1
local preparedProjectionJSON = ARGV[15]
local boundaryRequiresSummary = tonumber(ARGV[16]) == 1
local explicitHazard = tonumber(ARGV[17]) == 1

local function setPreserveTTL(key, value)
    local ttlMs = redis.call('PTTL', key)
    redis.call('SET', key, value)
    if ttlMs >= 0 then
        redis.call('PEXPIRE', key, ttlMs)
    end
end

-- 1. Check session meta exists first to avoid orphan events
local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    return 0
end

local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then
    revision = cjson.decode(revisionJSON)
end
local generation = tonumber(revision.generation or 0)
if hasExpectedGeneration and generation ~= expectedGeneration then
    return -1
end
local head = tonumber(revision.head or 0)
if hasExpectedHead and head ~= expectedHead then
    return -2
end
if boundaryRequiresSummary and redis.call('EXISTS', summaryKey) == 0 then
    return -3
end
local projectionAppendable = not hasPreparedProjection or preparedProjectionJSON ~= ''
if shouldStoreEvent and (hasPreparedProjection or revision.checkpoint) then
    local latest = redis.call('ZREVRANGE', evtTimeKey, 0, 0, 'WITHSCORES')
    projectionAppendable = projectionAppendable and
        redis.call('HEXISTS', evtDataKey, eventID) == 0 and
        (#latest == 0 or timestamp > tonumber(latest[2]))
end
if hasPreparedProjection then
    if projectionAppendable then
        revision.projection = cjson.decode(preparedProjectionJSON)
    else
        revision.projection = nil
    end
end
local revisionChanged = false
revision.head = head + 1
revisionChanged = true
if revision.checkpoint and (not hasExpectedGeneration or explicitHazard) then
    revision.checkpoint.hazard = true
    revisionChanged = true
end
local evt = cjson.decode(eventJSON)
local evtRequestID = evt.requestID or ''
local evtInvocationID = evt.invocationId or ''
local validStart = startRequestID ~= '' and shouldStoreEvent and
    startRequestID == evtRequestID and startInvocationID == evtInvocationID and
    boundaryBase64 ~= ''
if validStart then
    local checkpoint = revision.checkpoint
    if not checkpoint or checkpoint.terminal then
        revision.checkpoint = {
            requestID = startRequestID,
            invocationID = startInvocationID,
            priorHeadRequestID = revision.headRequestID,
            boundary = boundaryBase64,
            terminal = false,
            hazard = explicitHazard or
                (revision.headRequestID and revision.headRequestID == startRequestID)
        }
        revisionChanged = true
    elseif checkpoint.requestID ~= startRequestID or checkpoint.invocationID ~= startInvocationID then
        checkpoint.hazard = true
        revisionChanged = true
    end
end
if shouldStoreEvent and evtRequestID ~= '' then
    revision.headRequestID = evtRequestID
end

local checkpoint = revision.checkpoint
if checkpoint then
    if not projectionAppendable then
        checkpoint.hazard = true
    end
    if not validStart and checkpoint.terminal then
        checkpoint.hazard = true
    end
    if evtRequestID ~= checkpoint.requestID then
        checkpoint.hazard = true
    end
    revisionChanged = true
end

-- 2. Store event data only if shouldStoreEvent is true
if shouldStoreEvent then
    redis.call('HSET', evtDataKey, eventID, eventJSON)
    redis.call('ZADD', evtTimeKey, timestamp, eventID)
end

-- 3. Apply StateDelta to session meta's state (always, regardless of shouldStoreEvent)
local stateDelta = evt.stateDelta
if stateDelta and next(stateDelta) ~= nil then
    local meta = cjson.decode(metaJSON)
    if not meta.state or type(meta.state) ~= 'table' then
        meta.state = {}
    end
    for k, v in pairs(stateDelta) do
        meta.state[k] = v
    end
    if ttl > 0 then
        redis.call('SET', sessionMetaKey, cjson.encode(meta))
    else
        setPreserveTTL(sessionMetaKey, cjson.encode(meta))
    end
end

if runnerCompletion and revision.checkpoint then
    checkpoint = revision.checkpoint
    if checkpoint.requestID == evtRequestID and checkpoint.invocationID == evtInvocationID then
        checkpoint.terminal = true
    else
        checkpoint.hazard = true
    end
    revisionChanged = true
end

if revisionChanged or hasExpectedGeneration then
    if ttl > 0 then
        redis.call('SET', revisionKey, cjson.encode(revision), 'EX', ttl)
    else
        setPreserveTTL(revisionKey, cjson.encode(revision))
    end
end

-- 4. Refresh TTL on event data keys
if ttl > 0 then
    redis.call('EXPIRE', sessionMetaKey, ttl)
    redis.call('EXPIRE', evtDataKey, ttl)
    redis.call('EXPIRE', evtTimeKey, ttl)
end
return 1
`)

// luaLoadEvents loads one bounded batch of events in reverse chronological order.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key
// ARGV[1] = minScore, ARGV[2] = offset, ARGV[3] = batch size
// The first returned element is the number of indexed IDs scanned. The remaining
// elements are event JSON payloads; missing Hash fields are skipped.
var luaLoadEvents = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local minScore = ARGV[1]
local offset = tonumber(ARGV[2])
local batchSize = math.min(tonumber(ARGV[3]), 512)

local eventIDs = redis.call(
    'ZREVRANGEBYSCORE', evtTimeKey, '+inf', minScore,
    'LIMIT', offset, batchSize
)
local result = {tostring(#eventIDs)}
if #eventIDs > 0 then
    local dataList = redis.call('HMGET', evtDataKey, unpack(eventIDs))
    for _, data in ipairs(dataList) do
        if data then table.insert(result, data) end
    end
end

return result
`)

var luaLoadStateInitializationValue = redis.NewScript(`
local sessionMetaKey = KEYS[1]
local revisionKey = KEYS[2]
local generationCandidate = ARGV[1]

local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    return false
end

local decoded, meta = pcall(cjson.decode, metaJSON)
if not decoded or not meta or type(meta) ~= 'table' then
    return redis.error_reply('unmarshal session meta')
end
if meta.generation ~= nil and type(meta.generation) ~= 'string' then
    return redis.error_reply('invalid session generation')
end
if not meta.generation or meta.generation == '' then
    -- Legacy records have no generation. Assign one atomically while preserving
    -- the existing TTL; the temporary marker keeps state encoded as an object.
    local existingTTL = redis.call('PTTL', sessionMetaKey)
    -- generationCandidate is a UUID generated by the Go caller. Removing its
    -- hyphens keeps the marker free of Lua pattern characters used by gsub.
    local stateMarker = "__TRPC_AGENT_GO_STATE_GENERATION_" ..
        string.gsub(generationCandidate, "-", "") .. "__"
    if not meta.state or type(meta.state) ~= 'table' then
        meta.state = {}
    end
    meta.state[stateMarker] = stateMarker
    meta.generation = generationCandidate
    metaJSON = cjson.encode(meta)
    local markerPair = '"' .. stateMarker .. '":"' .. stateMarker .. '"'
    metaJSON = string.gsub(metaJSON, markerPair .. ',', '')
    metaJSON = string.gsub(metaJSON, ',' .. markerPair, '')
    metaJSON = string.gsub(metaJSON, markerPair, '')
    if existingTTL >= 0 then
        if existingTTL == 0 then
            existingTTL = 1
        end
        redis.call('SET', sessionMetaKey, metaJSON, 'PX', existingTTL)
    else
        redis.call('SET', sessionMetaKey, metaJSON)
    end
end
return {metaJSON, redis.call('GET', revisionKey) or ''}
`)

var luaCommitStateInitialization = redis.NewScript(`
local leaseKey = KEYS[1]
local sessionMetaKey = KEYS[2]
local revisionKey = KEYS[3]
local ownerToken = ARGV[1]
local encodedState = ARGV[2]
-- nilSentinel is derived from a UUID by the Go caller and contains no Lua
-- pattern characters, so it is safe to use as a gsub pattern below.
local nilSentinel = ARGV[3]
local expectedGeneration = ARGV[4]
local updatedAt = ARGV[5]
local ttlMs = tonumber(ARGV[6])
local expectedRevisionJSON = ARGV[7]
local updatedRevisionJSON = ARGV[8]

if redis.call('GET', leaseKey) ~= ownerToken then
    return 0
end

local stateDecoded, state = pcall(cjson.decode, encodedState)
if not stateDecoded or not state or type(state) ~= 'table' then
    redis.call('DEL', leaseKey)
    return redis.error_reply('unmarshal initialized state')
end

local metaJSON = redis.call('GET', sessionMetaKey)
if not metaJSON then
    redis.call('DEL', leaseKey)
    return -1
end

local decoded, meta = pcall(cjson.decode, metaJSON)
if not decoded or not meta or type(meta) ~= 'table' then
    redis.call('DEL', leaseKey)
    return redis.error_reply('unmarshal session meta')
end
if expectedGeneration == '' or meta.generation ~= expectedGeneration then
    redis.call('DEL', leaseKey)
    return -2
end
local currentRevisionJSON = redis.call('GET', revisionKey) or ''
if currentRevisionJSON ~= expectedRevisionJSON then
    redis.call('DEL', leaseKey)
    return -3
end
if not meta.state or type(meta.state) ~= 'table' then
    meta.state = {}
end
for stateKey, encodedValue in pairs(state) do
    meta.state[stateKey] = encodedValue
end
meta.updatedAt = updatedAt

local encodedMeta = cjson.encode(meta)
encodedMeta = string.gsub(encodedMeta, '"' .. nilSentinel .. '"', 'null')
if ttlMs > 0 then
    redis.call('SET', sessionMetaKey, encodedMeta, 'PX', ttlMs)
else
    local existingTTL = redis.call('PTTL', sessionMetaKey)
    if existingTTL >= 0 then
        if existingTTL == 0 then
            existingTTL = 1
        end
        redis.call('SET', sessionMetaKey, encodedMeta, 'PX', existingTTL)
    else
        redis.call('SET', sessionMetaKey, encodedMeta)
    end
end
local sessionTTL = redis.call('PTTL', sessionMetaKey)
redis.call('SET', revisionKey, updatedRevisionJSON)
if sessionTTL >= 0 then
    redis.call('PEXPIRE', revisionKey, sessionTTL)
elseif sessionTTL == -1 then
    redis.call('PERSIST', revisionKey)
end
redis.call('DEL', leaseKey)
return 1
`)

// luaSummarySetIfNewer atomically merges one filterKey summary into the stored
// JSON map (String key) only if the incoming UpdatedAt is newer-or-equal.
//
// KEYS[1] = summaryKey (String containing JSON map of all filterKey summaries)
// KEYS[2] = private revision metadata key
// KEYS[3] = session metadata key
// ARGV[1] = filterKey
// ARGV[2] = newSummaryJSON (single Summary, e.g. {"summary":"...","updated_at":"..."})
// ARGV[3] = TTL (seconds, 0 = no TTL)
//
// Returns 1 if updated, 0 if skipped (existing is newer), and -1 if stale.
var luaSummarySetIfNewer = redis.NewScript(`
local sumKey = KEYS[1]
local revisionKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local fk = ARGV[1]
local newSum = cjson.decode(ARGV[2])
local ttl = tonumber(ARGV[3])
local hasExpectedGeneration = tonumber(ARGV[4]) == 1
local expectedGeneration = tonumber(ARGV[5])
local requestID = ARGV[6]

local function setPreserveTTL(key, value)
    local ttlMs = redis.call('PTTL', key)
    redis.call('SET', key, value)
    if ttlMs >= 0 then
        redis.call('PEXPIRE', key, ttlMs)
    end
end

local function setFromSessionTTL(key, value)
    local ttlMs = redis.call('PTTL', sessionMetaKey)
    redis.call('SET', key, value)
    if ttlMs >= 0 then
        redis.call('PEXPIRE', key, ttlMs)
    elseif ttlMs == -1 then
        redis.call('PERSIST', key)
    end
end

local sessionExists = redis.call('EXISTS', sessionMetaKey) == 1
if not sessionExists and hasExpectedGeneration then return -1 end

local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then revision = cjson.decode(revisionJSON) end
if hasExpectedGeneration and tonumber(revision.generation or 0) ~= expectedGeneration then
    return -1
end
if revision.checkpoint and (not hasExpectedGeneration or requestID == '' or
    requestID ~= revision.checkpoint.requestID) then
    revision.checkpoint.hazard = true
end
local function touchRevision()
    if not sessionExists then return end
    setFromSessionTTL(revisionKey, cjson.encode(revision))
end

local cur = redis.call('GET', sumKey)
if not cur or cur == '' then
    local m = {}
    m[fk] = newSum
    if ttl > 0 then
        redis.call('SET', sumKey, cjson.encode(m), 'EX', ttl)
    else
        redis.call('SET', sumKey, cjson.encode(m))
    end
    revision.head = tonumber(revision.head or 0) + 1
    touchRevision()
    return 1
end

local map = cjson.decode(cur)
local old = map[fk]

local old_ts = old and old['updated_at'] or nil
local new_ts = newSum and newSum['updated_at'] or nil

if not old or (old_ts and new_ts and old_ts <= new_ts) then
    map[fk] = newSum
    setPreserveTTL(sumKey, cjson.encode(map))
    revision.head = tonumber(revision.head or 0) + 1
    touchRevision()
    return 1
end
touchRevision()
return 0
`)

// luaLoadSessionData loads user state and summary in a single Lua call.
// Events are loaded separately in bounded batches, and tracks use their own loader.
//
// KEYS layout (all {userID}-scoped, same Redis Cluster slot):
//
//	KEYS[1] = summaryKey (STRING, JSON map of filterKey -> Summary)
//	KEYS[2] = userStateKey (HASH)
//
// Returns: cjson-encoded table:
//
//	{
//	  "summary": "..." or nil,                          -- raw summary JSON string (entire map)
//	  "userState": {"key": "value", ...} or nil,        -- user state map
//	}
var luaLoadSessionData = redis.NewScript(`
local summaryKey = KEYS[1]
local userStateKey = KEYS[2]

local result = {loaded = true}

-- 1. Load summary (String key containing entire JSON map)
local sumData = redis.call('GET', summaryKey)
if sumData then
    result['summary'] = sumData
end

-- 2. Load user state
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

// luaDeleteEvent deletes an event and its indexes and invalidates the rolling
// revision projection in the same script.
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key,
// KEYS[3] = session metadata key, KEYS[4] = private revision metadata key
// ARGV[1] = eventID
var luaDeleteEvent = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local revisionKey = KEYS[4]
local eventID = ARGV[1]

local removed = redis.call('HDEL', evtDataKey, eventID) +
    redis.call('ZREM', evtTimeKey, eventID)
if removed > 0 and redis.call('EXISTS', sessionMetaKey) == 1 then
    local revisionJSON = redis.call('GET', revisionKey)
    local revision = {generation = 0}
    if revisionJSON then revision = cjson.decode(revisionJSON) end
    revision.head = tonumber(revision.head or 0) + 1
    if revision.checkpoint then revision.checkpoint.hazard = true end
    revision.projection = nil
    local ttlMs = redis.call('PTTL', sessionMetaKey)
    redis.call('SET', revisionKey, cjson.encode(revision))
    if ttlMs >= 0 then
        redis.call('PEXPIRE', revisionKey, ttlMs)
    elseif ttlMs == -1 then
        redis.call('PERSIST', revisionKey)
    end
end

return removed
`)

// luaTrimConversations trims the most recent N conversations (by RequestID).
// KEYS[1] = evtdata key, KEYS[2] = evtidx:time key,
// KEYS[3] = session metadata key, KEYS[4] = private revision metadata key
// ARGV[1] = count
var luaTrimConversations = redis.NewScript(`
local evtDataKey = KEYS[1]
local evtTimeKey = KEYS[2]
local sessionMetaKey = KEYS[3]
local revisionKey = KEYS[4]
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

if #toDelete > 0 and redis.call('EXISTS', sessionMetaKey) == 1 then
    local revisionJSON = redis.call('GET', revisionKey)
    local revision = {generation = 0}
    if revisionJSON then revision = cjson.decode(revisionJSON) end
    revision.head = tonumber(revision.head or 0) + 1
    if revision.checkpoint then revision.checkpoint.hazard = true end
    revision.projection = nil
    local ttlMs = redis.call('PTTL', sessionMetaKey)
    redis.call('SET', revisionKey, cjson.encode(revision))
    if ttlMs >= 0 then
        redis.call('PEXPIRE', revisionKey, ttlMs)
    elseif ttlMs == -1 then
        redis.call('PERSIST', revisionKey)
    end
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
// KEYS[4] = trkidx:names key (Set, member=trackName)
// KEYS[5] = private revision metadata key
// ARGV[1] = TrackEvent JSON
// ARGV[2] = timestamp (UnixNano)
// ARGV[3] = TTL (seconds, 0 = no TTL)
// ARGV[4] = track TTL override set (1 or 0)
// ARGV[5] = updated tracks value (base64-encoded JSON array, to set as state.tracks)
// ARGV[6] = track name
// ARGV[7] = has expected generation, ARGV[8] = expected generation,
// ARGV[9] = has expected head, ARGV[10] = expected head,
// ARGV[11] = has prepared projection, ARGV[12] = prepared projection JSON,
// ARGV[13] = request ID associated with the track write
// (empty JSON clears the projection).
// Returns: generated eventID on success, 0 if session not found, -1 if stale
// generation, -2 if stale projection.
var luaAppendTrackEvent = redis.NewScript(`
local dataKey = KEYS[1]
local idxKey = KEYS[2]
local metaKey = KEYS[3]
local trackNamesKey = KEYS[4]
local revisionKey = KEYS[5]

local payload = ARGV[1]
local ts = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local trackTTLSet = tonumber(ARGV[4]) == 1
local tracksVal = ARGV[5]
local trackName = ARGV[6]
local hasExpectedGeneration = tonumber(ARGV[7]) == 1
local expectedGeneration = tonumber(ARGV[8])
local hasExpectedHead = tonumber(ARGV[9]) == 1
local expectedHead = tonumber(ARGV[10])
local hasPreparedProjection = tonumber(ARGV[11]) == 1
local preparedProjectionJSON = ARGV[12]
local trackRequestID = ARGV[13]

local function setPreserveTTL(key, value)
    local ttlMs = redis.call('PTTL', key)
    redis.call('SET', key, value)
    if ttlMs >= 0 then
        redis.call('PEXPIRE', key, ttlMs)
    end
end

-- Check session exists and read meta
local metaJSON = redis.call('GET', metaKey)
if not metaJSON then
    return 0
end
local sessionTTLms = redis.call('PTTL', metaKey)

local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then
    revision = cjson.decode(revisionJSON)
end
if hasExpectedGeneration and tonumber(revision.generation or 0) ~= expectedGeneration then
    return -1
end
local head = tonumber(revision.head or 0)
if hasExpectedHead and head ~= expectedHead then
    return -2
end
local projectionAppendable = not hasPreparedProjection or preparedProjectionJSON ~= ''
if hasPreparedProjection or revision.checkpoint then
    local latest = redis.call('ZREVRANGE', idxKey, 0, 0, 'WITHSCORES')
    projectionAppendable = projectionAppendable and
        (#latest == 0 or ts > tonumber(latest[2]))
end
if hasPreparedProjection then
    if projectionAppendable then
        revision.projection = cjson.decode(preparedProjectionJSON)
    else
        revision.projection = nil
    end
end
revision.head = head + 1
if not hasExpectedGeneration and revision.checkpoint then
    revision.checkpoint.hazard = true
end
if revision.checkpoint then
    if not projectionAppendable or revision.checkpoint.terminal or trackRequestID == '' or
        trackRequestID ~= revision.checkpoint.requestID then
        revision.checkpoint.hazard = true
    end
end

-- Generate auto-increment ID via reserved "_seq" field in the data Hash
local id = redis.call('HINCRBY', dataKey, '_seq', 1)

-- Store event data and time index
redis.call('HSET', dataKey, id, payload)
redis.call('ZADD', idxKey, ts, id)
redis.call('SADD', trackNamesKey, trackName)

-- Update session meta's state.tracks with the Go-provided value
local meta = cjson.decode(metaJSON)
if not meta.state or type(meta.state) ~= 'table' then
    meta.state = {}
end
meta.state['tracks'] = tracksVal
setPreserveTTL(metaKey, cjson.encode(meta))
redis.call('SET', revisionKey, cjson.encode(revision))
if sessionTTLms >= 0 then
    redis.call('PEXPIRE', revisionKey, sessionTTLms)
elseif sessionTTLms == -1 then
    redis.call('PERSIST', revisionKey)
end
-- Refresh TTL for track data keys
if ttl > 0 then
    redis.call('EXPIRE', dataKey, ttl)
    redis.call('EXPIRE', idxKey, ttl)
    redis.call('EXPIRE', trackNamesKey, ttl)
elseif trackTTLSet then
    redis.call('PERSIST', dataKey)
    redis.call('PERSIST', idxKey)
    redis.call('PERSIST', trackNamesKey)
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
    local batchSize = 512
    for startIdx = 1, #eventIDs, batchSize do
        local batchIDs = {}
        local endIdx = math.min(startIdx + batchSize - 1, #eventIDs)
        for i = startIdx, endIdx do
            table.insert(batchIDs, eventIDs[i])
        end
        local dataList = redis.call('HMGET', dataKey, unpack(batchIDs))
        for _, data in ipairs(dataList) do
            if data then
                table.insert(result, data)
            end
        end
    end
end

return result
`)
