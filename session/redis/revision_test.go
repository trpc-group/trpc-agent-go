//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLatestTurnReplacementContract(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []ServiceOpt
	}{
		{name: "hashidx", opts: []ServiceOpt{WithCompatMode(CompatModeNone)}},
		{name: "zset", opts: []ServiceOpt{WithCompatMode(CompatModeTransition)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			opts := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.opts...)
			service, err := NewService(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			replacementtest.Run(t, service)
		})
	}
}

func TestReplaceLatestTurnRealRedis(t *testing.T) {
	redisURL := os.Getenv("TRPC_AGENT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("TRPC_AGENT_REDIS_TEST_URL is not set")
	}
	tests := []struct {
		name string
		opts []ServiceOpt
	}{
		{name: "hashidx", opts: []ServiceOpt{WithCompatMode(CompatModeNone)}},
		{name: "zset", opts: []ServiceOpt{WithCompatMode(CompatModeTransition)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.opts...)
			service, err := NewService(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			testRedisLatestTurnReplacement(t, service, false)
		})
	}
}

func TestReplaceLatestTurnRestoresRedisLayouts(t *testing.T) {
	tests := []struct {
		name  string
		opts  []ServiceOpt
		async bool
	}{
		{name: "hashidx sync", opts: []ServiceOpt{WithCompatMode(CompatModeNone)}},
		{name: "hashidx async", opts: []ServiceOpt{
			WithCompatMode(CompatModeNone),
			WithEnableAsyncPersist(true),
			WithAsyncPersisterNum(1),
		}, async: true},
		{name: "zset sync", opts: []ServiceOpt{WithCompatMode(CompatModeTransition)}},
		{name: "zset async", opts: []ServiceOpt{
			WithCompatMode(CompatModeTransition),
			WithEnableAsyncPersist(true),
			WithAsyncPersisterNum(1),
		}, async: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			opts := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.opts...)
			service, err := NewService(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			testRedisLatestTurnReplacement(t, service, tt.async)
		})
	}
}

func TestTrimConversationsMakesLatestTurnUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []ServiceOpt
	}{
		{name: "hashidx", opts: []ServiceOpt{WithCompatMode(CompatModeNone)}},
		{name: "zset", opts: []ServiceOpt{WithCompatMode(CompatModeTransition)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			opts := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.opts...)
			service, err := NewService(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })

			ctx := context.Background()
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "session",
			}
			sess, err := service.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			turnCtx := sessionrevision.ContextWithTurnStart(
				ctx,
				sessionrevision.TurnStart{
					RequestID: "request", InvocationID: "invocation",
				},
			)
			require.NoError(t, service.AppendEvent(
				turnCtx,
				sess,
				redisTestMessageEvent(
					"turn", "request", "invocation", "latest",
				),
			))
			require.NoError(t, service.AppendEvent(
				ctx,
				sess,
				redisTestCompletionEvent("request", "invocation"),
			))
			deleted, err := service.TrimConversations(ctx, key, WithCount(1))
			require.NoError(t, err)
			require.NotEmpty(t, deleted)
			var record *sessionrevision.PersistedRecord
			if tt.name == "zset" {
				record, err = service.zsetClient.Revision(ctx, key)
			} else {
				record, err = service.hashidxClient.Revision(ctx, key)
			}
			require.NoError(t, err)
			assert.False(t, sessionrevision.ProjectionInitialized(record))

			_, err = sessionrevision.ReplaceLatestTurn(
				ctx,
				service,
				sessionrevision.LatestTurnReplacementRequest{
					Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
				},
			)
			assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
		})
	}
}

