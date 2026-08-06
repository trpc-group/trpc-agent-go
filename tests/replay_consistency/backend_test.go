//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chstorage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// ---------------------------------------------------------------------------
// ClickHouse mock client
// ClickHouse initDB only calls Exec (no schema verification), so a simple
// stub that returns nil / empty rows is sufficient to exercise the full
// constructor success path.
// ---------------------------------------------------------------------------

type mockCHClient struct{}

func (m *mockCHClient) Exec(_ context.Context, _ string, _ ...any) error { return nil }

func (m *mockCHClient) Query(_ context.Context, _ string, _ ...any) (driver.Rows, error) {
	return &mockCHRows{}, nil
}

func (m *mockCHClient) QueryRow(_ context.Context, _ []any, _ string, _ ...any) error { return nil }

func (m *mockCHClient) QueryToStruct(_ context.Context, _ any, _ string, _ ...any) error {
	return nil
}

func (m *mockCHClient) QueryToStructs(_ context.Context, _ any, _ string, _ ...any) error {
	return nil
}

func (m *mockCHClient) BatchInsert(_ context.Context, _ string, _ chstorage.BatchFn, _ ...driver.PrepareBatchOption) error {
	return nil
}

func (m *mockCHClient) AsyncInsert(_ context.Context, _ string, _ bool, _ ...any) error {
	return nil
}

func (m *mockCHClient) Close() error { return nil }

type mockCHRows struct{}

func (m *mockCHRows) Next() bool                       { return false }
func (m *mockCHRows) Scan(_ ...any) error              { return nil }
func (m *mockCHRows) ScanStruct(_ any) error           { return nil }
func (m *mockCHRows) ColumnTypes() []driver.ColumnType { return nil }
func (m *mockCHRows) Totals(_ ...any) error            { return nil }
func (m *mockCHRows) Columns() []string                { return nil }
func (m *mockCHRows) Close() error                     { return nil }
func (m *mockCHRows) Err() error                       { return nil }
func (m *mockCHRows) HasData() bool                    { return false }

// ---------------------------------------------------------------------------
// newClickHouseReplayBackend tests
// ---------------------------------------------------------------------------

func TestNewClickHouseReplayBackend_Success(t *testing.T) {
	orig := chstorage.GetClientBuilder()
	defer chstorage.SetClientBuilder(orig)

	chstorage.SetClientBuilder(func(_ ...chstorage.ClientBuilderOpt) (chstorage.Client, error) {
		return &mockCHClient{}, nil
	})

	t.Setenv(replayEnableClickHouse, "true")
	t.Setenv("CLICKHOUSE_HOST", "localhost")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_USER", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")

	backend, err := newClickHouseReplayBackend()
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.Equal(t, "clickhouse", backend.Name())
	assert.Equal(t, BackendKindSession, backend.Kind())
	assert.False(t, backend.Supports("track"))
	assert.True(t, backend.Supports("summary"))
	assert.True(t, backend.Supports("memory"))
	assert.NoError(t, backend.Close())
}

func TestNewDefaultReplayBackends_WithClickHouse(t *testing.T) {
	orig := chstorage.GetClientBuilder()
	defer chstorage.SetClientBuilder(orig)

	chstorage.SetClientBuilder(func(_ ...chstorage.ClientBuilderOpt) (chstorage.Client, error) {
		return &mockCHClient{}, nil
	})

	t.Setenv(replayEnableClickHouse, "true")
	t.Setenv("CLICKHOUSE_HOST", "localhost")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_USER", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")

	backends, err := newDefaultReplayBackends(HarnessOptions{})
	require.NoError(t, err)
	require.Len(t, backends, 3) // inmemory + sqlite + clickhouse
	assert.Equal(t, "inmemory", backends[0].Name())
	assert.Equal(t, "sqlite", backends[1].Name())
	assert.Equal(t, "clickhouse", backends[2].Name())
	for _, b := range backends {
		assert.NoError(t, b.Close())
	}
}

func TestNewClickHouseReplayBackend_MemoryDBError(t *testing.T) {
	orig := chstorage.GetClientBuilder()
	defer chstorage.SetClientBuilder(orig)

	chstorage.SetClientBuilder(func(_ ...chstorage.ClientBuilderOpt) (chstorage.Client, error) {
		return &mockCHClient{}, nil
	})

	t.Setenv(replayEnableClickHouse, "true")
	t.Setenv("CLICKHOUSE_HOST", "localhost")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_USER", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")
	// Force openTempSQLiteDB to fail for the memory DB creation path.
	t.Setenv("TMPDIR", "/nonexistent/path")

	_, err := newClickHouseReplayBackend()
	require.Error(t, err)
}
