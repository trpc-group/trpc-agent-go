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
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
)

func TestService_SessionTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) session.Key
		validate func(t *testing.T, client *redis.Client, sessionKey session.Key)
	}{
		{
			name: "session_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) session.Key {
				sessionKey := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}
				sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				testEvent := event.New("test-invocation", "test-author")
				testEvent.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Test message for TTL test"}}},
				}
				err = service.AppendEvent(context.Background(), sess, testEvent)
				require.NoError(t, err)
				return sessionKey
			},
			validate: func(t *testing.T, client *redis.Client, sessionKey session.Key) {
				sessionMetaKey := hashidx.GetSessionMetaKey("", sessionKey)
				ttl := client.TTL(context.Background(), sessionMetaKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second)

				eventKey := hashidx.GetEventTimeIndexKey("", sessionKey)
				ttl = client.TTL(context.Background(), eventKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second)
			},
		},
		{
			name: "session_ttl_not_refreshed_on_get",
			setup: func(t *testing.T, service *Service) session.Key {
				sessionKey := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session456"}
				sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				testEvent := event.New("test-invocation", "test-author")
				testEvent.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Test message for TTL test"}}},
				}
				err = service.AppendEvent(context.Background(), sess, testEvent)
				require.NoError(t, err)

				// Wait for async persist to complete.
				time.Sleep(1 * time.Second)

				// Manually set a known shorter TTL on sessionMeta key.
				// This ensures we have a deterministic baseline.
				client := buildRedisClient(t, service.opts.url)
				sessionMetaKey := hashidx.GetSessionMetaKey("", sessionKey)
				err = client.Expire(context.Background(), sessionMetaKey, 2*time.Second).Err()
				require.NoError(t, err)

				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)
				return sessionKey
			},
			validate: func(t *testing.T, client *redis.Client, sessionKey session.Key) {
				// GetSession (read path) should NOT refresh TTL.
				// We manually set TTL to 2s, so after GetSession it should still be <= 2s.
				sessionMetaKey := hashidx.GetSessionMetaKey("", sessionKey)
				ttl := client.TTL(context.Background(), sessionMetaKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 2*time.Second,
					"expected TTL to NOT be refreshed back to 5s, got %v", ttl.Val())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(5*time.Second))
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			sessionKey := tt.setup(t, service)
			tt.validate(t, client, sessionKey)
		})
	}
}

func TestService_getSessionSummaryTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ttl := 3 * time.Second
	service, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(ttl))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "summary-app", UserID: "summary-user", SessionID: "summary-session"}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	summary := map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "hello", UpdatedAt: time.Now()},
	}
	summaryBytes, err := json.Marshal(summary)
	require.NoError(t, err)

	client := buildRedisClient(t, redisURL)
	summaryKey := hashidx.GetSessionSummaryKey("", key)
	err = client.Set(ctx, summaryKey, summaryBytes, 0).Err()
	require.NoError(t, err)

	_, err = service.GetSession(ctx, key)
	require.NoError(t, err)

	// GetSession (read path) should NOT refresh TTL.
	// The summaryKey was set without TTL, so it should remain without TTL.
	ttlVal := client.TTL(ctx, summaryKey).Val()
	assert.Equal(t, time.Duration(-1), ttlVal, "expected no TTL on summaryKey after GetSession (read path should not refresh TTL)")
}

func TestService_AppStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) string
		validate func(t *testing.T, client *redis.Client, appName string)
	}{
		{
			name: "app_state_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) string {
				appName := "testapp"
				err := service.UpdateAppState(context.Background(), appName, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)
				return appName
			},
			validate: func(t *testing.T, client *redis.Client, appName string) {
				appStateKey := zset.GetAppStateKey(appName)
				ttl := client.TTL(context.Background(), appStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second)
			},
		},
		{
			name: "app_state_ttl_refreshed_on_get",
			setup: func(t *testing.T, service *Service) string {
				appName := "testapp2"
				err := service.UpdateAppState(context.Background(), appName, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				time.Sleep(2 * time.Second)

				sessionKey := session.Key{AppName: appName, UserID: "user123", SessionID: "session123"}
				_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
				require.NoError(t, err)

				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)
				return appName
			},
			validate: func(t *testing.T, client *redis.Client, appName string) {
				appStateKey := zset.GetAppStateKey(appName)
				ttl := client.TTL(context.Background(), appStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL), WithAppStateTTL(5*time.Second))
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			appName := tt.setup(t, service)
			tt.validate(t, client, appName)
		})
	}
}

