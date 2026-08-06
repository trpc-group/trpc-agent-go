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
	"errors"
	"testing"

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
		Snapshot:  []byte(`{"id":"session"}`),
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
			RequestID: "request", Snapshot: []byte(`{}`), Hazard: true,
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

func TestSnapshotRoundTrip(t *testing.T) {
	_, err := Snapshot(nil)
	require.ErrorIs(t, err, session.ErrNilSession)
	_, err = DecodeSnapshot(nil)
	require.ErrorIs(t, err, session.ErrNilSession)
	_, err = DecodeSnapshot([]byte("not-json"))
	require.Error(t, err)

	sess := session.NewSession("app", "user", "session")
	sess.State = session.StateMap{
		"session:key":                   []byte("session"),
		session.StateAppPrefix + "key":  []byte("app"),
		session.StateUserPrefix + "key": []byte("user"),
	}
	SetGeneration(sess, 3)
	raw, err := Snapshot(sess)
	require.NoError(t, err)
	restored, err := DecodeSnapshot(raw)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, restored.ID)
	assert.Equal(t, sess.Hash, restored.Hash)
	assert.Equal(t, []byte("session"), restored.State["session:key"])
	assert.NotContains(t, restored.State, session.StateAppPrefix+"key")
	assert.NotContains(t, restored.State, session.StateUserPrefix+"key")
	assert.Empty(t, restored.ServiceMeta)
}

func TestApplyEventWriteTracksCanonicalTurn(t *testing.T) {
	record := &PersistedRecord{}
	start := Write{
		HasExpectedGeneration: true,
		Start: &TurnStart{
			RequestID:    "request",
			InvocationID: "invocation",
		},
		Snapshot: []byte(`{"id":"session"}`),
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
					Snapshot: []byte(`{"id":"session"}`),
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

	t.Run("shared state delta taints checkpoint", func(t *testing.T) {
		record := runningRevisionRecord(t)
		evt := revisionTestEvent("request", "invocation", false)
		evt.StateDelta = session.StateMap{session.StateAppPrefix + "key": []byte("value")}
		ApplyEventWrite(record, Write{HasExpectedGeneration: true}, evt, true)
		assert.True(t, record.Checkpoint.Hazard)
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
		}, Snapshot: []byte(`{"id":"other"}`)},
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
		}, Snapshot: []byte(`{"id":"next"}`)},
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
		Snapshot: []byte(`{"id":"session"}`),
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
