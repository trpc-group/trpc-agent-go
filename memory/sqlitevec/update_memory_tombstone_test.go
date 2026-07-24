//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlitevec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

type tombstoneOnEmbeddingEmbedder struct {
	dimension int
	callback  func() error
}

func (e *tombstoneOnEmbeddingEmbedder) GetEmbedding(
	_ context.Context,
	text string,
) ([]float64, error) {
	if err := e.callback(); err != nil {
		return nil, err
	}
	embedding := make([]float64, e.dimension)
	if text == "beta" {
		embedding[1] = 1
	} else {
		embedding[0] = 1
	}
	return embedding, nil
}

func (e *tombstoneOnEmbeddingEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (e *tombstoneOnEmbeddingEmbedder) GetDimensions() int {
	return e.dimension
}

func TestService_UpdateMemory_HardDeleteRejectsTombstoneSource(t *testing.T) {
	tests := []struct {
		name      string
		memoryStr string
	}{
		{
			name:      "same identity",
			memoryStr: "alpha",
		},
		{
			name:      "rotated identity",
			memoryStr: "beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vecAuto()
			db, cleanup := openTempSQLiteDB(t)
			t.Cleanup(cleanup)

			softDeleteSvc, err := NewService(
				db,
				WithEmbedder(&mockEmbedder{dimension: 2}),
				WithIndexDimension(2),
				WithSoftDelete(true),
			)
			require.NoError(t, err)
			userKey := memory.UserKey{AppName: "app", UserID: "user"}
			require.NoError(t, softDeleteSvc.AddMemory(
				context.Background(),
				userKey,
				"alpha",
				nil,
			))
			entries, err := softDeleteSvc.ReadMemories(
				context.Background(),
				userKey,
				0,
			)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			sourceKey := memory.Key{
				AppName:  userKey.AppName,
				UserID:   userKey.UserID,
				MemoryID: entries[0].ID,
			}
			require.NoError(t, softDeleteSvc.DeleteMemory(
				context.Background(),
				sourceKey,
			))

			hardDeleteSvc, err := NewService(
				db,
				WithEmbedder(&mockEmbedder{dimension: 2}),
				WithIndexDimension(2),
				WithSoftDelete(false),
			)
			require.NoError(t, err)
			result := &memory.UpdateResult{MemoryID: "unchanged"}
			err = hardDeleteSvc.UpdateMemory(
				context.Background(),
				sourceKey,
				tt.memoryStr,
				nil,
				memory.WithUpdateResult(result),
			)
			require.ErrorContains(t, err, "not found")
			require.Equal(t, "unchanged", result.MemoryID)

			var (
				memoryContent string
				deletedAt     int64
			)
			err = db.QueryRow(
				"SELECT memory_content, deleted_at FROM memories "+
					"WHERE app_name = ? AND user_id = ? AND memory_id = ?",
				sourceKey.AppName,
				sourceKey.UserID,
				sourceKey.MemoryID,
			).Scan(&memoryContent, &deletedAt)
			require.NoError(t, err)
			require.NotEqual(t, notDeletedAtNs, deletedAt)
			require.Equal(t, "alpha", memoryContent)

			var count int
			err = db.QueryRow(
				"SELECT COUNT(*) FROM memories WHERE app_name = ? AND user_id = ?",
				sourceKey.AppName,
				sourceKey.UserID,
			).Scan(&count)
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}

func TestService_UpdateMemory_HardDeleteRollsBackWhenSourceBecomesTombstone(
	t *testing.T,
) {
	vecAuto()
	db, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)

	svc, err := NewService(
		db,
		WithEmbedder(&mockEmbedder{dimension: 2}),
		WithIndexDimension(2),
		WithSoftDelete(false),
	)
	require.NoError(t, err)
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, svc.AddMemory(
		context.Background(),
		userKey,
		"alpha",
		nil,
	))
	entries, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	sourceKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}

	svc.opts.embedder = &tombstoneOnEmbeddingEmbedder{
		dimension: 2,
		callback: func() error {
			_, updateErr := db.Exec(
				"UPDATE memories SET deleted_at = 1 "+
					"WHERE app_name = ? AND user_id = ? AND memory_id = ?",
				sourceKey.AppName,
				sourceKey.UserID,
				sourceKey.MemoryID,
			)
			return updateErr
		},
	}

	result := &memory.UpdateResult{MemoryID: "unchanged"}
	err = svc.UpdateMemory(
		context.Background(),
		sourceKey,
		"beta",
		nil,
		memory.WithUpdateResult(result),
	)
	require.ErrorContains(t, err, "not found")
	require.Equal(t, "unchanged", result.MemoryID)

	var deletedAt int64
	err = db.QueryRow(
		"SELECT deleted_at FROM memories "+
			"WHERE app_name = ? AND user_id = ? AND memory_id = ?",
		sourceKey.AppName,
		sourceKey.UserID,
		sourceKey.MemoryID,
	).Scan(&deletedAt)
	require.NoError(t, err)
	require.Equal(t, int64(1), deletedAt)

	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE app_name = ? AND user_id = ?",
		sourceKey.AppName,
		sourceKey.UserID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
