//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlitevec

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type mockEmbedder struct {
	dimension int
}

func (m *mockEmbedder) GetEmbedding(
	ctx context.Context,
	text string,
) ([]float64, error) {
	_ = ctx
	out := make([]float64, m.dimension)
	switch text {
	case "alpha":
		out[0] = 1
	case "beta":
		out[1] = 1
	default:
		out[0] = 1
	}
	return out, nil
}

func (m *mockEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	emb, err := m.GetEmbedding(ctx, text)
	return emb, nil, err
}

func (m *mockEmbedder) GetDimensions() int {
	return m.dimension
}

type errorEmbedder struct {
	dimension int
	err       error
}

func (e *errorEmbedder) GetEmbedding(
	ctx context.Context,
	text string,
) ([]float64, error) {
	_ = ctx
	_ = text
	return nil, e.err
}

func (e *errorEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	emb, err := e.GetEmbedding(ctx, text)
	return emb, nil, err
}

func (e *errorEmbedder) GetDimensions() int {
	return e.dimension
}

func openTempSQLiteDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-mem-vec-*.db")
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

func TestNewService_EmbedderRequired(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestService_CRUD_HardDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", []string{"pref"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))

	got, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: got[0].ID,
	}
	require.NoError(t,
		svc.UpdateMemory(ctx, memKey, "alpha", []string{"updated"}))

	results, err := svc.SearchMemories(ctx, userKey, "alpha")
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "alpha", results[0].Memory.Memory)

	require.NoError(t, svc.DeleteMemory(ctx, memKey))
	got, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestService_SoftDelete_ResurrectOnAdd(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithSoftDelete(true),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
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
	require.Empty(t, entries)

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestService_MemoryLimitExceeded_NewOnly(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithMemoryLimit(1),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))

	err = svc.AddMemory(ctx, userKey, "beta", nil)
	require.Error(t, err)

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
}

func TestService_Search_EmptyQuery(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
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

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithSoftDelete(true),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	results, err := svc.SearchMemories(ctx, userKey, "alpha")
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestService_AddMemory_DimensionMismatch(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(3),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	err = svc.AddMemory(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		"alpha",
		nil,
	)
	require.Error(t, err)
}

func TestService_InvalidUserKey(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	invalid := memory.UserKey{AppName: "", UserID: "u1"}

	require.Error(t, svc.AddMemory(ctx, invalid, "alpha", nil))
	require.Error(t, svc.ClearMemories(ctx, invalid))

	_, err = svc.ReadMemories(ctx, invalid, 0)
	require.Error(t, err)

	_, err = svc.SearchMemories(ctx, invalid, "alpha")
	require.Error(t, err)
}

func TestService_InvalidMemoryKey(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	require.Error(t, svc.UpdateMemory(ctx, memory.Key{}, "alpha", nil))
	require.Error(t, svc.DeleteMemory(ctx, memory.Key{}))
}

func TestService_SearchMemories_EmbedderError(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	expectedErr := errors.New("embedder error")
	svc, err := NewService(
		db,
		WithEmbedder(&errorEmbedder{dimension: 2, err: expectedErr}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		"alpha",
	)
	require.Error(t, err)
}

func TestService_UpdateMemory_SoftDeleteNotFound(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithSoftDelete(true),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	err = svc.UpdateMemory(ctx, memKey, "alpha", nil)
	require.Error(t, err)
}

func TestService_AddMemory_SoftDelete_MemoryLimitResurrect(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithSoftDelete(true),
		WithMemoryLimit(1),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
}

func TestService_WithMaxResults(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
		WithMaxResults(1),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))

	results, err := svc.SearchMemories(ctx, userKey, "alpha")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "alpha", results[0].Memory.Memory)
}

func TestService_ClearMemories_HardDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))

	require.NoError(t, svc.ClearMemories(ctx, userKey))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestService_ClearMemories_SoftDelete(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithSoftDelete(true),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))

	require.NoError(t, svc.ClearMemories(ctx, userKey))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestService_ReadMemories_Limit(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "u1", MemoryID: "nope"}

	err = svc.UpdateMemory(ctx, memKey, "alpha", nil)
	require.Error(t, err)
}

func TestService_Tools_EnqueueAutoMemoryJob(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithExtractor(&fakeExtractor{}),
		WithIndexDimension(2),
	)
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

