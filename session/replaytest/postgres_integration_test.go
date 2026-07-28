//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

const (
	postgresReplayTablePrefix = "replaytest_"
	postgresReplayMemoryTable = "replaytest_memories"
)

func TestPostgresReplayFromEnvironment(t *testing.T) {
	ctx := context.Background()
	backends, skipped, err := LoadOptionalBackends(ctx, OptionalBackend{
		Name:        "postgres",
		Environment: EnvPostgresDSN,
		Factory: func(_ context.Context, dsn string) (Backend, error) {
			return newPostgresBackend(dsn)
		},
	})
	require.NoError(t, err)
	if len(backends) == 0 {
		require.Equal(t, []string{"postgres: " + EnvPostgresDSN + " is not set"}, skipped)
		t.Skip(EnvPostgresDSN + " is not set")
	}
	backend := backends[0]
	t.Cleanup(func() { cleanupSQLReplayBackend(t, backend) })

	report, err := Run(ctx, []Backend{newInMemoryBackend(t), backend}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func newPostgresBackend(dsn string) (Backend, error) {
	sessionService, err := sessionpostgres.NewService(
		sessionpostgres.WithPostgresClientDSN(dsn),
		sessionpostgres.WithTablePrefix(postgresReplayTablePrefix),
		sessionpostgres.WithSummarizer(manualReplaySummarizer{}),
	)
	if err != nil {
		return Backend{}, fmt.Errorf("create postgres session service: %w", err)
	}
	memoryService, err := memorypostgres.NewService(
		memorypostgres.WithPostgresClientDSN(dsn),
		memorypostgres.WithTableName(postgresReplayMemoryTable),
	)
	if err != nil {
		_ = sessionService.Close()
		return Backend{}, fmt.Errorf("create postgres memory service: %w", err)
	}
	return Backend{Name: "postgres", Session: sessionService, Memory: memoryService}, nil
}

func cleanupSQLReplayBackend(t *testing.T, backend Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, replayCase := range StandardCases() {
		if err := backend.Session.DeleteSession(ctx, session.Key{
			AppName: replayApp, UserID: replayUser, SessionID: replayCase.Name,
		}); err != nil {
			t.Errorf("delete %s replay session %q: %v", backend.Name, replayCase.Name, err)
		}
	}
	if err := backend.Memory.ClearMemories(ctx, memory.UserKey{AppName: replayApp, UserID: replayUser}); err != nil {
		t.Errorf("clear %s replay memories: %v", backend.Name, err)
	}
	if err := backend.Memory.Close(); err != nil {
		t.Errorf("close %s replay memory: %v", backend.Name, err)
	}
	if err := backend.Session.Close(); err != nil {
		t.Errorf("close %s replay session: %v", backend.Name, err)
	}
}
