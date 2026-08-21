//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package revision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type replacementTestService struct {
	session.Service
	result *LatestTurnReplacementResult
	err    error
}

func (s *replacementTestService) ReplaceLatestTurn(
	context.Context,
	LatestTurnReplacementRequest,
) (*LatestTurnReplacementResult, error) {
	return s.result, s.err
}

type reportingReplacementTestService struct {
	*replacementTestService
	supported bool
}

func (s *reportingReplacementTestService) SupportsLatestTurnReplacement() bool {
	return s.supported
}

func TestLatestTurnReplacementCapability(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	valid := &replacementTestService{result: &LatestTurnReplacementResult{
		ActiveSession: active,
		Applied:       true,
	}}

	assert.False(t, SupportsLatestTurnReplacement(nil))
	assert.False(t, SupportsLatestTurnReplacement(&struct{ session.Service }{}))
	assert.True(t, SupportsLatestTurnReplacement(valid))
	assert.False(t, SupportsLatestTurnReplacement(
		&reportingReplacementTestService{
			replacementTestService: valid,
		},
	))
	assert.True(t, SupportsLatestTurnReplacement(
		&reportingReplacementTestService{
			replacementTestService: valid,
			supported:              true,
		},
	))

	request := LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "old-request",
		IdempotencyKey:    "new-request",
	}
	result, err := ReplaceLatestTurn(context.Background(), valid, request)
	require.NoError(t, err)
	assert.Same(t, active, result.ActiveSession)
	assert.True(t, result.Applied)

	tests := []struct {
		name    string
		service session.Service
		request LatestTurnReplacementRequest
		wantErr string
		is      error
	}{
		{
			name:    "invalid key",
			service: valid,
			request: LatestTurnReplacementRequest{},
			wantErr: "appName is required",
		},
		{
			name:    "empty expected request",
			service: valid,
			request: LatestTurnReplacementRequest{Key: key, IdempotencyKey: "new"},
			wantErr: "expected request id is required",
		},
		{
			name:    "empty idempotency key",
			service: valid,
			request: LatestTurnReplacementRequest{Key: key, ExpectedRequestID: "old"},
			wantErr: "idempotency key is required",
		},
		{
			name:    "unsupported",
			service: &struct{ session.Service }{},
			request: request,
			is:      ErrLatestTurnReplacementUnsupported,
		},
		{
			name: "reported unsupported",
			service: &reportingReplacementTestService{
				replacementTestService: valid,
			},
			request: request,
			is:      ErrLatestTurnReplacementUnsupported,
		},
		{
			name:    "backend error",
			service: &replacementTestService{err: context.Canceled},
			request: request,
			is:      context.Canceled,
		},
		{
			name:    "nil result",
			service: &replacementTestService{},
			request: request,
			wantErr: "returned no active session",
		},
		{
			name:    "nil active session",
			service: &replacementTestService{result: &LatestTurnReplacementResult{}},
			request: request,
			wantErr: "returned no active session",
		},
		{
			name: "wrong active session",
			service: &replacementTestService{result: &LatestTurnReplacementResult{
				ActiveSession: session.NewSession("other", "user", "session"),
			}},
			request: request,
			wantErr: "returned session key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReplaceLatestTurn(
				context.Background(),
				tt.service,
				tt.request,
			)
			require.Nil(t, result)
			if tt.is != nil {
				require.ErrorIs(t, err, tt.is)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestLoadStableProjectionRetriesGenerationChange(t *testing.T) {
	generations := []uint64{0, 1, 1, 1}
	reads := 0
	projectionReads := 0
	projection, err := LoadStableProjection(
		context.Background(),
		func(context.Context) (uint64, error) {
			generation := generations[reads]
			reads++
			return generation, nil
		},
		func(context.Context) (*session.Session, error) {
			projectionReads++
			return session.NewSession("app", "user", "session"), nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, projection)
	assert.Equal(t, 2, projectionReads)
	generation, ok := Generation(projection)
	require.True(t, ok)
	assert.Equal(t, uint64(1), generation)
}

func TestLoadStableProjectionRejectsPersistentChange(t *testing.T) {
	var generation uint64
	projection, err := LoadStableProjection(
		context.Background(),
		func(context.Context) (uint64, error) {
			generation++
			return generation, nil
		},
		func(context.Context) (*session.Session, error) {
			return session.NewSession("app", "user", "session"), nil
		},
	)
	assert.Nil(t, projection)
	assert.ErrorIs(t, err, ErrStaleProjection)
}

func TestLoadStableProjectionReadFailures(t *testing.T) {
	t.Run("generation before projection", func(t *testing.T) {
		wantErr := errors.New("read generation before projection")
		projection, err := LoadStableProjection(
			context.Background(),
			func(context.Context) (uint64, error) { return 0, wantErr },
			func(context.Context) (*session.Session, error) {
				t.Fatal("projection read should not run")
				return nil, nil
			},
		)
		assert.Nil(t, projection)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("projection", func(t *testing.T) {
		wantErr := errors.New("read projection")
		projection, err := LoadStableProjection(
			context.Background(),
			func(context.Context) (uint64, error) { return 0, nil },
			func(context.Context) (*session.Session, error) {
				return nil, wantErr
			},
		)
		assert.Nil(t, projection)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("missing projection", func(t *testing.T) {
		projection, err := LoadStableProjection(
			context.Background(),
			func(context.Context) (uint64, error) { return 0, nil },
			func(context.Context) (*session.Session, error) { return nil, nil },
		)
		assert.Nil(t, projection)
		require.NoError(t, err)
	})

	t.Run("generation after projection", func(t *testing.T) {
		wantErr := errors.New("read generation after projection")
		reads := 0
		projection, err := LoadStableProjection(
			context.Background(),
			func(context.Context) (uint64, error) {
				reads++
				if reads == 2 {
					return 0, wantErr
				}
				return 0, nil
			},
			func(context.Context) (*session.Session, error) {
				return session.NewSession("app", "user", "session"), nil
			},
		)
		assert.Nil(t, projection)
		assert.ErrorIs(t, err, wantErr)
	})
}

func TestLoadStableListedProjection(t *testing.T) {
	t.Run("nil batched projection", func(t *testing.T) {
		got, err := LoadStableListedProjectionAtGeneration(
			context.Background(), nil, false, 1, nil, nil,
		)
		assert.Nil(t, got)
		require.NoError(t, err)
	})

	t.Run("nil projection", func(t *testing.T) {
		got, err := LoadStableListedProjection(
			context.Background(),
			nil,
			false,
			func(context.Context) (uint64, error) {
				t.Fatal("generation read should not run")
				return 0, nil
			},
			func(context.Context) (*session.Session, error) {
				t.Fatal("projection read should not run")
				return nil, nil
			},
		)
		assert.Nil(t, got)
		require.NoError(t, err)
	})

	t.Run("generation error", func(t *testing.T) {
		wantErr := errors.New("read generation")
		got, err := LoadStableListedProjection(
			context.Background(),
			session.NewSession("app", "user", "session"),
			false,
			func(context.Context) (uint64, error) { return 0, wantErr },
			func(context.Context) (*session.Session, error) {
				t.Fatal("projection read should not run")
				return nil, nil
			},
		)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("generation zero keeps listed projection", func(t *testing.T) {
		listed := session.NewSession("app", "user", "session")
		projectionReads := 0
		got, err := LoadStableListedProjection(
			context.Background(),
			listed,
			false,
			func(context.Context) (uint64, error) { return 0, nil },
			func(context.Context) (*session.Session, error) {
				projectionReads++
				return nil, nil
			},
		)
		require.NoError(t, err)
		assert.Same(t, listed, got)
		assert.Zero(t, projectionReads)
	})

	t.Run("nonzero generation refreshes projection", func(t *testing.T) {
		listed := session.NewSession("app", "user", "session")
		refreshed := session.NewSession("app", "user", "session")
		refreshed.Events = append(refreshed.Events, event.Event{})
		refreshed.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace"},
		}
		refreshed.Summaries = map[string]*session.Summary{
			"all": {Summary: "summary"},
		}
		got, err := LoadStableListedProjection(
			context.Background(),
			listed,
			true,
			func(context.Context) (uint64, error) { return 2, nil },
			func(context.Context) (*session.Session, error) {
				return refreshed, nil
			},
		)
		require.NoError(t, err)
		assert.Same(t, refreshed, got)
		generation, ok := Generation(got)
		require.True(t, ok)
		assert.Equal(t, uint64(2), generation)
		assert.Nil(t, got.Events)
		assert.Nil(t, got.Tracks)
		assert.Nil(t, got.Summaries)
	})

	t.Run("non-metadata projection is preserved", func(t *testing.T) {
		refreshed := session.NewSession("app", "user", "session")
		refreshed.Events = append(refreshed.Events, event.Event{})
		got, err := LoadStableListedProjection(
			context.Background(),
			session.NewSession("app", "user", "session"),
			false,
			func(context.Context) (uint64, error) { return 2, nil },
			func(context.Context) (*session.Session, error) {
				return refreshed, nil
			},
		)
		require.NoError(t, err)
		assert.Same(t, refreshed, got)
		assert.Len(t, got.Events, 1)
	})

	t.Run("refresh error", func(t *testing.T) {
		wantErr := errors.New("refresh projection")
		got, err := LoadStableListedProjection(
			context.Background(),
			session.NewSession("app", "user", "session"),
			false,
			func(context.Context) (uint64, error) { return 2, nil },
			func(context.Context) (*session.Session, error) {
				return nil, wantErr
			},
		)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("missing refresh", func(t *testing.T) {
		got, err := LoadStableListedProjection(
			context.Background(),
			session.NewSession("app", "user", "session"),
			false,
			func(context.Context) (uint64, error) { return 2, nil },
			func(context.Context) (*session.Session, error) { return nil, nil },
		)
		assert.Nil(t, got)
		require.NoError(t, err)
	})
}

func TestTrackEventsEqualIgnoresJSONStorageNormalization(t *testing.T) {
	timestamp := time.Now()
	a := session.TrackEvent{
		Track: "trace", RequestID: "request",
		Payload: json.RawMessage(`{"a":1,"b":2}`), Timestamp: timestamp,
	}
	b := session.TrackEvent{
		Track: "trace", RequestID: "request",
		Payload:   json.RawMessage(`{ "b": 2, "a": 1 }`),
		Timestamp: timestamp.In(time.FixedZone("offset", 8*60*60)),
	}
	assert.True(t, TrackEventsEqual(a, b))
	b.RequestID = "other"
	assert.False(t, TrackEventsEqual(a, b))

	assert.True(t, rawJSONEqual([]byte("invalid"), []byte("invalid")))
	assert.False(t, rawJSONEqual([]byte("{"), []byte("[")))
	assert.False(t, rawJSONEqual([]byte("{}"), []byte("[")))
}

func TestLatestTurnReplacementReplay(t *testing.T) {
	record := &PersistedRecord{
		Generation: 2,
		Head:       3,
		Replays: map[string]PersistedReplay{
			"replacement": {
				RequestID: "request", Generation: 2, Head: 3,
			},
		},
	}
	replay, ok, err := LatestTurnReplacementReplay(
		record,
		"request",
		"replacement",
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, record.Replays["replacement"], replay)

	_, ok, err = LatestTurnReplacementReplay(record, "request", "missing")
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = LatestTurnReplacementReplay(nil, "request", "replacement")
	require.NoError(t, err)
	assert.False(t, ok)

	for _, mutate := range []func(*PersistedRecord){
		func(record *PersistedRecord) {
			record.Replays["replacement"] = PersistedReplay{
				RequestID: "other", Generation: 2, Head: 3,
			}
		},
		func(record *PersistedRecord) { record.Generation++ },
		func(record *PersistedRecord) { record.Head++ },
	} {
		conflict := *record
		conflict.Replays = map[string]PersistedReplay{
			"replacement": record.Replays["replacement"],
		}
		mutate(&conflict)
		_, ok, err := LatestTurnReplacementReplay(
			&conflict,
			"request",
			"replacement",
		)
		assert.True(t, ok)
		assert.ErrorIs(t, err, ErrLatestTurnReplacementConflict)
	}
}

func TestLatestTurnReplacementCheckpoint(t *testing.T) {
	checkpoint := &PersistedCheckpoint{
		RequestID: "request",
		Boundary:  []byte(`{"id":"session"}`),
	}
	got, err := LatestTurnReplacementCheckpoint(
		&PersistedRecord{Checkpoint: checkpoint},
		"request",
	)
	require.NoError(t, err)
	assert.Same(t, checkpoint, got)

	for _, record := range []*PersistedRecord{
		nil,
		{},
		{Checkpoint: &PersistedCheckpoint{RequestID: "request"}},
		{Checkpoint: &PersistedCheckpoint{
			RequestID: "request", Boundary: []byte(`{}`), Hazard: true,
		}},
	} {
		_, err := LatestTurnReplacementCheckpoint(record, "request")
		assert.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)
	}
	_, err = LatestTurnReplacementCheckpoint(
		&PersistedRecord{Checkpoint: checkpoint},
		"other",
	)
	assert.ErrorIs(t, err, ErrLatestTurnReplacementConflict)
}

func TestRevisionContextAndGeneration(t *testing.T) {
	start := TurnStart{RequestID: "request", InvocationID: "invocation"}
	ctx := ContextWithTurnStart(nil, start)
	gotStart, ok := TurnStartFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, start, gotStart)
	_, ok = TurnStartFromContext(nil)
	assert.False(t, ok)
	_, ok = TurnStartFromContext(ContextWithTurnStart(
		context.Background(),
		TurnStart{},
	))
	assert.False(t, ok)

	sess := session.NewSession("app", "user", "session")
	SetGeneration(nil, 1)
	SetGeneration(sess, 7)
	generation, ok := Generation(sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(7), generation)
	_, ok = Generation(nil)
	assert.False(t, ok)
	delete(sess.ServiceMeta, generationServiceMetaKey)
	_, ok = Generation(sess)
	assert.False(t, ok)
	sess.ServiceMeta[generationServiceMetaKey] = "invalid"
	_, ok = Generation(sess)
	assert.False(t, ok)

	ctx = ContextWithGeneration(nil, 9)
	generation, ok = GenerationFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, uint64(9), generation)
	_, ok = GenerationFromContext(nil)
	assert.False(t, ok)

	SetGeneration(sess, 11)
	generation, ok = ExpectedGeneration(ctx, sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(11), generation)
	generation, ok = ExpectedGeneration(ctx, nil)
	assert.True(t, ok)
	assert.Equal(t, uint64(9), generation)

	writeCtx := ContextWithHazard(nil)
	writeCtx = ContextWithGeneration(writeCtx, 13)
	writeCtx = ContextWithTurnStart(writeCtx, start)
	write := NewWrite(writeCtx, nil)
	assert.Equal(t, uint64(13), write.ExpectedGeneration)
	assert.True(t, write.HasExpectedGeneration)
	assert.True(t, write.Hazard)
	assert.Equal(t, &start, write.Start)
}

func TestAttachRecord(t *testing.T) {
	assert.False(t, RecordActive(nil))
	AttachRecord(nil, &PersistedRecord{Generation: 1})
	sess := session.NewSession("app", "user", "session")
	AttachRecord(sess, nil)
	assert.False(t, RecordActive(sess))

	AttachRecord(sess, &PersistedRecord{})
	generation, ok := Generation(sess)
	assert.True(t, ok)
	assert.Zero(t, generation)
	assert.False(t, RecordActive(sess))

	AttachRecord(sess, &PersistedRecord{Generation: 2, Head: 1})
	generation, ok = Generation(sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(2), generation)
	assert.True(t, RecordActive(sess))
}

func TestRunPreparationSignal(t *testing.T) {
	ctx, done := ContextWithRunPreparation(nil)
	CompleteRunPreparation(ctx, nil)
	CompleteRunPreparation(ctx, errors.New("ignored"))
	require.NoError(t, <-done)
	require.NotPanics(t, func() {
		CompleteRunPreparation(nil, errors.New("ignored"))
		CompleteRunPreparation(context.Background(), errors.New("ignored"))
	})
}

func TestBoundaryRestore(t *testing.T) {
	_, err := NewBoundary(nil)
	require.ErrorIs(t, err, session.ErrNilSession)
	_, err = RestoreBoundary(nil, nil)
	require.ErrorIs(t, err, session.ErrNilSession)
	_, err = RestoreBoundary(
		session.NewSession("app", "user", "session"),
		nil,
	)
	require.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)

	createdAt := time.Unix(100, 0).UTC()
	updatedAt := time.Unix(200, 0).UTC()
	sess := session.NewSession("app", "user", "session")
	sess.CreatedAt = createdAt
	sess.UpdatedAt = updatedAt
	sess.State = session.StateMap{
		"session:key":                   []byte("before"),
		session.StateAppPrefix + "key":  []byte("app"),
		session.StateUserPrefix + "key": []byte("user"),
	}
	sess.Events = []event.Event{{InvocationID: "before"}}
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"trace": {
			Track: "trace",
			Events: []session.TrackEvent{{
				Track: "trace", RequestID: "before",
			}},
		},
		"empty": {Track: "empty"},
	}
	SetGeneration(sess, 3)
	raw, err := NewBoundary(sess)
	require.NoError(t, err)

	current := sess.Clone()
	current.State["session:key"] = []byte("after")
	current.Events = append(current.Events, event.Event{InvocationID: "after"})
	current.Tracks["trace"].Events = append(
		current.Tracks["trace"].Events,
		session.TrackEvent{Track: "trace", RequestID: "after"},
	)
	delete(current.Tracks, "empty")
	current.UpdatedAt = time.Unix(300, 0).UTC()
	restored, err := RestoreBoundary(current, raw)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, restored.ID)
	assert.Equal(t, sess.Hash, restored.Hash)
	assert.Equal(t, []byte("before"), restored.State["session:key"])
	assert.NotContains(t, restored.State, session.StateAppPrefix+"key")
	assert.NotContains(t, restored.State, session.StateUserPrefix+"key")
	assert.Len(t, restored.Events, 1)
	assert.Len(t, restored.Tracks["trace"].Events, 1)
	assert.Empty(t, restored.Tracks["empty"].Events)
	assert.Equal(t, createdAt, restored.CreatedAt)
	assert.Equal(t, updatedAt, restored.UpdatedAt)
	assert.Empty(t, restored.ServiceMeta)

	var corruptedBoundary persistedBoundary
	require.NoError(t, json.Unmarshal(raw, &corruptedBoundary))
	emptyPrefix := corruptedBoundary.Tracks["empty"]
	emptyPrefix.Digest[0] ^= 0xff
	corruptedBoundary.Tracks["empty"] = emptyPrefix
	corruptedRaw, err := json.Marshal(corruptedBoundary)
	require.NoError(t, err)
	_, err = RestoreBoundary(current, corruptedRaw)
	assert.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)

	tampered := current.Clone()
	tampered.Events[0].InvocationID = "tampered"
	_, err = RestoreBoundary(tampered, raw)
	assert.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)
	_, err = RestoreBoundary(current, []byte("not-json"))
	assert.Error(t, err)
}

func TestBoundaryRestoreStateOverlay(t *testing.T) {
	sess := session.NewSession(
		"app",
		"user",
		"session",
		session.WithSessionState(session.StateMap{
			"kept":    []byte("value"),
			"removed": []byte("value"),
		}),
	)
	record := &PersistedRecord{}
	require.NoError(t, InitializeProjection(record, sess))

	raw, err := NewBoundaryFromProjection(
		sess,
		record.Projection,
		session.StateMap{
			"route":   []byte("child"),
			"removed": nil,
		},
	)
	require.NoError(t, err)
	assert.NotContains(t, sess.State, "route")
	assert.Contains(t, sess.State, "removed")

	current := sess.Clone()
	current.Events = append(current.Events, event.Event{InvocationID: "after"})
	restored, err := RestoreBoundary(current, raw)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), restored.State["kept"])
	assert.Equal(t, []byte("child"), restored.State["route"])
	assert.NotContains(t, restored.State, "removed")
}

