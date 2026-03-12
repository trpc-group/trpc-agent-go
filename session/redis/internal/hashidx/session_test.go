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

func makeTestEvent(id string, ts time.Time) *event.Event {
	return &event.Event{
		ID:        id,
		Timestamp: ts,
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: "msg-" + id}},
			},
		},
	}
}

func TestClient_CreateSession(t *testing.T) {
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
	})

	t.Run("empty session ID", func(t *testing.T) {
		_, err := c.CreateSession(ctx, session.Key{AppName: "app", UserID: "u1"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sessionID is required")
	})

	t.Run("duplicate session", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("nil state deep copied as empty map", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s2"}
		sess, err := c.CreateSession(ctx, key2, nil)
		require.NoError(t, err)
		assert.NotNil(t, sess.State)
		assert.Empty(t, sess.State)
	})

	t.Run("state is deep copied", func(t *testing.T) {
		key3 := session.Key{AppName: "app", UserID: "u1", SessionID: "s3"}
		original := session.StateMap{"key": []byte("original")}
		sess, err := c.CreateSession(ctx, key3, original)
		require.NoError(t, err)

		// Mutate original
		original["key"][0] = 'X'
		assert.Equal(t, []byte("original"), sess.State["key"])
	})
}

func TestClient_GetSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "get1"}

	t.Run("not found returns nil", func(t *testing.T) {
		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("returns session with events", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, session.StateMap{"x": []byte("y")})
		require.NoError(t, err)

		baseTime := time.Now()
		for i := 0; i < 3; i++ {
			evt := makeTestEvent(fmt.Sprintf("e%d", i), baseTime.Add(time.Duration(i)*time.Second))
			require.NoError(t, c.AppendEvent(ctx, key, evt))
		}

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "get1", sess.ID)
		assert.Equal(t, []byte("y"), sess.State["x"])
		assert.Len(t, sess.Events, 3)
		assert.Equal(t, "e0", sess.Events[0].ID)
		assert.Equal(t, "e2", sess.Events[2].ID)
	})
}

func TestClient_AppendEvent(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ae1"}

	t.Run("session not found", func(t *testing.T) {
		evt := makeTestEvent("e1", time.Now())
		err := c.AppendEvent(ctx, key, evt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("stores valid event", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := makeTestEvent("e1", time.Now())
		require.NoError(t, c.AppendEvent(ctx, key, evt))

		sess, err := c.GetSession(ctx, key, 0, time.Time{})
		require.NoError(t, err)
		assert.Len(t, sess.Events, 1)
		assert.Equal(t, "e1", sess.Events[0].ID)
	})

	t.Run("skips partial event storage but applies state delta", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "ae2"}
		_, err := c.CreateSession(ctx, key2, nil)
		require.NoError(t, err)

		partialEvt := &event.Event{
			ID:        "pe1",
			Timestamp: time.Now(),
			Response: &model.Response{
				IsPartial: true,
				Choices:   []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}},
			},
			StateDelta: session.StateMap{
				"delta_key": []byte("delta_val"),
			},
		}
		require.NoError(t, c.AppendEvent(ctx, key2, partialEvt))

		sess, err := c.GetSession(ctx, key2, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, sess.Events)
		assert.Equal(t, []byte("delta_val"), sess.State["delta_key"])
	})

	t.Run("event without response not stored", func(t *testing.T) {
		key3 := session.Key{AppName: "app", UserID: "u1", SessionID: "ae3"}
		_, err := c.CreateSession(ctx, key3, nil)
		require.NoError(t, err)

		noRespEvt := &event.Event{ID: "nr1", Timestamp: time.Now()}
		require.NoError(t, c.AppendEvent(ctx, key3, noRespEvt))

		sess, err := c.GetSession(ctx, key3, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, sess.Events)
	})

	t.Run("chronological order maintained", func(t *testing.T) {
		key4 := session.Key{AppName: "app", UserID: "u1", SessionID: "ae4"}
		_, err := c.CreateSession(ctx, key4, nil)
		require.NoError(t, err)

		baseTime := time.Now()
		// Insert out of order
		require.NoError(t, c.AppendEvent(ctx, key4, makeTestEvent("newest", baseTime.Add(2*time.Second))))
		require.NoError(t, c.AppendEvent(ctx, key4, makeTestEvent("oldest", baseTime)))
		require.NoError(t, c.AppendEvent(ctx, key4, makeTestEvent("middle", baseTime.Add(1*time.Second))))

		sess, err := c.GetSession(ctx, key4, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, sess.Events, 3)
		assert.Equal(t, "oldest", sess.Events[0].ID)
		assert.Equal(t, "middle", sess.Events[1].ID)
		assert.Equal(t, "newest", sess.Events[2].ID)
	})
}

