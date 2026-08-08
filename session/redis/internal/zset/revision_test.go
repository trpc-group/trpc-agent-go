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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReplaceLatestTurnPreservesRemainingTTL(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	cfg := defaultConfig()
	cfg.SessionTTL = time.Hour
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	sessionrevision.SetGeneration(sess, 0)
	baseline := revisionEvent("baseline", "baseline", "baseline-invocation", false)
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		baseline,
		sessionrevision.Write{},
	))
	require.NoError(t, c.AppendTrackEventWithRevision(
		ctx,
		key,
		&session.TrackEvent{
			Track: "ui", Payload: []byte(`{"before":true}`), Timestamp: time.Now(),
		},
		sessionrevision.Write{},
	))
	require.NoError(t, c.CreateSummaryWithRevision(
		ctx,
		key,
		"",
		&session.Summary{Summary: "before", UpdatedAt: time.Now()},
		cfg.SessionTTL,
		sessionrevision.Write{},
	))
	sess, err = c.RevisionProjection(ctx, key)
	require.NoError(t, err)

	start := revisionEvent("turn", "request", "invocation", false)
	snapshot, err := sessionrevision.Snapshot(sess)
	require.NoError(t, err)
	require.NoError(t, c.AppendEventWithRevision(ctx, key, start, sessionrevision.Write{
		HasExpectedGeneration: true,
		Start: &sessionrevision.TurnStart{
			RequestID: "request", InvocationID: "invocation",
		},
		Snapshot: snapshot,
	}))
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("completion", "request", "invocation", true),
		sessionrevision.Write{HasExpectedGeneration: true},
	))
	mr.FastForward(20 * time.Minute)
	stateKey := c.sessionStateKey(key)
	before := mr.TTL(stateKey)

	active, applied, err := c.ReplaceLatestTurn(ctx, key, "request", "replacement")
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, before, mr.TTL(stateKey))
	assert.Equal(t, before, mr.TTL(c.revisionKey(key)))
	assert.Equal(t, before, mr.TTL(c.revisionArchiveKey(key)))
	replayed, replayApplied, err := c.ReplaceLatestTurn(
		ctx,
		key,
		"request",
		"replacement",
	)
	require.NoError(t, err)
	assert.False(t, replayApplied)
	require.NotNil(t, replayed)
	assert.Equal(t, active.Events, replayed.Events)

	generation, ok := sessionrevision.Generation(active)
	require.True(t, ok)
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("after", "after", "after-invocation", false),
		sessionrevision.Write{
			HasExpectedGeneration: true,
			ExpectedGeneration:    generation,
		},
	))
	assert.Equal(t, cfg.SessionTTL, mr.TTL(stateKey))
	assert.Equal(t, mr.TTL(stateKey), mr.TTL(c.revisionKey(key)))
	assert.Equal(t, mr.TTL(stateKey), mr.TTL(c.revisionArchiveKey(key)))
}

func TestApplyRemainingTTL(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()
	const key = "projection"
	require.NoError(t, rdb.Set(ctx, key, "value", time.Minute).Err())

	pipe := rdb.Pipeline()
	applyRemainingTTL(ctx, pipe, key, -1, 0)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)
	ttl, err := rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, time.Duration(-1), ttl)

	pipe = rdb.Pipeline()
	applyRemainingTTL(ctx, pipe, key, -2, 5*time.Minute)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	ttl, err = rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, ttl)
}

func TestRevisionMetadataFailures(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	revisionKey := c.revisionKey(key)

	record, err := c.Revision(ctx, key)
	require.NoError(t, err)
	assert.Empty(t, record.Generation)
	require.NoError(t, rdb.Set(ctx, revisionKey, "not-json", 0).Err())
	_, err = c.Revision(ctx, key)
	assert.ErrorContains(t, err, "decode revision metadata")
	_, _, err = c.ReplaceLatestTurn(ctx, key, "request", "replacement")
	assert.ErrorContains(t, err, "decode revision metadata")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.Revision(cancelled, key)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReplaceLatestTurnRejectsInvalidRevisionRecords(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	revisionKey := c.revisionKey(key)

	setRecord := func(record sessionrevision.PersistedRecord) {
		raw, err := json.Marshal(record)
		require.NoError(t, err)
		require.NoError(t, rdb.Set(ctx, revisionKey, raw, 0).Err())
	}
	tests := []struct {
		name   string
		record sessionrevision.PersistedRecord
		is     error
	}{
		{
			name: "missing checkpoint",
			is:   sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "request mismatch",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "other", Terminal: true, Snapshot: []byte(`{}`),
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "generation exhausted",
			record: sessionrevision.PersistedRecord{
				Generation: math.MaxUint64,
				Checkpoint: &sessionrevision.PersistedCheckpoint{
					RequestID: "request", Terminal: true, Snapshot: []byte(`{}`),
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "invalid checkpoint",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "request", Terminal: true, Snapshot: []byte("not-json"),
			}},
		},
		{
			name: "replay identity mismatch",
			record: sessionrevision.PersistedRecord{Replays: map[string]sessionrevision.PersistedReplay{
				"replacement": {RequestID: "other"},
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "replay generation mismatch",
			record: sessionrevision.PersistedRecord{
				Generation: 2,
				Replays: map[string]sessionrevision.PersistedReplay{
					"replacement": {RequestID: "request", Generation: 1},
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "replay session missing",
			record: sessionrevision.PersistedRecord{Replays: map[string]sessionrevision.PersistedReplay{
				"replacement": {RequestID: "request"},
			}},
			is: sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRecord(tt.record)
			_, _, err := c.ReplaceLatestTurn(
				ctx,
				key,
				"request",
				"replacement",
			)
			require.Error(t, err)
			if tt.is != nil {
				assert.ErrorIs(t, err, tt.is)
			}
		})
	}
}

func TestTurnStartRejectsChangedProjection(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	snapshot, err := sessionrevision.Snapshot(sess)
	require.NoError(t, err)

	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("earlier", "earlier", "earlier-invocation", false),
		sessionrevision.Write{},
	))
	err = c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("turn", "request", "invocation", false),
		sessionrevision.Write{
			HasExpectedGeneration: true,
			HasExpectedHead:       true,
			ExpectedHead:          0,
			Start: &sessionrevision.TurnStart{
				RequestID: "request", InvocationID: "invocation",
			},
			Snapshot: snapshot,
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
}

func TestRevisionProjectionIgnoresReadLimit(t *testing.T) {
	_, rdb := setupMiniredis(t)
	cfg := defaultConfig()
	cfg.SessionEventLimit = 1
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	for i, id := range []string{"first", "second"} {
		evt := revisionEvent(id, id, id+"-invocation", false)
		evt.Timestamp = time.Now().Add(time.Duration(i) * time.Second)
		require.NoError(t, c.AppendEventWithRevision(
			ctx,
			key,
			evt,
			sessionrevision.Write{},
		))
	}
	bounded, err := c.GetSession(ctx, key, cfg.SessionEventLimit, time.Time{})
	require.NoError(t, err)
	require.Len(t, bounded.Events, 1)

	projection, err := c.RevisionProjection(ctx, key)
	require.NoError(t, err)
	require.Len(t, projection.Events, 2)
}

func revisionEvent(
	id string,
	requestID string,
	invocationID string,
	completion bool,
) *event.Event {
	response := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: id},
		}},
	}
	if completion {
		response.Object = model.ObjectTypeRunnerCompletion
		response.Choices = nil
	}
	return &event.Event{
		ID:           id,
		RequestID:    requestID,
		InvocationID: invocationID,
		Timestamp:    time.Now(),
		Response:     response,
	}
}