func TestBoundaryDoesNotCopyEventPayloads(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	sess.Events = []event.Event{{
		InvocationID: "before",
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{
				Content: strings.Repeat("x", 1<<20),
			}}},
		},
	}}
	raw, err := NewBoundary(sess)
	require.NoError(t, err)
	assert.Less(t, len(raw), 1024)
}

func TestRollingProjectionMatchesAuthoritativeBoundary(t *testing.T) {
	before := session.NewSession("app", "user", "session")
	before.Events = []event.Event{{ID: "event-1"}}
	before.Tracks = map[session.Track]*session.TrackEvents{
		"trace": {
			Track:  "trace",
			Events: []session.TrackEvent{{Track: "trace", RequestID: "one"}},
		},
	}
	record := &PersistedRecord{}
	require.NoError(t, InitializeProjection(record, before))
	assert.True(t, ProjectionInitialized(record))

	rollingBoundary, err := NewBoundaryFromProjection(
		before, record.Projection, nil,
	)
	require.NoError(t, err)
	authoritativeBoundary, err := NewBoundary(before)
	require.NoError(t, err)
	assert.JSONEq(t, string(authoritativeBoundary), string(rollingBoundary))

	after := before.Clone()
	nextEvent := event.Event{ID: "event-2"}
	nextTrack := session.TrackEvent{Track: "trace", RequestID: "two"}
	after.Events = append(after.Events, nextEvent)
	after.Tracks["trace"].Events = append(
		after.Tracks["trace"].Events, nextTrack,
	)
	require.NoError(t, AppendProjectionEvent(record, &nextEvent))
	require.NoError(t, AppendProjectionTrack(record, &nextTrack))

	rollingBoundary, err = NewBoundaryFromProjection(after, record.Projection, nil)
	require.NoError(t, err)
	authoritativeBoundary, err = NewBoundary(after)
	require.NoError(t, err)
	assert.JSONEq(t, string(authoritativeBoundary), string(rollingBoundary))

	clone := CloneProjection(record.Projection)
	require.NotNil(t, clone)
	clone.Events.Digest[0] ^= 0xff
	assert.NotEqual(t, clone.Events.Digest, record.Projection.Events.Digest)
}

