//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gormmemory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory_gorm_test.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newTestService(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	svc, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestServiceOpts_WithSkipDBInit(t *testing.T) {
	opts := defaultOptions.clone()
	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)
	WithSkipDBInit(false)(&opts)
	assert.False(t, opts.skipDBInit)
}

func TestNewService_requiresDB(t *testing.T) {
	_, err := NewService(nil)
	require.Error(t, err)
}

func TestService_AddMemory_idempotent(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "user"}

	require.NoError(t, svc.AddMemory(ctx, uk, "same content", []string{"a"}))
	require.NoError(t, svc.AddMemory(ctx, uk, "same content", []string{"b"}))

	entries, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"b"}, entries[0].Memory.Topics)
}

func TestService_UpdateMemory_rotatesID(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "user"}

	require.NoError(t, svc.AddMemory(ctx, uk, "likes coffee", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindFact})))

	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)
	oldID := read[0].ID

	var updateResult memory.UpdateResult
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: oldID,
	}, "likes tea", nil, memory.WithUpdateMetadata(&memory.Metadata{Kind: memory.KindEpisode}),
		memory.WithUpdateResult(&updateResult)))

	assert.NotEmpty(t, updateResult.MemoryID)
	assert.NotEqual(t, oldID, updateResult.MemoryID)
	newID := updateResult.MemoryID

	after, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, newID, after[0].ID)
	assert.Equal(t, memory.KindEpisode, imemory.EffectiveKind(after[0].Memory))
}

func TestService_SearchMemories_kindFilter(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "user1"}

	ev2024 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AddMemory(ctx, uk, "episode about hiking in Alps", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &ev2024})))
	require.NoError(t, svc.AddMemory(ctx, uk, "user likes coffee", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindFact})))

	res, err := svc.SearchMemories(ctx, uk, "hiking", memory.WithSearchOptions(memory.SearchOptions{
		Query: "hiking",
		Kind:  memory.KindEpisode,
	}))
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, memory.KindEpisode, imemory.EffectiveKind(res[0].Memory))
}

func TestService_ReadMemories_limit(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "u2"}
	for i := 0; i < 5; i++ {
		content := string(rune('a'+i)) + " content token"
		require.NoError(t, svc.AddMemory(ctx, uk, content, nil))
	}
	all, err := svc.ReadMemories(ctx, uk, 3)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestService_UpdateMemory_notFound(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	err := svc.UpdateMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: "nope"}, "x", nil)
	require.Error(t, err)
}

func TestService_DeleteAndClear(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "clear-me"}
	require.NoError(t, svc.AddMemory(ctx, uk, "to delete", nil))

	read, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, read, 1)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID,
	}))
	afterDelete, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Empty(t, afterDelete)

	require.NoError(t, svc.AddMemory(ctx, uk, "again", nil))
	require.NoError(t, svc.ClearMemories(ctx, uk))
	afterClear, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Empty(t, afterClear)
}

func TestService_Tools_emptyByDefault(t *testing.T) {
	svc := newTestService(t, testDB(t))
	assert.Empty(t, svc.Tools())
	require.NoError(t, svc.Close())
	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestService_WithSkipDBInit(t *testing.T) {
	db := testDB(t)
	require.NoError(t, db.Table("memories").AutoMigrate(&memoryRow{}))

	svc, err := NewService(db, WithSkipDBInit(true))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	uk := memory.UserKey{AppName: "app", UserID: "u"}
	require.NoError(t, svc.AddMemory(ctx, uk, "hello", nil))
	entries, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestService_SoftDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "soft"}
	require.NoError(t, svc.AddMemory(ctx, uk, "secret", nil))
	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID,
	}))
	after, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Empty(t, after)

	var count int64
	require.NoError(t, db.Table("memories").Unscoped().Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
