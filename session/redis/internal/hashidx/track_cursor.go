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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type trackCursor struct {
	Scope      string
	Generation string
	Epoch      string
	Position   string
}

// ReadTrackCursor reads an atomic append-order snapshot or a bounded continuation.
func (c *Client) ReadTrackCursor(ctx context.Context, key session.Key, track session.Track, cursor string) (*session.TrackEvents, error) {
	scope, _ := json.Marshal([]string{"hashidx", c.cfg.KeyPrefix, key.AppName, key.UserID, key.SessionID, string(track)})
	token := trackCursor{Scope: fmt.Sprintf("%x", sha256.Sum256(scope))}
	keys := []string{c.keys.TrackDataKey(key, track), c.keys.SessionMetaKey(key), c.keys.TrackIndexKey(key)}
	var cmd *redis.Cmd
	if cursor == "" {
		// Ensure legacy sessions have the same generation used by coordinated
		// state initialization. The script below captures the actual generation.
		_, _, _, exists, err := c.LoadSessionStateValue(ctx, key, "")
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: session expired", session.ErrTrackCursorInvalid)
		}
		ttl := c.cfg.effectiveTrackEventTTL()
		millis := ttl.Milliseconds()
		if ttl > 0 && millis == 0 {
			millis = 1
		}
		cmd = c.runScript(ctx, luaSnapshotTrackCursor, keys, uuid.NewString(), millis, string(track))
	} else {
		var err error
		token, err = decodeTrackCursor(cursor, token.Scope)
		if err != nil {
			return nil, err
		}
		cmd = c.runScript(ctx, luaContinueTrackCursor, keys, token.Generation, token.Epoch, token.Position)
	}
	raw, err := cmd.StringSlice()
	if err != nil {
		if redis.HasErrorPrefix(err, "TRACKCURSORINVALID ") {
			return nil, fmt.Errorf("%w: %v", session.ErrTrackCursorInvalid, err)
		}
		return nil, fmt.Errorf("read track cursor: %w", err)
	}
	return trackCursorResult(raw, token, track, cursor == "")
}

func decodeTrackCursor(cursor, scope string) (trackCursor, error) {
	var token trackCursor
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, &token) != nil || token.Scope != scope || !token.valid() {
		return trackCursor{}, session.ErrTrackCursorInvalid
	}
	return token, nil
}

func (t trackCursor) valid() bool {
	position, err := strconv.ParseUint(t.Position, 10, 63)
	return err == nil && strconv.FormatUint(position, 10) == t.Position && t.Generation != "" && t.Epoch != ""
}

func trackCursorResult(raw []string, token trackCursor, track session.Track, snapshot bool) (*session.TrackEvents, error) {
	if len(raw) < 3 {
		return nil, fmt.Errorf("missing track cursor metadata")
	}
	token.Generation, token.Epoch, token.Position = raw[0], raw[1], raw[2]
	if !token.valid() {
		return nil, fmt.Errorf("invalid track cursor metadata")
	}
	payloads := raw[3:]
	if snapshot {
		var err error
		payloads, err = orderTrackSnapshot(payloads)
		if err != nil {
			return nil, err
		}
	}
	result := &session.TrackEvents{Track: track, Events: make([]session.TrackEvent, 0, len(payloads))}
	for _, payload := range payloads {
		var event session.TrackEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, fmt.Errorf("decode track event: %w", err)
		}
		result.Events = append(result.Events, event)
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return nil, err
	}
	next := base64.RawURLEncoding.EncodeToString(encoded)
	result.Cursor = &next
	return result, nil
}

func orderTrackSnapshot(fields []string) ([]string, error) {
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("incomplete track snapshot")
	}
	type entry struct {
		id      uint64
		payload string
	}
	events := make([]entry, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		switch fields[i] {
		case "_seq", "_epoch":
			continue
		}
		id, err := strconv.ParseUint(fields[i], 10, 63)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid track event id %q", fields[i])
		}
		events = append(events, entry{id: id, payload: fields[i+1]})
	}
	slices.SortFunc(events, func(a, b entry) int { return cmp.Compare(a.id, b.id) })
	payloads := make([]string, 0, len(events))
	for _, event := range events {
		payloads = append(payloads, event.payload)
	}
	return payloads, nil
}

// The snapshot and high-water mark are read together. Sort the fields in Go
// so Redis does not hold the script lock while sorting a full snapshot.
var luaSnapshotTrackCursor = redis.NewScript(`
local raw = redis.call('GET', KEYS[2])
if not raw then return redis.error_reply('TRACKCURSORINVALID session expired') end
local generation = cjson.decode(raw).generation
if not generation or generation == '' then
    return redis.error_reply('TRACKCURSORINVALID session generation missing')
end
local fields = redis.call('HGETALL', KEYS[1])
redis.call('HSETNX', KEYS[1], '_epoch', ARGV[1])
if #fields == 0 then
    -- Register an empty track for deletion and let the marker expire without
    -- refreshing the retention of an existing track.
    local namesTTL = redis.call('PTTL', KEYS[3])
    local ttl = tonumber(ARGV[2])
    redis.call('SADD', KEYS[3], ARGV[3])
    if ttl > 0 then
        redis.call('PEXPIRE', KEYS[1], ttl)
        if namesTTL == -2 or (namesTTL >= 0 and namesTTL < ttl) then
            redis.call('PEXPIRE', KEYS[3], ttl)
        end
    else
        redis.call('PERSIST', KEYS[3])
    end
end
local result = {
    generation,
    redis.call('HGET', KEYS[1], '_epoch'),
    redis.call('HGET', KEYS[1], '_seq') or '0'
}
-- Adding each item avoids Lua's unpack argument limit on large snapshots.
for _, field in ipairs(fields) do table.insert(result, field) end
return result
`)

var luaContinueTrackCursor = redis.NewScript(`
local raw = redis.call('GET', KEYS[2])
if not raw then return redis.error_reply('TRACKCURSORINVALID session expired') end
local generation = cjson.decode(raw).generation
local epoch = redis.call('HGET', KEYS[1], '_epoch')
local high = redis.call('HGET', KEYS[1], '_seq') or '0'
local position = ARGV[3]
local function less(a, b)
    if #a ~= #b then return #a < #b end
    return a < b
end
if generation ~= ARGV[1] or epoch ~= ARGV[2] or less(high, position) then
    return redis.error_reply('TRACKCURSORINVALID history expired or replaced')
end
-- Decimal string arithmetic preserves Redis integer IDs above Lua's exact range.
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
local result = {generation, epoch, position}
if #ids > 0 then
    for _, payload in ipairs(redis.call('HMGET', KEYS[1], unpack(ids))) do
        if not payload then return redis.error_reply('TRACKCURSORINVALID history incomplete') end
        table.insert(result, payload)
    end
end
return result
`)