func TestRollingProjectionInvalidatesNonSuffixWrites(t *testing.T) {
	newest := time.Unix(30, 0)
	sess := session.NewSession("app", "user", "session")
	sess.Events = []event.Event{
		{ID: "newest", Timestamp: newest},
		{ID: "older-in-storage-order", Timestamp: time.Unix(20, 0)},
	}
	record := &PersistedRecord{
		Checkpoint: &PersistedCheckpoint{RequestID: "request"},
	}
	require.NoError(t, InitializeProjection(record, sess))

	require.NoError(t, AppendProjectionEvent(record, &event.Event{
		ID: "backdated", Timestamp: time.Unix(25, 0),
	}))
	assert.Nil(t, record.Projection)
	assert.True(t, record.Checkpoint.Hazard)

	record.Checkpoint.Hazard = false
	require.NoError(t, InitializeProjection(record, sess))
	require.NoError(t, AppendProjectionTrack(record, &session.TrackEvent{
		Track: "trace", Timestamp: time.Unix(10, 0),
	}))
	require.NotNil(t, record.Projection)
	require.NoError(t, AppendProjectionTrack(record, &session.TrackEvent{
		Track: "trace", Timestamp: time.Unix(9, 0),
	}))
	assert.Nil(t, record.Projection)
	assert.True(t, record.Checkpoint.Hazard)
}

