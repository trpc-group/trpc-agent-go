//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

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
			db, cleanup := openTempSQLiteDB(t)
			t.Cleanup(cleanup)

			softDeleteSvc, err := NewService(db, WithSoftDelete(true))
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

			hardDeleteSvc, err := NewService(db, WithSoftDelete(false))
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
				memoryData []byte
				deletedAt  sql.NullInt64
			)
			err = db.QueryRow(
				"SELECT memory_data, deleted_at FROM memories "+
					"WHERE app_name = ? AND user_id = ? AND memory_id = ?",
				sourceKey.AppName,
				sourceKey.UserID,
				sourceKey.MemoryID,
			).Scan(&memoryData, &deletedAt)
			require.NoError(t, err)
			require.True(t, deletedAt.Valid)

			entry := &memory.Entry{}
			require.NoError(t, json.Unmarshal(memoryData, entry))
			require.Equal(t, "alpha", entry.Memory.Memory)

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
	db, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)

	svc, err := NewService(db, WithSoftDelete(false))
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

	_, err = db.Exec("CREATE TABLE update_test_source (memory_id TEXT NOT NULL)")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO update_test_source (memory_id) VALUES (?)",
		sourceKey.MemoryID,
	)
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TRIGGER soft_delete_source_after_target_insert
AFTER INSERT ON memories
WHEN NEW.memory_id != (
	SELECT memory_id FROM update_test_source LIMIT 1
)
BEGIN
	UPDATE memories
	SET deleted_at = 1
	WHERE memory_id = (
		SELECT memory_id FROM update_test_source LIMIT 1
	);
END`)
	require.NoError(t, err)

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

	var deletedAt sql.NullInt64
	err = db.QueryRow(
		"SELECT deleted_at FROM memories "+
			"WHERE app_name = ? AND user_id = ? AND memory_id = ?",
		sourceKey.AppName,
		sourceKey.UserID,
		sourceKey.MemoryID,
	).Scan(&deletedAt)
	require.NoError(t, err)
	require.False(t, deletedAt.Valid)

	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE app_name = ? AND user_id = ?",
		sourceKey.AppName,
		sourceKey.UserID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
