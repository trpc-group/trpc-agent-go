//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// ReadTrackCursor reads an atomic snapshot or a bounded append-order page.
func (c *Client) ReadTrackCursor(ctx context.Context, key session.Key, track session.Track, opts *session.Options) (*session.TrackEvents, error) {
	token, err := util.DecodeTrackCursor(*opts.TrackCursor, "hashidx:"+c.cfg.KeyPrefix, key, track)
	if err != nil {
		return nil, err
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
	keys := []string{c.keys.TrackDataKey(key, track), c.keys.TrackTimeIndexKey(key, track), c.keys.SessionMetaKey(key), c.keys.TrackIndexKey(key)}
	var raw []string
	if *opts.TrackCursor == "" {
		min := "-inf"
		if !opts.EventTime.IsZero() {
			min = fmt.Sprint(opts.EventTime.UnixNano())
		}
		raw, err = c.runScript(ctx, luaLoadTrackEvents, keys, min, "+inf", opts.EventNum, uuid.NewString(), trackTTLMillis, string(track)).StringSlice()
	} else {
		if _, err := strconv.ParseUint(token.Position, 10, 63); err != nil {
			return nil, fmt.Errorf("invalid track cursor position")
		}
		raw, err = c.runScript(ctx, luaReadTrackCursor, keys, token.Generation, token.Epoch, token.Position).StringSlice()
	}
	if err != nil {
		return nil, fmt.Errorf("read track cursor: %w", err)
	}
	return util.TrackCursorResult(raw, token, track)
}

var luaReadTrackCursor = redis.NewScript(`
local raw = redis.call('GET', KEYS[3])
if not raw then return redis.error_reply('track cursor session expired') end
local meta = cjson.decode(raw)
local generation = meta.generation
if not generation or generation == '' then generation = meta.createdAt end
local epoch = redis.call('HGET', KEYS[1], '_epoch') or ''
local high = redis.call('HGET', KEYS[1], '_seq') or '0'
local position = ARGV[3]
local function less(a, b)
    if #a ~= #b then return #a < #b end
    return a < b
end
if generation ~= ARGV[1] or less(high, position) or
    epoch == '' or epoch ~= ARGV[2] then
    return redis.error_reply('track cursor generation expired')
end
local function increment(s)
    for i = #s, 1, -1 do
        local digit = tonumber(string.sub(s, i, i))
        if digit < 9 then
            return string.sub(s, 1, i - 1) .. tostring(digit + 1) .. string.rep('0', #s - i)
        end
    end
    return '1' .. string.rep('0', #s)
end
local ids = {}
while less(position, high) and #ids < 512 do
    position = increment(position)
    table.insert(ids, position)
end
local result = {cjson.encode({Generation = generation, Epoch = epoch, Position = position})}
if #ids > 0 then
    for _, payload in ipairs(redis.call('HMGET', KEYS[1], unpack(ids))) do
        if not payload then return redis.error_reply('track cursor history expired') end
        table.insert(result, payload)
    end
end
return result
`)
