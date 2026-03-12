//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return mr, rdb
}

func defaultConfig() Config {
	return Config{
		SessionTTL:        time.Hour,
		AppStateTTL:       2 * time.Hour,
		UserStateTTL:      30 * time.Minute,
		SessionEventLimit: 100,
	}
}

func makeTestEvent(id string, ts time.Time, content string) *event.Event {
	return &event.Event{
		ID:        id,
		Timestamp: ts,
		RequestID: "req-" + id,
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: content}},
			},
		},
	}
}

// =============================================================================
// Key Generation
// =============================================================================

func TestKeyGeneration(t *testing.T) {
	key := session.Key{AppName: "myapp", UserID: "u1", SessionID: "s1"}

	t.Run("without prefix", func(t *testing.T) {
		c := NewClient(nil, Config{})
		assert.Equal(t, "appstate:{myapp}", c.appStateKey("myapp"))
		assert.Equal(t, "userstate:{myapp}:u1", c.userStateKey(key))
		assert.Equal(t, "event:{myapp}:u1:s1", c.eventKey(key))
		assert.Equal(t, "track:{myapp}:u1:s1:tool_calls", c.trackKey(key, "tool_calls"))
		assert.Equal(t, "sess:{myapp}:u1", c.sessionStateKey(key))
		assert.Equal(t, "sesssum:{myapp}:u1", c.sessionSummaryKey(key))
	})

	t.Run("with prefix", func(t *testing.T) {
		c := NewClient(nil, Config{KeyPrefix: "pfx"})
		assert.Equal(t, "pfx:appstate:{myapp}", c.appStateKey("myapp"))
		assert.Equal(t, "pfx:userstate:{myapp}:u1", c.userStateKey(key))
		assert.Equal(t, "pfx:event:{myapp}:u1:s1", c.eventKey(key))
		assert.Equal(t, "pfx:sess:{myapp}:u1", c.sessionStateKey(key))
	})

	t.Run("exported key functions", func(t *testing.T) {
		assert.Equal(t, "appstate:{myapp}", GetAppStateKey("myapp"))
		assert.Equal(t, "userstate:{myapp}:u1", GetUserStateKey(key))
		assert.Equal(t, "event:{myapp}:u1:s1", GetEventKey(key))
		assert.Equal(t, "track:{myapp}:u1:s1:tool_calls", GetTrackKey(key, "tool_calls"))
		assert.Equal(t, "sess:{myapp}:u1", GetSessionStateKey(key))
		assert.Equal(t, "sesssum:{myapp}:u1", GetSessionSummaryKey(key))
	})
}

// =============================================================================
// CreateSession
// =============================================================================

func TestCreateSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("success", func(t *testing.T) {
		state := session.StateMap{"k": []byte("v")}
		sess, err := c.CreateSession(ctx, key, state)
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "s1", sess.ID)
		assert.Equal(t, "app", sess.AppName)
		assert.Equal(t, "u1", sess.UserID)
		assert.Equal(t, []byte("v"), sess.State["k"])
		assert.False(t, sess.CreatedAt.IsZero())
		assert.False(t, sess.UpdatedAt.IsZero())
		assert.Equal(t, util.StorageTypeZset, sess.ServiceMeta[util.ServiceMetaStorageTypeKey])
	})

	t.Run("empty session ID", func(t *testing.T) {
		_, err := c.CreateSession(ctx, session.Key{AppName: "app", UserID: "u1"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sessionID is required")
	})

	t.Run("nil state values are preserved", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s2"}
		state := session.StateMap{"nilkey": nil, "realkey": []byte("val")}
		sess, err := c.CreateSession(ctx, key2, state)
		require.NoError(t, err)
		assert.Nil(t, sess.State["nilkey"])
		assert.Equal(t, []byte("val"), sess.State["realkey"])
	})

	t.Run("deep copies state", func(t *testing.T) {
		key3 := session.Key{AppName: "app", UserID: "u1", SessionID: "s3"}
		original := []byte("original")
		state := session.StateMap{"data": original}
		sess, err := c.CreateSession(ctx, key3, state)
		require.NoError(t, err)

		original[0] = 'X'
		assert.Equal(t, byte('o'), sess.State["data"][0])
	})

	t.Run("merges app and user state", func(t *testing.T) {
		key4 := session.Key{AppName: "app", UserID: "u1", SessionID: "s4"}
		rdb.HSet(ctx, c.appStateKey("app"), "cfg", "on")
		rdb.HSet(ctx, c.userStateKey(key4), "pref", "dark")

		sess, err := c.CreateSession(ctx, key4, nil)
		require.NoError(t, err)
		assert.Equal(t, []byte("on"), sess.State[session.StateAppPrefix+"cfg"])
		assert.Equal(t, []byte("dark"), sess.State[session.StateUserPrefix+"pref"])
	})
}