var _ extractor.MemoryExtractor = (*fakeExtractor)(nil)

type fakeExtractor struct{}

func (f *fakeExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	_ = ctx
	_ = messages
	_ = existing
	return nil, nil
}

func (f *fakeExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	_ = ctx
	return false
}

func (f *fakeExtractor) SetPrompt(prompt string) {
	_ = prompt
}

func (f *fakeExtractor) SetModel(m model.Model) {
	_ = m
}

func (f *fakeExtractor) Metadata() map[string]any {
	return nil
}

func TestServiceOpts_Defaults(t *testing.T) {
	opts := defaultOptions.clone()

	require.Equal(t, defaultTableName, opts.tableName)
	require.Equal(t, defaultIndexDimension, opts.indexDimension)
	require.Equal(t, defaultMaxResults, opts.maxResults)
	require.Equal(t, imemory.DefaultMemoryLimit, opts.memoryLimit)
	require.False(t, opts.softDelete)
	require.False(t, opts.skipDBInit)
	require.NotNil(t, opts.toolCreators)
	require.NotNil(t, opts.enabledTools)
}

func TestServiceOpts_WithEmbedder(t *testing.T) {
	opts := ServiceOpts{}
	e := &mockEmbedder{dimension: 2}
	var _ embedder.Embedder = e

	WithEmbedder(e)(&opts)
	require.Equal(t, e, opts.embedder)
}

func TestServiceOpts_Coverage(t *testing.T) {
	opts := defaultOptions.clone()

	WithTableName("memories_v2")(&opts)
	require.Equal(t, "memories_v2", opts.tableName)

	WithIndexDimension(123)(&opts)
	require.Equal(t, 123, opts.indexDimension)

	WithMaxResults(7)(&opts)
	require.Equal(t, 7, opts.maxResults)

	WithMemoryLimit(9)(&opts)
	require.Equal(t, 9, opts.memoryLimit)

	WithSoftDelete(true)(&opts)
	require.True(t, opts.softDelete)

	WithSkipDBInit(true)(&opts)
	require.True(t, opts.skipDBInit)

	WithExtractor(&fakeExtractor{})(&opts)
	require.NotNil(t, opts.extractor)

	WithAsyncMemoryNum(0)(&opts)
	require.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	WithAsyncMemoryNum(2)(&opts)
	require.Equal(t, 2, opts.asyncMemoryNum)

	WithMemoryQueueSize(0)(&opts)
	require.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)
	WithMemoryQueueSize(3)(&opts)
	require.Equal(t, 3, opts.memoryQueueSize)

	WithMemoryJobTimeout(time.Second)(&opts)
	require.Equal(t, time.Second, opts.memoryJobTimeout)

	WithCustomTool(
		memory.ClearToolName,
		func() tool.Tool { return nil },
	)(&opts)
	_, ok := opts.toolCreators[memory.ClearToolName]
	require.True(t, ok)
	_, ok = opts.enabledTools[memory.ClearToolName]
	require.True(t, ok)

	WithCustomTool("bad_tool", func() tool.Tool { return nil })(&opts)
	WithCustomTool(memory.ClearToolName, nil)(&opts)

	opts2 := ServiceOpts{}
	WithToolEnabled(memory.LoadToolName, true)(&opts2)
	_, ok = opts2.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	require.True(t, opts2.userExplicitlySet[memory.LoadToolName])

	WithToolEnabled(memory.LoadToolName, false)(&opts2)
	_, ok = opts2.enabledTools[memory.LoadToolName]
	require.False(t, ok)
	require.True(t, opts2.userExplicitlySet[memory.LoadToolName])

	WithToolEnabled("bad_tool", true)(&opts2)
}

func TestNewService_SkipDBInit(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NoError(t, svc.Close())
}

func TestService_EnqueueAutoMemoryJob_NoWorker(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestService_Close_NilDB(t *testing.T) {
	noop := &Service{}
	require.NoError(t, noop.Close())
}

func TestParseTopics(t *testing.T) {
	out, err := parseTopics("")
	require.NoError(t, err)
	require.Nil(t, out)

	out, err = parseTopics("{")
	require.Error(t, err)
	require.Nil(t, out)
}

func TestNewService_InitDBError(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()
	require.NoError(t, db.Close())

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.Error(t, err)
	require.Nil(t, svc)
}
