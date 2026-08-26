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
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const revisionProjectionLimit = -1

const revisionWriteAttempts = 8

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

// RevisionGenerations returns revision generations for a session-key batch.
func (c *Client) RevisionGenerations(
	ctx context.Context,
	keys []session.Key,
) (map[session.Key]uint64, error) {
	generations := make(map[session.Key]uint64, len(keys))
	if len(keys) == 0 {
		return generations, nil
	}
	revisionKeys := make([]string, len(keys))
	for i, key := range keys {
		revisionKeys[i] = c.keys.RevisionKey(key)
	}
	values, err := c.client.MGet(ctx, revisionKeys...).Result()
	if err != nil {
		return nil, fmt.Errorf("get revision metadata batch: %w", err)
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		raw, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("decode revision metadata batch: unexpected value %T", value)
		}
		var record sessionrevision.PersistedRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode revision metadata batch: %w", err)
		}
		generations[keys[i]] = record.Generation
	}
	return generations, nil
}

// RevisionProjection loads the complete active projection without applying a
// caller-facing event or Track window.
func (c *Client) RevisionProjection(
	ctx context.Context,
	key session.Key,
) (*session.Session, error) {
	return c.GetSession(ctx, key, revisionProjectionLimit, time.Time{})
}

func (c *Client) prepareProjectionWrite(
	ctx context.Context,
	key session.Key,
	write sessionrevision.Write,
	advance func(*sessionrevision.PersistedRecord) error,
) (sessionrevision.Write, string, bool, error) {
	record, err := c.Revision(ctx, key)
	if err != nil {
		return write, "", false, err
	}
	if !write.HasExpectedHead {
		write.ExpectedHead = record.Head
		write.HasExpectedHead = true
	}
	projectionPrepared := record.Projection != nil || write.Projection != nil
	if write.Projection != nil {
		record.Projection = sessionrevision.CloneProjection(write.Projection)
	}
	if advance != nil {
		if err := advance(record); err != nil {
			return write, "", false, err
		}
	}
	if record.Projection == nil {
		return write, "", projectionPrepared, nil
	}
	raw, err := json.Marshal(record.Projection)
	if err != nil {
		return write, "", false, fmt.Errorf(
			"encode revision projection: %w", err,
		)
	}
	return write, string(raw), true, nil
}

// RevisionBoundaryBase loads the mutable session fields needed for a turn
// boundary without loading event or track histories. The returned boolean is
// false when expiring projection keys are already missing and the caller must
// rebuild the rolling projection from an authoritative full read.
func (c *Client) RevisionBoundaryBase(
	ctx context.Context,
	key session.Key,
	projection *sessionrevision.PersistedProjection,
) (*session.Session, bool, error) {
	pipe := c.client.Pipeline()
	metaCmd := pipe.Get(ctx, c.keys.SessionMetaKey(key))
	summaryCmd := pipe.Get(ctx, c.keys.SummaryKey(key))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, false, fmt.Errorf("load revision boundary base: %w", err)
	}
	metaJSON, err := metaCmd.Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load revision boundary metadata: %w", err)
	}
	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, false, fmt.Errorf("decode revision boundary metadata: %w", err)
	}
	base := session.NewSession(meta.AppName, meta.UserID, meta.ID)
	base.State = meta.State
	base.CreatedAt = meta.CreatedAt
	base.UpdatedAt = meta.UpdatedAt
	if summaryJSON, err := summaryCmd.Bytes(); err == nil {
		if err := json.Unmarshal(summaryJSON, &base.Summaries); err != nil {
			return nil, false, fmt.Errorf("decode revision boundary summaries: %w", err)
		}
	} else if err != redis.Nil {
		return nil, false, fmt.Errorf("load revision boundary summaries: %w", err)
	}
	intact, err := c.revisionProjectionStorageIntact(ctx, key, projection)
	if err != nil {
		return nil, false, err
	}
	return base, intact, nil
}

func (c *Client) revisionProjectionStorageIntact(
	ctx context.Context,
	key session.Key,
	projection *sessionrevision.PersistedProjection,
) (bool, error) {
	keys := make([]string, 0)
	if c.cfg.SessionTTL > 0 && projection != nil &&
		projection.Events.Count > 0 {
		keys = append(keys,
			c.keys.EventDataKey(key),
			c.keys.EventTimeIndexKey(key),
		)
	}
	if c.cfg.effectiveTrackEventTTL() > 0 && projection != nil {
		for track, prefix := range projection.Tracks {
			if prefix.Count == 0 {
				continue
			}
			keys = append(keys,
				c.keys.TrackDataKey(key, track),
				c.keys.TrackTimeIndexKey(key, track),
			)
		}
	}
	if len(keys) == 0 {
		return true, nil
	}
	existing, err := c.client.Exists(ctx, keys...).Result()
	if err != nil {
		return false, fmt.Errorf("validate revision projection storage: %w", err)
	}
	return existing == int64(len(keys)), nil
}