func TestClient_DeleteSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "del1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent("e1", time.Now())))

	require.NoError(t, c.DeleteSession(ctx, key))

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestClient_DeleteSession_WithTracks(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "del-t1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload1"`),
		Timestamp: time.Now(),
	}
	tracksJSON, _ := json.Marshal([]string{"alpha"})
	require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

	// Verify track keys exist
	trkDataKey := c.keys.TrackDataKey(key, "alpha")
	trkIdxKey := c.keys.TrackTimeIndexKey(key, "alpha")
	n, err := rdb.Exists(ctx, trkDataKey, trkIdxKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	require.NoError(t, c.DeleteSession(ctx, key))

	// All keys should be gone
	n, err = rdb.Exists(ctx, trkDataKey, trkIdxKey, c.keys.SessionMetaKey(key)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestClient_DeleteEvent(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "dele1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Now()
	require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent("e1", baseTime)))
	require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent("e2", baseTime.Add(time.Second))))

	require.NoError(t, c.DeleteEvent(ctx, key, "e1"))

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, sess.Events, 1)
	assert.Equal(t, "e2", sess.Events[0].ID)
}

func TestClient_TrimConversations(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trim1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Now()
	events := []*event.Event{
		{ID: "e1", RequestID: "req1", Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "q1"}}}}},
		{ID: "e2", RequestID: "req1", Timestamp: baseTime.Add(-2 * time.Hour),
			Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "a1"}}}}},
		{ID: "e3", RequestID: "req2", Timestamp: baseTime.Add(-1 * time.Hour),
			Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "q2"}}}}},
	}
	for _, evt := range events {
		require.NoError(t, c.AppendEvent(ctx, key, evt))
	}

	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	assert.Equal(t, "req2", deleted[0].RequestID)

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, "e1", sess.Events[0].ID)
	assert.Equal(t, "e2", sess.Events[1].ID)
}

func TestClient_TrimConversations_ZeroDefaultsToOne(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trimz"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, c.AppendEvent(ctx, key, &event.Event{
		ID: "e1", RequestID: "r1", Timestamp: time.Now().Add(-time.Hour),
		Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "x"}}}},
	}))
	require.NoError(t, c.AppendEvent(ctx, key, &event.Event{
		ID: "e2", RequestID: "r2", Timestamp: time.Now(),
		Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "y"}}}},
	}))

	deleted, err := c.TrimConversations(ctx, key, 0)
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Equal(t, "r2", deleted[0].RequestID)
}

func TestClient_ListSessions(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	userKey := session.UserKey{AppName: "app", UserID: "u1"}
	for i := 0; i < 3; i++ {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: fmt.Sprintf("ls%d", i)}
		_, err := c.CreateSession(ctx, key, session.StateMap{"idx": []byte(fmt.Sprintf("%d", i))})
		require.NoError(t, err)
	}

	sessions, err := c.ListSessions(ctx, userKey, 0, time.Time{})
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
}

func TestClient_Exists(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ex1"}

	exists, err := c.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	exists, err = c.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestClient_RefreshSummaryTTL(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{SessionTTL: 10 * time.Second})
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "rst1"}

	sumKey := c.keys.SummaryKey(key)
	rdb.Set(ctx, sumKey, "{}", 0)

	require.NoError(t, c.RefreshSummaryTTL(ctx, key))

	ttl := mr.TTL(sumKey)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, 10*time.Second)
}

func TestClient_RefreshSummaryTTL_NoTTL(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, Config{SessionTTL: 0})
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "rst2"}

	// Should be a no-op when TTL is 0
	require.NoError(t, c.RefreshSummaryTTL(ctx, key))
}

func Test_deepCopyState(t *testing.T) {
	t.Run("nil returns empty map", func(t *testing.T) {
		result := deepCopyState(nil)
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("copies values", func(t *testing.T) {
		original := session.StateMap{"a": []byte("hello"), "b": nil}
		copied := deepCopyState(original)

		assert.Equal(t, []byte("hello"), copied["a"])
		assert.Nil(t, copied["b"])

		// Mutate original
		original["a"][0] = 'X'
		assert.Equal(t, []byte("hello"), copied["a"])
	})
}

func Test_shouldStoreEventInList(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		assert.False(t, shouldStoreEventInList(nil))
	})

	t.Run("nil response", func(t *testing.T) {
		assert.False(t, shouldStoreEventInList(&event.Event{}))
	})

	t.Run("partial event", func(t *testing.T) {
		assert.False(t, shouldStoreEventInList(&event.Event{
			Response: &model.Response{
				IsPartial: true,
				Choices:   []model.Choice{{Message: model.Message{Content: "hi"}}},
			},
		}))
	})

	t.Run("valid event", func(t *testing.T) {
		assert.True(t, shouldStoreEventInList(&event.Event{
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hi"}}}},
		}))
	})

	t.Run("empty content", func(t *testing.T) {
		assert.False(t, shouldStoreEventInList(&event.Event{
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: ""}}}},
		}))
	})
}

func Test_boolToInt(t *testing.T) {
	assert.Equal(t, 1, boolToInt(true))
	assert.Equal(t, 0, boolToInt(false))
}

func TestClient_GetSession_WithTracksAndSummary(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "full1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append event
	evt := makeTestEvent("e1", time.Now())
	require.NoError(t, c.AppendEvent(ctx, key, evt))

	// Append track event
	tracksJSON, _ := json.Marshal([]string{"alpha"})
	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"trackdata"`),
		Timestamp: time.Now(),
	}
	require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

	// Create summary
	sum := &session.Summary{Summary: "test-sum", UpdatedAt: time.Now().UTC()}
	require.NoError(t, c.CreateSummary(ctx, key, "", sum, time.Hour))

	// Full GetSession should return events, tracks, summary
	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Len(t, sess.Events, 1)
	assert.NotEmpty(t, sess.Tracks)
	assert.NotNil(t, sess.Summaries)
	assert.Equal(t, "test-sum", sess.Summaries[""].Summary)
}