func TestResetAndInvalidateProjection(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	sess.Events = []event.Event{{ID: "event-1"}}
	raw, err := NewBoundary(sess)
	require.NoError(t, err)
	record := &PersistedRecord{}
	require.NoError(t, ResetProjectionFromBoundary(record, raw))
	assert.True(t, ProjectionInitialized(record))
	rolling, err := NewBoundaryFromProjection(sess, record.Projection, nil)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(rolling))

	InvalidateProjection(record)
	assert.False(t, ProjectionInitialized(record))
	assert.NoError(t, AppendProjectionEvent(record, &event.Event{}))
	assert.NoError(t, AppendProjectionTrack(record, &session.TrackEvent{}))
	_, err = NewBoundaryFromProjection(sess, record.Projection, nil)
	assert.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)
}

func TestRollingProjectionRejectsInvalidMetadata(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	assert.Error(t, InitializeProjection(nil, sess))
	_, err := NewBoundaryFromProjection(nil, &PersistedProjection{}, nil)
	assert.ErrorIs(t, err, session.ErrNilSession)

	invalid := &PersistedRecord{Projection: &PersistedProjection{Version: 99}}
	assert.False(t, ProjectionInitialized(invalid))
	_, err = NewBoundaryFromProjection(sess, invalid.Projection, nil)
	assert.ErrorIs(t, err, ErrLatestTurnReplacementUnavailable)
	assert.ErrorIs(
		t,
		AppendProjectionEvent(invalid, &event.Event{}),
		ErrLatestTurnReplacementUnavailable,
	)
	assert.ErrorIs(
		t,
		AppendProjectionTrack(invalid, &session.TrackEvent{}),
		ErrLatestTurnReplacementUnavailable,
	)

	record := &PersistedRecord{}
	require.NoError(t, InitializeProjection(record, sess))
	record.Projection.Events.Count = math.MaxUint64
	assert.ErrorIs(
		t,
		AppendProjectionEvent(record, &event.Event{}),
		ErrLatestTurnReplacementUnavailable,
	)
	require.NoError(t, InitializeProjection(record, sess))
	assert.Error(t, AppendProjectionTrack(record, nil))
	assert.Error(t, AppendProjectionTrack(record, &session.TrackEvent{
		Track: "trace", Payload: json.RawMessage("{"),
	}))
	record.Projection.Tracks = map[session.Track]persistedPrefix{
		"trace": {Digest: []byte("short")},
	}
	assert.False(t, ProjectionInitialized(record))

	assert.Error(t, ResetProjectionFromBoundary(nil, nil))
	assert.Error(t, ResetProjectionFromBoundary(record, []byte("{")))
	legacy, err := json.Marshal(persistedBoundary{Version: 1})
	require.NoError(t, err)
	require.NoError(t, ResetProjectionFromBoundary(record, legacy))
	assert.Nil(t, record.Projection)
	assert.NotPanics(t, func() { InvalidateProjection(nil) })
}

