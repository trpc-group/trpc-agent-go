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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
)

func TestService_GetSession_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "non-existent",
	}

	// Get non-existent session should return nil without error
	sess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestService_ListSessions_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// List sessions for non-existent user should return empty list
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestService_UpdateAppState_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update app state should succeed even if app doesn't exist yet
	err = service.UpdateAppState(ctx, "new-app", session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify state was created
	states, err := service.ListAppStates(ctx, "new-app")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

func TestService_UpdateUserState_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Update user state should succeed even if user doesn't exist yet
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify state was created
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

func TestService_ListAppStates_Empty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List states for non-existent app should return empty map
	states, err := service.ListAppStates(ctx, "non-existent-app")
	require.NoError(t, err)
	assert.Empty(t, states)
}

func TestService_ListUserStates_Empty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// List states for non-existent user should return empty map
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, states)
}

func TestService_DeleteAppState_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete non-existent app state should not error
	err = service.DeleteAppState(ctx, "non-existent-app", "key")
	require.NoError(t, err)
}

func TestService_DeleteUserState_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// Delete non-existent user state should not error
	err = service.DeleteUserState(ctx, userKey, "key")
	require.NoError(t, err)
}

func TestService_ProcessStateCmd_Errors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Manually corrupt the session meta in Redis to trigger unmarshal error
	client := buildRedisClient(t, redisURL)
	metaKey := hashidx.GetSessionMetaKey("", key)
	err = client.Set(ctx, metaKey, "invalid json", 0).Err()
	require.NoError(t, err)

	// Try to get the session - should return error
	_, err = service.GetSession(ctx, key)
	assert.Error(t, err)
}

func TestService_UpdateAppState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update app state with empty app name should error
	err = service.UpdateAppState(ctx, "", session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestService_UpdateUserState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update user state with empty user key should error
	err = service.UpdateUserState(ctx, session.UserKey{}, session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestService_ListAppStates_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List app states with empty app name should error
	_, err = service.ListAppStates(ctx, "")
	require.Error(t, err)
}

func TestService_ListUserStates_EmptyUserKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List user states with empty user key should error
	_, err = service.ListUserStates(ctx, session.UserKey{})
	require.Error(t, err)
}

func TestService_DeleteAppState_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete app state with empty app name should error
	err = service.DeleteAppState(ctx, "", "key")
	require.Error(t, err)
}

func TestService_DeleteUserState_EmptyUserKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete user state with empty user key should error
	err = service.DeleteUserState(ctx, session.UserKey{}, "key")
	require.Error(t, err)
}

func TestService_CorruptedData_AppState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session first
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Manually corrupt app state data in Redis
	client := buildRedisClient(t, redisURL)
	appStateKey := zset.GetAppStateKey(key.AppName)
	// Set invalid data that can't be converted to bytes properly
	// This tests the error path in processStateCmd
	err = client.HSet(ctx, appStateKey, "corrupted_key", string([]byte{0xff, 0xfe, 0xfd})).Err()
	require.NoError(t, err)

	// Try to get the session - should still work as processStateCmd handles conversion
	sess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
}

func TestService_CorruptedData_Events(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// For V2: Manually corrupt event time index by setting it to wrong type
	// V2 uses ZSet for time index, corrupt it to a string
	client := buildRedisClient(t, redisURL)
	eventTimeIndexKey := hashidx.GetEventTimeIndexKey("", key)
	// Delete and set to wrong type
	err = client.Del(ctx, eventTimeIndexKey).Err()
	require.NoError(t, err)
	err = client.Set(ctx, eventTimeIndexKey, "corrupted", 0).Err()
	require.NoError(t, err)

	// Try to get the session - should return error due to wrong type
	_, err = service.GetSession(ctx, key)
	assert.Error(t, err)
}
