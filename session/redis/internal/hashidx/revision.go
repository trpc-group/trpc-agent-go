//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hashidx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const revisionProjectionLimit = -1

// Revision returns the private revision metadata for a session.
func (c *Client) Revision(
	ctx context.Context,
	key session.Key,
) (*sessionrevision.PersistedRecord, error) {
	raw, err := c.client.Get(ctx, c.keys.RevisionKey(key)).Bytes()
	if err == redis.Nil {
		return &sessionrevision.PersistedRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get revision metadata: %w", err)
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode revision metadata: %w", err)
	}
	return &record, nil
}

// RevisionProjection loads the complete active projection without applying a
// caller-facing event or Track window.
func (c *Client) RevisionProjection(
	ctx context.Context,
	key session.Key,
) (*session.Session, error) {
	return c.GetSession(ctx, key, revisionProjectionLimit, time.Time{})
}

// ReplaceLatestTurn atomically restores the checkpoint immediately before the
// latest persisted turn.
func (c *Client) ReplaceLatestTurn(
	ctx context.Context,
	key session.Key,
	expectedRequestID string,
	idempotencyKey string,
) (*session.Session, bool, error) {
	revisionKey := c.keys.RevisionKey(key)
	var (
		active  *session.Session
		applied bool
	)
	for attempt := 0; attempt < 8; attempt++ {
		err := c.client.Watch(ctx, func(tx *redis.Tx) error {
			record, err := revisionRecordFromCmd(ctx, tx, revisionKey)
			if err != nil {
				return err
			}
			if _, replayed, err := sessionrevision.LatestTurnReplacementReplay(
				record,
				expectedRequestID,
				idempotencyKey,
			); err != nil {
				return err
			} else if replayed {
				active, err = c.requiredRevisionProjection(ctx, key)
				if err != nil {
					return fmt.Errorf("load idempotent replacement result: %w", err)
				}
				if _, err := tx.TxPipelined(
					ctx,
					func(pipe redis.Pipeliner) error {
						pipe.Get(ctx, revisionKey)
						return nil
					},
				); err != nil {
					return err
				}
				sessionrevision.SetGeneration(active, record.Generation)
				applied = false
				return nil
			}
			checkpoint, err := sessionrevision.LatestTurnReplacementCheckpoint(
				record,
				expectedRequestID,
			)
			if err != nil {
				return err
			}
			if record.Generation == math.MaxUint64 {
				return sessionrevision.ErrLatestTurnReplacementUnavailable
			}
			current, err := c.requiredRevisionProjection(ctx, key)
			if err != nil {
				return fmt.Errorf("load active session for replacement: %w", err)
			}
			restored, err := sessionrevision.RestoreBoundary(
				current, checkpoint.Boundary,
			)
			if err != nil {
				return fmt.Errorf("restore latest-turn boundary: %w", err)
			}
			remainingTTLs, err := c.activeProjectionTTLs(ctx, tx, key, current)
			if err != nil {
				return err
			}
			if remainingTTLs[c.keys.SessionMetaKey(key)] == -2 {
				return sessionrevision.ErrLatestTurnReplacementUnavailable
			}

			record.Generation++
			record.Head++
			record.Checkpoint = nil
			sessionrevision.RecordLatestTurnReplacementReplay(
				record,
				idempotencyKey,
				sessionrevision.PersistedReplay{
					RequestID:  expectedRequestID,
					Generation: record.Generation,
					Head:       record.Head,
				},
			)
			recordJSON, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("encode revision metadata: %w", err)
			}
			if err := c.replaceActiveSession(
				ctx,
				tx,
				key,
				current,
				restored,
				recordJSON,
				remainingTTLs,
			); err != nil {
				return err
			}
			active = restored
			sessionrevision.SetGeneration(active, record.Generation)
			applied = true
			return nil
		}, revisionKey)
		if err == redis.TxFailedErr {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return active, applied, nil
	}
	return nil, false, fmt.Errorf("latest-turn replacement contention: %w", sessionrevision.ErrLatestTurnReplacementUnavailable)
}

func (c *Client) requiredRevisionProjection(
	ctx context.Context,
	key session.Key,
) (*session.Session, error) {
	active, err := c.RevisionProjection(ctx, key)
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	return active, nil
}

func revisionRecordFromCmd(
	ctx context.Context,
	cmd redis.Cmdable,
	key string,
) (*sessionrevision.PersistedRecord, error) {
	raw, err := cmd.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return &sessionrevision.PersistedRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get revision metadata: %w", err)
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode revision metadata: %w", err)
	}
	return &record, nil
}