func BenchmarkBoundaryCapture(b *testing.B) {
	for _, eventCount := range []int{100, 10_000} {
		sess := session.NewSession("app", "user", "session")
		sess.Events = make([]event.Event, eventCount)
		for i := range sess.Events {
			sess.Events[i] = event.Event{
				ID:           fmt.Sprintf("event-%d", i),
				InvocationID: "invocation",
			}
		}
		record := &PersistedRecord{}
		require.NoError(b, InitializeProjection(record, sess))

		b.Run(fmt.Sprintf("full/%d", eventCount), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := NewBoundary(sess); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("rolling/%d", eventCount), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := NewBoundaryFromProjection(
					sess, record.Projection, nil,
				); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestApplyEventWriteTracksCanonicalTurn(t *testing.T) {
	record := &PersistedRecord{}
	start := Write{
		HasExpectedGeneration: true,
		Start: &TurnStart{
			RequestID:    "request",
			InvocationID: "invocation",
		},
		Boundary: []byte(`{"id":"session"}`),
	}
	input := revisionTestEvent("request", "invocation", false)

	assert.True(t, ApplyEventWrite(record, start, input, true))
	require.NotNil(t, record.Checkpoint)
	assert.False(t, record.Checkpoint.Terminal)
	assert.False(t, record.Checkpoint.Hazard)
	assert.Equal(t, uint64(1), record.Head)

	completion := revisionTestEvent("request", "invocation", true)
	assert.True(t, ApplyEventWrite(record, Write{HasExpectedGeneration: true}, completion, false))
	assert.True(t, record.Checkpoint.Terminal)
	assert.False(t, record.Checkpoint.Hazard)
	assert.Equal(t, uint64(2), record.Head)
}

func TestApplyEventWriteRejectsUnsafeBoundaries(t *testing.T) {
	t.Run("marker must match a persisted event", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			event     *event.Event
			persisted bool
		}{
			{name: "rewritten", event: revisionTestEvent("other", "invocation", false), persisted: true},
			{name: "not persisted", event: revisionTestEvent("request", "invocation", false)},
		} {
			t.Run(test.name, func(t *testing.T) {
				record := &PersistedRecord{}
				ApplyEventWrite(record, Write{
					HasExpectedGeneration: true,
					Start: &TurnStart{
						RequestID:    "request",
						InvocationID: "invocation",
					},
					Boundary: []byte(`{"id":"session"}`),
				}, test.event, test.persisted)
				assert.Nil(t, record.Checkpoint)
			})
		}
	})

	t.Run("post terminal event taints checkpoint", func(t *testing.T) {
		record := terminalRevisionRecord(t)
		ApplyEventWrite(
			record,
			Write{HasExpectedGeneration: true},
			revisionTestEvent("request", "invocation", false),
			true,
		)
		assert.True(t, record.Checkpoint.Hazard)
	})

	t.Run("shared state delta remains replaceable", func(t *testing.T) {
		record := runningRevisionRecord(t)
		evt := revisionTestEvent("request", "invocation", false)
		evt.StateDelta = session.StateMap{session.StateAppPrefix + "key": []byte("value")}
		ApplyEventWrite(record, Write{HasExpectedGeneration: true}, evt, true)
		assert.False(t, record.Checkpoint.Hazard)
	})
}

func TestApplyTrackWriteRequiresOpenRequest(t *testing.T) {
	record := runningRevisionRecord(t)
	ApplyTrackWrite(record, Write{HasExpectedGeneration: true}, &session.TrackEvent{
		RequestID: "request",
	})
	assert.False(t, record.Checkpoint.Hazard)

	ApplyTrackWrite(record, Write{HasExpectedGeneration: true}, &session.TrackEvent{})
	assert.True(t, record.Checkpoint.Hazard)

	record = terminalRevisionRecord(t)
	ApplyTrackWrite(record, Write{HasExpectedGeneration: true}, &session.TrackEvent{
		RequestID: "request",
	})
	assert.False(t, record.Checkpoint.Hazard)

	ApplyTrackWrite(record, Write{HasExpectedGeneration: true}, &session.TrackEvent{
		RequestID: "other-request",
	})
	assert.True(t, record.Checkpoint.Hazard)
}

func TestApplyWriteWithoutGenerationTaintsCheckpoint(t *testing.T) {
	record := runningRevisionRecord(t)
	ApplyWrite(record, Write{})
	assert.True(t, record.Checkpoint.Hazard)
	assert.Equal(t, uint64(2), record.Head)
}

func TestApplyWriteWithExplicitHazardTaintsCheckpoint(t *testing.T) {
	record := runningRevisionRecord(t)
	ApplyWrite(record, Write{HasExpectedGeneration: true, Hazard: true})
	assert.True(t, record.Checkpoint.Hazard)
}

func TestApplyRevisionWritesHandleMissingMetadata(t *testing.T) {
	assert.False(t, ApplyWrite(nil, Write{}))
	assert.False(t, ApplyEventWrite(nil, Write{}, nil, false))
	assert.False(t, ApplyTrackWrite(nil, Write{}, nil))

	record := &PersistedRecord{}
	assert.True(t, ApplyTrackWrite(record, Write{}, nil))
	assert.Equal(t, uint64(1), record.Head)
}

func TestPendingErrorsAreIsolatedBySession(t *testing.T) {
	keyA := session.Key{AppName: "app", UserID: "user", SessionID: "a"}
	keyB := session.Key{AppName: "app", UserID: "user", SessionID: "b"}
	errA1 := errors.New("a1")
	errA2 := errors.New("a2")
	errB := errors.New("b")
	var pending PendingErrors

	pending.Add(keyA, errA1)
	pending.Add(keyA, errA2)
	pending.Add(keyB, errB)
	pending.Add(keyB, nil)

	gotB := deliverPendingError(context.Background(), t, &pending, keyB)
	assert.ErrorIs(t, gotB, errB)
	assert.NotErrorIs(t, gotB, errA1)
	gotA := deliverPendingError(context.Background(), t, &pending, keyA)
	assert.ErrorIs(t, gotA, errA1)
	assert.NotErrorIs(t, gotA, errA2)
	assert.NoError(t, deliverPendingError(
		context.Background(), t, &pending, keyA,
	))
	assert.NoError(t, deliverPendingError(
		context.Background(), t, &PendingErrors{}, keyA,
	))
}

func TestPendingErrorsRetainCancelledBarrierAndBoundCapacity(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cancelled"}
	persistErr := errors.New("persist failed")
	var pending PendingErrors
	pending.Add(key, persistErr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error)
	delivered := make(chan struct{})
	go func() {
		pending.Deliver(ctx, key, done)
		close(delivered)
	}()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("cancelled barrier did not return")
	}
	assert.ErrorIs(t, deliverPendingError(
		context.Background(), t, &pending, key,
	), persistErr)

	for i := 0; i < maxPendingErrorKeys+100; i++ {
		pending.Add(session.Key{
			AppName: "app", UserID: "user", SessionID: fmt.Sprintf("session-%d", i),
		}, persistErr)
	}
	assert.Len(t, pending.byKey, maxPendingErrorKeys)
	assert.Error(t, pending.overflow)
	unknown := session.Key{AppName: "app", UserID: "user", SessionID: "overflow"}
	assert.Error(t, deliverPendingError(
		context.Background(), t, &pending, unknown,
	))
	assert.Error(t, deliverPendingError(
		context.Background(), t, &pending, unknown,
	))
}