func TestRollingProjectionSupportsSuccessiveTurns(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []ServiceOpt
	}{
		{name: "hashidx", opts: []ServiceOpt{WithCompatMode(CompatModeNone)}},
		{name: "zset", opts: []ServiceOpt{WithCompatMode(CompatModeTransition)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			opts := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.opts...)
			service, err := NewService(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })

			ctx := context.Background()
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "rolling-" + tt.name,
			}
			sess, err := service.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			baseline := redisTestMessageEvent(
				"baseline", "baseline", "baseline", "baseline",
			)
			baseline.Response.Choices[0].Message.Role = model.RoleUser
			require.NoError(t, service.AppendEvent(ctx, sess, baseline))

			for i, requestID := range []string{"request-1", "request-2"} {
				invocationID := fmt.Sprintf("invocation-%d", i+1)
				turnCtx := sessionrevision.ContextWithTurnStart(
					ctx,
					sessionrevision.TurnStart{
						RequestID: requestID, InvocationID: invocationID,
					},
				)
				require.NoError(t, service.AppendEvent(
					turnCtx,
					sess,
					redisTestMessageEvent(
						"event-"+requestID, requestID, invocationID, requestID,
					),
				))
				require.NoError(t, service.AppendTrackEvent(
					ctx,
					sess,
					&session.TrackEvent{
						Track:     "ui",
						RequestID: requestID,
						Payload:   []byte(fmt.Sprintf(`{"turn":%d}`, i+1)),
						Timestamp: time.Now().Add(
							time.Duration(i-10) * time.Second,
						),
					},
				))
				require.NoError(t, service.AppendEvent(
					ctx,
					sess,
					redisTestCompletionEvent(requestID, invocationID),
				))
			}

			var (
				record *sessionrevision.PersistedRecord
				active *session.Session
			)
			if tt.name == "zset" {
				record, err = service.zsetClient.Revision(ctx, key)
				if err == nil {
					active, err = service.zsetClient.RevisionProjection(ctx, key)
				}
			} else {
				record, err = service.hashidxClient.Revision(ctx, key)
				if err == nil {
					active, err = service.hashidxClient.RevisionProjection(ctx, key)
				}
			}
			require.NoError(t, err)
			require.True(t, sessionrevision.ProjectionInitialized(record))
			boundary, err := sessionrevision.NewBoundaryFromProjection(
				active, record.Projection,
			)
			require.NoError(t, err)
			fullBoundary, fullErr := sessionrevision.NewBoundary(active)
			require.NoError(t, fullErr)
			assert.JSONEq(t, string(fullBoundary), string(boundary))

			result, err := sessionrevision.ReplaceLatestTurn(
				ctx,
				service,
				sessionrevision.LatestTurnReplacementRequest{
					Key:               key,
					ExpectedRequestID: "request-2",
					IdempotencyKey:    "replace-request-2",
				},
			)
			require.NoError(t, err)
			assert.True(t, result.Applied)
			for _, evt := range result.ActiveSession.Events {
				assert.NotEqual(t, "request-2", evt.RequestID)
			}
			trackEvents, err := result.ActiveSession.GetTrackEvents("ui")
			require.NoError(t, err)
			require.Len(t, trackEvents.Events, 1)
			assert.Equal(t, "request-1", trackEvents.Events[0].RequestID)
		})
	}
}

