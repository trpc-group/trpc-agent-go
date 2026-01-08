//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRefreshSessionSummaryTTLs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(time.Hour))
	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user-123", SessionID: "session-456"}

	// Mock successful update
	mock.ExpectExec(`UPDATE session_summaries`).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 5)) // 5 rows affected

	err = s.refreshSessionSummaryTTLs(ctx, key)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshSessionSummaryTTLs_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(time.Hour))
	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user-123", SessionID: "session-456"}

	// Mock update error
	mock.ExpectExec(`UPDATE session_summaries`).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("update error"))

	err = s.refreshSessionSummaryTTLs(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refresh session summary TTLs failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}
