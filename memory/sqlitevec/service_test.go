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
	"fmt"
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
	updateResult := &memory.UpdateResult{}
	require.NoError(t,
		svc.UpdateMemory(ctx, memKey, "gamma", []string{"updated"}, memory.WithUpdateResult(updateResult)))
	memKey.MemoryID = updateResult.MemoryID

	results, err := svc.SearchMemories(ctx, userKey, "gamma")
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "gamma", results[0].Memory.Memory)

	require.NoError(t, svc.DeleteMemory(ctx, memKey))
	got, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestService_EpisodicMetadataRoundTrip(t *testing.T) {
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
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"alpha",
		[]string{"travel"},
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice", "Bob"},
			Location:     "Kyoto",
		}),
	))

	got, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Memory)
	require.NotNil(t, got[0].Memory.EventTime)
	require.Equal(t, memory.KindEpisode, got[0].Memory.Kind)
	require.Equal(t, eventTime, *got[0].Memory.EventTime)
	require.Equal(t, []string{"Alice", "Bob"}, got[0].Memory.Participants)
	require.Equal(t, "Kyoto", got[0].Memory.Location)
}

func TestService_UpdateMemory_PreservesMetadataWhenNotProvided(t *testing.T) {
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
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"alpha",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice"},
			Location:     "Kyoto",
		}),
	))

	got, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)

	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: got[0].ID,
	}
	require.NoError(t, svc.UpdateMemory(ctx, memKey, "alpha", []string{"updated"}))

	got, err = svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, memory.KindEpisode, got[0].Memory.Kind)
	require.NotNil(t, got[0].Memory.EventTime)
	require.Equal(t, eventTime, *got[0].Memory.EventTime)
	require.Equal(t, []string{"Alice"}, got[0].Memory.Participants)
	require.Equal(t, "Kyoto", got[0].Memory.Location)
	require.Equal(t, []string{"updated"}, got[0].Memory.Topics)
}

func TestService_UpdateMemory_SameIdentityKeepsID(t *testing.T) {
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

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", []string{"old"}))

	got, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)

	oldID := got[0].ID
	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: oldID,
	}
	updateResult := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memKey,
		"alpha",
		[]string{"new"},
		memory.WithUpdateResult(updateResult),
	))
	require.Equal(t, oldID, updateResult.MemoryID)

	got, err = svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, oldID, got[0].ID)
	require.Equal(t, []string{"new"}, got[0].Memory.Topics)
}

func TestService_Search_WithEpisodicOptions(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
		WithMaxResults(10),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	day1 := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"alpha",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &day2,
		}),
	))
	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"alpha older",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &day1,
		}),
	))
	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"alpha fact",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind: memory.KindFact,
		}),
	))

	results, err := svc.SearchMemories(
		ctx,
		userKey,
		"alpha",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:            "alpha",
			Kind:             memory.KindEpisode,
			TimeAfter:        &day1,
			OrderByEventTime: true,
			KindFallback:     true,
			MaxResults:       10,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.Equal(t, memory.KindEpisode, results[0].Memory.Kind)
	require.NotNil(t, results[0].Memory.EventTime)
	require.Equal(t, day1, *results[0].Memory.EventTime)
	require.Equal(t, memory.KindEpisode, results[1].Memory.Kind)
	require.NotNil(t, results[1].Memory.EventTime)
	require.Equal(t, day2, *results[1].Memory.EventTime)
	require.Equal(t, memory.KindFact, results[2].Memory.Kind)
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

func TestEnsureSchemaColumns(t *testing.T) {
	t.Run("accepts table with all episodic columns", func(t *testing.T) {
		db, cleanup := openTempSQLiteDB(t)
		defer cleanup()

		_, err := db.Exec(`
CREATE TABLE memories (
  memory_id TEXT,
  embedding BLOB,
  app_name TEXT,
  user_id TEXT,
  created_at INTEGER,
  updated_at INTEGER,
  deleted_at INTEGER,
  memory_content TEXT,
  topics TEXT,
  memory_kind TEXT,
  event_time INTEGER,
  participants TEXT,
  location TEXT
)`)
		require.NoError(t, err)

		svc := &Service{db: db, tableName: "memories"}
		require.NoError(t, svc.ensureSchemaColumns(context.Background()))
	})

	t.Run("rejects outdated table with missing episodic columns", func(t *testing.T) {
		db, cleanup := openTempSQLiteDB(t)
		defer cleanup()

		_, err := db.Exec(`
CREATE TABLE memories (
  memory_id TEXT,
  embedding BLOB,
  app_name TEXT,
  user_id TEXT,
  created_at INTEGER,
  updated_at INTEGER,
  deleted_at INTEGER,
  memory_content TEXT,
  topics TEXT
)`)
		require.NoError(t, err)

		svc := &Service{db: db, tableName: "memories"}
		err = svc.ensureSchemaColumns(context.Background())
		require.Error(t, err)
		require.ErrorContains(t, err, "outdated schema")
		require.ErrorContains(t, err, "event_time")
		require.ErrorContains(t, err, "location")
		require.ErrorContains(t, err, "memory_kind")
		require.ErrorContains(t, err, "participants")
	})
}

func TestNewService_MigratesLegacySchema(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	vecAuto()
	createLegacyMemoriesTable(t, db)
	insertLegacyMemoryRow(t, db, "memories", "m1")

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	entries, err := svc.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		10,
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "m1", entries[0].ID)
	require.Equal(t, "alpha", entries[0].Memory.Memory)
	require.Equal(t, []string{"pref"}, entries[0].Memory.Topics)
	require.Equal(t, memory.KindFact, entries[0].Memory.Kind)
	require.Nil(t, entries[0].Memory.EventTime)
	require.Empty(t, entries[0].Memory.Participants)
	require.Empty(t, entries[0].Memory.Location)
	require.NoError(t, svc.ensureSchemaColumns(context.Background()))
	require.False(
		t,
		sqliteTableExists(
			t,
			db,
			"memories"+schemaBackupName,
		),
	)
}