func TestReplaceLatestTurnValidatesRequest(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	tests := []struct {
		name string
		req  sessionrevision.LatestTurnReplacementRequest
	}{
		{name: "invalid key"},
		{name: "missing expected request", req: sessionrevision.LatestTurnReplacementRequest{
			Key: key, IdempotencyKey: "replacement",
		}},
		{name: "missing idempotency key", req: sessionrevision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "request",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.ReplaceLatestTurn(ctx, tt.req)
			require.Error(t, err)
		})
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = service.ReplaceLatestTurn(
		cancelled,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
}

func TestRevisionFlushHandlesEmptyChannelSets(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	require.NoError(t, flushPairChannel(
		ctx,
		nil,
		key,
		&sessionEventPair{},
	))
	require.NoError(t, flushTrackPairChannel(ctx, nil, key))
}

func TestRevisionFlushHonorsCancellation(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	t.Run("before event barrier is accepted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := flushPairChannel(
			ctx,
			[]chan *sessionEventPair{make(chan *sessionEventPair)},
			key,
			&sessionEventPair{},
		)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("while waiting for event barrier", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan *sessionEventPair)
		go func() {
			<-ch
			cancel()
		}()
		err := flushPairChannel(
			ctx,
			[]chan *sessionEventPair{ch},
			key,
			&sessionEventPair{},
		)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("before track barrier is accepted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := flushTrackPairChannel(
			ctx,
			[]chan *trackEventPair{make(chan *trackEventPair)},
			key,
		)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("while waiting for track barrier", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan *trackEventPair)
		go func() {
			<-ch
			cancel()
		}()
		err := flushTrackPairChannel(
			ctx,
			[]chan *trackEventPair{ch},
			key,
		)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestPrepareTurnStartWriteRejectsMissingOrClosedStorage(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeNone),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err = service.prepareTurnStartWrite(
		ctx,
		"hashidx",
		key,
		sessionrevision.Write{},
	)
	assert.ErrorContains(t, err, "session not found")

	require.NoError(t, service.Close())
	_, err = service.prepareTurnStartWrite(
		ctx,
		"hashidx",
		key,
		sessionrevision.Write{},
	)
	assert.ErrorContains(t, err, "load authoritative pre-turn session")
}

func testRedisLatestTurnReplacement(t *testing.T, service *Service, async bool) {
	ctx := context.Background()
	key := session.Key{
		AppName: "app",
		UserID:  "user",
		SessionID: strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()) +
			fmt.Sprintf("-%d", time.Now().UnixNano()),
	}
	sess, err := service.CreateSession(ctx, key, session.StateMap{"phase": []byte("before")})
	require.NoError(t, err)

	baseline := redisTestMessageEvent("event-before", "request-before", "invocation-before", "before")
	baseline.Response.Choices[0].Message.Role = model.RoleUser
	require.NoError(t, service.AppendEvent(ctx, sess, baseline))
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		Payload:   []byte(`{"value":"before"}`),
		Timestamp: time.Now().Add(-time.Second),
	}))

	turnCtx := sessionrevision.ContextWithTurnStart(ctx, sessionrevision.TurnStart{
		RequestID:    "request-latest",
		InvocationID: "invocation-latest",
	})
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		redisTestMessageEvent("event-latest", "request-latest", "invocation-latest", "latest"),
	))
	stateEvent := redisTestMessageEvent("event-state", "request-latest", "invocation-latest", "state")
	stateEvent.StateDelta = session.StateMap{"phase": []byte("after")}
	require.NoError(t, service.AppendEvent(ctx, sess, stateEvent))
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		RequestID: "request-latest",
		Payload:   []byte(`{"value":"latest"}`),
		Timestamp: time.Now(),
	}))
	require.NoError(t, service.AppendEvent(ctx, sess, redisTestCompletionEvent(
		"request-latest",
		"invocation-latest",
	)))

	result, err := sessionrevision.ReplaceLatestTurn(ctx, service, sessionrevision.LatestTurnReplacementRequest{
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

	replayed, err := sessionrevision.ReplaceLatestTurn(ctx, service, sessionrevision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request-latest",
		IdempotencyKey:    "replacement-1",
	})
	require.NoError(t, err)
	assert.False(t, replayed.Applied)

	err = service.AppendEvent(ctx, sess, redisTestMessageEvent(
		"event-stale",
		"request-stale",
		"invocation-stale",
		"stale",
	))
	if async {
		require.NoError(t, err)
		assert.ErrorIs(
			t,
			service.flushRevisionPersistence(ctx, key),
			sessionrevision.ErrStaleGeneration,
		)
	} else {
		assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
	}
	err = service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		RequestID: "request-stale",
		Payload:   []byte(`{"value":"stale"}`),
		Timestamp: time.Now().Add(time.Second),
	})
	if async {
		require.NoError(t, err)
		assert.ErrorIs(
			t,
			service.flushRevisionPersistence(ctx, key),
			sessionrevision.ErrStaleGeneration,
		)
	} else {
		assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
	}
	active, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, active.Events, 1)
	assert.Equal(t, "event-before", active.Events[0].ID)
	trackEvents, err = active.GetTrackEvents("ui")
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	assert.JSONEq(t, `{"value":"before"}`, string(trackEvents.Events[0].Payload))
	staleGeneration, ok := sessionrevision.Generation(sess)
	require.True(t, ok)
	err = service.UpdateSessionState(
		sessionrevision.ContextWithGeneration(ctx, staleGeneration),
		key,
		session.StateMap{"phase": []byte("stale")},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
}

func redisTestMessageEvent(id, requestID, invocationID, content string) *event.Event {
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

func redisTestCompletionEvent(requestID, invocationID string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "app", &model.Response{
		Done:   true,
		Object: model.ObjectTypeRunnerCompletion,
	})
	evt.RequestID = requestID
	return evt
}
