//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDeleteAppState(t *testing.T) {
	service := NewSessionService()
	defer service.Close()
	ctx := context.Background()
	appName := "test-app"

	// Create app state first.
	state := session.StateMap{"key1": []byte("value1"), "key2": []byte("value2")}
	err := service.UpdateAppState(ctx, appName, state)
	require.NoError(t, err)

	// Verify state exists.
	appState, err := service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), appState["key1"])
	assert.Equal(t, []byte("value2"), appState["key2"])

	// Delete one key.
	err = service.DeleteAppState(ctx, appName, "key1")
	require.NoError(t, err)

	// Verify key1 is deleted, key2 still exists.
	appState, err = service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Nil(t, appState["key1"])
	assert.Equal(t, []byte("value2"), appState["key2"])

	// Delete app state prefix.
	err = service.UpdateAppState(ctx, appName, session.StateMap{session.StateAppPrefix + "key3": []byte("value3")})
	require.NoError(t, err)

	err = service.DeleteAppState(ctx, appName, session.StateAppPrefix+"key3")
	require.NoError(t, err)

	appState, err = service.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Nil(t, appState["key3"])

	// Test delete from non-existent app.
	err = service.DeleteAppState(ctx, "non-existent-app", "key1")
	require.NoError(t, err)

	// Test error case: empty app name.
	err = service.DeleteAppState(ctx, "", "key1")
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestDeleteUserState(t *testing.T) {
	service := NewSessionService()
	defer service.Close()
	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Create user state first.
	state := session.StateMap{"pref1": []byte("dark"), "pref2": []byte("zh-CN")}
	err := service.UpdateUserState(ctx, userKey, state)
	require.NoError(t, err)

	// Verify state exists.
	userState, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("dark"), userState["pref1"])
	assert.Equal(t, []byte("zh-CN"), userState["pref2"])

	// Delete one key.
	err = service.DeleteUserState(ctx, userKey, "pref1")
	require.NoError(t, err)

	// Verify pref1 is deleted, pref2 still exists.
	userState, err = service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Nil(t, userState["pref1"])
	assert.Equal(t, []byte("zh-CN"), userState["pref2"])

	// Test user state prefix is allowed (will be trimmed).
	err = service.UpdateUserState(ctx, userKey, session.StateMap{session.StateUserPrefix + "pref3": []byte("value3")})
	require.NoError(t, err)

	userState, err = service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value3"), userState["pref3"])

	// Clean up pref3.
	err = service.DeleteUserState(ctx, userKey, "pref3")
	require.NoError(t, err)

	// Delete the last key, should clean up user state.
	err = service.DeleteUserState(ctx, userKey, "pref2")
	require.NoError(t, err)

	userState, err = service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, userState)

	// Test delete from non-existent user.
	err = service.DeleteUserState(ctx, session.UserKey{AppName: "non-existent-app", UserID: "non-existent-user"}, "pref1")
	require.NoError(t, err)

	// Test error cases.
	err = service.DeleteUserState(ctx, session.UserKey{AppName: "", UserID: "user"}, "key")
	require.Error(t, err)

	err = service.DeleteUserState(ctx, session.UserKey{AppName: "app", UserID: ""}, "key")
	require.Error(t, err)
}

func TestUpdateUserState_ErrorCases(t *testing.T) {
	service := NewSessionService()
	defer service.Close()
	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Test app prefix not allowed.
	err := service.UpdateUserState(ctx, userKey, session.StateMap{session.StateAppPrefix + "key": []byte("value")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not allowed")

	// Test temp prefix not allowed.
	err = service.UpdateUserState(ctx, userKey, session.StateMap{session.StateTempPrefix + "key": []byte("value")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not allowed")
}
