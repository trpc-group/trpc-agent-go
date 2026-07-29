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
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryclickhouse "trpc.group/trpc-go/trpc-agent-go/memory/clickhouse"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

const (
	clickHouseReplayTablePrefix = "replaytest_"
	clickHouseReplayMemoryTable = "replaytest_memories"
)

func TestClickHouseReplayFromEnvironment(t *testing.T) {
	ctx := context.Background()
	backends, skipped, err := LoadOptionalBackends(ctx, OptionalBackend{
		Name:        "clickhouse",
		Environment: EnvClickHouseDSN,
		Factory: func(_ context.Context, dsn string) (Backend, error) {
			return newClickHouseBackend(dsn)
		},
	})
	require.NoError(t, err)
	if len(backends) == 0 {
		require.Equal(t, []string{"clickhouse: " + EnvClickHouseDSN + " is not set"}, skipped)
		t.Skip(EnvClickHouseDSN + " is not set")
	}
	backend := backends[0]
	t.Cleanup(func() { cleanupReplayBackend(t, backend) })

	report, err := Run(ctx, []Backend{newInMemoryBackend(t), backend}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func TestClickHouseMemoryLifecycleFromEnvironment(t *testing.T) {
	dsn := os.Getenv(EnvClickHouseDSN)
	if dsn == "" {
		t.Skip(EnvClickHouseDSN + " is not set")
	}
	ctx := context.Background()
	service, err := memoryclickhouse.NewService(
		memoryclickhouse.WithClickHouseDSN(dsn),
		memoryclickhouse.WithTableName(clickHouseReplayMemoryTable),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, service.ClearMemories(ctx, memory.UserKey{AppName: replayApp, UserID: replayUser}))
		require.NoError(t, service.Close())
	})

	userKey := memory.UserKey{AppName: replayApp, UserID: replayUser}
	require.NoError(t, service.AddMemory(ctx, userKey, "before update", []string{"draft"}))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	result := &memory.UpdateResult{}
	require.NoError(t, service.UpdateMemory(ctx, memory.Key{
		AppName: replayApp, UserID: replayUser, MemoryID: entries[0].ID,
	}, "after update", []string{"final"}, memory.WithUpdateResult(result)))
	require.NotEmpty(t, result.MemoryID)
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: replayApp, UserID: replayUser, MemoryID: result.MemoryID,
	}))
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestClickHouseConcurrentTrackAppendFromEnvironment(t *testing.T) {
	dsn := os.Getenv(EnvClickHouseDSN)
	if dsn == "" {
		t.Skip(EnvClickHouseDSN + " is not set")
	}
	ctx := context.Background()
	backend, err := newClickHouseBackend(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { cleanupReplayBackend(t, backend) })
	secondService, err := sessionclickhouse.NewService(
		sessionclickhouse.WithClickHouseDSN(dsn),
		sessionclickhouse.WithTablePrefix(clickHouseReplayTablePrefix),
		sessionclickhouse.WithSummarizer(manualReplaySummarizer{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondService.Close()) })

	key := session.Key{
		AppName:   replayApp,
		UserID:    replayUser,
		SessionID: fmt.Sprintf("concurrent-tracks-%d", time.Now().UnixNano()),
	}
	t.Cleanup(func() { cleanupReplaySession(t, backend, key) })
	firstSession, err := backend.Session.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	secondSession, err := secondService.GetSession(ctx, key)
	require.NoError(t, err)
	firstTrackService, ok := backend.Session.(session.TrackService)
	require.True(t, ok)
	trackServices := []session.TrackService{firstTrackService, secondService}
	sessions := []*session.Session{firstSession, secondSession}

	const eventCount = 24
	errs := make(chan error, eventCount)
	var wg sync.WaitGroup
	for i := 0; i < eventCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, marshalErr := json.Marshal(map[string]int{"index": i})
			if marshalErr != nil {
				errs <- marshalErr
				return
			}
			serviceIndex := i % len(trackServices)
			errs <- trackServices[serviceIndex].AppendTrackEvent(ctx, sessions[serviceIndex], &session.TrackEvent{
				Track:     "model",
				Payload:   payload,
				Timestamp: time.Now().UTC(),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	persisted, err := backend.Session.GetSession(ctx, key)
	require.NoError(t, err)
	require.Contains(t, persisted.Tracks, session.Track("model"))
	require.Len(t, persisted.Tracks["model"].Events, eventCount)
	counts := make(map[int]int, eventCount)
	for _, trackEvent := range persisted.Tracks["model"].Events {
		var payload struct {
			Index int `json:"index"`
		}
		require.NoError(t, json.Unmarshal(trackEvent.Payload, &payload))
		counts[payload.Index]++
	}
	for i := 0; i < eventCount; i++ {
		require.Equal(t, 1, counts[i], "payload %d must be persisted exactly once", i)
	}
}

func newClickHouseBackend(dsn string) (Backend, error) {
	sessionService, err := sessionclickhouse.NewService(
		sessionclickhouse.WithClickHouseDSN(dsn),
		sessionclickhouse.WithTablePrefix(clickHouseReplayTablePrefix),
		sessionclickhouse.WithSummarizer(manualReplaySummarizer{}),
	)
	if err != nil {
		return Backend{}, fmt.Errorf("create clickhouse session service: %w", err)
	}
	memoryService, err := memoryclickhouse.NewService(
		memoryclickhouse.WithClickHouseDSN(dsn),
		memoryclickhouse.WithTableName(clickHouseReplayMemoryTable),
	)
	if err != nil {
		_ = sessionService.Close()
		return Backend{}, fmt.Errorf("create clickhouse memory service: %w", err)
	}
	return Backend{Name: "clickhouse", Session: sessionService, Memory: memoryService}, nil
}
