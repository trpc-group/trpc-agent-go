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
		[]byte(`["ui"]`),
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
	boundary, err := sessionrevision.NewBoundary(sess)
	require.NoError(t, err)
	require.NoError(t, c.AppendEventWithRevision(ctx, key, start, sessionrevision.Write{
		HasExpectedGeneration: true,
		Start: &sessionrevision.TurnStart{
			RequestID: "request", InvocationID: "invocation",
		},
		Boundary: boundary,
	}))
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("completion", "request", "invocation", true),
		sessionrevision.Write{HasExpectedGeneration: true},
	))
	mr.FastForward(20 * time.Minute)
	metaKey := c.keys.SessionMetaKey(key)
	before := mr.TTL(metaKey)

	active, applied, err := c.ReplaceLatestTurn(ctx, key, "request", "replacement")
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, before, mr.TTL(metaKey))
	assert.Equal(t, before, mr.TTL(c.keys.RevisionKey(key)))
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

	err = c.CreateSummaryWithRevision(
		ctx,
		key,
		"",
		&session.Summary{Summary: "stale", UpdatedAt: time.Now()},
		cfg.SessionTTL,
		sessionrevision.Write{
			HasExpectedGeneration: true,
			ExpectedGeneration:    0,
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = c.CreateSummaryWithRevision(
		cancelled,
		key,
		"",
		&session.Summary{Summary: "cancelled", UpdatedAt: time.Now()},
		cfg.SessionTTL,
		sessionrevision.Write{},
	)
	assert.ErrorIs(t, err, context.Canceled)

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
	assert.Equal(t, cfg.SessionTTL, mr.TTL(metaKey))
	assert.Equal(t, mr.TTL(metaKey), mr.TTL(c.keys.RevisionKey(key)))
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
	revisionKey := c.keys.RevisionKey(key)

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
	revisionKey := c.keys.RevisionKey(key)

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
				RequestID: "other", Terminal: true, Boundary: []byte(`{}`),
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "generation exhausted",
			record: sessionrevision.PersistedRecord{
				Generation: math.MaxUint64,
				Checkpoint: &sessionrevision.PersistedCheckpoint{
					RequestID: "request", Terminal: true, Boundary: []byte(`{}`),
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "invalid checkpoint",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "request", Terminal: true, Boundary: []byte("not-json"),
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

func TestTrackTTLDoesNotShortenRevisionLifetime(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	trackTTL := 5 * time.Minute
	cfg := defaultConfig()
	cfg.SessionTTL = time.Hour
	cfg.TrackEventTTL = &trackTTL
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	mr.FastForward(10 * time.Minute)

	require.NoError(t, c.AppendTrackEventWithRevision(
		ctx,
		key,
		&session.TrackEvent{
			Track: "ui", Payload: []byte(`{}`), Timestamp: time.Now(),
		},
		[]byte(`["ui"]`),
		sessionrevision.Write{},
	))
	metaTTL := mr.TTL(c.keys.SessionMetaKey(key))
	assert.Equal(t, 50*time.Minute, metaTTL)
	assert.Equal(t, metaTTL, mr.TTL(c.keys.RevisionKey(key)))
	assert.Equal(t, trackTTL, mr.TTL(c.keys.TrackDataKey(key, "ui")))
}

func TestTurnStartRejectsChangedProjection(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	boundary, err := sessionrevision.NewBoundary(sess)
	require.NoError(t, err)
	projection := initializedHashidxProjection(t, c, ctx, key)

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
			Boundary:   boundary,
			Projection: projection,
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
	record, err := c.Revision(ctx, key)
	require.NoError(t, err)
	assert.False(t, sessionrevision.ProjectionInitialized(record))
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

func TestOutOfOrderWritesInvalidateRollingProjection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		equalScore bool
	}{
		{name: "older score"},
		{name: "equal score", equalScore: true},
	} {
		t.Run("event "+tt.name, func(t *testing.T) {
			_, rdb := setupMiniredis(t)
			c := NewClient(rdb, defaultConfig())
			ctx := context.Background()
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "event-" + tt.name,
			}
			_, err := c.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			baseTime := time.Now().Add(-10 * time.Second)
			initial := revisionEvent("initial", "initial", "initial", false)
			initial.Timestamp = baseTime
			require.NoError(t, c.AppendEventWithRevision(
				ctx, key, initial, sessionrevision.Write{},
			))
			projection := initializedHashidxProjection(t, c, ctx, key)
			latest := revisionEvent("latest", "latest", "latest", false)
			latest.Timestamp = baseTime.Add(2 * time.Second)
			require.NoError(t, c.AppendEventWithRevision(
				ctx,
				key,
				latest,
				sessionrevision.Write{Projection: projection},
			))

			outOfOrder := revisionEvent("out-of-order", "older", "older", false)
			outOfOrder.Timestamp = baseTime.Add(time.Second)
			if tt.equalScore {
				outOfOrder.Timestamp = latest.Timestamp
			}
			require.NoError(t, c.AppendEventWithRevision(
				ctx, key, outOfOrder, sessionrevision.Write{},
			))
			record, err := c.Revision(ctx, key)
			require.NoError(t, err)
			assert.False(t, sessionrevision.ProjectionInitialized(record))
		})

		t.Run("track "+tt.name, func(t *testing.T) {
			_, rdb := setupMiniredis(t)
			c := NewClient(rdb, defaultConfig())
			ctx := context.Background()
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "track-" + tt.name,
			}
			_, err := c.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			baseTime := time.Now().Add(-10 * time.Second)
			tracksState := []byte(`["ui"]`)
			require.NoError(t, c.AppendTrackEventWithRevision(
				ctx,
				key,
				&session.TrackEvent{
					Track: "ui", Payload: []byte(`{"value":0}`), Timestamp: baseTime,
				},
				tracksState,
				sessionrevision.Write{},
			))
			projection := initializedHashidxProjection(t, c, ctx, key)
			latest := &session.TrackEvent{
				Track: "ui", Payload: []byte(`{"value":2}`),
				Timestamp: baseTime.Add(2 * time.Second),
			}
			require.NoError(t, c.AppendTrackEventWithRevision(
				ctx,
				key,
				latest,
				tracksState,
				sessionrevision.Write{Projection: projection},
			))
			outOfOrder := &session.TrackEvent{
				Track: "ui", Payload: []byte(`{"value":1}`),
				Timestamp: baseTime.Add(time.Second),
			}
			if tt.equalScore {
				outOfOrder.Timestamp = latest.Timestamp
			}
			require.NoError(t, c.AppendTrackEventWithRevision(
				ctx,
				key,
				outOfOrder,
				tracksState,
				sessionrevision.Write{},
			))
			record, err := c.Revision(ctx, key)
			require.NoError(t, err)
			assert.False(t, sessionrevision.ProjectionInitialized(record))
		})
	}
}

func TestRevisionBoundaryBaseDetectsExpiredProjectionKeys(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	cfg := defaultConfig()
	cfg.SessionTTL = time.Hour
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "ttl-gap"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	emptyProjection := initializedHashidxProjection(t, c, ctx, key)
	_, intact, err := c.RevisionBoundaryBase(ctx, key, emptyProjection)
	require.NoError(t, err)
	assert.True(t, intact)
	initial := revisionEvent("initial", "initial", "initial", false)
	require.NoError(t, c.AppendEventWithRevision(
		ctx, key, initial, sessionrevision.Write{},
	))
	projection := initializedHashidxProjection(t, c, ctx, key)
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("latest", "latest", "latest", false),
		sessionrevision.Write{Projection: projection},
	))
	mr.FastForward(30 * time.Minute)
	require.NoError(t, c.UpdateSessionStateWithRevision(
		ctx,
		key,
		session.StateMap{"phase": []byte("active")},
		sessionrevision.Write{},
	))
	mr.FastForward(31 * time.Minute)

	base, intact, err := c.RevisionBoundaryBase(ctx, key, projection)
	require.NoError(t, err)
	require.NotNil(t, base)
	assert.False(t, intact)
	assert.False(t, mr.Exists(c.keys.EventDataKey(key)))
	assert.False(t, mr.Exists(c.keys.EventTimeIndexKey(key)))
}

func TestDeleteEventInvalidatesRollingProjection(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "delete"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("initial", "initial", "initial", false),
		sessionrevision.Write{},
	))
	projection := initializedHashidxProjection(t, c, ctx, key)
	require.NoError(t, c.AppendEventWithRevision(
		ctx,
		key,
		revisionEvent("delete-me", "latest", "latest", false),
		sessionrevision.Write{Projection: projection},
	))
	before, err := c.Revision(ctx, key)
	require.NoError(t, err)
	require.True(t, sessionrevision.ProjectionInitialized(before))

	require.NoError(t, c.DeleteEvent(ctx, key, "delete-me"))
	after, err := c.Revision(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, before.Head+1, after.Head)
	assert.False(t, sessionrevision.ProjectionInitialized(after))
}

func initializedHashidxProjection(
	t *testing.T,
	c *Client,
	ctx context.Context,
	key session.Key,
) *sessionrevision.PersistedProjection {
	t.Helper()
	active, err := c.RevisionProjection(ctx, key)
	require.NoError(t, err)
	record := &sessionrevision.PersistedRecord{}
	require.NoError(t, sessionrevision.InitializeProjection(record, active))
	return record.Projection
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
