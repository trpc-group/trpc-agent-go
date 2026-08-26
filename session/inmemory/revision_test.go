//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/rewindtest"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLatestTurnReplacementContract(t *testing.T) {
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	rewindtest.Run(t, service)
}

func TestEventLimitInvalidatesRollingProjection(t *testing.T) {
	ctx := context.Background()
	service := NewSessionService(WithSessionEventLimit(1))
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "limited"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	firstCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "first", InvocationID: "first-invocation",
	})
	require.NoError(t, service.AppendEvent(
		firstCtx,
		sess,
		testMessageEvent(
			"first", "first", "first-invocation", "first",
		),
	))
	require.NoError(t, service.AppendEvent(
		ctx, sess, testCompletionEvent("first", "first-invocation"),
	))
	secondCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "second", InvocationID: "second-invocation",
	})
	require.NoError(t, service.AppendEvent(
		secondCtx,
		sess,
		testMessageEvent(
			"second", "second", "second-invocation", "second",
		),
	))

	app, ok := service.getAppSessions(key.AppName)
	require.True(t, ok)
	app.mu.Lock()
	defer app.mu.Unlock()
	stored := app.sessions[key.UserID][key.SessionID]
	require.NotNil(t, stored)
	require.NotNil(t, stored.revision)
	assert.False(t, revision.ProjectionInitialized(&stored.revision.record))
	require.NotNil(t, stored.revision.record.Checkpoint)
	assert.True(t, stored.revision.record.Checkpoint.Hazard)
}

func TestAppendTrackEventRevisionFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "request", InvocationID: "invocation",
	})
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		testMessageEvent("event", "request", "invocation", "message"),
	))

	storedBefore, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	callerBefore := sess.Clone()
	trackCtx := revision.ContextWithRequestID(ctx, "request")
	err = service.AppendTrackEvent(trackCtx, sess, &session.TrackEvent{
		Track:     "trace",
		Payload:   []byte("{"),
		Timestamp: time.Now(),
	})
	require.ErrorContains(t, err, "advance session revision projection")

	storedAfter, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, storedBefore.State, storedAfter.State)
	assert.Equal(t, storedBefore.Tracks, storedAfter.Tracks)
	assert.Equal(t, storedBefore.UpdatedAt, storedAfter.UpdatedAt)
	assert.Equal(t, callerBefore.State, sess.State)
	assert.Equal(t, callerBefore.Tracks, sess.Tracks)
	assert.Equal(t, callerBefore.UpdatedAt, sess.UpdatedAt)
}

func TestRewindRestoresCheckpointAndFencesOldProjection(t *testing.T) {
	ctx := context.Background()
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, session.StateMap{"phase": []byte("before")})
	require.NoError(t, err)

	baseline := testMessageEvent("event-before", "request-before", "invocation-before", "before")
	baseline.Response.Choices[0].Message.Role = model.RoleUser
	require.NoError(t, service.AppendEvent(ctx, sess, baseline))
	baselineTrack := &session.TrackEvent{
		Track:     "ui",
		Payload:   []byte(`{"value":"before"}`),
		Timestamp: time.Now().Add(-time.Second),
	}
	require.NoError(t, service.AppendTrackEvent(ctx, sess, baselineTrack))

	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "request-latest",
		InvocationID: "invocation-latest",
	})
	latest := testMessageEvent("event-latest", "request-latest", "invocation-latest", "latest")
	require.NoError(t, service.AppendEvent(turnCtx, sess, latest))
	stateEvent := testMessageEvent("event-state", "request-latest", "invocation-latest", "state")
	stateEvent.StateDelta = session.StateMap{"phase": []byte("after")}
	require.NoError(t, service.AppendEvent(ctx, sess, stateEvent))
	trackCtx := revision.ContextWithRequestID(ctx, "request-latest")
	require.NoError(t, service.AppendTrackEvent(trackCtx, sess, &session.TrackEvent{
		Track:     "ui",
		Payload:   []byte(`{"value":"latest"}`),
		Timestamp: time.Now(),
	}))
	require.NoError(t, service.AppendEvent(ctx, sess, testCompletionEvent(
		"request-latest",
		"invocation-latest",
	)))

	result, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "request-latest", ExpectedHeadRequestID: "request-latest",
		IdempotencyKey: "replacement-1",
	})
	require.NoError(t, err)
	require.Len(t, result.Session.Events, 1)
	assert.Equal(t, "event-before", result.Session.Events[0].ID)
	phase, ok := result.Session.GetState("phase")
	require.True(t, ok)
	assert.Equal(t, []byte("before"), phase)
	trackEvents, err := result.Session.GetTrackEvents("ui")
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	assert.JSONEq(t, `{"value":"before"}`, string(trackEvents.Events[0].Payload))

	replayed, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "request-latest", ExpectedHeadRequestID: "request-latest",
		IdempotencyKey: "replacement-1",
	})
	require.NoError(t, err)
	assert.Equal(t, result.Session.Events, replayed.Session.Events)

	err = service.AppendEvent(ctx, sess, testMessageEvent(
		"event-stale",
		"request-stale",
		"invocation-stale",
		"stale",
	))
	assert.ErrorIs(t, err, revision.ErrStaleGeneration)

	active, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, active.Events, 1)
	assert.Equal(t, "event-before", active.Events[0].ID)
}

