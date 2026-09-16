//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gormmemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestWrapDBErr(t *testing.T) {
	assert.NoError(t, wrapDBErr("noop", nil))

	err := wrapDBErr("list memories", errors.New("connection reset"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gorm memory service list memories failed")
	assert.Contains(t, err.Error(), "connection reset")
}

func TestService_memoryTable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db))
	require.NoError(t, err)
	defer svc.Close()
	assert.NotNil(t, svc.memoryTable(ctx))
}

func TestService_initDB_failure(t *testing.T) {
	db := testDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = NewService(WithDB(db))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")
}

func TestBoundIndexName(t *testing.T) {
	short := boundIndexName("idx_memories_app_user")
	assert.Equal(t, "idx_memories_app_user", short)

	long := "idx_" + strings.Repeat("a", 80) + "_app_user"
	got := boundIndexName(long)
	assert.LessOrEqual(t, len(got), maxIndexNameLength)
	assert.NotEqual(t, long, got)
}

func TestService_ensureMemoryIndexes_reservedTable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithTableName("order"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	require.NoError(t, svc.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: "user"}, "hello", nil))
	entries, err := svc.ReadMemories(ctx, memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Idempotent re-init should succeed without IF NOT EXISTS.
	require.NoError(t, svc.ensureMemoryIndexes(ctx))
}

func TestQuoteIdent(t *testing.T) {
	db := testDB(t)
	quoted := quoteIdent(db, "order")
	assert.Contains(t, quoted, "order")
	assert.NotEqual(t, "order", quoted)
}

