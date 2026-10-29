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
	min := "-inf"
	if !opts.EventTime.IsZero() {
		min = fmt.Sprint(opts.EventTime.UnixNano())
	}
	raw, err := c.runScript(ctx, luaReadTrackCursor, []string{c.sessionStateKey(key), c.trackKey(key, track), c.trackCursorKey(key, track)}, key.SessionID, token.Generation, token.Position, min, opts.EventNum, *opts.TrackCursor == "", token.Epoch).StringSlice()
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
local legacyCount = redis.call('ZCARD', KEYS[2]) - redis.call('ZCARD', KEYS[3])
if ARGV[6] == '1' then
    local limit = tonumber(ARGV[5])
    if limit > 0 then
        local reversed = redis.call('ZREVRANGEBYSCORE', KEYS[2], '+inf', ARGV[4], 'LIMIT', 0, limit)
        values = {}
        for i = #reversed, 1, -1 do table.insert(values, reversed[i]) end
    else
        values = redis.call('ZRANGEBYSCORE', KEYS[2], ARGV[4], '+inf')
    end
    local latest = redis.call('ZREVRANGE', KEYS[3], 0, 0, 'WITHSCORES')
    position = #latest > 0 and tonumber(latest[2]) or 0
else
    if generation ~= ARGV[2] then
        return redis.error_reply('track cursor generation expired')
    end
    if tostring(legacyCount) ~= ARGV[7] then return redis.error_reply('track cursor legacy history changed') end
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
local result = {cjson.encode({Generation = generation, Epoch = tostring(legacyCount), Position = string.format('%.0f', position)})}
for _, payload in ipairs(values) do table.insert(result, payload) end
return result
`)

// nextTrackSequence runs inside the session-state WATCH callback. New writers
// update that watched state in the same transaction as both track indexes.
func (c *Client) nextTrackSequence(ctx context.Context, key session.Key, track session.Track, current uint64) (uint64, error) {
	latest, err := c.client.ZRevRange(ctx, c.trackCursorKey(key, track), 0, 0).Result()
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
