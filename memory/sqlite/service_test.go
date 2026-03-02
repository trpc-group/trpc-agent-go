//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func openTempSQLiteDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-mem-*.db")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	db, err := sql.Open("sqlite3", f.Name())
	require.NoError(t, err)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return db, cleanup
}

func TestNewService_NilDB(t *testing.T) {
	svc, err := NewService(nil)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestService_CRUD_HardDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t,
		svc.AddMemory(ctx, userKey, "Alice likes Go", []string{"pref"}))
	got, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	memID := got[0].ID

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memID,
	}
	require.NoError(t,
		svc.UpdateMemory(ctx, memKey, "Alice likes Go and SQL", nil))

	results, err := svc.SearchMemories(ctx, userKey, "sql")
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.NoError(t, svc.DeleteMemory(ctx, memKey))
	got, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 0)
}

func TestService_SoftDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 0)

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	require.NoError(t, svc.ClearMemories(ctx, userKey))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 0)
}

func TestService_MemoryLimitExceeded_NewOnly(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithMemoryLimit(1))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))

	err = svc.AddMemory(ctx, userKey, "B", nil)
	require.Error(t, err)
}

func TestService_SoftDelete_MemoryLimit_ResurrectBlocked(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSoftDelete(true),
		WithMemoryLimit(1),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	require.NoError(t, svc.AddMemory(ctx, userKey, "B", nil))

	err = svc.AddMemory(ctx, userKey, "A", nil)
	require.Error(t, err)
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "u1", MemoryID: "nope"}

	err = svc.UpdateMemory(ctx, memKey, "x", nil)
	require.Error(t, err)
}

func TestService_ClearMemories_HardDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))
	require.NoError(t, svc.ClearMemories(ctx, userKey))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 0)
}

func TestService_Search_EmptyQuery(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	results, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		" ",
	)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestService_Search_SoftDeleteFiltered(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	results, err := svc.SearchMemories(ctx, userKey, "A")
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestService_InvalidKeys(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	invalidUserKey := memory.UserKey{AppName: "", UserID: "u1"}

	require.Error(t, svc.AddMemory(ctx, invalidUserKey, "A", nil))
	require.Error(t, svc.ClearMemories(ctx, invalidUserKey))

	_, err = svc.ReadMemories(ctx, invalidUserKey, 0)
	require.Error(t, err)

	_, err = svc.SearchMemories(ctx, invalidUserKey, "A")
	require.Error(t, err)

	require.Error(t, svc.UpdateMemory(ctx, memory.Key{}, "A", nil))
	require.Error(t, svc.DeleteMemory(ctx, memory.Key{}))
}

func TestService_ReadMemories_Limit(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "B", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestService_SoftDelete_MemoryLimit_ResurrectAllowed(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSoftDelete(true),
		WithMemoryLimit(1),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	require.NoError(t, svc.AddMemory(ctx, userKey, "A", nil))
}

func TestService_Tools_EnqueueAutoMemoryJob(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	tools := svc.Tools()
	require.NotEmpty(t, tools)

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestWithTableName_InvalidPanics(t *testing.T) {
	require.Panics(t, func() {
		opt := WithTableName("bad-name")
		opt(&ServiceOpts{})
	})
}

func TestNewService_InitDBError(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()
	require.NoError(t, db.Close())

	svc, err := NewService(db)
	require.Error(t, err)
	require.Nil(t, svc)
}

type fakeExtractor struct{}

func (f *fakeExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (f *fakeExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return false
}

func (f *fakeExtractor) SetPrompt(prompt string) {}

func (f *fakeExtractor) SetModel(m model.Model) {}

func (f *fakeExtractor) Metadata() map[string]any { return nil }

func TestOptionsAndClose_Coverage(t *testing.T) {
	const jobTimeout = 123 * time.Millisecond

	opts := defaultOptions.clone()

	WithSkipDBInit(true)(&opts)
	require.True(t, opts.skipDBInit)

	WithTableName("memories_v2")(&opts)
	require.Equal(t, "memories_v2", opts.tableName)

	WithAsyncMemoryNum(0)(&opts)
	require.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)

	WithMemoryQueueSize(0)(&opts)
	require.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)

	WithMemoryJobTimeout(jobTimeout)(&opts)
	require.Equal(t, jobTimeout, opts.memoryJobTimeout)

	WithExtractor(&fakeExtractor{})(&opts)
	require.NotNil(t, opts.extractor)

	WithCustomTool(memory.LoadToolName, func() tool.Tool { return nil })(&opts)
	_, ok := opts.toolCreators[memory.LoadToolName]
	require.True(t, ok)

	WithCustomTool("bad_tool", func() tool.Tool { return nil })(&opts)
	WithCustomTool(memory.LoadToolName, nil)(&opts)

	WithToolEnabled(memory.LoadToolName, true)(&opts)
	_, ok = opts.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	require.True(t, opts.userExplicitlySet[memory.LoadToolName])

	opts2 := ServiceOpts{}
	WithToolEnabled(memory.LoadToolName, true)(&opts2)
	_, ok = opts2.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	require.True(t, opts2.userExplicitlySet[memory.LoadToolName])
	WithToolEnabled("bad_tool", true)(&opts2)

	WithToolEnabled(memory.LoadToolName, false)(&opts)
	_, ok = opts.enabledTools[memory.LoadToolName]
	require.False(t, ok)

	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithExtractor(&fakeExtractor{}),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(1),
		WithMemoryJobTimeout(time.Millisecond),
	)
	require.NoError(t, err)
	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
	require.NoError(t, svc.Close())

	noop := &Service{}
	require.NoError(t, noop.Close())
}