func (c *Client) replaceActiveSession(
	ctx context.Context,
	tx *redis.Tx,
	key session.Key,
	current *session.Session,
	restored *session.Session,
	recordJSON []byte,
	remainingTTLs map[string]time.Duration,
) error {
	metaJSON, err := json.Marshal(sessionMeta{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     restored.SnapshotState(),
		CreatedAt: restored.CreatedAt,
		UpdatedAt: restored.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("encode restored session metadata: %w", err)
	}
	summariesJSON, err := json.Marshal(restored.Summaries)
	if err != nil {
		return fmt.Errorf("encode restored summaries: %w", err)
	}
	tracks := make(map[session.Track]struct{}, len(current.Tracks)+len(restored.Tracks))
	for track := range current.Tracks {
		tracks[track] = struct{}{}
	}
	for track := range restored.Tracks {
		tracks[track] = struct{}{}
	}

	metaKey := c.keys.SessionMetaKey(key)
	sourceTTL := remainingTTLs[metaKey]
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, metaKey, metaJSON, 0)
		applyRemainingTTL(ctx, pipe, metaKey, sourceTTL, sourceTTL)
		pipe.Del(ctx, c.keys.EventDataKey(key), c.keys.EventTimeIndexKey(key))
		for i := range restored.Events {
			evt := restored.Events[i]
			raw, marshalErr := json.Marshal(&evt)
			if marshalErr != nil {
				return fmt.Errorf("encode restored event: %w", marshalErr)
			}
			pipe.HSet(ctx, c.keys.EventDataKey(key), evt.ID, raw)
			pipe.ZAdd(ctx, c.keys.EventTimeIndexKey(key), redis.Z{
				Score:  float64(evt.Timestamp.UnixNano()),
				Member: evt.ID,
			})
		}
		pipe.Del(ctx, c.keys.SummaryKey(key))
		if len(restored.Summaries) > 0 {
			pipe.Set(ctx, c.keys.SummaryKey(key), summariesJSON, c.cfg.SessionTTL)
		}
		pipe.Del(ctx, c.keys.TrackIndexKey(key))
		for track := range tracks {
			pipe.Del(ctx, c.keys.TrackDataKey(key, track), c.keys.TrackTimeIndexKey(key, track))
		}
		for track, history := range restored.Tracks {
			if history == nil || len(history.Events) == 0 {
				continue
			}
			pipe.SAdd(ctx, c.keys.TrackIndexKey(key), string(track))
			for i := range history.Events {
				trackEvent := history.Events[i]
				raw, marshalErr := json.Marshal(&trackEvent)
				if marshalErr != nil {
					return fmt.Errorf("encode restored track event: %w", marshalErr)
				}
				id := "replacement-" + strconv.Itoa(i+1)
				pipe.HSet(ctx, c.keys.TrackDataKey(key, track), id, raw)
				pipe.ZAdd(ctx, c.keys.TrackTimeIndexKey(key, track), redis.Z{
					Score:  float64(trackEvent.Timestamp.UnixNano()),
					Member: id,
				})
			}
			pipe.HSet(ctx, c.keys.TrackDataKey(key, track), "_seq", len(history.Events))
		}
		revisionKey := c.keys.RevisionKey(key)
		pipe.Set(ctx, revisionKey, recordJSON, 0)
		applyRemainingTTL(ctx, pipe, revisionKey, sourceTTL, sourceTTL)
		for _, projectionKey := range []string{
			c.keys.EventDataKey(key),
			c.keys.EventTimeIndexKey(key),
			c.keys.SummaryKey(key),
			c.keys.TrackIndexKey(key),
		} {
			applyRemainingTTL(
				ctx,
				pipe,
				projectionKey,
				remainingTTLs[projectionKey],
				sourceTTL,
			)
		}
		for track := range restored.Tracks {
			for _, trackKey := range []string{
				c.keys.TrackDataKey(key, track),
				c.keys.TrackTimeIndexKey(key, track),
			} {
				applyRemainingTTL(
					ctx,
					pipe,
					trackKey,
					remainingTTLs[trackKey],
					sourceTTL,
				)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("replace active session: %w", err)
	}
	return nil
}

func (c *Client) activeProjectionTTLs(
	ctx context.Context,
	tx *redis.Tx,
	key session.Key,
	current *session.Session,
) (map[string]time.Duration, error) {
	keys := []string{
		c.keys.SessionMetaKey(key),
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
		c.keys.SummaryKey(key),
		c.keys.TrackIndexKey(key),
	}
	for track := range current.Tracks {
		keys = append(
			keys,
			c.keys.TrackDataKey(key, track),
			c.keys.TrackTimeIndexKey(key, track),
		)
	}
	remaining := make(map[string]time.Duration, len(keys))
	for _, redisKey := range keys {
		ttl, err := tx.PTTL(ctx, redisKey).Result()
		if err != nil {
			return nil, fmt.Errorf("read active projection TTL: %w", err)
		}
		remaining[redisKey] = ttl
	}
	return remaining, nil
}

func applyRemainingTTL(
	ctx context.Context,
	pipe redis.Pipeliner,
	key string,
	remaining time.Duration,
	fallback time.Duration,
) {
	if remaining == -2 {
		remaining = fallback
	}
	if remaining >= 0 {
		pipe.PExpire(ctx, key, remaining)
		return
	}
	if remaining == -1 {
		pipe.Persist(ctx, key)
	}
}
