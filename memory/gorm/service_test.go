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
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	storagegorm "trpc.group/trpc-go/trpc-agent-go/storage/gorm"
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
	svc, err := NewService(WithDB(db))
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
	_, err := NewService()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires WithDB")
}

func TestNewService_WithGormInstance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "instance_test.db")
	instanceName := "test-gorm-instance"

	storagegorm.RegisterGormInstance(instanceName, storagegorm.WithDialector(sqlite.Open(path)))

	svc, err := NewService(WithGormInstance(instanceName))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	require.NoError(t, svc.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: "user"}, "hello", nil))
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
	require.Len(t, all, 3)
	assert.Equal(t, "e content token", all[0].Memory.Memory)
	assert.Equal(t, "d content token", all[1].Memory.Memory)
	assert.Equal(t, "c content token", all[2].Memory.Memory)
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
	require.NoError(t, db.Table(defaultTableName).AutoMigrate(&memoryRow{}))

	svc, err := NewService(WithDB(db), WithSkipDBInit(true))
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
	svc, err := NewService(WithDB(db), WithSoftDelete(true))
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
	require.NoError(t, db.Table(defaultTableName).Unscoped().Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestService_SoftDelete_reAddRestores(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithSoftDelete(true))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "restore"}
	require.NoError(t, svc.AddMemory(ctx, uk, "favorite color is teal", nil))

	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID,
	}))
	afterDelete, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Empty(t, afterDelete)

	require.NoError(t, svc.AddMemory(ctx, uk, "favorite color is teal", nil))
	restored, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, restored, 1)
	assert.Equal(t, "favorite color is teal", restored[0].Memory.Memory)

	var count int64
	require.NoError(t, db.Table(defaultTableName).Unscoped().Count(&count).Error)
	assert.Equal(t, int64(1), count, "restore should reuse the existing row, not insert a duplicate")
}

type fakeExtractor struct{}

func (f *fakeExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (f *fakeExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool { return false }

func (f *fakeExtractor) SetPrompt(_ string) {}

func (f *fakeExtractor) SetModel(_ model.Model) {}

func (f *fakeExtractor) SetEnabledTools(_ map[string]struct{}) {}

func (f *fakeExtractor) Metadata() map[string]any { return nil }

func TestService_validationErrors(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	invalidUser := memory.UserKey{AppName: "", UserID: "u"}

	_, err := svc.ReadMemories(ctx, invalidUser, 0)
	assert.ErrorIs(t, err, memory.ErrAppNameRequired)

	err = svc.AddMemory(ctx, invalidUser, "x", nil)
	assert.ErrorIs(t, err, memory.ErrAppNameRequired)

	_, err = svc.SearchMemories(ctx, invalidUser, "q")
	assert.ErrorIs(t, err, memory.ErrAppNameRequired)

	err = svc.ClearMemories(ctx, invalidUser)
	assert.ErrorIs(t, err, memory.ErrAppNameRequired)

	err = svc.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "", MemoryID: "id"})
	assert.ErrorIs(t, err, memory.ErrUserIDRequired)
}

func TestService_AddMemory_memoryLimit(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithMemoryLimit(2))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "limited"}
	require.NoError(t, svc.AddMemory(ctx, uk, "first memory", nil))
	require.NoError(t, svc.AddMemory(ctx, uk, "second memory", nil))

	err = svc.AddMemory(ctx, uk, "third memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_WithCustomTableName(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	const table = "guild_memories"

	svc, err := NewService(WithDB(db), WithTableName(table))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "custom-table"}
	require.NoError(t, svc.AddMemory(ctx, uk, "stored in custom table", nil))

	var count int64
	require.NoError(t, db.Table(table).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestService_Tools_enabledAndHidden(t *testing.T) {
	db := testDB(t)
	svc, err := NewService(WithDB(db),
		WithToolEnabled(memory.AddToolName, true),
		WithToolEnabled(memory.SearchToolName, true),
		WithToolEnabled(memory.LoadToolName, true),
		WithToolHidden(memory.LoadToolName, true),
	)
	require.NoError(t, err)
	defer svc.Close()

	tools := svc.Tools()
	require.Len(t, tools, 2)
}

func TestService_SearchMemories_maxResults(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithMaxResults(1))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "search"}
	require.NoError(t, svc.AddMemory(ctx, uk, "running shoes", nil))
	require.NoError(t, svc.AddMemory(ctx, uk, "running marathon", nil))

	res, err := svc.SearchMemories(ctx, uk, "running")
	require.NoError(t, err)
	assert.Len(t, res, 1)
}