func TestClient_GetSession_WithEventLimit(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "lim1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent(fmt.Sprintf("e%d", i), baseTime.Add(time.Duration(i)*time.Second))))
	}

	// Get with limit
	sess, err := c.GetSession(ctx, key, 2, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Len(t, sess.Events, 2)
}

func TestClient_GetSession_WithAfterTime(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "aft1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent(fmt.Sprintf("e%d", i), baseTime.Add(time.Duration(i)*time.Hour))))
	}

	// Filter by time
	afterTime := baseTime.Add(2 * time.Hour)
	sess, err := c.GetSession(ctx, key, 0, afterTime)
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Should only get events after the threshold
	for _, evt := range sess.Events {
		assert.True(t, evt.Timestamp.After(afterTime) || evt.Timestamp.Equal(afterTime))
	}
}

func TestClient_GetSession_CorruptedMeta(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "corrupt1"}

	// Write corrupted meta directly
	metaKey := c.keys.SessionMetaKey(key)
	rdb.Set(ctx, metaKey, "not-valid-json", 0)

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "unmarshal session meta")
}

func TestClient_ListSessions_WithEventsAndAppState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	// Pre-populate app state
	require.NoError(t, c.UpdateAppState(ctx, "lsapp", session.StateMap{"ak": []byte("av")}, time.Hour))

	// Pre-populate user state
	userKey := session.UserKey{AppName: "lsapp", UserID: "lsu"}
	require.NoError(t, c.UpdateUserState(ctx, userKey, session.StateMap{"uk": []byte("uv")}, time.Hour))

	// Create sessions with events and tracks
	for i := 0; i < 2; i++ {
		key := session.Key{AppName: "lsapp", UserID: "lsu", SessionID: fmt.Sprintf("lssid%d", i)}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := makeTestEvent(fmt.Sprintf("e%d", i), time.Now())
		require.NoError(t, c.AppendEvent(ctx, key, evt))

		// Add track
		tracksJSON, _ := json.Marshal([]string{"t1"})
		te := &session.TrackEvent{Track: "t1", Payload: json.RawMessage(`"p"`), Timestamp: time.Now()}
		require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))
	}

	sessions, err := c.ListSessions(ctx, userKey, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	// Each session should have merged app/user state and tracks
	for _, s := range sessions {
		assert.Equal(t, []byte("av"), s.State["app:ak"])
		assert.Equal(t, []byte("uv"), s.State["user:uk"])
		assert.NotEmpty(t, s.Tracks)
	}
}

func TestClient_loadAndAttachTrackEvents_NilSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nil1"}

	// Should not panic
	c.loadAndAttachTrackEvents(ctx, key, nil, nil, 0, time.Time{})
}

func TestClient_loadAndAttachTrackEvents_ResolveFromState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "resolve1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append track events
	tracksJSON, _ := json.Marshal([]string{"gamma"})
	te := &session.TrackEvent{Track: "gamma", Payload: json.RawMessage(`"gp"`), Timestamp: time.Now()}
	require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

	// Create session with tracks in state (simulate by setting tracks state)
	sess := session.NewSession("app", "u1", "resolve1")
	sess.State = session.StateMap{"tracks": tracksJSON}

	// Pass nil tracks -> should resolve from session state
	c.loadAndAttachTrackEvents(ctx, key, sess, nil, 0, time.Time{})
	assert.NotEmpty(t, sess.Tracks)
}

