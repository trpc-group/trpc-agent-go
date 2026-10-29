//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorLegacyGenerationUpgrade(t *testing.T) {
	var rdb redis.UniversalClient
	if url := os.Getenv("TRPC_TEST_REDIS_URL"); url != "" {
		opts, err := redis.ParseURL(url)
		require.NoError(t, err)
		rdb = redis.NewClient(opts)
		t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	} else {
		_, rdb = setupMiniredis(t)
	}
	cfg := defaultConfig()
	cfg.KeyPrefix = "generation-" + uuid.NewString()
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "legacy-review"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.DeleteSession(ctx, key)) })
	raw, err := rdb.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	require.NoError(t, err)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &state))
	delete(state, "generation")
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, rdb.Set(ctx, c.keys.SessionMetaKey(key), raw, 0).Err())
	require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(`1`)}, nil))
	require.NoError(t, rdb.Expire(ctx, c.keys.SessionMetaKey(key), time.Minute).Err())
	empty := ""
	snapshot, err := c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &empty})
	require.NoError(t, err)
	ttl, err := rdb.PTTL(ctx, c.keys.SessionMetaKey(key)).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, time.Minute)
	_, _, _, exists, err := c.LoadSessionStateValue(ctx, key, "state-key")
	require.NoError(t, err)
	require.True(t, exists)
	_, err = c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &snapshot.Cursor})
	require.NoError(t, err, "initializing metadata must not invalidate a cursor for the same session")
}