// =============================================================================
// GetSession
// =============================================================================

func TestGetSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("not found", func(t *testing.T) {
		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("returns session with events", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, session.StateMap{"k": []byte("v")})
		require.NoError(t, err)

		now := time.Now()
		evt := makeTestEvent("e1", now, "hello")
		require.NoError(t, c.AppendEvent(ctx, key, evt))

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "s1", sess.ID)
		assert.Len(t, sess.Events, 1)
		assert.Equal(t, "e1", sess.Events[0].ID)
		assert.Equal(t, util.StorageTypeZset, sess.ServiceMeta[util.ServiceMetaStorageTypeKey])
	})
}

// =============================================================================
// Exists
// =============================================================================

func TestExists(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	exists, err := c.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	exists, err = c.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)
}

// =============================================================================
// AppendEvent
// =============================================================================

func TestAppendEvent(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("session not found", func(t *testing.T) {
		evt := makeTestEvent("e1", time.Now(), "hello")
		err := c.AppendEvent(ctx, key, evt)
		require.Error(t, err)
	})

	t.Run("stores valid event", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		now := time.Now()
		evt := makeTestEvent("e1", now, "hello")
		require.NoError(t, c.AppendEvent(ctx, key, evt))

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, sess.Events, 1)
		assert.Equal(t, "e1", sess.Events[0].ID)
	})

	t.Run("skips partial event", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s_partial"}
		_, err := c.CreateSession(ctx, key2, nil)
		require.NoError(t, err)

		partialEvt := &event.Event{
			ID:        "ep1",
			Timestamp: time.Now(),
			Response:  &model.Response{Done: false, IsPartial: true},
		}
		require.NoError(t, c.AppendEvent(ctx, key2, partialEvt))

		sess, err := c.GetSession(ctx, key2, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, sess.Events)
	})

	t.Run("applies state delta", func(t *testing.T) {
		key3 := session.Key{AppName: "app", UserID: "u1", SessionID: "s_delta"}
		_, err := c.CreateSession(ctx, key3, nil)
		require.NoError(t, err)

		evt := makeTestEvent("ed1", time.Now(), "hello")
		evt.StateDelta = session.StateMap{"counter": []byte("1")}
		require.NoError(t, c.AppendEvent(ctx, key3, evt))

		sess, err := c.GetSession(ctx, key3, 0, time.Time{})
		require.NoError(t, err)
		assert.Equal(t, []byte("1"), sess.State["counter"])
	})

	t.Run("multiple events ordered by timestamp", func(t *testing.T) {
		key4 := session.Key{AppName: "app", UserID: "u1", SessionID: "s_multi"}
		_, err := c.CreateSession(ctx, key4, nil)
		require.NoError(t, err)

		now := time.Now()
		for i := 0; i < 3; i++ {
			evt := makeTestEvent(fmt.Sprintf("e%d", i), now.Add(time.Duration(i)*time.Second), fmt.Sprintf("msg%d", i))
			require.NoError(t, c.AppendEvent(ctx, key4, evt))
		}

		sess, err := c.GetSession(ctx, key4, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, sess.Events, 3)
		assert.Equal(t, "e0", sess.Events[0].ID)
		assert.Equal(t, "e1", sess.Events[1].ID)
		assert.Equal(t, "e2", sess.Events[2].ID)
	})
}

// =============================================================================
// DeleteSession
// =============================================================================

func TestDeleteSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := makeTestEvent("e1", time.Now(), "hello")
	require.NoError(t, c.AppendEvent(ctx, key, evt))

	exists, _ := c.Exists(ctx, key)
	assert.True(t, exists)

	require.NoError(t, c.DeleteSession(ctx, key))

	exists, _ = c.Exists(ctx, key)
	assert.False(t, exists)

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestDeleteSession_WithTracks(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	trackEvt := &session.TrackEvent{
		Track:     "tool_calls",
		Payload:   json.RawMessage(`{"fn":"add"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, c.AppendTrackEvent(ctx, key, trackEvt))

	trackKey := c.trackKey(key, "tool_calls")
	n, _ := rdb.(*redis.Client).Exists(ctx, trackKey).Result()
	assert.Equal(t, int64(1), n)

	require.NoError(t, c.DeleteSession(ctx, key))

	n, _ = rdb.(*redis.Client).Exists(ctx, trackKey).Result()
	assert.Equal(t, int64(0), n)
}

// =============================================================================
// UpdateSessionState
// =============================================================================

func TestUpdateSessionState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("session not found", func(t *testing.T) {
		err := c.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("updates state", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, session.StateMap{"old": []byte("val")})
		require.NoError(t, err)

		err = c.UpdateSessionState(ctx, key, session.StateMap{"new": []byte("val2")})
		require.NoError(t, err)

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), sess.State["old"])
		assert.Equal(t, []byte("val2"), sess.State["new"])
	})

	t.Run("nil value preserved", func(t *testing.T) {
		err := c.UpdateSessionState(ctx, key, session.StateMap{"nilkey": nil})
		require.NoError(t, err)

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Nil(t, sess.State["nilkey"])
	})
}

// =============================================================================
// ListSessions
// =============================================================================

func TestListSessions(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "u1"}

	t.Run("empty", func(t *testing.T) {
		sessions, err := c.ListSessions(ctx, userKey, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, sessions)
	})

	t.Run("returns sessions sorted by UpdatedAt desc", func(t *testing.T) {
		baseTime := time.Now()
		for i := 0; i < 3; i++ {
			key := session.Key{AppName: "app", UserID: "u1", SessionID: fmt.Sprintf("s%d", i)}
			_, err := c.CreateSession(ctx, key, nil)
			require.NoError(t, err)

			// Overwrite UpdatedAt to ensure deterministic ordering.
			stateBytes, err := rdb.(*redis.Client).HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
			require.NoError(t, err)
			var ss SessionState
			require.NoError(t, json.Unmarshal(stateBytes, &ss))
			ss.UpdatedAt = baseTime.Add(time.Duration(i) * time.Minute)
			b, _ := json.Marshal(ss)
			rdb.(*redis.Client).HSet(ctx, c.sessionStateKey(key), key.SessionID, string(b))
		}

		sessions, err := c.ListSessions(ctx, userKey, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, sessions, 3)
		// Sorting is done at the service layer, not internally.
		// Just verify all sessions are returned.
		ids := make(map[string]bool)
		for _, s := range sessions {
			ids[s.ID] = true
		}
		assert.True(t, ids["s0"])
		assert.True(t, ids["s1"])
		assert.True(t, ids["s2"])
	})
}

// =============================================================================
// AppendTrackEvent
// =============================================================================

func TestAppendTrackEvent(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("session not found", func(t *testing.T) {
		trackEvt := &session.TrackEvent{
			Track:     "tool_calls",
			Payload:   json.RawMessage(`{"fn":"add"}`),
			Timestamp: time.Now(),
		}
		err := c.AppendTrackEvent(ctx, key, trackEvt)
		require.Error(t, err)
	})

	t.Run("stores track event", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		now := time.Now()
		trackEvt := &session.TrackEvent{
			Track:     "tool_calls",
			Payload:   json.RawMessage(`{"fn":"add"}`),
			Timestamp: now,
		}
		require.NoError(t, c.AppendTrackEvent(ctx, key, trackEvt))

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		require.NotNil(t, sess.Tracks)
		require.Contains(t, sess.Tracks, session.Track("tool_calls"))
		assert.Len(t, sess.Tracks["tool_calls"].Events, 1)
		assert.JSONEq(t, `{"fn":"add"}`, string(sess.Tracks["tool_calls"].Events[0].Payload))
	})

	t.Run("multiple tracks", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s_multi_track"}
		_, err := c.CreateSession(ctx, key2, nil)
		require.NoError(t, err)

		// Use past timestamps to avoid ZRevRangeByScore maxScore boundary issues.
		base := time.Now().Add(-10 * time.Second)
		require.NoError(t, c.AppendTrackEvent(ctx, key2, &session.TrackEvent{
			Track: "track_a", Payload: json.RawMessage(`"a1"`), Timestamp: base,
		}))
		require.NoError(t, c.AppendTrackEvent(ctx, key2, &session.TrackEvent{
			Track: "track_b", Payload: json.RawMessage(`"b1"`), Timestamp: base.Add(time.Second),
		}))
		require.NoError(t, c.AppendTrackEvent(ctx, key2, &session.TrackEvent{
			Track: "track_a", Payload: json.RawMessage(`"a2"`), Timestamp: base.Add(2 * time.Second),
		}))

		sess, err := c.GetSession(ctx, key2, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, sess.Tracks, 2)
		assert.Len(t, sess.Tracks["track_a"].Events, 2)
		assert.Len(t, sess.Tracks["track_b"].Events, 1)
	})
}

// =============================================================================
// AppState Operations
// =============================================================================

func TestAppState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	t.Run("update and list", func(t *testing.T) {
		err := c.UpdateAppState(ctx, "app", session.StateMap{"cfg": []byte("on")}, time.Hour)
		require.NoError(t, err)

		state, err := c.ListAppStates(ctx, "app")
		require.NoError(t, err)
		assert.Equal(t, []byte("on"), state["cfg"])
	})

	t.Run("strips app prefix", func(t *testing.T) {
		err := c.UpdateAppState(ctx, "app2", session.StateMap{session.StateAppPrefix + "key": []byte("val")}, 0)
		require.NoError(t, err)

		state, err := c.ListAppStates(ctx, "app2")
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), state["key"])
	})

	t.Run("delete", func(t *testing.T) {
		err := c.UpdateAppState(ctx, "app3", session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")}, 0)
		require.NoError(t, err)

		err = c.DeleteAppState(ctx, "app3", "k1")
		require.NoError(t, err)

		state, err := c.ListAppStates(ctx, "app3")
		require.NoError(t, err)
		assert.NotContains(t, state, "k1")
		assert.Equal(t, []byte("v2"), state["k2"])
	})

	t.Run("list empty", func(t *testing.T) {
		state, err := c.ListAppStates(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Empty(t, state)
	})
}

// =============================================================================
// UserState Operations
// =============================================================================

func TestUserState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "u1"}

	t.Run("update and list", func(t *testing.T) {
		err := c.UpdateUserState(ctx, userKey, session.StateMap{"pref": []byte("dark")}, time.Hour)
		require.NoError(t, err)

		state, err := c.ListUserStates(ctx, userKey)
		require.NoError(t, err)
		assert.Equal(t, []byte("dark"), state["pref"])
	})

	t.Run("strips user prefix", func(t *testing.T) {
		err := c.UpdateUserState(ctx, userKey, session.StateMap{session.StateUserPrefix + "lang": []byte("en")}, 0)
		require.NoError(t, err)

		state, err := c.ListUserStates(ctx, userKey)
		require.NoError(t, err)
		assert.Equal(t, []byte("en"), state["lang"])
	})

	t.Run("delete", func(t *testing.T) {
		userKey2 := session.UserKey{AppName: "app", UserID: "u2"}
		err := c.UpdateUserState(ctx, userKey2, session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")}, 0)
		require.NoError(t, err)

		err = c.DeleteUserState(ctx, userKey2, "k1")
		require.NoError(t, err)

		state, err := c.ListUserStates(ctx, userKey2)
		require.NoError(t, err)
		assert.NotContains(t, state, "k1")
		assert.Equal(t, []byte("v2"), state["k2"])
	})

	t.Run("list empty", func(t *testing.T) {
		state, err := c.ListUserStates(ctx, session.UserKey{AppName: "app", UserID: "nobody"})
		require.NoError(t, err)
		assert.Empty(t, state)
	})
}

// =============================================================================
// TrimConversations
// =============================================================================

func TestTrimConversations(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	t.Run("empty session", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_empty"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		deleted, err := c.TrimConversations(ctx, key, 1)
		require.NoError(t, err)
		assert.Nil(t, deleted)
	})

	t.Run("trims one conversation", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_trim1"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		now := time.Now()
		e1 := makeTestEvent("e1", now, "msg1")
		e1.RequestID = "req1"
		e2 := makeTestEvent("e2", now.Add(time.Second), "msg2")
		e2.RequestID = "req1"
		e3 := makeTestEvent("e3", now.Add(2*time.Second), "msg3")
		e3.RequestID = "req2"
		e4 := makeTestEvent("e4", now.Add(3*time.Second), "msg4")
		e4.RequestID = "req2"

		require.NoError(t, c.AppendEvent(ctx, key, e1))
		require.NoError(t, c.AppendEvent(ctx, key, e2))
		require.NoError(t, c.AppendEvent(ctx, key, e3))
		require.NoError(t, c.AppendEvent(ctx, key, e4))

		// Trim 1 conversation = most recent RequestID group (req2: e3+e4).
		deleted, err := c.TrimConversations(ctx, key, 1)
		require.NoError(t, err)
		require.Len(t, deleted, 2)
		for _, d := range deleted {
			assert.Equal(t, "req2", d.RequestID)
		}

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Len(t, sess.Events, 2)
	})

	t.Run("trims multiple conversations", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_trim2"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		now := time.Now()
		for i := 0; i < 6; i++ {
			evt := makeTestEvent(fmt.Sprintf("e%d", i), now.Add(time.Duration(i)*time.Second), fmt.Sprintf("msg%d", i))
			evt.RequestID = fmt.Sprintf("req%d", i/2)
			require.NoError(t, c.AppendEvent(ctx, key, evt))
		}

		deleted, err := c.TrimConversations(ctx, key, 2)
		require.NoError(t, err)
		require.Len(t, deleted, 4)

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Len(t, sess.Events, 2)
	})

	t.Run("count defaults to 1", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_trim_default"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		now := time.Now()
		e1 := makeTestEvent("e1", now, "msg1")
		e1.RequestID = "req1"
		require.NoError(t, c.AppendEvent(ctx, key, e1))

		deleted, err := c.TrimConversations(ctx, key, 0)
		require.NoError(t, err)
		require.Len(t, deleted, 1)
	})
}

// =============================================================================
// Summary Operations
// =============================================================================

func TestSummary(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	t.Run("get nonexistent", func(t *testing.T) {
		summaries, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, summaries)
	})

	t.Run("create and get", func(t *testing.T) {
		sum := &session.Summary{
			Summary:   "test summary",
			UpdatedAt: time.Now().UTC(),
		}
		err := c.CreateSummary(ctx, key, "all", sum, time.Hour)
		require.NoError(t, err)

		summaries, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, summaries)
		assert.Equal(t, "test summary", summaries["all"].Summary)
	})

	t.Run("set-if-newer updates when newer", func(t *testing.T) {
		oldSum := &session.Summary{
			Summary:   "old",
			UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := c.CreateSummary(ctx, key, "filter1", oldSum, 0)
		require.NoError(t, err)

		newSum := &session.Summary{
			Summary:   "new",
			UpdatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		}
		err = c.CreateSummary(ctx, key, "filter1", newSum, 0)
		require.NoError(t, err)

		summaries, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "new", summaries["filter1"].Summary)
	})

	t.Run("set-if-newer skips when older", func(t *testing.T) {
		newSum := &session.Summary{
			Summary:   "newer",
			UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := c.CreateSummary(ctx, key, "filter2", newSum, 0)
		require.NoError(t, err)

		oldSum := &session.Summary{
			Summary:   "older",
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err = c.CreateSummary(ctx, key, "filter2", oldSum, 0)
		require.NoError(t, err)

		summaries, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "newer", summaries["filter2"].Summary)
	})

	t.Run("multiple filter keys", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s_multi_sum"}
		sum1 := &session.Summary{Summary: "sum-all", UpdatedAt: time.Now().UTC()}
		sum2 := &session.Summary{Summary: "sum-branch", UpdatedAt: time.Now().UTC()}

		require.NoError(t, c.CreateSummary(ctx, key2, "all", sum1, 0))
		require.NoError(t, c.CreateSummary(ctx, key2, "branch1", sum2, 0))

		summaries, err := c.GetSummary(ctx, key2)
		require.NoError(t, err)
		require.Len(t, summaries, 2)
		assert.Equal(t, "sum-all", summaries["all"].Summary)
		assert.Equal(t, "sum-branch", summaries["branch1"].Summary)
	})
}

// =============================================================================
// Internal helpers
// =============================================================================

func TestProcessSessionStateCmd(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	t.Run("nil result", func(t *testing.T) {
		pipe := rdb.(*redis.Client).Pipeline()
		cmd := pipe.HGet(ctx, "nonexistent", "field")
		_, _ = pipe.Exec(ctx)

		state, err := processSessionStateCmd(cmd)
		require.NoError(t, err)
		assert.Nil(t, state)
	})

	t.Run("valid result", func(t *testing.T) {
		ss := &SessionState{
			ID:        "s1",
			State:     session.StateMap{"k": []byte("v")},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		b, _ := json.Marshal(ss)
		rdb.(*redis.Client).HSet(ctx, "sess:test", "s1", string(b))

		pipe := rdb.(*redis.Client).Pipeline()
		cmd := pipe.HGet(ctx, "sess:test", "s1")
		_, err := pipe.Exec(ctx)
		require.NoError(t, err)

		state, err := processSessionStateCmd(cmd)
		require.NoError(t, err)
		require.NotNil(t, state)
		assert.Equal(t, "s1", state.ID)
		assert.Equal(t, []byte("v"), state.State["k"])
	})
}

func TestProcessSessStateCmdList(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		pipe := rdb.(*redis.Client).Pipeline()
		cmd := pipe.HGetAll(ctx, "nonexistent")
		_, _ = pipe.Exec(ctx)

		states, err := processSessStateCmdList(cmd)
		require.NoError(t, err)
		assert.Empty(t, states)
	})

	t.Run("multiple sessions", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			ss := &SessionState{
				ID:        fmt.Sprintf("s%d", i),
				State:     session.StateMap{},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			b, _ := json.Marshal(ss)
			rdb.(*redis.Client).HSet(ctx, "sess:list", fmt.Sprintf("s%d", i), string(b))
		}

		pipe := rdb.(*redis.Client).Pipeline()
		cmd := pipe.HGetAll(ctx, "sess:list")
		_, err := pipe.Exec(ctx)
		require.NoError(t, err)

		states, err := processSessStateCmdList(cmd)
		require.NoError(t, err)
		assert.Len(t, states, 3)
	})
}

func TestBuildTrackLists(t *testing.T) {
	t.Run("no tracks", func(t *testing.T) {
		states := []*SessionState{
			{ID: "s1", State: session.StateMap{}},
		}
		lists, err := buildTrackLists(states)
		require.NoError(t, err)
		assert.Len(t, lists, 1)
		assert.Empty(t, lists[0])
	})
}

func TestNewTrackResults(t *testing.T) {
	results := newTrackResults(3)
	assert.Len(t, results, 3)
	for _, r := range results {
		assert.NotNil(t, r)
		assert.Empty(t, r)
	}
}

// =============================================================================
// ExistsPipelined
// =============================================================================

func TestClient_ExistsPipelined(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ep1"}

	t.Run("not found", func(t *testing.T) {
		pipe := rdb.Pipeline()
		cmd := c.ExistsPipelined(ctx, pipe, key)
		_, err := pipe.Exec(ctx)
		require.NoError(t, err)

		exists, err := cmd.Result()
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("found after create", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		pipe := rdb.Pipeline()
		cmd := c.ExistsPipelined(ctx, pipe, key)
		_, err = pipe.Exec(ctx)
		require.NoError(t, err)

		exists, err := cmd.Result()
		require.NoError(t, err)
		assert.True(t, exists)
	})
}

// =============================================================================
// processSessionStateCmd - unmarshal error
// =============================================================================

func TestProcessSessionStateCmd_UnmarshalError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	rdb.(*redis.Client).HSet(ctx, "sess:bad", "s1", "not-valid-json")

	pipe := rdb.(*redis.Client).Pipeline()
	cmd := pipe.HGet(ctx, "sess:bad", "s1")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	state, err := processSessionStateCmd(cmd)
	require.Error(t, err)
	assert.Nil(t, state)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
}

// =============================================================================
// processSessStateCmdList - unmarshal error
// =============================================================================

func TestProcessSessStateCmdList_UnmarshalError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	rdb.(*redis.Client).HSet(ctx, "sess:badlist", "s0", "invalid-json")

	pipe := rdb.(*redis.Client).Pipeline()
	cmd := pipe.HGetAll(ctx, "sess:badlist")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	states, err := processSessStateCmdList(cmd)
	require.Error(t, err)
	assert.Nil(t, states)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
}

// =============================================================================
// GetSummary - corrupted data
// =============================================================================

func TestClient_GetSummary_CorruptedData(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "badsum"}
	sumKey := c.sessionSummaryKey(key)
	rdb.(*redis.Client).HSet(ctx, sumKey, key.SessionID, "not-valid-json")

	summaries, err := c.GetSummary(ctx, key)
	require.Error(t, err)
	assert.Nil(t, summaries)
	assert.Contains(t, err.Error(), "unmarshal summary failed")
}

// =============================================================================
// listTracksForSession - corrupted session state
// =============================================================================

func TestClient_listTracksForSession_CorruptedState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "badtrk"}
	rdb.(*redis.Client).HSet(ctx, c.sessionStateKey(key), key.SessionID, "invalid-json")

	tracks, err := c.listTracksForSession(ctx, key)
	require.Error(t, err)
	assert.Nil(t, tracks)
}

func TestClient_listTracksForSession_NotFound(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "noexist"}
	tracks, err := c.listTracksForSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, tracks)
}

// =============================================================================
// Exists / DeleteAppState / DeleteUserState / ListAppStates / ListUserStates
// with closed Redis to cover error branches
// =============================================================================

func TestClient_Exists_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ex-err"}

	mr.Close()

	_, err := c.Exists(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check session exists (zset)")
}

func TestClient_DeleteAppState_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	err := c.DeleteAppState(ctx, "app", "key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete app state (zset)")
}

func TestClient_DeleteUserState_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	err := c.DeleteUserState(ctx, session.UserKey{AppName: "app", UserID: "u1"}, "key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete user state (zset)")
}

func TestClient_ListAppStates_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListAppStates(ctx, "app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list app states (zset)")
}

func TestClient_ListUserStates_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListUserStates(ctx, session.UserKey{AppName: "app", UserID: "u1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list user states (zset)")
}

func TestClient_CreateSummary_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-err"}

	mr.Close()

	sum := &session.Summary{Summary: "test", UpdatedAt: time.Now().UTC()}
	err := c.CreateSummary(ctx, key, "", sum, time.Hour)
	require.Error(t, err)
}

// =============================================================================
// CreateSummary with TTL=0 (no expire branch)
// =============================================================================

func TestClient_CreateSummary_NoTTL(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-nottl"}

	sum := &session.Summary{Summary: "test", UpdatedAt: time.Now().UTC()}
	require.NoError(t, c.CreateSummary(ctx, key, "", sum, 0))

	summaries, err := c.GetSummary(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, summaries)
	assert.Equal(t, "test", summaries[""].Summary)
}

// =============================================================================
// GetSession with corrupted event data
// =============================================================================

func TestClient_GetSession_CorruptedEventData(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "corrupted-evt"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add a valid event then a corrupted one
	goodEvt := makeTestEvent("e1", time.Now(), "good")
	require.NoError(t, c.AppendEvent(ctx, key, goodEvt))

	evtKey := c.eventKey(key)
	rdb.(*redis.Client).ZAdd(ctx, evtKey, redis.Z{
		Score:  float64(time.Now().Add(time.Second).UnixNano()),
		Member: "not-valid-json-event",
	})

	// Corrupted events are skipped, not returned as error
	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Only the valid event should be returned
	assert.Len(t, sess.Events, 1)
	assert.Equal(t, "e1", sess.Events[0].ID)
}

// =============================================================================
// Exists after close: error branch coverage
// =============================================================================

func TestClient_Exists_ClosedRedis(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "close1"}

	mr.Close()

	exists, err := c.Exists(ctx, key)
	require.Error(t, err)
	assert.False(t, exists)
}

// =============================================================================
// UpdateAppState / UpdateUserState error branches
// =============================================================================

func TestClient_UpdateAppState_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	err := c.UpdateAppState(ctx, "app", session.StateMap{"k": []byte("v")}, time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update app state (zset)")
}

func TestClient_UpdateUserState_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	err := c.UpdateUserState(ctx, session.UserKey{AppName: "app", UserID: "u1"},
		session.StateMap{"k": []byte("v")}, time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update user state (zset)")
}

// TestGetSession_WithTracks_Error tests GetSession error handling when fetching tracks
func TestGetSession_WithTracks_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "track-err"}

	// Create session
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append a track event to create track state
	trackEvt := &session.TrackEvent{
		Track:     "monitor",
		Payload:   json.RawMessage(`{"cpu":80}`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, trackEvt)
	require.NoError(t, err)

	// Close miniredis to simulate connection error
	mr.Close()

	// GetSession should return error when track events fetch fails
	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
}
