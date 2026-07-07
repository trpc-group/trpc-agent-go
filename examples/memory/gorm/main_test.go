//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestGormMemoryService_sharedDB(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "example_gorm_memory.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	svc, err := newGormMemoryService(db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	ctx := context.Background()
	uk := memory.UserKey{AppName: "gorm-memory-chat", UserID: "test-user"}
	require.NoError(t, svc.AddMemory(ctx, uk, "likes hiking in the Alps", nil))

	entries, err := svc.ReadMemories(ctx, uk, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "likes hiking in the Alps", entries[0].Memory.Memory)

	results, err := svc.SearchMemories(ctx, uk, "hiking")
	require.NoError(t, err)
	require.NotEmpty(t, results)
}

func TestRedactDSN(t *testing.T) {
	t.Parallel()
	redacted := redactDSN("postgres://user:pass@localhost:5432/app?sslmode=disable")
	assert.NotContains(t, redacted, "pass")
	assert.Contains(t, redacted, "user:")
	assert.Contains(t, redacted, "@localhost:5432")
	assert.Equal(t, "not-a-url", redactDSN("not-a-url"))
}
