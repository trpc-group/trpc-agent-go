//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRefreshSessionTTL_Success(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{sessionTTL: time.Hour})
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock the UPDATE query for refreshing TTL.
	mock.ExpectExec("UPDATE session_states").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.refreshSessionTTL(context.Background(), key)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshSessionTTL_Error(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{sessionTTL: time.Hour})
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock UPDATE error.
	mock.ExpectExec("UPDATE session_states").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnError(assert.AnError)

	err := s.refreshSessionTTL(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh session TTL failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