// Rewind atomically restores the checkpoint immediately before the
// latest persisted turn.
func (c *Client) Rewind(
	ctx context.Context,
	key session.Key,
	targetRequestID string,
	expectedHeadRequestID string,
	idempotencyKey string,
) (*session.Session, bool, error) {
	revisionKey := c.keys.RevisionKey(key)
	var (
		active  *session.Session
		applied bool
	)
	for attempt := 0; attempt < revisionWriteAttempts; attempt++ {
		trackNames, err := c.client.SMembers(
			ctx, c.keys.TrackIndexKey(key),
		).Result()
		if err != nil {
			return nil, false, fmt.Errorf("list active tracks: %w", err)
		}
		slices.Sort(trackNames)
		watchKeys := []string{
			revisionKey,
			c.keys.SessionMetaKey(key),
			c.keys.EventDataKey(key),
			c.keys.EventTimeIndexKey(key),
			c.keys.SummaryKey(key),
			c.keys.TrackIndexKey(key),
		}
		for _, trackName := range trackNames {
			track := session.Track(trackName)
			watchKeys = append(watchKeys,
				c.keys.TrackDataKey(key, track),
				c.keys.TrackTimeIndexKey(key, track),
			)
		}
		err = c.client.Watch(ctx, func(tx *redis.Tx) error {
			currentTrackNames, err := tx.SMembers(
				ctx, c.keys.TrackIndexKey(key),
			).Result()
			if err != nil {
				return err
			}
			slices.Sort(currentTrackNames)
			if !slices.Equal(trackNames, currentTrackNames) {
				return redis.TxFailedErr
			}
			record, err := revisionRecordFromCmd(ctx, tx, revisionKey)
			if err != nil {
				return err
			}
			if _, replayed, err := sessionrevision.RewindReplay(
				record,
				targetRequestID,
				expectedHeadRequestID,
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
						// Force EXEC so Redis validates the WATCH set before an
						// idempotent replay result is accepted.
						pipe.Get(ctx, revisionKey)
						return nil
					},
				); err != nil {
					return err
				}
				sessionrevision.AttachRewindFence(active, record)
				applied = false
				return nil
			}
			checkpoint, err := sessionrevision.RewindCheckpoint(
				record,
				targetRequestID,
				expectedHeadRequestID,
			)
			if err != nil {
				return err
			}
			if record.Generation == math.MaxUint64 {
				return sessionrevision.ErrRewindUnavailable
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
				return sessionrevision.ErrRewindUnavailable
			}

			record.Generation++
			record.Head++
			record.HeadRequestID = checkpoint.PriorHeadRequestID
			record.Checkpoint = nil
			if err := sessionrevision.ResetProjectionFromBoundary(
				record, checkpoint.Boundary,
			); err != nil {
				return err
			}
			sessionrevision.RecordRewindReplay(
				record,
				idempotencyKey,
				sessionrevision.PersistedReplay{
					TargetRequestID:       targetRequestID,
					ExpectedHeadRequestID: expectedHeadRequestID,
					Generation:            record.Generation,
					Head:                  record.Head,
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
			sessionrevision.AttachRewindFence(active, record)
			applied = true
			return nil
		}, watchKeys...)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return active, applied, nil
	}
	return nil, false, fmt.Errorf("latest-turn replacement contention: %w", sessionrevision.ErrRewindUnavailable)
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
		return nil, sessionrevision.ErrRewindUnavailable
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
	metaKey := c.keys.SessionMetaKey(key)
	storedMetaJSON, err := tx.Get(ctx, metaKey).Bytes()
	if err != nil {
		return fmt.Errorf("load active session metadata: %w", err)
	}
	var storedMeta sessionMeta
	if err := json.Unmarshal(storedMetaJSON, &storedMeta); err != nil {
		return fmt.Errorf("decode active session metadata: %w", err)
	}
	metaJSON, err := json.Marshal(sessionMeta{
		ID: key.SessionID, AppName: key.AppName, UserID: key.UserID,
		State: restored.SnapshotState(), Generation: storedMeta.Generation,
		CreatedAt: restored.CreatedAt, UpdatedAt: restored.UpdatedAt,
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

	sourceTTL := remainingTTLs[metaKey]
	if !validRemainingTTL(sourceTTL) {
		return sessionrevision.ErrRewindUnavailable
	}
	if len(restored.Events) > 0 &&
		(!validRemainingTTL(remainingTTLs[c.keys.EventDataKey(key)]) ||
			!validRemainingTTL(remainingTTLs[c.keys.EventTimeIndexKey(key)])) {
		return sessionrevision.ErrRewindUnavailable
	}
	if len(restored.Summaries) > 0 &&
		!validRemainingTTL(remainingTTLs[c.keys.SummaryKey(key)]) {
		return sessionrevision.ErrRewindUnavailable
	}
	hasRestoredTracks := false
	for track, history := range restored.Tracks {
		if history == nil || len(history.Events) == 0 {
			continue
		}
		hasRestoredTracks = true
		if !validRemainingTTL(remainingTTLs[c.keys.TrackDataKey(key, track)]) ||
			!validRemainingTTL(remainingTTLs[c.keys.TrackTimeIndexKey(key, track)]) {
			return sessionrevision.ErrRewindUnavailable
		}
	}
	if hasRestoredTracks &&
		!validRemainingTTL(remainingTTLs[c.keys.TrackIndexKey(key)]) {
		return sessionrevision.ErrRewindUnavailable
	}
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, metaKey, metaJSON, 0)
		applyRemainingTTL(ctx, pipe, metaKey, sourceTTL)
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
		applyRemainingTTL(ctx, pipe, revisionKey, sourceTTL)
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
) {
	if remaining >= 0 {
		pipe.PExpire(ctx, key, remaining)
		return
	}
	if remaining == -1 {
		pipe.Persist(ctx, key)
	}
}

func validRemainingTTL(ttl time.Duration) bool {
	return ttl == -1 || ttl > 0
}
