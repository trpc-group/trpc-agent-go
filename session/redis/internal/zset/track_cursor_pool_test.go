//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorSingleConnectionAppend(t *testing.T) {
	var options *redis.Options
	if url := os.Getenv("TRPC_TEST_REDIS_URL"); url != "" {
		var err error
		options, err = redis.ParseURL(url)
		require.NoError(t, err)
	} else {
		options = &redis.Options{Addr: miniredis.RunT(t).Addr()}
	}
	options.PoolSize = 1
	options.PoolTimeout = 50 * time.Millisecond
	options.MaxRetries = -1
	rdb := redis.NewClient(options)
	defer rdb.Close()
	cfg := defaultConfig()
	cfg.KeyPrefix = "pool-" + uuid.NewString()
	c := NewClient(rdb, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "review"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	defer c.DeleteSession(context.Background(), key)
	empty := ""
	_, err = c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &empty})
	require.NoError(t, err)
	err = c.AppendTrackEvent(ctx, key, &session.TrackEvent{Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(`1`)})
	require.NoError(t, err, "a valid write must work with one pooled connection")
}