func TestService_UserStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) session.UserKey
		validate func(t *testing.T, client *redis.Client, userKey session.UserKey)
	}{
		{
			name: "user_state_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) session.UserKey {
				userKey := session.UserKey{AppName: "testapp", UserID: "user123"}
				err := service.UpdateUserState(context.Background(), userKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)
				return userKey
			},
			validate: func(t *testing.T, client *redis.Client, userKey session.UserKey) {
				userStateKey := hashidx.GetUserStateKey("", userKey.AppName, userKey.UserID)
				ttl := client.TTL(context.Background(), userStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second)
			},
		},
		{
			name: "user_state_ttl_refreshed_on_get",
			setup: func(t *testing.T, service *Service) session.UserKey {
				userKey := session.UserKey{AppName: "testapp2", UserID: "user456"}
				err := service.UpdateUserState(context.Background(), userKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				time.Sleep(2 * time.Second)

				sessionKey := session.Key{AppName: userKey.AppName, UserID: userKey.UserID, SessionID: "session456"}
				_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
				require.NoError(t, err)

				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)
				return userKey
			},
			validate: func(t *testing.T, client *redis.Client, userKey session.UserKey) {
				userStateKey := hashidx.GetUserStateKey("", userKey.AppName, userKey.UserID)
				ttl := client.TTL(context.Background(), userStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL), WithUserStateTTL(5*time.Second))
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			userKey := tt.setup(t, service)
			tt.validate(t, client, userKey)
		})
	}
}

func TestService_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	err = service.UpdateAppState(ctx, key.AppName, session.StateMap{"app_key": []byte("app_value")})
	require.NoError(t, err)

	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"user_key": []byte("user_value")})
	require.NoError(t, err)

	sess, err := service.CreateSession(ctx, key, session.StateMap{"sess_key": []byte("sess_value")})
	require.NoError(t, err)
	assert.NotNil(t, sess)

	evt := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.Equal(t, []byte("sess_value"), retrievedSess.State["sess_key"])
	assert.Equal(t, []byte("app_value"), retrievedSess.State["app:app_key"])
	assert.Equal(t, []byte("user_value"), retrievedSess.State["user:user_key"])

	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

func TestService_DeleteSession_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	evt := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)
}

func TestService_ListSessions_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "testapp", UserID: "user123"}

	for i := 0; i < 3; i++ {
		key := session.Key{AppName: userKey.AppName, UserID: userKey.UserID, SessionID: fmt.Sprintf("session%d", i)}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
	}

	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
}

func TestService_TrimConversations_TTLRefresh(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ttl := 2 * time.Hour
	service, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(ttl))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess_ttl"}

	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	events := []*event.Event{
		{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-1 * time.Hour), Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "test1"}}}}},
		{ID: "e2", RequestID: "req2", Timestamp: time.Now(), Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "test2"}}}}},
	}
	for _, evt := range events {
		require.NoError(t, service.AppendEvent(ctx, sess, evt))
	}

	_, err = service.TrimConversations(ctx, key, WithCount(1))
	require.NoError(t, err)

	client := buildRedisClient(t, redisURL)
	defer client.Close()

	eventTimeIndexKey := hashidx.GetEventTimeIndexKey("", key)
	sessMetaKey := hashidx.GetSessionMetaKey("", key)

	eventTTL := client.TTL(ctx, eventTimeIndexKey).Val()
	sessTTL := client.TTL(ctx, sessMetaKey).Val()

	assert.Greater(t, eventTTL, time.Duration(0))
	assert.Greater(t, sessTTL, time.Duration(0))
}
