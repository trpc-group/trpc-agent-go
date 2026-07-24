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

func setupUpdateMemoryRotation(
	t *testing.T,
	softDelete bool,
	deletedTarget bool,
) (*sql.DB, *Service, memory.Key, string) {
	t.Helper()

	db, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)

	var opts []ServiceOpt
	if softDelete {
		opts = append(opts, WithSoftDelete(true))
	}
	svc, err := NewService(db, opts...)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = svc.Close()
	})

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	var targetID string
	if deletedTarget {
		require.NoError(t, svc.AddMemory(ctx, userKey, "target", nil))
		entries, err := svc.ReadMemories(ctx, userKey, 0)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		targetID = entries[0].ID
		_, err = db.Exec(
			"UPDATE memories SET deleted_at = 1 WHERE memory_id = ?",
			targetID,
		)
		require.NoError(t, err)
	}

	require.NoError(t, svc.AddMemory(ctx, userKey, "source", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	var sourceID string
	for _, entry := range entries {
		if entry.Memory.Memory == "source" {
			sourceID = entry.ID
			break
		}
	}
	require.NotEmpty(t, sourceID)

	return db, svc, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: sourceID,
	}, targetID
}

func TestNewService_NilDB(t *testing.T) {
	svc, err := NewService(nil)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestServiceOpts_SearchOptions(t *testing.T) {
	opts := ServiceOpts{}

	WithMinSearchScore(0.6)(&opts)
	WithMaxResults(25)(&opts)
	WithMinSearchScore(-1)(&opts)
	WithMaxResults(-1)(&opts)

	require.Equal(t, 0.6, opts.searchMinScore)
	require.Equal(t, 25, opts.maxSearchResults)
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
	updateResult := &memory.UpdateResult{}
	oldID := memID
	require.NoError(t,
		svc.UpdateMemory(ctx, memKey, "Alice likes Go and SQL", nil, memory.WithUpdateResult(updateResult)))
	require.NotEmpty(t, updateResult.MemoryID)
	require.NotEqual(t, oldID, updateResult.MemoryID)
	memKey.MemoryID = updateResult.MemoryID

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

func TestService_UpdateMemory_SameIDUpdatesInPlace(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", []string{"old"}))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	oldID := entries[0].ID

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: oldID,
		},
		"alpha",
		[]string{"new"},
		memory.WithUpdateResult(result),
	))
	require.Equal(t, oldID, result.MemoryID)

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, oldID, entries[0].ID)
	require.Equal(t, []string{"new"}, entries[0].Memory.Topics)
}

func TestService_UpdateMemory_ZeroRowsAffectedDoesNotReportSuccess(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "alpha", []string{"old"}))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	_, err = db.Exec(`
CREATE TRIGGER ignore_memory_updates
BEFORE UPDATE ON memories
BEGIN
	SELECT RAISE(IGNORE);
END`)
	require.NoError(t, err)

	result := &memory.UpdateResult{MemoryID: "unchanged"}
	err = svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: entries[0].ID,
		},
		"alpha",
		[]string{"new"},
		memory.WithUpdateResult(result),
	)
	require.Error(t, err)
	require.Equal(t, "unchanged", result.MemoryID)
}

func TestService_UpdateMemory_ActiveIDConflictPreservesEntries(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "source", []string{"source"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "target", []string{"target"}))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var sourceID string
	for _, entry := range entries {
		if entry.Memory.Memory == "source" {
			sourceID = entry.ID
		}
	}
	require.NotEmpty(t, sourceID)

	result := &memory.UpdateResult{MemoryID: "unchanged"}
	err = svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: sourceID,
		},
		"target",
		[]string{"target"},
		memory.WithUpdateResult(result),
	)
	require.Error(t, err)
	require.Equal(t, "unchanged", result.MemoryID)

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	entriesByID := make(map[string]*memory.Entry, len(entries))
	for _, entry := range entries {
		entriesByID[entry.ID] = entry
	}
	require.Contains(t, entriesByID, sourceID)
	require.Equal(t, "source", entriesByID[sourceID].Memory.Memory)
	require.Equal(t, []string{"source"}, entriesByID[sourceID].Memory.Topics)
}

