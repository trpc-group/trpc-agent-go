//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
)

func TestService_AppendTrackEvent_SessionError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"test"`),
		Timestamp: time.Now(),
	}

	err = service.AppendTrackEvent(ctx, sess, trackEvent)
	require.NoError(t, err)

	// Verify track event was stored
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess.Tracks)
	alpha, ok := retrievedSess.Tracks["alpha"]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
}

func TestService_AppendTrackEvent_Persistence(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	baseTime := time.Now()
	for i := 0; i < 3; i++ {
		trackEvent := &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(`"payload"`),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		}
		err = service.AppendTrackEvent(ctx, sess, trackEvent)
		require.NoError(t, err)
	}

	// Verify all events were stored
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess.Tracks)
	alpha, ok := retrievedSess.Tracks["alpha"]
	require.True(t, ok)
	assert.Len(t, alpha.Events, 3)
}

func TestService_GetSessionTrackAfterTime(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		trackEvent := &session.TrackEvent{
			Track:     "beta",
			Payload:   json.RawMessage(`"data"`),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		}
		err = service.AppendTrackEvent(ctx, sess, trackEvent)
		require.NoError(t, err)
	}

	afterTime := baseTime.Add(2 * time.Second)
	retrievedSess, err := service.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)
	if retrievedSess.Tracks != nil {
		beta, ok := retrievedSess.Tracks["beta"]
		if ok {
			assert.GreaterOrEqual(t, len(beta.Events), 2)
		}
	}
}

func TestService_ListSessionsWithTrackEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "track-app", UserID: "track-user", SessionID: "track-session"}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	payload := json.RawMessage(`"list-track"`)
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "alpha",
		Payload:   payload,
		Timestamp: time.Now(),
	}))

	sessions, err := service.ListSessions(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NotNil(t, sessions[0].Tracks)
	alpha, ok := sessions[0].Tracks["alpha"]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
	assert.Equal(t, payload, alpha.Events[0].Payload)
}

func TestService_DeleteSession_WithTracks(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user1", SessionID: "sess-del-tracks"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: "alpha", Payload: json.RawMessage(`"a1"`), Timestamp: time.Now(),
	}))
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: "beta", Payload: json.RawMessage(`"b1"`), Timestamp: time.Now(),
	}))

	client := buildRedisClient(t, redisURL)
	alphaDataKey := hashidx.GetTrackDataKey("", key, "alpha")
	alphaIdxKey := hashidx.GetTrackTimeIndexKey("", key, "alpha")
	betaDataKey := hashidx.GetTrackDataKey("", key, "beta")
	betaIdxKey := hashidx.GetTrackTimeIndexKey("", key, "beta")
	n, err := client.Exists(ctx, alphaDataKey, alphaIdxKey, betaDataKey, betaIdxKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(4), n)

	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	got, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, got)

	n, err = client.Exists(ctx, alphaDataKey, alphaIdxKey, betaDataKey, betaIdxKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	metaKey := hashidx.GetSessionMetaKey("", key)
	evtDataKey := hashidx.GetEventDataKey("", key)
	evtTimeKey := hashidx.GetEventTimeIndexKey("", key)
	sumKey := hashidx.GetSessionSummaryKey("", key)
	n, err = client.Exists(ctx, metaKey, evtDataKey, evtTimeKey, sumKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestService_DeleteSession_NoTracks(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user1", SessionID: "sess-no-tracks"}

	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	evt := createTestEvent("e1", "agent", "hello", time.Now(), false)
	sess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NoError(t, service.AppendEvent(ctx, sess, evt))

	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	got, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestService_AppendTrackEventRecover(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "recover-app", UserID: "recover-user", SessionID: "recover-session"}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	require.NoError(t, service.Close())

	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"recover"`),
		Timestamp: time.Now(),
	}

	assert.NotPanics(t, func() {
		err = service.AppendTrackEvent(ctx, sess, trackEvent)
	})
	assert.NoError(t, err)
}

func TestService_AppendTrackEvent_Async_ContextCancelled(t *testing.T) {
	// Create a service with unbuffered channel and no consumer
	// to simulate blocking on channel write
	ch := make(chan *trackEventPair) // Unbuffered channel

	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		trackEventChans: []chan *trackEventPair{ch},
	}

	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}

	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"test"`),
		Timestamp: time.Now(),
	}

	// Create a cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// When context is cancelled, enqueue should return ctx.Err()
	err := service.AppendTrackEvent(cancelledCtx, sess, trackEvent)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}