func TestClient_loadAndMergeAppState_NilSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nil2"}

	// Should not panic
	c.loadAndMergeAppState(ctx, key, nil)
}

func Test_parseEvents_EmptyObject(t *testing.T) {
	// Lua cjson encodes empty arrays as {} (JSON object)
	r := &sessionDataResult{Events: json.RawMessage(`{}`)}
	result := r.parseEvents()
	assert.Nil(t, result)
}

func Test_parseEvents_EmptyInput(t *testing.T) {
	r := &sessionDataResult{Events: nil}
	result := r.parseEvents()
	assert.Nil(t, result)
}

func Test_parseEvents_ValidArray(t *testing.T) {
	r := &sessionDataResult{Events: json.RawMessage(`["a","b"]`)}
	result := r.parseEvents()
	assert.Equal(t, []string{"a", "b"}, result)
}

func TestClient_DeleteEvent_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "del-err"}

	// Create session first
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Close miniredis to simulate connection error
	mr.Close()

	// DeleteEvent should return error when Redis is unavailable
	err = c.DeleteEvent(ctx, key, "evt1")
	require.Error(t, err)
}

func TestClient_Exists_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "exists-err"}

	// Create session first
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Close miniredis to simulate connection error
	mr.Close()

	// Exists should return error when Redis is unavailable
	_, err = c.Exists(ctx, key)
	require.Error(t, err)
}

// ============================================================================
// Coverage tests for error paths in session.go
// ============================================================================

func TestCreateSession_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-create-err"}

	mr.Close()

	_, err := c.CreateSession(ctx, key, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create session")
}

func TestGetSession_MetaRedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-get-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get session meta")
}

func TestLoadSessionComplete_UnmarshalMetaError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-unmarshal"}

	_, err := c.loadSessionComplete(ctx, key, []byte("not valid json"), 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session meta")
}

func TestLoadSessionComplete_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-lua-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Fetch valid metaJSON before closing Redis
	metaJSON, err := rdb.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	require.NoError(t, err)

	// Close Redis so Lua script fails in loadSessionComplete
	mr.Close()

	_, err = c.loadSessionComplete(ctx, key, metaJSON, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load session data")
}

func TestGetSession_WithUserState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-user-state"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Set user state directly into the user state key that Lua reads
	userStateKey := c.keys.UserStateKey(key.AppName, key.UserID)
	err = rdb.HSet(ctx, userStateKey, "pref", "dark").Err()
	require.NoError(t, err)

	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// user state should be merged into session state with "user:" prefix
	assert.Equal(t, []byte("dark"), sess.State[session.StateUserPrefix+"pref"])
}

func TestAppendEvent_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-append-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	evt := makeTestEvent("e1", time.Now())
	err = c.AppendEvent(ctx, key, evt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "append event")
}

func TestDeleteSession_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-del-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	err = c.DeleteSession(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete session")
}

func TestTrimConversations_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-trim-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	_, err = c.TrimConversations(ctx, key, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trim conversations")
}

func TestLoadSessionBasic_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-basic-lua"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Fetch valid metaJSON before closing Redis
	metaJSON, err := rdb.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	require.NoError(t, err)

	// Close Redis so loadSessionBasic's Lua call fails
	mr.Close()

	_, err = c.loadSessionBasic(ctx, key, metaJSON, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load events")
}

func TestLoadSessionBasic_BadEventJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cov-basic-bad"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add a valid event
	require.NoError(t, c.AppendEvent(ctx, key, makeTestEvent("e1", time.Now().Add(-time.Hour))))

	// Add bad JSON to event data + time index
	eventDataKey := c.keys.EventDataKey(key)
	eventTimeIndexKey := c.keys.EventTimeIndexKey(key)
	badID := "cov-bad-id"
	require.NoError(t, rdb.HSet(ctx, eventDataKey, badID, "invalid event json").Err())
	require.NoError(t, rdb.ZAdd(ctx, eventTimeIndexKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: badID,
	}).Err())

	metaJSON, err := rdb.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	require.NoError(t, err)

	// loadSessionBasic should succeed but skip the bad JSON event via continue
	sess, err := c.loadSessionBasic(ctx, key, metaJSON, 0, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Only the valid event should be returned (bad one skipped)
	assert.Len(t, sess.Events, 1)
}
