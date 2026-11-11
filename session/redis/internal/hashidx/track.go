//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AppendTrackEvent persists a track event to HashIdx storage.
// Track events are stored in a Hash (data) + ZSet (time index) structure
// with an auto-increment sequence for event IDs.
// tracksState is the serialized tracks state value (from session.State["tracks"])
// that will be set on the session meta atomically.
// This operation is atomic via Lua script.
func (c *Client) AppendTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent, tracksState []byte) error {
	eventJSON, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event: %w", err)
	}

	ttlSeconds := int64(0)
	trackTTL := c.cfg.effectiveTrackEventTTL()
	if trackTTL > 0 {
		ttlSeconds = ttlSecondsCeil(trackTTL)
	}

	// Encode tracksState as base64 to match Go's json.Marshal behavior for []byte.
	// In sessionMeta JSON, state values (map[string][]byte) are base64-encoded strings.
	tracksVal := ""
	if len(tracksState) > 0 {
		tracksVal = base64.StdEncoding.EncodeToString(tracksState)
	}

	track := trackEvent.Track
	keys := []string{
		c.keys.TrackDataKey(key, track),
		c.keys.TrackTimeIndexKey(key, track),
		c.keys.SessionMetaKey(key),
		c.keys.TrackIndexKey(key),
	}
	args := []any{
		string(eventJSON),
		trackEvent.Timestamp.UnixNano(),
		ttlSeconds,
		boolToInt(c.cfg.TrackEventTTL != nil),
		tracksVal,
		string(track),
	}

	result, err := c.runScript(ctx, luaAppendTrackEvent, keys, args...).Int64()
	if err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

func ttlSecondsCeil(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	seconds := ttl / time.Second
	if ttl%time.Second != 0 {
		seconds++
	}
	return int64(seconds)
}

// GetTrackEvents retrieves track events for a session using Hash+ZSet structure.
// Each track is loaded from the time index and event data as one consistent read.
func (c *Client) GetTrackEvents(
	ctx context.Context,
	key session.Key,
	tracks []session.Track,
	limit int,
	afterTime time.Time,
) (map[session.Track][]session.TrackEvent, error) {
	if len(tracks) == 0 {
		return make(map[session.Track][]session.TrackEvent), nil
	}

	minScore := "-inf"
	if !afterTime.IsZero() {
		minScore = fmt.Sprintf("%d", afterTime.UnixNano())
	}
	maxScore := "+inf"

	results := make(map[session.Track][]session.TrackEvent)
	for _, track := range tracks {
		events, err := c.loadTrackEvents(ctx, key, track, minScore, maxScore, limit)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			results[track] = events
		}
	}
	return results, nil
}

func (c *Client) loadTrackEvents(
	ctx context.Context,
	key session.Key,
	track session.Track,
	minScore, maxScore string,
	limit int,
) ([]session.TrackEvent, error) {
	keys := []string{c.keys.TrackDataKey(key, track), c.keys.TrackTimeIndexKey(key, track)}
	rawEvents, err := c.runScript(ctx, luaLoadTrackEvents, keys, minScore, maxScore, limit, trackReadBatchSize).StringSlice()
	if err == redis.Nil {
		if limit <= 0 {
			return nil, nil
		}
		rawEvents, err = c.loadTrackEventsPaged(ctx, keys, minScore, maxScore, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("load track events: %w", err)
	}

	events := make([]session.TrackEvent, 0, len(rawEvents))
	for _, raw := range rawEvents {
		var evt session.TrackEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}

const trackReadBatchSize = 512

// loadTrackEventsPaged scans large cutoff ties outside Lua so Redis can serve
// other clients between pages. EXEC validates that the index and selected
// payloads came from the same track version. Changes restart the read.
func (c *Client) loadTrackEventsPaged(
	ctx context.Context,
	keys []string,
	minScore, maxScore string,
	limit int,
) ([]string, error) {
	dataKey, indexKey := keys[0], keys[1]
	runRange := luaLoadTrackEvents.Run
	if c.cfg.DisableScriptCache {
		runRange = luaLoadTrackEvents.Eval
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var payloads []string
		err := c.client.Watch(ctx, func(tx *redis.Tx) error {
			ranks, err := runRange(ctx, tx, keys, minScore, maxScore, limit, 0).Int64Slice()
			if err != nil {
				return err
			}
			if ranks[0] > ranks[1] {
				return nil
			}

			ids, err := selectTrackEventIDs(ctx, tx, indexKey, ranks[0], ranks[1], limit)
			if err != nil {
				return err
			}
			for first := 0; first < len(ids); first += trackReadBatchSize {
				last := min(first+trackReadBatchSize, len(ids))
				values, err := tx.HMGet(ctx, dataKey, ids[first:last]...).Result()
				if err != nil {
					return err
				}
				for _, value := range values {
					if value != nil {
						payloads = append(payloads, value.(string))
					}
				}
			}

			var indexExists, dataExists *redis.IntCmd
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				indexExists = pipe.Exists(ctx, indexKey)
				dataExists = pipe.Exists(ctx, dataKey)
				return nil
			})
			if err != nil {
				return err
			}
			// Before Redis 6.0.9, expiration alone did not abort a WATCH.
			if indexExists.Val() == 0 || (len(payloads) > 0 && dataExists.Val() == 0) {
				return redis.TxFailedErr
			}
			return nil
		}, keys...)
		if !errors.Is(err, redis.TxFailedErr) {
			return payloads, err
		}
	}
	return nil, fmt.Errorf("track changed during paged history read: %w", redis.TxFailedErr)
}

func selectTrackEventIDs(
	ctx context.Context,
	tx *redis.Tx,
	indexKey string,
	first, last int64,
	limit int,
) ([]string, error) {
	var selected trackEventHeap
	for start := first; start <= last; start += trackReadBatchSize {
		end := min(start+trackReadBatchSize-1, last)
		entries, err := tx.ZRangeWithScores(ctx, indexKey, start, end).Result()
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if len(selected) < limit {
				heap.Push(&selected, entry)
			} else if trackEventLess(selected[0], entry) {
				selected[0] = entry
				heap.Fix(&selected, 0)
			}
		}
	}
	sort.Sort(selected)
	ids := make([]string, len(selected))
	for i, entry := range selected {
		ids[i] = entry.Member.(string)
	}
	return ids, nil
}

type trackEventHeap []redis.Z

func (h trackEventHeap) Len() int           { return len(h) }
func (h trackEventHeap) Less(i, j int) bool { return trackEventLess(h[i], h[j]) }
func (h trackEventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *trackEventHeap) Push(value any)    { *h = append(*h, value.(redis.Z)) }
func (h *trackEventHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	*h = (*h)[:last]
	return value
}

func trackEventLess(a, b redis.Z) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	aID, bID := a.Member.(string), b.Member.(string)
	if len(aID) != len(bID) {
		return len(aID) < len(bID)
	}
	return aID < bID
}

// ListTracksForSession returns the list of tracks from session state.
func (c *Client) ListTracksForSession(ctx context.Context, key session.Key) ([]session.Track, error) {
	metaJSON, err := c.client.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get session meta: %w", err)
	}

	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}

	return session.TracksFromState(meta.State)
}
