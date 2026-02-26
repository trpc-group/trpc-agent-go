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
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "delt1"}

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
