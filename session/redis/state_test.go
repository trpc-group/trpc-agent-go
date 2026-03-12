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

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRedisService_ListAppStates(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	appName := "test-app"

	// Test list empty app states.
	states, err := service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Empty(t, states)

	// Create app state.
	state := session.StateMap{"key1": []byte("value1"), "key2": []byte("value2")}
	err = service.UpdateAppState(ctx, appName, state)
	require.NoError(t, err)

	// List app states.
	states, err = service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), states["key1"])
	assert.Equal(t, []byte("value2"), states["key2"])

	// Test error case: empty app name.
	_, err = service.ListAppStates(ctx, "")
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestRedisService_DeleteAppState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	appName := "test-app"

	// Create app state.
	state := session.StateMap{"key1": []byte("value1"), "key2": []byte("value2")}
	err = service.UpdateAppState(ctx, appName, state)
	require.NoError(t, err)

	// Delete one key.
	err = service.DeleteAppState(ctx, appName, "key1")
	require.NoError(t, err)

	// Verify key1 is deleted, key2 still exists.
	states, err := service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Nil(t, states["key1"])
	assert.Equal(t, []byte("value2"), states["key2"])

	// Test error cases.
	err = service.DeleteAppState(ctx, "", "key")
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)

	err = service.DeleteAppState(ctx, appName, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

func TestRedisService_ListUserStates(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Test list empty user states.
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, states)

	// Create user state.
	state := session.StateMap{"pref1": []byte("dark"), "pref2": []byte("zh-CN")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// List user states.
	states, err = service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("dark"), states["pref1"])
	assert.Equal(t, []byte("zh-CN"), states["pref2"])

	// Test error cases.
	_, err = service.ListUserStates(ctx, session.UserKey{AppName: "", UserID: "user"})
	require.Error(t, err)

	_, err = service.ListUserStates(ctx, session.UserKey{AppName: "app", UserID: ""})
	require.Error(t, err)
}

func TestRedisService_DeleteUserState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Create user state.
	state := session.StateMap{"pref1": []byte("dark"), "pref2": []byte("zh-CN")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// Delete one key.
	err = service.DeleteUserState(ctx, userKey, "pref1")
	require.NoError(t, err)

	// Verify pref1 is deleted, pref2 still exists.
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Nil(t, states["pref1"])
	assert.Equal(t, []byte("zh-CN"), states["pref2"])

	// Test error cases.
	err = service.DeleteUserState(ctx, session.UserKey{AppName: "", UserID: "user"}, "key")
	require.Error(t, err)

	err = service.DeleteUserState(ctx, session.UserKey{AppName: "app", UserID: ""}, "key")
	require.Error(t, err)

	err = service.DeleteUserState(ctx, userKey, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

func TestRedisService_DeleteSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Create a session.
	sess, err := service.CreateSession(ctx, key, session.StateMap{"test": []byte("data")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session exists.
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	// Delete the session.
	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted.
	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)

	// Test error cases.
	err = service.DeleteSession(ctx, session.Key{AppName: "", UserID: "user", SessionID: "sess"})
	require.Error(t, err)

	err = service.DeleteSession(ctx, session.Key{AppName: "app", UserID: "", SessionID: "sess"})
	require.Error(t, err)

	err = service.DeleteSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: ""})
	require.Error(t, err)
}

func TestRedisService_UpdateUserState_TransitionMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with transition mode
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Create user state in transition mode (writes to both hashidx and zset)
	state := session.StateMap{"pref1": []byte("dark")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// Verify state can be read back
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("dark"), states["pref1"])
}

func TestRedisService_ListUserStates_FallbackToZSet(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with legacy mode to enable zset fallback
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeLegacy),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Create user state
	state := session.StateMap{"pref1": []byte("light")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// List should return the state
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("light"), states["pref1"])
}

func TestRedisService_UpdateAppState_TransitionMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with transition mode
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	appName := "test-app"

	// Create app state in transition mode (writes to both hashidx and zset)
	state := session.StateMap{"key1": []byte("value1")}
	err = service.UpdateAppState(ctx, appName, state)
	require.NoError(t, err)

	// Verify state can be read back
	states, err := service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), states["key1"])
}

// TestRedisService_UpdateUserState_ErrorPaths tests UpdateUserState error paths in transition mode
func TestRedisService_UpdateUserState_ErrorPaths(t *testing.T) {
	// Test invalid key errors
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Test empty app name
	err = service.UpdateUserState(ctx, session.UserKey{AppName: "", UserID: "user"}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)

	// Test empty user ID
	err = service.UpdateUserState(ctx, session.UserKey{AppName: "app", UserID: ""}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Equal(t, session.ErrUserIDRequired, err)
}

// TestRedisService_DeleteUserState_ZSetError tests DeleteUserState when zset deletion fails
func TestRedisService_DeleteUserState_ZSetError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service with transition mode
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}

	// Create user state first
	state := session.StateMap{"pref1": []byte("dark")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// Close Redis to cause deletion to fail
	mr.Close()

	// Delete should return error when zset deletion fails
	err = service.DeleteUserState(ctx, userKey, "pref1")
	require.Error(t, err)
	service.Close()
}

// TestRedisService_DeleteUserState_HashidxError tests DeleteUserState when hashidx deletion fails
func TestRedisService_DeleteUserState_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service with None mode (only hashidx)
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeNone),
	)
	require.NoError(t, err)

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}

	// Create user state first
	state := session.StateMap{"pref1": []byte("dark")}
	err = service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// Close Redis to cause deletion to fail
	mr.Close()

	// Delete should return error when hashidx deletion fails
	err = service.DeleteUserState(ctx, userKey, "pref1")
	require.Error(t, err)
	service.Close()
}

// TestRedisService_UpdateUserState_TransitionMode_HashidxError tests UpdateUserState when hashidx fails in transition mode
func TestRedisService_UpdateUserState_TransitionMode_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service with transition mode
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}

	// Close Redis to cause update to fail
	mr.Close()

	// Update should return error when hashidx fails
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash idx")
	service.Close()
}

// TestRedisService_ListUserStates_EmptyFallback tests ListUserStates returns empty when both storages are empty
func TestRedisService_ListUserStates_EmptyFallback(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with legacy mode
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeLegacy),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "nonexistent-user"}

	// List should return empty when user has no states
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, states)
}