func TestRewindMatchesExpectedTurn(t *testing.T) {
	ctx := context.Background()
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "request",
		InvocationID: "invocation",
	})
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		testMessageEvent("event", "request", "invocation", "message"),
	))

	result, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "request", ExpectedHeadRequestID: "request",
		IdempotencyKey: "replacement-incomplete",
	})
	require.NoError(t, err)
	require.Empty(t, result.Session.Events)

	completedKey := session.Key{
		AppName: "app", UserID: "user", SessionID: "completed-session",
	}
	sess, err = service.CreateSession(ctx, completedKey, nil)
	require.NoError(t, err)
	turnCtx = revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "request",
		InvocationID: "invocation",
	})
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		testMessageEvent("event", "request", "invocation", "message"),
	))
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		testCompletionEvent("request", "invocation"),
	))
	_, err = revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             completedKey,
		TargetRequestID: "different-request", ExpectedHeadRequestID: "different-request",
		IdempotencyKey: "replacement-conflict",
	})
	assert.ErrorIs(t, err, revision.ErrRewindConflict)

	_, err = revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             completedKey,
		TargetRequestID: "request", ExpectedHeadRequestID: "request",
		IdempotencyKey: "replacement-success",
	})
	require.NoError(t, err)
	_, err = revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             completedKey,
		TargetRequestID: "different-request", ExpectedHeadRequestID: "different-request",
		IdempotencyKey: "replacement-success",
	})
	assert.True(t, errors.Is(err, revision.ErrRewindConflict))
}

func TestRewindRequiresPersistedCanonicalStart(t *testing.T) {
	for _, test := range []struct {
		name string
		hook session.AppendEventHook
	}{
		{
			name: "skipped",
			hook: func(ctx *session.AppendEventContext, next func() error) error {
				if ctx.Event.ID == "start" {
					return nil
				}
				return next()
			},
		},
		{
			name: "rewritten identity",
			hook: func(ctx *session.AppendEventContext, next func() error) error {
				if ctx.Event.ID == "start" {
					ctx.Event.RequestID = "rewritten"
				}
				return next()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service := NewSessionService(WithAppendEventHook(test.hook))
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
			sess, err := service.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			start := testMessageEvent("start", "request", "invocation", "message")
			require.NoError(t, service.AppendEvent(
				revision.ContextWithTurnStart(ctx, revision.TurnStart{
					RequestID: "request", InvocationID: "invocation",
				}),
				sess,
				start,
			))
			require.NoError(t, service.AppendEvent(
				ctx,
				sess,
				testCompletionEvent("request", "invocation"),
			))
			_, err = revision.Rewind(ctx, service, revision.RewindRequest{
				Key: key, TargetRequestID: "request", ExpectedHeadRequestID: "request", IdempotencyKey: "replacement",
			})
			if test.name == "rewritten identity" {
				assert.ErrorIs(t, err, revision.ErrRewindConflict)
			} else {
				assert.ErrorIs(t, err, revision.ErrRewindUnavailable)
			}
		})
	}
}

func TestRewindRestoresSummaryAndRejectsStaleSummary(t *testing.T) {
	ctx := context.Background()
	summarizer := &fakeSummarizer{allow: true, out: "before-summary"}
	service := NewSessionService(WithSummarizer(summarizer))
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		testMessageEvent("baseline", "baseline", "baseline-invocation", "before"),
	))
	require.NoError(t, service.CreateSessionSummary(
		revision.ContextWithRequestID(ctx, "request"), sess, "", true,
	))
	sess, err = service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "before-summary", sess.Summaries[""].Summary)

	require.NoError(t, service.AppendEvent(
		revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "request", InvocationID: "invocation",
		}),
		sess,
		testMessageEvent("latest", "request", "invocation", "latest"),
	))
	summarizer.out = "after-summary"
	require.NoError(t, service.CreateSessionSummary(
		revision.ContextWithRequestID(ctx, "request"), sess, "", true,
	))
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		testCompletionEvent("request", "invocation"),
	))
	result, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key: key, TargetRequestID: "request", ExpectedHeadRequestID: "request", IdempotencyKey: "replacement",
	})
	require.NoError(t, err)
	require.Equal(t, "before-summary", result.Session.Summaries[""].Summary)

	err = service.CreateSessionSummary(ctx, sess, "", true)
	assert.ErrorIs(t, err, revision.ErrStaleGeneration)
	active, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "before-summary", active.Summaries[""].Summary)
}

