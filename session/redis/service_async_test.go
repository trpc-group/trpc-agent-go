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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_AsyncPersisterNum_DefaultClamp(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(0),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, defaultAsyncPersisterNum, len(service.eventPairChans))
}

func TestService_AppendEvent_NoPanic_AfterClose(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)

	// Create a session.
	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}
	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	// Close service to close channels.
	service.Close()

	// Append after close should not panic due to recover in AppendEvent.
	evt := createTestEvent("event-after-close", "agent", "msg", time.Now(), false)
	assert.NotPanics(t, func() {
		_ = service.AppendEvent(context.Background(), sess, evt)
	})
}

func TestStartAsyncPersistWorker(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(3),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that event pair channels are initialized
	assert.Len(t, service.eventPairChans, 3)
	assert.NotNil(t, service.eventPairChans[0])
	assert.NotNil(t, service.eventPairChans[1])
	assert.NotNil(t, service.eventPairChans[2])

	// Verify channel buffer sizes
	for i, ch := range service.eventPairChans {
		assert.Equal(t, defaultChanBufferSize, cap(ch), "Channel %d should have buffer size %d", i, defaultChanBufferSize)
	}
}

func TestStartAsyncSummaryWorker(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test without summarizer - async workers should not be started
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(50),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that asyncWorker is not initialized when no summarizer is provided
	assert.Nil(t, service.asyncWorker)
}

func TestService_Close(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test closing with async persist enabled
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)

	// Verify channels are open before close
	assert.Len(t, service.eventPairChans, 2)
	for i, ch := range service.eventPairChans {
		assert.NotNil(t, ch, "Channel %d should not be nil before close", i)
	}

	// Close service
	err = service.Close()
	assert.NoError(t, err)

	// Verify channels are closed (we can't directly test this, but we can test that no panic occurs)
	// and that the service is in a closed state
}

func TestService_AsyncPersist_Enabled(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async persist enabled
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Append events
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Give async workers time to process
	time.Sleep(100 * time.Millisecond)

	// Verify events were persisted
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.NotEmpty(t, retrievedSess.Events)
}

func TestAppendEvent_AsyncRecover(t *testing.T) {
	ch := make(chan *sessionEventPair, 1)
	close(ch)

	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		eventPairChans: []chan *sessionEventPair{ch},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	evt := &event.Event{Response: &model.Response{}}

	assert.NotPanics(t, func() {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	})
}

func TestAppendTrackEvent_AsyncRecover(t *testing.T) {
	ch := make(chan *trackEventPair, 1)
	close(ch)

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
		Timestamp: time.Now(),
	}

	assert.NotPanics(t, func() {
		err := service.AppendTrackEvent(
			context.Background(),
			sess,
			trackEvent,
		)
		require.NoError(t, err)
	})
}

// TestEnqueueTrackEvent_SendOnClosedChannel tests enqueueTrackEvent recovers from closed channel panic
func TestEnqueueTrackEvent_SendOnClosedChannel(t *testing.T) {
	ch := make(chan *trackEventPair, 1)
	close(ch)

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
		Hash:    0, // will use channel index 0
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	// Should not panic when sending on closed channel
	assert.NotPanics(t, func() {
		err := service.enqueueTrackEvent(context.Background(), sess, session.Key{}, trackEvent, nil)
		// Error is expected since channel is closed, but function logs it and returns nil
		require.NoError(t, err)
	})
}

// TestEnqueueTrackEvent_ContextCancelled tests enqueueTrackEvent when context is cancelled
func TestEnqueueTrackEvent_ContextCancelled(t *testing.T) {
	// Create a full buffer channel so send blocks
	ch := make(chan *trackEventPair, 0)

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
		Hash:    0,
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	// Cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Should return context error when context is cancelled
	err := service.enqueueTrackEvent(ctx, sess, session.Key{}, trackEvent, nil)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// TestStartAsyncPersistWorker_WithTrackEvents tests startAsyncPersistWorker handles track events
func TestStartAsyncPersistWorker_WithTrackEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that track event channels are initialized
	assert.Len(t, service.trackEventChans, 2)
	for i, ch := range service.trackEventChans {
		assert.NotNil(t, ch, "Track event channel %d should not be nil", i)
		assert.Equal(t, defaultChanBufferSize, cap(ch), "Track event channel %d should have buffer size %d", i, defaultChanBufferSize)
	}
}
