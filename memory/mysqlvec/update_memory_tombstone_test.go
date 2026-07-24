//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
			memoryStr: "source",
		},
		{
			name:      "rotated identity",
			memoryStr: "target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupMockDB(t)
			svc := setupMockService(
				t,
				db,
				mock,
				WithSkipDBInit(true),
				WithSoftDelete(false),
			)
			t.Cleanup(func() {
				require.NoError(t, mock.ExpectationsWereMet())
				_ = svc.Close()
			})

			key := memory.Key{
				AppName:  "app",
				UserID:   "user",
				MemoryID: "source-id",
			}
			mock.ExpectQuery("SELECT memory_id, app_name, user_id.*deleted_at IS NULL").
				WithArgs(key.MemoryID, key.AppName, key.UserID).
				WillReturnRows(sqlmock.NewRows(memCols))

			result := &memory.UpdateResult{MemoryID: "unchanged"}
			err := svc.UpdateMemory(
				context.Background(),
				key,
				tt.memoryStr,
				nil,
				memory.WithUpdateResult(result),
			)
			require.ErrorContains(t, err, "not found")
			require.Equal(t, "unchanged", result.MemoryID)
		})
	}
}

func TestService_UpdateMemory_HardDeleteRollsBackWhenSourceBecomesTombstone(
	t *testing.T,
) {
	db, mock := setupMockDB(t)
	svc := setupMockService(
		t,
		db,
		mock,
		WithSkipDBInit(true),
		WithSoftDelete(false),
	)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = svc.Close()
	})

	key := memory.Key{
		AppName:  "app",
		UserID:   "user",
		MemoryID: "source-id",
	}
	expectUpdateLoad(mock, key, false)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT deleted_at IS NULL FROM memories.*FOR UPDATE").
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM memories.*deleted_at IS NULL").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	result := &memory.UpdateResult{MemoryID: "unchanged"}
	err := svc.UpdateMemory(
		context.Background(),
		key,
		"target",
		nil,
		memory.WithUpdateResult(result),
	)
	require.ErrorContains(t, err, "not found")
	require.Equal(t, "unchanged", result.MemoryID)
}
