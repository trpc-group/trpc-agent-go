//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestGetSession_UnmarshalStateError tests error when unmarshaling state fails
func TestGetSession_UnmarshalStateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	// Mock query returning invalid JSON
	rows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow([]byte("invalid-json"), time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	sess, err := s.getSession(context.Background(), key, 0, time.Time{})
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetSession_QueryEventUnmarshalError tests error when unmarshaling event fails
func TestGetSession_QueryEventUnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	// Mock session state query
	stateData := SessionState{
		State:     session.StateMap{"key": []byte("value")},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(stateData)

	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, stateData.CreatedAt, stateData.UpdatedAt)

	mock.ExpectQuery("SELECT .+ FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	// Mock app states query
	mock.ExpectQuery("SELECT .+ FROM app_states").
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock user states query
	mock.ExpectQuery("SELECT .+ FROM user_states").
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock events query with invalid JSON
	eventRows := sqlmock.NewRows([]string{"event"}).
		AddRow([]byte("invalid-event-json"))

	mock.ExpectQuery("SELECT .+ FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(eventRows)

	sess, err := s.getSession(context.Background(), key, 0, time.Time{})
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "unmarshal event failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListSessions_UnmarshalStateError tests error when unmarshaling session state fails
func TestListSessions_UnmarshalStateError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user1",
	}

	// Mock ListAppStates query (called first in listSessions)
	mock.ExpectQuery("SELECT .+ FROM app_states").
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock ListUserStates query
	mock.ExpectQuery("SELECT .+ FROM user_states").
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock query returning invalid JSON
	rows := sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
		AddRow("sess1", []byte("invalid-json"), time.Now(), time.Now())

	mock.ExpectQuery("SELECT .+ FROM session_states").
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	sessions, err := s.listSessions(context.Background(), userKey, 0, time.Time{})
	assert.Error(t, err)
	assert.Nil(t, sessions)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAddEvent_QueryError tests error when getting session state fails
func TestAddEvent_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	evt := event.New("inv-1", "test-author")

	// Mock query returning error
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("database error"))

	err = s.addEvent(context.Background(), key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAddEvent_UnmarshalStateError tests error when unmarshaling session state fails
func TestAddEvent_UnmarshalStateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	evt := event.New("inv-1", "test-author")

	// Mock query returning invalid JSON
	rows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow([]byte("invalid-json"), sql.NullTime{Valid: false})

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(rows)

	err = s.addEvent(context.Background(), key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnforceEventLimit_NoEventsToDelete tests when there are fewer events than limit
func TestEnforceEventLimit_NoEventsToDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.sessionEventLimit = 100

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	mock.ExpectBegin()

	// Mock cutoff query returning no rows (fewer events than limit)
	mock.ExpectQuery("SELECT created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, s.opts.sessionEventLimit).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}))

	mock.ExpectCommit()

	tx, _ := db.Begin()
	err = s.enforceEventLimit(context.Background(), tx, key, time.Now())
	assert.NoError(t, err)

	tx.Commit()
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnforceEventLimit_QueryError tests error when getting cutoff time fails
func TestEnforceEventLimit_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.sessionEventLimit = 100

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	mock.ExpectBegin()

	// Mock cutoff query returning error
	mock.ExpectQuery("SELECT created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, s.opts.sessionEventLimit).
		WillReturnError(fmt.Errorf("database error"))

	mock.ExpectRollback()

	tx, _ := db.Begin()
	err = s.enforceEventLimit(context.Background(), tx, key, time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get cutoff time failed")

	tx.Rollback()
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRefreshSessionTTL_Error tests error when updating session TTL fails
func TestRefreshSessionTTL_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
	}

	mock.ExpectExec("UPDATE session_states").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("database error"))

	err = s.refreshSessionTTL(context.Background(), key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refresh session TTL failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventsList_EmptySessionKeys tests batch get with empty session keys
func TestGetEventsList_EmptySessionKeys(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	result, err := s.getEventsList(context.Background(), []session.Key{}, 0, time.Time{})
	assert.NoError(t, err)
	assert.Nil(t, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventsList_UnmarshalError tests batch get when unmarshal fails
func TestGetEventsList_UnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	// Mock query returning invalid JSON
	rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
		AddRow("app1", "user1", "sess1", []byte("invalid-json"))

	mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
		WithArgs("app1", "user1", "sess1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unmarshal event failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventsList_WithLimit tests batch get with event limit per session
func TestGetEventsList_WithLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	// Mock query returning events (will be limited)
	evt1 := event.New("inv-1", "author1")
	evt2 := event.New("inv-2", "author2")
	evt3 := event.New("inv-3", "author3")

	evt1Bytes, _ := json.Marshal(evt1)
	evt2Bytes, _ := json.Marshal(evt2)
	evt3Bytes, _ := json.Marshal(evt3)

	rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
		AddRow("app1", "user1", "sess1", evt3Bytes). // DESC order
		AddRow("app1", "user1", "sess1", evt2Bytes).
		AddRow("app1", "user1", "sess1", evt1Bytes)

	mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
		WithArgs("app1", "user1", "sess1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getEventsList(context.Background(), keys, 2, time.Time{})
	assert.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0], 2) // Limited to 2 events
	// Events are returned in DESC order: [evt3, evt2, evt1]
	// Take first 2: [evt3, evt2]
	// Then reverse to chronological order: [evt2, evt3]
	assert.Equal(t, "inv-2", result[0][0].InvocationID)
	assert.Equal(t, "inv-3", result[0][1].InvocationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventsList_NoLimit tests batch get without event limit
func TestGetEventsList_NoLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	// Mock query returning events (no limit)
	evt1 := event.New("inv-1", "author1")
	evt2 := event.New("inv-2", "author2")
	evt3 := event.New("inv-3", "author3")

	evt1Bytes, _ := json.Marshal(evt1)
	evt2Bytes, _ := json.Marshal(evt2)
	evt3Bytes, _ := json.Marshal(evt3)

	rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
		AddRow("app1", "user1", "sess1", evt3Bytes). // DESC order
		AddRow("app1", "user1", "sess1", evt2Bytes).
		AddRow("app1", "user1", "sess1", evt1Bytes)

	mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
		WithArgs("app1", "user1", "sess1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
	assert.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0], 3) // All 3 events returned (no limit)
	// Events are returned in DESC order: [evt3, evt2, evt1]
	// Then reverse to chronological order: [evt1, evt2, evt3]
	assert.Equal(t, "inv-1", result[0][0].InvocationID)
	assert.Equal(t, "inv-2", result[0][1].InvocationID)
	assert.Equal(t, "inv-3", result[0][2].InvocationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventsList_ScanError tests batch get when scan fails
func TestGetEventsList_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	// Mock query returning wrong number of columns (will cause scan error)
	rows := sqlmock.NewRows([]string{"app_name", "user_id"}). // Missing session_id and event
									AddRow("app1", "user1")

	mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
		WithArgs("app1", "user1", "sess1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
	assert.Error(t, err)
	assert.Nil(t, result)
	// The error comes from Scan, wrapped by batch get events failed
	assert.Contains(t, err.Error(), "batch get events failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetSummariesList_UnmarshalError tests batch get when unmarshal fails
func TestGetSummariesList_UnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	// Mock query returning invalid JSON
	rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}).
		AddRow("app1", "user1", "sess1", "filter1", []byte("invalid-json"))

	mock.ExpectQuery("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries").
		WithArgs("app1", "user1", "sess1", sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getSummariesList(context.Background(), keys)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unmarshal summary failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredForUser_SoftDelete tests cleanup for specific user with soft delete
func TestCleanupExpiredForUser_SoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = true

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user1",
	}

	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s.cleanupExpiredForUser(context.Background(), userKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredForUser_HardDelete tests cleanup for specific user with hard delete
func TestCleanupExpiredForUser_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user1",
	}

	mock.ExpectExec("DELETE FROM session_states").
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s.cleanupExpiredForUser(context.Background(), userKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredForUser_Error tests cleanup error handling
func TestCleanupExpiredForUser_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = true

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user1",
	}

	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	// Should not panic, just log error
	s.cleanupExpiredForUser(context.Background(), userKey)
	require.NoError(t, mock.ExpectationsWereMet())
}
