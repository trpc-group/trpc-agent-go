//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

func TestRedisReplayWithMiniRedis(t *testing.T) {
	server := miniredis.RunT(t)
	backend, err := newRedisBackend("redis://"+server.Addr(), redisReplayKeyPrefix(t))
	require.NoError(t, err)
	t.Cleanup(func() { cleanupReplayBackend(t, backend) })

	report, err := Run(context.Background(), []Backend{newInMemoryBackend(t), backend}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func TestRedisReplayFromEnvironment(t *testing.T) {
	ctx := context.Background()
	backends, skipped, err := LoadOptionalBackends(ctx, OptionalBackend{
		Name:        "redis",
		Environment: EnvRedisURL,
		Factory: func(_ context.Context, url string) (Backend, error) {
			return newRedisBackend(url, redisReplayKeyPrefix(t))
		},
	})
	require.NoError(t, err)
	if len(backends) == 0 {
		require.Equal(t, []string{"redis: " + EnvRedisURL + " is not set"}, skipped)
		t.Skip(EnvRedisURL + " is not set")
	}
	backend := backends[0]
	t.Cleanup(func() { cleanupReplayBackend(t, backend) })

	report, err := Run(ctx, []Backend{newInMemoryBackend(t), backend}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func newRedisBackend(url, keyPrefix string) (Backend, error) {
	sessionService, err := sessionredis.NewService(
		sessionredis.WithRedisClientURL(url),
		sessionredis.WithKeyPrefix(keyPrefix),
		sessionredis.WithSummarizer(manualReplaySummarizer{}),
	)
	if err != nil {
		return Backend{}, fmt.Errorf("create redis session service: %w", err)
	}
	memoryService, err := memoryredis.NewService(
		memoryredis.WithRedisClientURL(url),
		memoryredis.WithKeyPrefix(keyPrefix),
	)
	if err != nil {
		_ = sessionService.Close()
		return Backend{}, fmt.Errorf("create redis memory service: %w", err)
	}
	return Backend{Name: "redis", Session: sessionService, Memory: memoryService}, nil
}

func redisReplayKeyPrefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("replaytest:%d", time.Now().UnixNano())
}