func TestNewService_RestoresSchemaBackup(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	vecAuto()
	createSchemaBackupTable(t, db, "memories"+schemaBackupName)
	insertSchemaBackupRow(t, db, "memories"+schemaBackupName, "m1")

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	entries, err := svc.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		10,
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "m1", entries[0].ID)
	require.Equal(t, "alpha", entries[0].Memory.Memory)
	require.Equal(t, []string{"pref"}, entries[0].Memory.Topics)
	require.Equal(t, memory.KindFact, entries[0].Memory.Kind)
	require.False(
		t,
		sqliteTableExists(
			t,
			db,
			"memories"+schemaBackupName,
		),
	)
}

func TestNewService_RestoresSchemaBackupOverExistingTable(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	vecAuto()
	createCurrentMemoriesTable(t, db)
	createSchemaBackupTable(t, db, "memories"+schemaBackupName)
	insertSchemaBackupRow(t, db, "memories"+schemaBackupName, "m1")

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	entries, err := svc.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		10,
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "m1", entries[0].ID)
	require.Equal(t, "alpha", entries[0].Memory.Memory)
	require.False(
		t,
		sqliteTableExists(
			t,
			db,
			"memories"+schemaBackupName,
		),
	)
}

func createLegacyMemoriesTable(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.Exec(`
CREATE VIRTUAL TABLE memories USING vec0(
  memory_id text primary key,
  embedding float[2] distance_metric=cosine,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  +memory_content text,
  +topics text
)`)
	require.NoError(t, err)
}

func createCurrentMemoriesTable(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.Exec(`
CREATE VIRTUAL TABLE memories USING vec0(
  memory_id text primary key,
  embedding float[2] distance_metric=cosine,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  +memory_content text,
  +topics text,
  +memory_kind text,
  +event_time integer,
  +participants text,
  +location text
)`)
	require.NoError(t, err)
}

func insertLegacyMemoryRow(
	t *testing.T,
	db *sql.DB,
	tableName string,
	memoryID string,
) {
	t.Helper()

	blob, err := vecSerializeFloat32([]float32{1, 0})
	require.NoError(t, err)

	query := fmt.Sprintf(
		`INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics
) VALUES (?, vec_f32(?), ?, ?, ?, ?, ?, ?, ?)`,
		tableName,
	)
	_, err = db.Exec(
		query,
		memoryID,
		blob,
		"app",
		"u1",
		int64(1),
		int64(2),
		int64(0),
		"alpha",
		`["pref"]`,
	)
	require.NoError(t, err)
}