func deliverPendingError(
	ctx context.Context,
	t *testing.T,
	pending *PendingErrors,
	key session.Key,
) error {
	t.Helper()
	done := make(chan error)
	delivered := make(chan struct{})
	go func() {
		pending.Deliver(ctx, key, done)
		close(delivered)
	}()
	select {
	case err := <-done:
		select {
		case <-delivered:
		case <-time.After(time.Second):
			t.Fatal("barrier delivery did not complete")
		}
		return err
	case <-time.After(time.Second):
		t.Fatal("barrier result was not delivered")
		return nil
	}
}

func TestRecordLatestTurnReplacementReplayIsBounded(t *testing.T) {
	record := &PersistedRecord{}
	RecordLatestTurnReplacementReplay(nil, "ignored", PersistedReplay{})
	RecordLatestTurnReplacementReplay(record, "", PersistedReplay{})
	for i := 0; i <= maxPersistedReplays; i++ {
		key := fmt.Sprintf("replacement-%02d", i)
		RecordLatestTurnReplacementReplay(record, key, PersistedReplay{
			RequestID: key, Generation: uint64(i), Head: uint64(i),
		})
	}

	assert.Len(t, record.Replays, maxPersistedReplays)
	assert.NotContains(t, record.Replays, "replacement-00")
	assert.Contains(t, record.Replays, fmt.Sprintf("replacement-%02d", maxPersistedReplays))
	assert.True(t, replayPrecedes(
		PersistedReplay{Generation: 1, Head: 1}, "b",
		PersistedReplay{Generation: 1, Head: 2}, "a",
	))
	assert.True(t, replayPrecedes(
		PersistedReplay{Generation: 1, Head: 1}, "a",
		PersistedReplay{Generation: 1, Head: 1}, "b",
	))
	assert.False(t, replayPrecedes(
		PersistedReplay{Generation: 2, Head: 1}, "a",
		PersistedReplay{Generation: 1, Head: 2}, "b",
	))
}

