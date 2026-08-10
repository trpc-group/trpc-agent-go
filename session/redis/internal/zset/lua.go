//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import "github.com/redis/go-redis/v9"

// luaSummariesSetIfNewer atomically merges one filterKey summary into the stored
// JSON map only if the incoming UpdatedAt is newer-or-equal.
//
// KEYS[1] = summary key
// ARGV[1] = hash field
// ARGV[2] = filterKey
// ARGV[3] = newSummaryJSON -> {"summary":"...","updated_at":"RFC3339 time"}
//
// Returns 1 if updated, 0 if skipped (existing is newer), and -1 if stale.
var luaSummariesSetIfNewer = redis.NewScript(`
local sumKey = KEYS[1]
local revisionKey = KEYS[2]
local sessionStateKey = KEYS[3]
local revisionArchiveKey = KEYS[4]
local hashField = ARGV[1]
local fk = ARGV[2]
local newSum = cjson.decode(ARGV[3])
local hasExpectedGeneration = tonumber(ARGV[4]) == 1
local expectedGeneration = tonumber(ARGV[5])

local sessionExists = redis.call('HEXISTS', sessionStateKey, hashField) == 1
if not sessionExists and hasExpectedGeneration then return -1 end

local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then revision = cjson.decode(revisionJSON) end
if hasExpectedGeneration and tonumber(revision.generation or 0) ~= expectedGeneration then
    return -1
end
if not hasExpectedGeneration and revision.checkpoint then
    revision.checkpoint.hazard = true
end
local function touchRevision()
    if not sessionExists then return end
    local ttlMs = redis.call('PTTL', sessionStateKey)
    redis.call('SET', revisionKey, cjson.encode(revision))
    if ttlMs >= 0 then
        redis.call('PEXPIRE', revisionKey, ttlMs)
    elseif ttlMs == -1 then
        redis.call('PERSIST', revisionKey)
    end
    if redis.call('EXISTS', revisionArchiveKey) == 1 then
        if ttlMs >= 0 then
            redis.call('PEXPIRE', revisionArchiveKey, ttlMs)
        elseif ttlMs == -1 then
            redis.call('PERSIST', revisionArchiveKey)
        end
    end
end

local cur = redis.call('HGET', sumKey, hashField)
if not cur or cur == '' then
    local m = {}
    m[fk] = newSum
    redis.call('HSET', sumKey, hashField, cjson.encode(m))
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
    redis.call('HSET', sumKey, hashField, cjson.encode(map))
    revision.head = tonumber(revision.head or 0) + 1
    touchRevision()
    return 1
end
touchRevision()
return 0
`)

// luaTrimEventsWithRevision removes selected event members and advances the
// private revision record in the same Redis transaction.
var luaTrimEventsWithRevision = redis.NewScript(`
local eventKey = KEYS[1]
local sessionStateKey = KEYS[2]
local revisionKey = KEYS[3]
local revisionArchiveKey = KEYS[4]
local sessionID = ARGV[1]

if #ARGV <= 1 then return 0 end
local removed = 0
local chunk = {}
for i = 2, #ARGV do
    table.insert(chunk, ARGV[i])
    if #chunk == 512 then
        removed = removed + redis.call('ZREM', eventKey, unpack(chunk))
        chunk = {}
    end
end
if #chunk > 0 then
    removed = removed + redis.call('ZREM', eventKey, unpack(chunk))
end
if removed == 0 or redis.call('HEXISTS', sessionStateKey, sessionID) == 0 then
    return removed
end

local revisionJSON = redis.call('GET', revisionKey)
local revision = {generation = 0}
if revisionJSON then revision = cjson.decode(revisionJSON) end
revision.head = tonumber(revision.head or 0) + 1
if revision.checkpoint then revision.checkpoint.hazard = true end

local ttlMs = redis.call('PTTL', sessionStateKey)
redis.call('SET', revisionKey, cjson.encode(revision))
if ttlMs >= 0 then
    redis.call('PEXPIRE', revisionKey, ttlMs)
elseif ttlMs == -1 then
    redis.call('PERSIST', revisionKey)
end
if redis.call('EXISTS', revisionArchiveKey) == 1 then
    if ttlMs >= 0 then
        redis.call('PEXPIRE', revisionArchiveKey, ttlMs)
    elseif ttlMs == -1 then
        redis.call('PERSIST', revisionArchiveKey)
    end
end
return removed
`)