func TestService_UpdateMemory_SoftDeleteRotationKeepsSourceTombstone(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "source", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	sourceID := entries[0].ID
	sourceCreatedAt := entries[0].CreatedAt

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: sourceID,
		},
		"target",
		nil,
		memory.WithUpdateResult(result),
	))
	require.NotEqual(t, sourceID, result.MemoryID)

	var tombstones int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE app_name = ? AND user_id = ? AND memory_id = ? AND deleted_at IS NOT NULL",
		userKey.AppName,
		userKey.UserID,
		sourceID,
	).Scan(&tombstones)
	require.NoError(t, err)
	require.Equal(t, 1, tombstones)

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, result.MemoryID, entries[0].ID)
	require.True(t, entries[0].CreatedAt.Equal(sourceCreatedAt))
}

func TestService_UpdateMemory_SoftDeletedTargetIsRevived(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "target", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	targetID := entries[0].ID
	targetCreatedAt := entries[0].CreatedAt
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: targetID,
	}))

	require.NoError(t, svc.AddMemory(ctx, userKey, "source", nil))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	sourceID := entries[0].ID

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: sourceID,
		},
		"target",
		nil,
		memory.WithUpdateResult(result),
	))
	require.Equal(t, targetID, result.MemoryID)

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, targetID, entries[0].ID)
	require.Equal(t, "target", entries[0].Memory.Memory)
	require.True(t, entries[0].CreatedAt.Equal(targetCreatedAt))
}

func TestService_UpdateMemory_HardDeleteReplacesSoftDeletedTarget(t *testing.T) {
	db, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "target", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	targetID := entries[0].ID
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: targetID,
	}))

	hardDeleteSvc, err := NewService(db)
	require.NoError(t, err)
	require.NoError(t, hardDeleteSvc.AddMemory(ctx, userKey, "source", nil))
	entries, err = hardDeleteSvc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var (
		sourceID        string
		sourceCreatedAt time.Time
	)
	for _, entry := range entries {
		if entry.Memory.Memory == "source" {
			sourceID = entry.ID
			sourceCreatedAt = entry.CreatedAt
		}
	}
	require.NotEmpty(t, sourceID)

	result := &memory.UpdateResult{}
	require.NoError(t, hardDeleteSvc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: sourceID,
		},
		"target",
		nil,
		memory.WithUpdateResult(result),
	))
	require.Equal(t, targetID, result.MemoryID)

	entries, err = hardDeleteSvc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, targetID, entries[0].ID)
	require.True(t, entries[0].CreatedAt.Equal(sourceCreatedAt))
}