func TestChildCompletionCannotCloseRootTurn(t *testing.T) {
	record := runningRevisionRecord(t)
	ApplyEventWrite(
		record,
		Write{HasExpectedGeneration: true},
		revisionTestEvent("request", "child-invocation", true),
		false,
	)
	assert.False(t, record.Checkpoint.Terminal)
	assert.True(t, record.Checkpoint.Hazard)
}

func TestNewSerialTurnReplacesClosedHazard(t *testing.T) {
	record := runningRevisionRecord(t)
	ApplyEventWrite(
		record,
		Write{HasExpectedGeneration: true, Start: &TurnStart{
			RequestID: "other", InvocationID: "other-invocation",
		}, Boundary: []byte(`{"id":"other"}`)},
		revisionTestEvent("other", "other-invocation", false),
		true,
	)
	assert.True(t, record.Checkpoint.Hazard)
	ApplyEventWrite(
		record,
		Write{HasExpectedGeneration: true},
		revisionTestEvent("request", "invocation", true),
		false,
	)
	assert.True(t, record.Checkpoint.Terminal)

	ApplyEventWrite(
		record,
		Write{HasExpectedGeneration: true, Start: &TurnStart{
			RequestID: "next", InvocationID: "next-invocation",
		}, Boundary: []byte(`{"id":"next"}`)},
		revisionTestEvent("next", "next-invocation", false),
		true,
	)
	assert.Equal(t, "next", record.Checkpoint.RequestID)
	assert.False(t, record.Checkpoint.Hazard)
	assert.False(t, record.Checkpoint.Terminal)
}

func runningRevisionRecord(t *testing.T) *PersistedRecord {
	t.Helper()
	record := &PersistedRecord{}
	ApplyEventWrite(record, Write{
		HasExpectedGeneration: true,
		Start: &TurnStart{
			RequestID:    "request",
			InvocationID: "invocation",
		},
		Boundary: []byte(`{"id":"session"}`),
	}, revisionTestEvent("request", "invocation", false), true)
	return record
}

func terminalRevisionRecord(t *testing.T) *PersistedRecord {
	t.Helper()
	record := runningRevisionRecord(t)
	ApplyEventWrite(
		record,
		Write{HasExpectedGeneration: true},
		revisionTestEvent("request", "invocation", true),
		false,
	)
	return record
}

func revisionTestEvent(requestID, invocationID string, completion bool) *event.Event {
	object := ""
	response := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, Content: "content"},
		}},
	}
	if completion {
		object = model.ObjectTypeRunnerCompletion
		response.Choices = nil
	}
	response.Object = object
	return &event.Event{
		RequestID:    requestID,
		InvocationID: invocationID,
		Response:     response,
	}
}
