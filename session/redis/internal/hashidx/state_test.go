//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestClient_UpdateSessionState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "uss1"}

	t.Run("session not found", func(t *testing.T) {
		err := c.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("merges state", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, session.StateMap{"existing": []byte("val")})
		require.NoError(t, err)

		err = c.UpdateSessionState(ctx, key, session.StateMap{"new": []byte("added")})
		require.NoError(t, err)

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), sess.State["existing"])
		assert.Equal(t, []byte("added"), sess.State["new"])
	})

	t.Run("nil value in state", func(t *testing.T) {
		err := c.UpdateSessionState(ctx, key, session.StateMap{"nilKey": nil})
		require.NoError(t, err)

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		_, exists := sess.State["nilKey"]
		assert.True(t, exists)
	})
}

func TestClient_UpdateAppState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	state := session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")}
	require.NoError(t, c.UpdateAppState(ctx, "myapp", state, time.Hour))

	result, err := c.ListAppStates(ctx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), result["k1"])
	assert.Equal(t, []byte("v2"), result["k2"])
}

func TestClient_DeleteAppState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	require.NoError(t, c.UpdateAppState(ctx, "myapp", session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")}, 0))
	require.NoError(t, c.DeleteAppState(ctx, "myapp", "k1"))

	result, err := c.ListAppStates(ctx, "myapp")
	require.NoError(t, err)
	assert.Nil(t, result["k1"])
	assert.Equal(t, []byte("v2"), result["k2"])
}

func TestClient_ListAppStates_Empty(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	result, err := c.ListAppStates(ctx, "nonexistent")
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestClient_UpdateUserState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	userKey := session.UserKey{AppName: "myapp", UserID: "u1"}
	state := session.StateMap{"pref": []byte("dark")}
	require.NoError(t, c.UpdateUserState(ctx, userKey, state, time.Hour))

	result, err := c.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("dark"), result["pref"])
}

func TestClient_DeleteUserState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	userKey := session.UserKey{AppName: "myapp", UserID: "u1"}
	require.NoError(t, c.UpdateUserState(ctx, userKey, session.StateMap{"p1": []byte("v1"), "p2": []byte("v2")}, 0))
	require.NoError(t, c.DeleteUserState(ctx, userKey, "p1"))

	result, err := c.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Nil(t, result["p1"])
	assert.Equal(t, []byte("v2"), result["p2"])
}

func TestClient_ListUserStates_Empty(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	result, err := c.ListUserStates(ctx, session.UserKey{AppName: "x", UserID: "y"})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestClient_ListAppStates_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListAppStates(ctx, "myapp")
	require.Error(t, err)
}

func TestClient_ListUserStates_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListUserStates(ctx, session.UserKey{AppName: "x", UserID: "y"})
	require.Error(t, err)
}

func TestClient_RefreshAppStateTTL(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{AppStateTTL: 5 * time.Second})
	ctx := context.Background()

	require.NoError(t, c.UpdateAppState(ctx, "myapp", session.StateMap{"k": []byte("v")}, 0))
	require.NoError(t, c.RefreshAppStateTTL(ctx, "myapp"))

	ttl := mr.TTL(c.keys.AppStateKey("myapp"))
	assert.Greater(t, ttl, time.Duration(0))
}

func TestClient_RefreshAppStateTTL_NoTTL(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{AppStateTTL: 0})
	ctx := context.Background()

	require.NoError(t, c.RefreshAppStateTTL(ctx, "myapp"))
}

func TestClient_RefreshUserStateTTL(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{UserStateTTL: 5 * time.Second})
	ctx := context.Background()

	userKey := session.UserKey{AppName: "myapp", UserID: "u1"}
	require.NoError(t, c.UpdateUserState(ctx, userKey, session.StateMap{"k": []byte("v")}, 0))
	require.NoError(t, c.RefreshUserStateTTL(ctx, userKey))

	ttl := mr.TTL(c.keys.UserStateKey("myapp", "u1"))
	assert.Greater(t, ttl, time.Duration(0))
}

func TestClient_RefreshUserStateTTL_NoTTL(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{UserStateTTL: 0})
	ctx := context.Background()

	require.NoError(t, c.RefreshUserStateTTL(ctx, session.UserKey{AppName: "a", UserID: "u"}))
}

func TestClient_ExistsPipelined(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ep1"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	pipe := rdb.Pipeline()
	cmd := c.ExistsPipelined(ctx, pipe, key)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cmd.Val())
}

func TestClient_UpdateSessionState_ConnectionError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "uss-err"}

	// Create session first
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Close miniredis to simulate connection error
	mr.Close()

	// UpdateSessionState should return error when Redis is unavailable
	err = c.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}

func TestClient_UpdateSessionState_UnmarshalError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "uss-unmarshal"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Overwrite the meta key with invalid JSON
	err = rdb.Set(ctx, c.keys.SessionMetaKey(key), "not valid json", 0).Err()
	require.NoError(t, err)

	err = c.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session meta")
}

func TestClient_UpdateSessionState_NilState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "uss-nilstate"}

	// Create session with no state (State field will be empty but not nil after deepCopyState)
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Manually fetch and overwrite meta with State=null to trigger nil State branch
	metaJSON := `{"id":"uss-nilstate","appName":"app","userID":"u1","state":null,"createdAt":"2025-01-01T00:00:00Z","updatedAt":"2025-01-01T00:00:00Z"}`
	err = rdb.Set(ctx, c.keys.SessionMetaKey(key), metaJSON, 0).Err()
	require.NoError(t, err)

	// UpdateSessionState should initialize nil State to empty map and merge
	err = c.UpdateSessionState(ctx, key, session.StateMap{"newkey": []byte("newval")})
	require.NoError(t, err)

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []byte("newval"), sess.State["newkey"])
}
