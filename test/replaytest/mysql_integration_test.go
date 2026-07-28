//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

const (
	mysqlReplayTablePrefix = "replaytest_"
	mysqlReplayMemoryTable = "replaytest_memories"
)

func TestMySQLReplayFromEnvironment(t *testing.T) {
	ctx := context.Background()
	backends, skipped, err := LoadOptionalBackends(ctx, OptionalBackend{
		Name:        "mysql",
		Environment: EnvMySQLDSN,
		Factory: func(_ context.Context, dsn string) (Backend, error) {
			return newMySQLBackend(dsn)
		},
	})
	require.NoError(t, err)
	if len(backends) == 0 {
		require.Equal(t, []string{"mysql: " + EnvMySQLDSN + " is not set"}, skipped)
		t.Skip(EnvMySQLDSN + " is not set")
	}
	backend := backends[0]
	t.Cleanup(func() { cleanupReplayBackend(t, backend) })

	report, err := Run(ctx, []Backend{newInMemoryBackend(t), backend}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func newMySQLBackend(dsn string) (Backend, error) {
	sessionService, err := sessionmysql.NewService(
		sessionmysql.WithMySQLClientDSN(dsn),
		sessionmysql.WithTablePrefix(mysqlReplayTablePrefix),
		sessionmysql.WithSummarizer(manualReplaySummarizer{}),
	)
	if err != nil {
		return Backend{}, fmt.Errorf("create mysql session service: %w", err)
	}
	memoryService, err := memorymysql.NewService(
		memorymysql.WithMySQLClientDSN(dsn),
		memorymysql.WithTableName(mysqlReplayMemoryTable),
	)
	if err != nil {
		_ = sessionService.Close()
		return Backend{}, fmt.Errorf("create mysql memory service: %w", err)
	}
	return Backend{Name: "mysql", Session: sessionService, Memory: memoryService}, nil
}
