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
// Returns 1 if updated, 0 if skipped (existing is newer).
var luaSummariesSetIfNewer = redis.NewScript(
	"local cur = redis.call('HGET', KEYS[1], ARGV[1])\n" +
		"local fk = ARGV[2]\n" +
		"local newSum = cjson.decode(ARGV[3])\n" +
		"if not cur or cur == '' then\n" +
		"  local m = {}\n" +
		"  m[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(m))\n" +
		"  return 1\n" +
		"end\n" +
		"local map = cjson.decode(cur)\n" +
		"local old = map[fk]\n" +
		"local old_ts = nil\n" +
		"local new_ts = nil\n" +
		"if old and old['updated_at'] then old_ts = old['updated_at'] end\n" +
		"if newSum and newSum['updated_at'] then new_ts = newSum['updated_at'] end\n" +
		"if not old or (old_ts and new_ts and old_ts <= new_ts) then\n" +
		"  map[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(map))\n" +
		"  return 1\n" +
		"end\n" +
		"return 0\n",
)

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
local stateKey = ARGV[3]
local encodedValue = ARGV[4]
local valueIsNil = tonumber(ARGV[5]) == 1
local expectedGeneration = ARGV[6]
local updatedAt = ARGV[7]
local ttlMs = tonumber(ARGV[8])
local nilSentinel = "__TRPC_AGENT_GO_STATE_INITIALIZATION_NULL_" ..
    string.gsub(ownerToken, "-", "") .. "__"

if redis.call('GET', leaseKey) ~= ownerToken then
    return 0
end

local sessionJSON = redis.call('HGET', sessionStateKey, sessionID)
if not sessionJSON then
    redis.call('DEL', leaseKey)
    return -1
end

local sessionState = cjson.decode(sessionJSON)
if not sessionState or type(sessionState) ~= 'table' then
    return redis.error_reply('unmarshal session state')
end
if expectedGeneration == '' or sessionState.generation ~= expectedGeneration then
    redis.call('DEL', leaseKey)
    return -2
end
if not sessionState.state or type(sessionState.state) ~= 'table' then
    sessionState.state = {}
end
if valueIsNil then
    sessionState.state[stateKey] = nilSentinel
else
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
