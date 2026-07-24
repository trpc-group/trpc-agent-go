//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

func TestService_AddMemoryAfterSoftDeleteRotationRevivesSource(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(
		t,
		db,
		mock,
		WithSkipDBInit(true),
		WithSoftDelete(true),
		WithMemoryLimit(0),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}
	sourceMemory := "source"
	targetMemory := "target"
	sourceID := imemory.GenerateMemoryID(
		&memory.Memory{Memory: sourceMemory},
		userKey.AppName,
		userKey.UserID,
	)
	sourceKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: sourceID,
	}
	targetID := imemory.GenerateMemoryID(
		&memory.Memory{Memory: targetMemory, Kind: memory.KindFact},
		userKey.AppName,
		userKey.UserID,
	)

	expectAddMemoryRevivesTombstone(mock)
	require.NoError(t, svc.AddMemory(ctx, userKey, sourceMemory, nil))

	mock.ExpectQuery("SELECT memory_data.*AND deleted_at IS NULL").
		WithArgs(sourceKey.MemoryID, sourceKey.AppName, sourceKey.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow(marshalUpdateTestEntry(t, sourceKey, sourceMemory, nil)))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT deleted_at IS NULL, memory_data FROM.*FOR UPDATE").
		WithArgs(targetID, sourceKey.AppName, sourceKey.UserID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE.*SET deleted_at").
		WithArgs(
			sqlmock.AnyArg(),
			sourceKey.MemoryID,
			sourceKey.AppName,
			sourceKey.UserID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		sourceKey,
		targetMemory,
		nil,
		memory.WithUpdateResult(result),
	))
	require.Equal(t, targetID, result.MemoryID)

	expectAddMemoryRevivesTombstone(mock)
	require.NoError(t, svc.AddMemory(ctx, userKey, sourceMemory, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectAddMemoryRevivesTombstone(mock sqlmock.Sqlmock) {
	mock.ExpectExec(
		"INSERT INTO.*ON CONFLICT \\(memory_id\\) DO UPDATE SET.*deleted_at = NULL",
	).
		WillReturnResult(sqlmock.NewResult(0, 1))
}