func TestLatestTurnRevisionBoundaryFailures(t *testing.T) {
	ctx := context.Background()
	validKey := session.Key{
		AppName: "app", UserID: "user", SessionID: "session",
	}
	validRequest := revision.RewindRequest{
		Key:             validKey,
		TargetRequestID: "request", ExpectedHeadRequestID: "request",
		IdempotencyKey: "replacement",
	}

	t.Run("invalid request", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		for _, req := range []revision.RewindRequest{
			{},
			{Key: validKey, IdempotencyKey: "replacement"},
			{
				Key:                   validKey,
				TargetRequestID:       "request",
				ExpectedHeadRequestID: "request",
			},
		} {
			_, err := service.Rewind(ctx, req)
			require.Error(t, err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := service.Rewind(cancelled, validRequest)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("missing app or session", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err := service.Rewind(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrRewindUnavailable)

		service.getOrCreateAppSessions(validKey.AppName)
		_, err = service.Rewind(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrRewindUnavailable)
	})

	t.Run("generation exhausted", func(t *testing.T) {
		service, stored := serviceWithRevisionCheckpoint(
			t,
			math.MaxUint64,
			[]byte(`{}`),
		)
		defer func() { require.NoError(t, service.Close()) }()
		_, err := service.Rewind(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrRewindUnavailable)
		assert.Equal(t, uint64(math.MaxUint64), stored.revision.record.Generation)
	})

	t.Run("corrupt checkpoint", func(t *testing.T) {
		service, _ := serviceWithRevisionCheckpoint(t, 0, []byte(`{`))
		defer func() { require.NoError(t, service.Close()) }()
		_, err := service.Rewind(ctx, validRequest)
		assert.ErrorContains(t, err, "decode session boundary")
	})

	t.Run("snapshot failure", func(t *testing.T) {
		stored := &sessionWithTTL{}
		projection := session.NewSession("app", "user", "session")
		turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "request", InvocationID: "invocation",
		})
		err := stored.applyEventRevisionWrite(
			turnCtx,
			projection,
			testMessageEvent("event", "request", "invocation", "message"),
		)
		assert.ErrorIs(t, err, session.ErrNilSession)
	})

	t.Run("stale track projection", func(t *testing.T) {
		stored := &sessionWithTTL{revision: &latestTurnRevision{
			record: revision.PersistedRecord{Generation: 1},
		}}
		projection := session.NewSession("app", "user", "session")
		revision.SetGeneration(projection, 0)
		err := stored.applyTrackRevisionWrite(ctx, projection, &session.TrackEvent{})
		assert.ErrorIs(t, err, revision.ErrStaleGeneration)
	})

	t.Run("missing scoped state", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		merged := service.mergeScopedStateLocked(
			&appSessions{},
			"user",
			session.NewSession("app", "user", "session"),
		)
		require.NotNil(t, merged)
	})
}

func serviceWithRevisionCheckpoint(
	t *testing.T,
	generation uint64,
	boundary []byte,
) (*SessionService, *sessionWithTTL) {
	t.Helper()
	service := NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	app, ok := service.getAppSessions(key.AppName)
	require.True(t, ok)
	stored := app.sessions[key.UserID][key.SessionID]
	stored.revision = &latestTurnRevision{record: revision.PersistedRecord{
		Generation: generation,
		Checkpoint: &revision.PersistedCheckpoint{
			RequestID: "request",
			Boundary:  boundary,
			Terminal:  true,
		},
	}}
	return service, stored
}

func testMessageEvent(id, requestID, invocationID, content string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "author", &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, Content: content},
		}},
	})
	evt.ID = id
	evt.RequestID = requestID
	return evt
}

func testCompletionEvent(requestID, invocationID string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "app", &model.Response{
		Done:   true,
		Object: model.ObjectTypeRunnerCompletion,
	})
	evt.RequestID = requestID
	return evt
}