func TestService_ReadMemories_returnsAllWhenLimitZero(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, testDB(t))
	uk := memory.UserKey{AppName: "app", UserID: "all"}

	require.NoError(t, svc.AddMemory(ctx, uk, "one", nil))
	require.NoError(t, svc.AddMemory(ctx, uk, "two", nil))

	all, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestService_HardDelete_removesRow(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithSoftDelete(false))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "hard-delete"}
	require.NoError(t, svc.AddMemory(ctx, uk, "ephemeral", nil))

	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID,
	}))

	var count int64
	require.NoError(t, db.Table(defaultTableName).Unscoped().Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestService_rowsToEntries_invalidJSON(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "bad-json"}
	now := time.Now()
	require.NoError(t, db.Table(defaultTableName).Create(&memoryRow{
		MemoryID:   "corrupt-id",
		AppName:    uk.AppName,
		UserID:     uk.UserID,
		MemoryData: datatypes.JSON("not-valid-json"),
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)

	_, err = svc.ReadMemories(ctx, uk, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")
}

func TestService_WithExtractor_startsWorker(t *testing.T) {
	db := testDB(t)
	svc, err := NewService(WithDB(db),
		WithExtractor(&fakeExtractor{}),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(2),
		WithMemoryJobTimeout(50*time.Millisecond),
	)
	require.NoError(t, err)
	require.NotNil(t, svc.autoMemoryWorker)

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
	require.NoError(t, svc.Close())

	noop := &Service{}
	require.NoError(t, noop.Close())
}

func TestRowsToEntries(t *testing.T) {
	now := time.Now()
	valid, err := rowsToEntries([]memoryRow{{
		MemoryData: datatypes.JSON(`{"id":"1","app_name":"app","user_id":"u","memory":{"memory":"hello"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}})
	require.NoError(t, err)
	require.Len(t, valid, 1)
	assert.Equal(t, "hello", valid[0].Memory.Memory)

	_, err = rowsToEntries([]memoryRow{{MemoryData: datatypes.JSON("{")}})
	require.Error(t, err)

	empty, err := rowsToEntries(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestService_closedDB_wrapsErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithMemoryLimit(10))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "closed-db"}
	require.NoError(t, svc.AddMemory(ctx, uk, "seed", nil))
	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)
	memKey := memory.Key{AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	assert.Error(t, svc.AddMemory(ctx, uk, "after close", nil))
	_, err = svc.ReadMemories(ctx, uk, 0)
	assert.Error(t, err)
	_, err = svc.SearchMemories(ctx, uk, "seed")
	assert.Error(t, err)
	assert.Error(t, svc.DeleteMemory(ctx, memKey))
	assert.Error(t, svc.ClearMemories(ctx, uk))
	assert.Error(t, svc.UpdateMemory(ctx, memKey, "updated", nil))
}

func TestService_UpdateMemory_corruptStoredJSON(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "corrupt-update"}
	require.NoError(t, svc.AddMemory(ctx, uk, "valid", nil))
	read, err := svc.ReadMemories(ctx, uk, 1)
	require.NoError(t, err)
	require.Len(t, read, 1)

	require.NoError(t, db.Table(defaultTableName).
		Where("memory_id = ?", read[0].ID).
		Update("memory_data", []byte("not-json")).Error)

	err = svc.UpdateMemory(ctx, memory.Key{
		AppName: uk.AppName, UserID: uk.UserID, MemoryID: read[0].ID,
	}, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")
}

func TestService_SoftDelete_ClearMemories(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(WithDB(db), WithSoftDelete(true))
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "soft-clear"}
	require.NoError(t, svc.AddMemory(ctx, uk, "one", nil))
	require.NoError(t, svc.AddMemory(ctx, uk, "two", nil))
	require.NoError(t, svc.ClearMemories(ctx, uk))

	after, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	assert.Empty(t, after)

	var count int64
	require.NoError(t, db.Table(defaultTableName).Unscoped().Count(&count).Error)
	assert.Equal(t, int64(2), count)
}

func TestService_AddMemory_hardDeleteSchemaWithoutDeletedAt(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	const table = "hard_delete_memories"
	require.NoError(t, db.Exec(`CREATE TABLE hard_delete_memories (
		memory_id char(64) PRIMARY KEY,
		app_name varchar(255) NOT NULL,
		user_id varchar(255) NOT NULL,
		memory_data blob NOT NULL,
		created_at datetime NOT NULL,
		updated_at datetime NOT NULL
	)`).Error)

	svc, err := NewService(
		WithDB(db),
		WithTableName(table),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	defer svc.Close()

	uk := memory.UserKey{AppName: "app", UserID: "hard-delete"}
	require.NoError(t, svc.AddMemory(ctx, uk, "same content", []string{"a"}))
	require.NoError(t, svc.AddMemory(ctx, uk, "same content", []string{"b"}))

	entries, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"b"}, entries[0].Memory.Topics)
}

type fakeGormClient struct {
	db      *gorm.DB
	closeFn func() error
}

func (c *fakeGormClient) DB() *gorm.DB { return c.db }

func (c *fakeGormClient) Close() error {
	if c.closeFn != nil {
		return c.closeFn()
	}
	return nil
}

func TestNewService_initDBFailureClosesOwnedClient(t *testing.T) {
	original := storagegorm.GetClientBuilder()
	defer storagegorm.SetClientBuilder(original)

	instanceName := "init-fail-close-test"
	storagegorm.RegisterGormInstance(instanceName, storagegorm.WithDialector(sqlite.Open(":memory:")))

	closedDB := testDB(t)
	sqlDB, err := closedDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	closed := false
	storagegorm.SetClientBuilder(func(ctx context.Context, opts ...storagegorm.ClientBuilderOpt) (storagegorm.Client, error) {
		return &fakeGormClient{
			db: closedDB,
			closeFn: func() error {
				closed = true
				return nil
			},
		}, nil
	})

	_, err = NewService(WithGormInstance(instanceName))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")
	assert.True(t, closed, "owned gorm client must be closed when initDB fails")
}

func TestService_WithToolExposed_hidesAutoModeDefaults(t *testing.T) {
	db := testDB(t)
	defaultSvc, err := NewService(WithDB(db), WithExtractor(&fakeExtractor{}))
	require.NoError(t, err)

	hiddenSvc, err := NewService(WithDB(db),
		WithExtractor(&fakeExtractor{}),
		WithToolExposed(memory.SearchToolName, false),
	)
	require.NoError(t, err)
	defer hiddenSvc.Close()
	defer defaultSvc.Close()

	assert.Len(t, hiddenSvc.Tools(), len(defaultSvc.Tools())-1)

	for _, tl := range hiddenSvc.Tools() {
		decl := tl.Declaration()
		if decl == nil {
			continue
		}
		assert.NotEqual(t, memory.SearchToolName, decl.Name)
	}
}
