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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
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
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	track := trackEvent.Track
	keys := []string{
		c.keys.TrackDataKey(key, track),
		c.keys.TrackTimeIndexKey(key, track),
		c.keys.SessionMetaKey(key),
	}
	scriptSHA, err := luaAppendTrackEvent.Load(ctx, c.client).Result()
	if err != nil {
		return fmt.Errorf("load append track script: %w", err)
	}

	retryDelay := 5 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.client.Watch(ctx, func(tx *redis.Tx) error {
			mergedTracksState, err := c.mergedTracksState(ctx, tx, key, track, tracksState)
			if err != nil {
				return err
			}
			tracksVal := ""
			if len(mergedTracksState) > 0 {
				tracksVal = base64.StdEncoding.EncodeToString(mergedTracksState)
			}
			var appendCmd *redis.Cmd
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				appendCmd = pipe.EvalSha(
					ctx,
					scriptSHA,
					keys,
					string(eventJSON),
					trackEvent.Timestamp.UnixNano(),
					ttlSeconds,
					tracksVal,
				)
				return nil
			})
			if err != nil {
				if err == redis.TxFailedErr {
					return err
				}
				return fmt.Errorf("append track event: %w", err)
			}
			result, err := appendCmd.Int64()
			if err != nil {
				return fmt.Errorf("append track event result: %w", err)
			}
			if result == 0 {
				return fmt.Errorf("session not found")
			}
			return nil
		}, keys[2])
		if err == nil {
			return nil
		}
		if err == redis.TxFailedErr {
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
			if retryDelay < 100*time.Millisecond {
				retryDelay *= 2
			}
			continue
		}
		return err
	}
}

func (c *Client) mergedTracksState(
	ctx context.Context,
	tx *redis.Tx,
	key session.Key,
	track session.Track,
	fallback []byte,
) ([]byte, error) {
	metaJSON, err := tx.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("session not found")
		}
		return nil, fmt.Errorf("get session meta: %w", err)
	}
	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}
	tracks, err := session.TracksFromState(meta.State)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 && len(fallback) > 0 {
		var fallbackTracks []session.Track
		if err := json.Unmarshal(fallback, &fallbackTracks); err != nil {
			return nil, fmt.Errorf("decode fallback tracks: %w", err)
		}
		tracks = fallbackTracks
	}
	if !slices.Contains(tracks, track) {
		tracks = append(tracks, track)
	}
	return json.Marshal(tracks)
}

// GetTrackEvents retrieves track events for a session using Hash+ZSet structure.
// Each track is loaded via a Lua script that reads from ZSet index then HMGETs from Hash.
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
		events, err := c.loadTrackEventsViaLua(ctx, key, track, minScore, maxScore, limit)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			results[track] = events
		}
	}
	return results, nil
}

// loadTrackEventsViaLua loads track events for a single track via Lua script.
func (c *Client) loadTrackEventsViaLua(
	ctx context.Context,
	key session.Key,
	track session.Track,
	minScore, maxScore string,
	limit int,
) ([]session.TrackEvent, error) {
	rawEvents, err := luaLoadTrackEvents.Run(ctx, c.client,
		[]string{
			c.keys.TrackDataKey(key, track),
			c.keys.TrackTimeIndexKey(key, track),
		},
		minScore, maxScore, limit,
	).StringSlice()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
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
