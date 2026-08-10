//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	record, _, err := readRevisionRecord(ctx, c.client, c.revisionKey(key))
	return record, err
}

// RevisionProjection loads the complete active projection without applying a
// caller-facing event or Track window.
func (c *Client) RevisionProjection(
	ctx context.Context,
	key session.Key,
) (*session.Session, error) {
	return c.GetSession(ctx, key, revisionProjectionLimit, time.Time{})
}

func readRevisionRecord(
	ctx context.Context,
	cmd redis.Cmdable,
	key string,
) (*sessionrevision.PersistedRecord, bool, error) {
	raw, err := cmd.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return &sessionrevision.PersistedRecord{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get revision metadata: %w", err)
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, false, fmt.Errorf("decode revision metadata: %w", err)
	}
	return &record, true, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ReplaceLatestTurn atomically archives the active projection and restores the
// checkpoint immediately before the latest persisted turn.
func (c *Client) ReplaceLatestTurn(
	ctx context.Context,
	key session.Key,
	expectedRequestID string,
	idempotencyKey string,
) (*session.Session, bool, error) {
	revisionKey := c.revisionKey(key)
	var (
		active  *session.Session
		applied bool
	)
	for attempt := 0; attempt < 8; attempt++ {
		err := c.client.Watch(ctx, func(tx *redis.Tx) error {
			record, _, err := readRevisionRecord(ctx, tx, revisionKey)
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
			restored, err := sessionrevision.DecodeSnapshot(checkpoint.Snapshot)
			if err != nil {
				return fmt.Errorf("decode latest-turn checkpoint: %w", err)
			}
			current, err := c.requiredRevisionProjection(ctx, key)
			if err != nil {
				return fmt.Errorf("load active session for archive: %w", err)
			}
			archive, err := sessionrevision.Snapshot(current)
			if err != nil {
				return fmt.Errorf("encode discarded revision: %w", err)
			}
			remainingTTLs, err := c.activeProjectionTTLs(ctx, tx, key, current)
			if err != nil {
				return err
			}
			if remainingTTLs[c.sessionStateKey(key)] == -2 {
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
				archive,
				record.Generation-1,
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

func (c *Client) replaceActiveSession(
	ctx context.Context,
	tx *redis.Tx,
	key session.Key,
	current *session.Session,
	restored *session.Session,
	archive []byte,
	archiveGeneration uint64,
	recordJSON []byte,
	remainingTTLs map[string]time.Duration,
) error {
	stateJSON, err := json.Marshal(&SessionState{
		ID:        key.SessionID,
		State:     restored.SnapshotState(),
		CreatedAt: restored.CreatedAt,
		UpdatedAt: restored.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("encode restored session state: %w", err)
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

	stateKey := c.sessionStateKey(key)
	sourceTTL := remainingTTLs[stateKey]
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, stateKey, key.SessionID, stateJSON)
		pipe.Del(ctx, c.eventKey(key))
		for i := range restored.Events {
			evt := restored.Events[i]
			raw, marshalErr := json.Marshal(&evt)
			if marshalErr != nil {
				return fmt.Errorf("encode restored event: %w", marshalErr)
			}
			pipe.ZAdd(ctx, c.eventKey(key), redis.Z{
				Score:  float64(evt.Timestamp.UnixNano()),
				Member: raw,
			})
		}
		pipe.HDel(ctx, c.sessionSummaryKey(key), key.SessionID)
		if len(restored.Summaries) > 0 {
			pipe.HSet(ctx, c.sessionSummaryKey(key), key.SessionID, summariesJSON)
		}
		pipe.Del(ctx, c.trackIndexKey(key))
		for track := range tracks {
			pipe.Del(ctx, c.trackKey(key, track))
		}
		for track, history := range restored.Tracks {
			if history == nil || len(history.Events) == 0 {
				continue
			}
			pipe.SAdd(ctx, c.trackIndexKey(key), string(track))
			for i := range history.Events {
				trackEvent := history.Events[i]
				raw, marshalErr := json.Marshal(&trackEvent)
				if marshalErr != nil {
					return fmt.Errorf("encode restored track event: %w", marshalErr)
				}
				pipe.ZAdd(ctx, c.trackKey(key, track), redis.Z{
					Score:  float64(trackEvent.Timestamp.UnixNano()),
					Member: raw,
				})
			}
		}
		archiveKey := c.revisionArchiveKey(key)
		pipe.HSet(ctx, archiveKey, archiveGeneration, archive)
		applyRemainingTTL(ctx, pipe, archiveKey, sourceTTL, sourceTTL)
		revisionKey := c.revisionKey(key)
		pipe.Set(ctx, revisionKey, recordJSON, 0)
		applyRemainingTTL(ctx, pipe, revisionKey, sourceTTL, sourceTTL)
		for _, projectionKey := range []string{
			stateKey,
			c.eventKey(key),
			c.sessionSummaryKey(key),
			c.trackIndexKey(key),
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
			trackKey := c.trackKey(key, track)
			applyRemainingTTL(
				ctx,
				pipe,
				trackKey,
				remainingTTLs[trackKey],
				sourceTTL,
			)
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
		c.sessionStateKey(key),
		c.eventKey(key),
		c.sessionSummaryKey(key),
		c.trackIndexKey(key),
		c.revisionKey(key),
		c.revisionArchiveKey(key),
	}
	for track := range current.Tracks {
		keys = append(keys, c.trackKey(key, track))
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
