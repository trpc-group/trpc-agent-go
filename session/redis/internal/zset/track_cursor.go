//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// ReadTrackCursor reads a snapshot or a bounded page from the append index.
func (c *Client) ReadTrackCursor(ctx context.Context, key session.Key, track session.Track, opts *session.Options) (*session.TrackEvents, error) {
	token, err := util.DecodeTrackCursor(*opts.TrackCursor, "zset:"+c.cfg.KeyPrefix, key, track)
	if err != nil {
		return nil, err
	}
	if pos, err := strconv.ParseUint(token.Position, 10, 53); err != nil || pos > 9007199254740991 {
		return nil, fmt.Errorf("invalid track cursor position")
	}
	if *opts.TrackCursor == "" {
		// Upgrade legacy metadata before capturing the atomic snapshot. A session
		// recreated between these calls is captured with its own generation.
		_, _, _, exists, err := c.LoadSessionStateValue(ctx, key, "")
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("track cursor session expired")
		}
	}
	trackTTL := c.cfg.effectiveTrackEventTTL()
	trackTTLMillis := trackTTL.Milliseconds()
	if trackTTL > 0 && trackTTLMillis == 0 {
		trackTTLMillis = 1
	}
	min := "-inf"
	if !opts.EventTime.IsZero() {
		min = fmt.Sprint(opts.EventTime.UnixNano())
	}
	keys := []string{c.sessionStateKey(key), c.trackKey(key, track), c.trackCursorKey(key, track), c.trackIndexKey(key)}
	args := []any{
		key.SessionID, token.Generation, token.Position, min, opts.EventNum,
		*opts.TrackCursor == "", token.Epoch, token.LegacyCount,
		uuid.NewString(), trackTTLMillis, string(track),
	}
	raw, err := c.runScript(ctx, luaReadTrackCursor, keys, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("read track cursor: %w", err)
	}
	return util.TrackCursorResult(raw, token, track)
}

var luaReadTrackCursor = redis.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw then return redis.error_reply('track cursor session expired') end
local meta = cjson.decode(raw)
local generation = meta.generation
if not generation or generation == '' then generation = meta.createdAt end
local position = tonumber(ARGV[3])
local values
-- Sequence scores are positive. Score zero holds the track incarnation so
-- even a cursor issued before the first event can detect expiry and recreation.
local epochs = redis.call('ZRANGEBYSCORE', KEYS[3], 0, 0)
if ARGV[6] == '1' and #epochs == 0 then
    local indexTTL = redis.call('PTTL', KEYS[3])
    local ttl = indexTTL
    if ttl == -2 then
        ttl = redis.call('PTTL', KEYS[2])
        if ttl == -2 then
            ttl = tonumber(ARGV[10])
            if ttl <= 0 then ttl = -1 end
        end
    end
    redis.call('ZADD', KEYS[3], 0, ARGV[9])
    epochs = {ARGV[9]}
    if indexTTL == -2 and ttl >= 0 then redis.call('PEXPIRE', KEYS[3], math.max(1, ttl)) end
    local namesTTL = redis.call('PTTL', KEYS[4])
    redis.call('SADD', KEYS[4], ARGV[11])
    if ttl < 0 then
        redis.call('PERSIST', KEYS[4])
    elseif namesTTL == -2 or (namesTTL >= 0 and namesTTL < ttl) then
        redis.call('PEXPIRE', KEYS[4], math.max(1, ttl))
    end
end
if #epochs ~= 1 then return redis.error_reply('track cursor history expired') end
local epoch = epochs[1]
local legacyCount = redis.call('ZCARD', KEYS[2]) - redis.call('ZCARD', KEYS[3]) + 1
if ARGV[6] == '1' then
    local limit = tonumber(ARGV[5])
    if limit > 0 then
        local reversed = redis.call('ZREVRANGEBYSCORE', KEYS[2], '+inf', ARGV[4], 'LIMIT', 0, limit)
        values = {}
        for i = #reversed, 1, -1 do table.insert(values, reversed[i]) end
    else
        values = redis.call('ZRANGEBYSCORE', KEYS[2], ARGV[4], '+inf')
    end
    local latest = redis.call('ZREVRANGEBYSCORE', KEYS[3], '+inf', '(0', 'WITHSCORES', 'LIMIT', 0, 1)
    position = #latest > 0 and tonumber(latest[2]) or 0
else
    if generation ~= ARGV[2] or epoch ~= ARGV[7] then
        return redis.error_reply('track cursor generation expired')
    end
    if tostring(legacyCount) ~= ARGV[8] then return redis.error_reply('track cursor legacy history changed') end
    if position > 0 and redis.call('ZCOUNT', KEYS[3], ARGV[3], ARGV[3]) ~= 1 then
        return redis.error_reply('track cursor history expired')
    end
    local entries = redis.call('ZRANGEBYSCORE', KEYS[3], '(' .. ARGV[3], '+inf', 'WITHSCORES', 'LIMIT', 0, 512)
    values = {}
    for i = 1, #entries, 2 do
        table.insert(values, entries[i])
        position = tonumber(entries[i + 1])
    end
end
local result = {cjson.encode({Generation = generation, Epoch = epoch, LegacyCount = tostring(legacyCount), Position = string.format('%.0f', position)})}
for _, payload in ipairs(values) do table.insert(result, payload) end
return result
`)

// nextTrackSequence uses the WATCH connection to avoid acquiring a second
// pooled connection. It runs inside the session-state WATCH callback. New writers
// update that watched state in the same transaction as both track indexes.
func nextTrackSequence(ctx context.Context, client redis.Cmdable, cursorKey string, current uint64) (uint64, error) {
	latest, err := client.ZRevRangeByScore(ctx, cursorKey, &redis.ZRangeBy{Min: "(0", Max: "+inf", Count: 1}).Result()
	if err != nil {
		return 0, fmt.Errorf("read track sequence: %w", err)
	}
	if len(latest) > 0 {
		var stored struct {
			Sequence string `json:"z_sequence"`
		}
		if err := json.Unmarshal([]byte(latest[0]), &stored); err != nil {
			return 0, fmt.Errorf("decode track sequence: %w", err)
		}
		sequence, err := strconv.ParseUint(stored.Sequence, 10, 53)
		if err != nil {
			return 0, fmt.Errorf("invalid stored track sequence: %w", err)
		}
		if sequence > current {
			current = sequence
		}
	}
	if current >= 9007199254740991 {
		return 0, fmt.Errorf("track sequence exhausted")
	}
	return current + 1, nil
}