func TestService_UpdateMemory_RotationErrorsRollback(t *testing.T) {
	tests := []struct {
		name          string
		softDelete    bool
		deletedTarget bool
		arrange       func(*testing.T, *sql.DB, memory.Key, string)
		want          string
	}{
		{
			name:          "target query",
			softDelete:    true,
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, targetID string) {
				_, err := db.Exec(
					"UPDATE memories SET deleted_at = 'invalid' WHERE memory_id = ?",
					targetID,
				)
				require.NoError(t, err)
			},
			want: "check rotated memory target",
		},
		{
			name:          "target data",
			softDelete:    true,
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, targetID string) {
				_, err := db.Exec(
					"UPDATE memories SET memory_data = 'invalid' WHERE memory_id = ?",
					targetID,
				)
				require.NoError(t, err)
			},
			want: "unmarshal rotated memory target",
		},
		{
			name:          "revive target exec",
			softDelete:    true,
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER fail_memory_update
BEFORE UPDATE ON memories
BEGIN
	SELECT RAISE(ABORT, 'update failed');
END`)
				require.NoError(t, err)
			},
			want: "revive rotated memory target",
		},
		{
			name:          "revive target zero rows",
			softDelete:    true,
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER ignore_memory_update
BEFORE UPDATE ON memories
BEGIN
	SELECT RAISE(IGNORE);
END`)
				require.NoError(t, err)
			},
			want: "not found",
		},
		{
			name:          "delete target exec",
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER fail_memory_delete
BEFORE DELETE ON memories
BEGIN
	SELECT RAISE(ABORT, 'delete failed');
END`)
				require.NoError(t, err)
			},
			want: "delete rotated memory target",
		},
		{
			name:          "delete target zero rows",
			deletedTarget: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER ignore_memory_delete
BEFORE DELETE ON memories
BEGIN
	SELECT RAISE(IGNORE);
END`)
				require.NoError(t, err)
			},
			want: "not found",
		},
		{
			name: "insert target",
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER fail_memory_insert
BEFORE INSERT ON memories
BEGIN
	SELECT RAISE(ABORT, 'insert failed');
END`)
				require.NoError(t, err)
			},
			want: "insert rotated memory target",
		},
		{
			name:       "remove source exec",
			softDelete: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER fail_memory_update
BEFORE UPDATE ON memories
BEGIN
	SELECT RAISE(ABORT, 'update failed');
END`)
				require.NoError(t, err)
			},
			want: "remove rotated memory source",
		},
		{
			name:       "remove source zero rows",
			softDelete: true,
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				_, err := db.Exec(`
CREATE TRIGGER ignore_memory_update
BEFORE UPDATE ON memories
BEGIN
	SELECT RAISE(IGNORE);
END`)
				require.NoError(t, err)
			},
			want: "not found",
		},
		{
			name: "commit",
			arrange: func(t *testing.T, db *sql.DB, _ memory.Key, _ string) {
				db.SetMaxOpenConns(1)
				_, err := db.Exec("PRAGMA foreign_keys = ON")
				require.NoError(t, err)
				_, err = db.Exec("CREATE TABLE rotation_parent (id INTEGER PRIMARY KEY)")
				require.NoError(t, err)
				_, err = db.Exec(`
CREATE TABLE rotation_child (
	parent_id INTEGER,
	FOREIGN KEY (parent_id) REFERENCES rotation_parent(id)
		DEFERRABLE INITIALLY DEFERRED
)`)
				require.NoError(t, err)
				_, err = db.Exec(`
CREATE TRIGGER fail_rotation_commit
AFTER INSERT ON memories
BEGIN
	INSERT INTO rotation_child (parent_id) VALUES (1);
END`)
				require.NoError(t, err)
			},
			want: "commit rotated memory transaction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, svc, key, targetID := setupUpdateMemoryRotation(
				t,
				tt.softDelete,
				tt.deletedTarget,
			)
			tt.arrange(t, db, key, targetID)

			result := &memory.UpdateResult{MemoryID: "unchanged"}
			err := svc.UpdateMemory(
				context.Background(),
				key,
				"target",
				nil,
				memory.WithUpdateResult(result),
			)
			require.ErrorContains(t, err, tt.want)
			require.Equal(t, "unchanged", result.MemoryID)

			var sourceRows int
			err = db.QueryRow(
				"SELECT COUNT(*) FROM memories WHERE memory_id = ? AND deleted_at IS NULL",
				key.MemoryID,
			).Scan(&sourceRows)
			require.NoError(t, err)
			require.Equal(t, 1, sourceRows)
		})
	}
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
	_, ok = opts.userExplicitlySet[memory.LoadToolName]
	require.True(t, ok)

	WithCustomTool("bad_tool", func() tool.Tool { return nil })(&opts)
	WithCustomTool(memory.LoadToolName, nil)(&opts)

	WithToolEnabled(memory.LoadToolName, true)(&opts)
	_, ok = opts.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	_, ok = opts.userExplicitlySet[memory.LoadToolName]
	require.True(t, ok)

	opts2 := ServiceOpts{}
	WithToolEnabled(memory.LoadToolName, true)(&opts2)
	_, ok = opts2.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	_, ok = opts2.userExplicitlySet[memory.LoadToolName]
	require.True(t, ok)
	WithToolEnabled("bad_tool", true)(&opts2)

	WithAutoMemoryExposedTools(memory.AddToolName)(&opts2)
	_, ok = opts2.toolExposed[memory.AddToolName]
	require.True(t, ok)
	_, ok = opts2.toolHidden[memory.AddToolName]
	require.False(t, ok)

	WithToolExposed(memory.AddToolName, false)(&opts2)
	_, ok = opts2.toolExposed[memory.AddToolName]
	require.False(t, ok)
	_, ok = opts2.toolHidden[memory.AddToolName]
	require.True(t, ok)

	WithAutoMemoryExposedTools("bad_tool")(&opts2)
	_, ok = opts2.toolExposed["bad_tool"]
	require.False(t, ok)

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