var luaLoadStateInitializationValue = redis.NewScript(`
local sessionStateKey = KEYS[1]
local sessionID = ARGV[1]
local generationCandidate = ARGV[2]

local sessionJSON = redis.call('HGET', sessionStateKey, sessionID)
if not sessionJSON then
    return false
end

local decoded, sessionState = pcall(cjson.decode, sessionJSON)
if not decoded or not sessionState or type(sessionState) ~= 'table' then
    return redis.error_reply('unmarshal session state')
end
if sessionState.generation ~= nil and type(sessionState.generation) ~= 'string' then
    return redis.error_reply('invalid session generation')
end
if not sessionState.generation or sessionState.generation == '' then
    -- Legacy records have no generation. Assign one atomically; HSET preserves
    -- the TTL of the containing session-state key.
    -- generationCandidate is a UUID generated by the Go caller. Removing its
    -- hyphens keeps the marker free of Lua pattern characters used by gsub.
    local stateMarker = "__TRPC_AGENT_GO_STATE_GENERATION_" ..
        string.gsub(generationCandidate, "-", "") .. "__"
    if not sessionState.state or type(sessionState.state) ~= 'table' then
        sessionState.state = {}
    end
    sessionState.state[stateMarker] = stateMarker
    sessionState.generation = generationCandidate
    sessionJSON = cjson.encode(sessionState)
    local markerPair = '"' .. stateMarker .. '":"' .. stateMarker .. '"'
    sessionJSON = string.gsub(sessionJSON, markerPair .. ',', '')
    sessionJSON = string.gsub(sessionJSON, ',' .. markerPair, '')
    sessionJSON = string.gsub(sessionJSON, markerPair, '')
    redis.call('HSET', sessionStateKey, sessionID, sessionJSON)
end
return sessionJSON
`)

var luaCommitStateInitialization = redis.NewScript(`
local leaseKey = KEYS[1]
local sessionStateKey = KEYS[2]
local ownerToken = ARGV[1]
local sessionID = ARGV[2]
local encodedState = ARGV[3]
-- nilSentinel is derived from a UUID by the Go caller and contains no Lua
-- pattern characters, so it is safe to use as a gsub pattern below.
local nilSentinel = ARGV[4]
local expectedGeneration = ARGV[5]
local updatedAt = ARGV[6]
local ttlMs = tonumber(ARGV[7])

if redis.call('GET', leaseKey) ~= ownerToken then
    return 0
end

local stateDecoded, state = pcall(cjson.decode, encodedState)
if not stateDecoded or not state or type(state) ~= 'table' then
    redis.call('DEL', leaseKey)
    return redis.error_reply('unmarshal initialized state')
end

local sessionJSON = redis.call('HGET', sessionStateKey, sessionID)
if not sessionJSON then
    redis.call('DEL', leaseKey)
    return -1
end

local decoded, sessionState = pcall(cjson.decode, sessionJSON)
if not decoded or not sessionState or type(sessionState) ~= 'table' then
    redis.call('DEL', leaseKey)
    return redis.error_reply('unmarshal session state')
end
if expectedGeneration == '' or sessionState.generation ~= expectedGeneration then
    redis.call('DEL', leaseKey)
    return -2
end
if not sessionState.state or type(sessionState.state) ~= 'table' then
    sessionState.state = {}
end
for stateKey, encodedValue in pairs(state) do
    sessionState.state[stateKey] = encodedValue
end
sessionState.updatedAt = updatedAt

local encodedSession = cjson.encode(sessionState)
encodedSession = string.gsub(encodedSession, '"' .. nilSentinel .. '"', 'null')
redis.call('HSET', sessionStateKey, sessionID, encodedSession)
if ttlMs > 0 then
    redis.call('PEXPIRE', sessionStateKey, ttlMs)
end
redis.call('DEL', leaseKey)
return 1
`)