func createSchemaBackupTable(
	t *testing.T,
	db *sql.DB,
	tableName string,
) {
	t.Helper()

	_, err := db.Exec(fmt.Sprintf(sqlCreateSchemaBackupTable, tableName))
	require.NoError(t, err)
}

func insertSchemaBackupRow(
	t *testing.T,
	db *sql.DB,
	tableName string,
	memoryID string,
) {
	t.Helper()

	blob, err := vecSerializeFloat32([]float32{1, 0})
	require.NoError(t, err)

	query := fmt.Sprintf(
		`INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind,
event_time, participants, location
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName,
	)
	_, err = db.Exec(
		query,
		memoryID,
		blob,
		"app",
		"u1",
		int64(1),
		int64(2),
		int64(0),
		"alpha",
		`["pref"]`,
		nil,
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
}

func sqliteTableExists(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
WHERE type IN ('table', 'view') AND name = ?`,
		tableName,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

func TestSearchHelperFunctions(t *testing.T) {
	day1 := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)
	results := []*memory.Entry{
		nil,
		{Memory: nil},
		{
			ID: "episode-1",
			Memory: &memory.Memory{
				Kind:      memory.KindEpisode,
				EventTime: &day1,
			},
		},
		{
			ID: "episode-2",
			Memory: &memory.Memory{
				Kind:      memory.KindEpisode,
				EventTime: &day2,
			},
		},
		{
			ID: "fact",
			Memory: &memory.Memory{
				Kind: memory.KindFact,
			},
		},
	}

	filtered := applySearchFilters(results, memory.SearchOptions{
		Kind:      memory.KindEpisode,
		TimeAfter: &day2,
	})
	require.Len(t, filtered, 1)
	require.Equal(t, "episode-2", filtered[0].ID)

	require.Equal(t, 5, resolveSearchLimit(5, 0))
	require.Equal(t, 7, resolveSearchLimit(5, 7))
	require.Equal(t, 9, resolveSearchCandidateLimit(5, 0, 9, memory.SearchOptions{
		Kind: memory.KindEpisode,
	}))
	require.Equal(t, 7, resolveSearchCandidateLimit(5, 7, 9, memory.SearchOptions{}))
	require.Nil(t, metadataEventTimeNS(nil))
	require.Equal(t, day1.UnixNano(), metadataEventTimeNS(&day1))
	require.Nil(t, metadataLocationValue("   "))
	require.Equal(t, "Kyoto", metadataLocationValue(" Kyoto "))
}

func TestScanEntryAndStringSliceHelpers(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	rows, err := db.Query(
		`SELECT 'id', 'alpha', '["topic"]', '', NULL, '[" Bob ","bob"]', ' Kyoto ', 0, 0`,
	)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())

	entry, err := scanEntry(rows, "app", "user")
	require.NoError(t, err)
	require.Equal(t, memory.KindFact, entry.Memory.Kind)
	require.Equal(t, []string{"Bob"}, entry.Memory.Participants)
	require.Equal(t, "Kyoto", entry.Memory.Location)

	rows, err = db.Query(
		`SELECT 'id', 'alpha', '["topic"]', '', NULL, 'not-json', 'Kyoto', 0, 0`,
	)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())

	_, err = scanEntry(rows, "app", "user")
	require.Error(t, err)
	require.ErrorContains(t, err, "unmarshal string slice")

	encoded, err := marshalStringSlice([]string{"Alice"})
	require.NoError(t, err)
	require.Equal(t, `["Alice"]`, encoded)

	decoded, err := parseStringSlice(encoded)
	require.NoError(t, err)
	require.Equal(t, []string{"Alice"}, decoded)

	decoded, err = parseTopics("")
	require.NoError(t, err)
	require.Nil(t, decoded)
}

func TestServiceResolveSearchCandidateLimit_CountsStoredMemories(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
		WithMaxResults(1),
		WithMemoryLimit(10),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "beta", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "gamma", nil))

	limit, err := svc.resolveSearchCandidateLimit(ctx, userKey, memory.SearchOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, limit)

	count, err := svc.countMemories(ctx, userKey)
	require.NoError(t, err)
	require.Equal(t, 3, count)

	limit, err = svc.resolveSearchCandidateLimit(ctx, userKey, memory.SearchOptions{
		Kind: memory.KindEpisode,
	})
	require.NoError(t, err)
	require.Equal(t, 10, limit)
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
