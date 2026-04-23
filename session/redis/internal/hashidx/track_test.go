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
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestClient_AppendTrackEvent(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trk1"}

	t.Run("session not found", func(t *testing.T) {
		te := &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(`"p"`),
			Timestamp: time.Now(),
		}
		err := c.AppendTrackEvent(ctx, key, te, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("appends and retrieves", func(t *testing.T) {
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		tracksJSON, _ := json.Marshal([]string{"alpha"})
		baseTime := time.Now()

		for i := 0; i < 3; i++ {
			te := &session.TrackEvent{
				Track:     "alpha",
				Payload:   json.RawMessage(`"payload"`),
				Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			}
			require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))
		}

		results, err := c.GetTrackEvents(ctx, key, []session.Track{"alpha"}, 0, time.Time{})
		require.NoError(t, err)
		require.Len(t, results["alpha"], 3)
	})

	t.Run("registers track in session state", func(t *testing.T) {
		tracks, err := c.ListTracksForSession(ctx, key)
		require.NoError(t, err)
		assert.Contains(t, tracks, session.Track("alpha"))
	})
}

func TestClient_AppendTrackEvent_PreservesExistingTTLWithoutRefresh(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	createCfg := defaultConfig()
	createCfg.SessionTTL = 10 * time.Second
	createClient := NewClient(rdb, createCfg)

	appendCfg := createCfg
	appendCfg.SessionTTL = 0
	appendClient := NewClient(rdb, appendCfg)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trk-ttl"}

	_, err := createClient.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	metaKey := createClient.keys.SessionMetaKey(key)
	assert.Equal(t, 10*time.Second, mr.TTL(metaKey))

	mr.FastForward(4 * time.Second)

	tracksJSON, err := json.Marshal([]string{"alpha"})
	require.NoError(t, err)

	err = appendClient.AppendTrackEvent(ctx, key, &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}, tracksJSON)
	require.NoError(t, err)

	assert.Equal(t, 6*time.Second, mr.TTL(metaKey))

	tracks, err := createClient.ListTracksForSession(ctx, key)
	require.NoError(t, err)
	assert.Contains(t, tracks, session.Track("alpha"))
}

func TestClient_GetTrackEvents(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "gte1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON, _ := json.Marshal([]string{"beta"})
	baseTime := time.Now()

	for i := 0; i < 5; i++ {
		te := &session.TrackEvent{
			Track:     "beta",
			Payload:   json.RawMessage(`"data"`),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))
	}

	t.Run("empty tracks returns empty", func(t *testing.T) {
		result, err := c.GetTrackEvents(ctx, key, nil, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("all events returned", func(t *testing.T) {
		result, err := c.GetTrackEvents(ctx, key, []session.Track{"beta"}, 0, time.Time{})
		require.NoError(t, err)
		assert.Len(t, result["beta"], 5)
	})

	t.Run("with limit", func(t *testing.T) {
		result, err := c.GetTrackEvents(ctx, key, []session.Track{"beta"}, 2, time.Time{})
		require.NoError(t, err)
		assert.Len(t, result["beta"], 2)
	})

	t.Run("with afterTime filter", func(t *testing.T) {
		afterTime := baseTime.Add(2 * time.Second)
		result, err := c.GetTrackEvents(ctx, key, []session.Track{"beta"}, 0, afterTime)
		require.NoError(t, err)
		// Events at t+2, t+3, t+4 (afterTime uses exclusive lower bound with UnixNano)
		assert.GreaterOrEqual(t, len(result["beta"]), 2)
	})

	t.Run("nonexistent track returns nothing", func(t *testing.T) {
		result, err := c.GetTrackEvents(ctx, key, []session.Track{"nonexistent"}, 0, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, result["nonexistent"])
	})
}

func TestClient_ListTracksForSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	t.Run("session not found", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "notrk"}
		tracks, err := c.ListTracksForSession(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, tracks)
	})

	t.Run("session with no tracks", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "notrk2"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		tracks, err := c.ListTracksForSession(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, tracks)
	})

	t.Run("session with tracks", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "wtrk"}
		_, err := c.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		tracksJSON, _ := json.Marshal([]string{"alpha", "beta"})
		te := &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(`"x"`),
			Timestamp: time.Now(),
		}
		require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

		tracks, err := c.ListTracksForSession(ctx, key)
		require.NoError(t, err)
		assert.Contains(t, tracks, session.Track("alpha"))
		assert.Contains(t, tracks, session.Track("beta"))
	})
}

func TestClient_AppendTrackEvent_MultipleTracksOnSameSession(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "mtk1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON1, _ := json.Marshal([]string{"alpha"})
	require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{
		Track: "alpha", Payload: json.RawMessage(`"a1"`), Timestamp: time.Now(),
	}, tracksJSON1))

	tracksJSON2, _ := json.Marshal([]string{"alpha", "beta"})
	require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{
		Track: "beta", Payload: json.RawMessage(`"b1"`), Timestamp: time.Now(),
	}, tracksJSON2))

	results, err := c.GetTrackEvents(ctx, key, []session.Track{"alpha", "beta"}, 0, time.Time{})
	require.NoError(t, err)
	assert.Len(t, results["alpha"], 1)
	assert.Len(t, results["beta"], 1)
}

func TestClient_GetTrackEvents_Error(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "err1"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON, _ := json.Marshal([]string{"alpha"})
	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}
	require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

	// Close miniredis to simulate connection error
	mr.Close()

	_, err = c.GetTrackEvents(ctx, key, []session.Track{"alpha"}, 0, time.Time{})
	require.Error(t, err)
}

func TestAppendTrackEvent_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "atk-lua-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"p"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "append track event")
}

func TestLoadTrackEventsViaLua_BadEventJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trk-bad-json"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON, _ := json.Marshal([]string{"alpha"})
	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"good"`),
		Timestamp: time.Now(),
	}
	require.NoError(t, c.AppendTrackEvent(ctx, key, te, tracksJSON))

	// Write malformed JSON directly to the track data hash for a new event ID
	trackDataKey := c.keys.TrackDataKey(key, "alpha")
	trackTimeIndexKey := c.keys.TrackTimeIndexKey(key, "alpha")
	badID := "bad-trk-id"
	require.NoError(t, rdb.HSet(ctx, trackDataKey, badID, "not valid json").Err())
	require.NoError(t, rdb.ZAdd(ctx, trackTimeIndexKey, redis.Z{
		Score:  float64(time.Now().Add(time.Second).UnixNano()),
		Member: badID,
	}).Err())

	// loadTrackEventsViaLua should skip bad JSON via continue, returning only valid events
	result, err := c.GetTrackEvents(ctx, key, []session.Track{"alpha"}, 0, time.Time{})
	require.NoError(t, err)
	assert.Len(t, result["alpha"], 1)
}

func TestListTracksForSession_ConnectionError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ltk-conn-err"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	_, err = c.ListTracksForSession(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get session meta")
}

func TestListTracksForSession_UnmarshalError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ltk-unmarshal"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Overwrite meta with invalid JSON
	err = rdb.Set(ctx, c.keys.SessionMetaKey(key), "bad json", 0).Err()
	require.NoError(t, err)

	_, err = c.ListTracksForSession(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session meta")
}
