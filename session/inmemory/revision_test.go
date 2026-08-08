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
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLatestTurnReplacementContract(t *testing.T) {
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	replacementtest.Run(t, service)
}

func TestReplaceLatestTurnRestoresCheckpointAndFencesOldProjection(t *testing.T) {
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
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		RequestID: "request-latest",
		Payload:   []byte(`{"value":"latest"}`),
		Timestamp: time.Now(),
	}))
	require.NoError(t, service.AppendEvent(ctx, sess, testCompletionEvent(
		"request-latest",
		"invocation-latest",
	)))

	result, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request-latest",
		IdempotencyKey:    "replacement-1",
	})
	require.NoError(t, err)
	assert.True(t, result.Applied)
	require.Len(t, result.ActiveSession.Events, 1)
	assert.Equal(t, "event-before", result.ActiveSession.Events[0].ID)
	phase, ok := result.ActiveSession.GetState("phase")
	require.True(t, ok)
	assert.Equal(t, []byte("before"), phase)
	trackEvents, err := result.ActiveSession.GetTrackEvents("ui")
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	assert.JSONEq(t, `{"value":"before"}`, string(trackEvents.Events[0].Payload))

	replayed, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request-latest",
		IdempotencyKey:    "replacement-1",
	})
	require.NoError(t, err)
	assert.False(t, replayed.Applied)
	assert.Equal(t, result.ActiveSession.Events, replayed.ActiveSession.Events)

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

func TestReplaceLatestTurnMatchesExpectedTurn(t *testing.T) {
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

	result, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request",
		IdempotencyKey:    "replacement-incomplete",
	})
	require.NoError(t, err)
	require.Empty(t, result.ActiveSession.Events)

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
	_, err = revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               completedKey,
		ExpectedRequestID: "different-request",
		IdempotencyKey:    "replacement-conflict",
	})
	assert.ErrorIs(t, err, revision.ErrLatestTurnReplacementConflict)

	_, err = revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               completedKey,
		ExpectedRequestID: "request",
		IdempotencyKey:    "replacement-success",
	})
	require.NoError(t, err)
	_, err = revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               completedKey,
		ExpectedRequestID: "different-request",
		IdempotencyKey:    "replacement-success",
	})
	assert.True(t, errors.Is(err, revision.ErrLatestTurnReplacementConflict))
}

func TestReplaceLatestTurnRequiresPersistedCanonicalStart(t *testing.T) {
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
			_, err = revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
				Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
			})
			assert.ErrorIs(t, err, revision.ErrLatestTurnReplacementUnavailable)
		})
	}
}

func TestReplaceLatestTurnRestoresSummaryAndRejectsStaleSummary(t *testing.T) {
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
	require.NoError(t, service.CreateSessionSummary(ctx, sess, "", true))
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
	require.NoError(t, service.CreateSessionSummary(ctx, sess, "", true))
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		testCompletionEvent("request", "invocation"),
	))
	result, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
	})
	require.NoError(t, err)
	require.Equal(t, "before-summary", result.ActiveSession.Summaries[""].Summary)

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
	validRequest := revision.LatestTurnReplacementRequest{
		Key:               validKey,
		ExpectedRequestID: "request",
		IdempotencyKey:    "replacement",
	}

	t.Run("invalid request", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		for _, req := range []revision.LatestTurnReplacementRequest{
			{},
			{Key: validKey, IdempotencyKey: "replacement"},
			{Key: validKey, ExpectedRequestID: "request"},
		} {
			_, err := service.ReplaceLatestTurn(ctx, req)
			require.Error(t, err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := service.ReplaceLatestTurn(cancelled, validRequest)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("missing app or session", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err := service.ReplaceLatestTurn(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrLatestTurnReplacementUnavailable)

		service.getOrCreateAppSessions(validKey.AppName)
		_, err = service.ReplaceLatestTurn(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("generation exhausted", func(t *testing.T) {
		service, stored := serviceWithRevisionCheckpoint(
			t,
			math.MaxUint64,
			[]byte(`{}`),
		)
		defer func() { require.NoError(t, service.Close()) }()
		_, err := service.ReplaceLatestTurn(ctx, validRequest)
		assert.ErrorIs(t, err, revision.ErrLatestTurnReplacementUnavailable)
		assert.Equal(t, uint64(math.MaxUint64), stored.revision.record.Generation)
	})

	t.Run("corrupt checkpoint", func(t *testing.T) {
		service, _ := serviceWithRevisionCheckpoint(t, 0, []byte(`{`))
		defer func() { require.NoError(t, service.Close()) }()
		_, err := service.ReplaceLatestTurn(ctx, validRequest)
		assert.ErrorContains(t, err, "decode latest-turn checkpoint")
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
	snapshot []byte,
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
			Snapshot:  snapshot,
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
