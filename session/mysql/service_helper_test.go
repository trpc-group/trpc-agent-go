//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestGetSession_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Prepare mock session state
	sessState := SessionState{
		ID: key.SessionID,
		State: session.StateMap{
			"key1": []byte(`"value1"`),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session (with time.Now() arg)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query events (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM ")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Equal(t, key.SessionID, sess.ID)
	assert.Equal(t, sessState.State, sess.State)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "nonexistent",
	}

	// Mock: Query returns no rows (using AnyArg for time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}))

	sess, err := s.GetSession(ctx, key)
	// Note: Current implementation returns (nil, nil) when session not found
	assert.NoError(t, err)
	assert.Nil(t, sess)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Prepare mock session state
	sessState := SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{"key1": []byte(`"value1"`)},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Prepare mock event
	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Hello, world!",
				},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)

	// Mock: Query events with limit
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow(key.AppName, key.UserID, key.SessionID, eventBytes))

	// Mock: Batch load summaries with data
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}))

	sess, err := s.GetSession(ctx, key, session.WithEventNum(10))
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Len(t, sess.Events, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithTrackEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	sessState := SessionState{
		ID: key.SessionID,
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session with tracks state.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query events (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	// Mock: Query track events.
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}
	trackBytes, _ := json.Marshal(trackEvent)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, "alpha", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"event"}).AddRow(trackBytes))

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotNil(t, sess.Tracks)
	alpha, ok := sess.Tracks[session.Track("alpha")]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
	assert.Equal(t, json.RawMessage(`"payload"`), alpha.Events[0].Payload)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithRefreshTTL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Prepare mock session state
	sessState := SessionState{
		ID: key.SessionID,
		State: session.StateMap{
			"key1": []byte(`"value1"`),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query events
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	// Mock: Refresh session TTL
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states")).
		WithArgs(
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Query session fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	sess, err := s.GetSession(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Prepare session state
	sessState := SessionState{
		ID:    "session-1",
		State: session.StateMap{"key1": []byte(`"value1"`)},
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: List app states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query session states for user
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", stateBytes, time.Now(), time.Now()))

	// Mock: Batch load events (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	// Mock: Batch load summaries (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_WithTrackEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: List app states (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Prepare session state with tracks index.
	sessState := SessionState{
		ID: "session-1",
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session states for user.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", stateBytes, time.Now(), time.Now()))

	// Mock: Batch load events (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	// Mock: Batch load summaries (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}))

	// Mock: Batch load track events.
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}
	trackBytes, _ := json.Marshal(trackEvent)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs(userKey.AppName, userKey.UserID, "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"event"}).AddRow(trackBytes))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NotNil(t, sessions[0].Tracks)
	alpha, ok := sessions[0].Tracks[session.Track("alpha")]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
	assert.Equal(t, json.RawMessage(`"payload"`), alpha.Events[0].Payload)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_WithMultipleSessions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("app-key", []byte(`"app-value"`)))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("user-key", []byte(`"user-value"`)))

	// Prepare session states
	sess1 := SessionState{ID: "session-1", State: session.StateMap{"s1": []byte(`"v1"`)}}
	sess2 := SessionState{ID: "session-2", State: session.StateMap{"s2": []byte(`"v2"`)}}
	state1Bytes, _ := json.Marshal(sess1)
	state2Bytes, _ := json.Marshal(sess2)

	// Mock: Query session states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", state1Bytes, time.Now(), time.Now()).
			AddRow("session-2", state2Bytes, time.Now(), time.Now()))

	// Mock: Batch load events
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	// Mock: Batch load summaries
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// Verify app state and user state are merged
	assert.Contains(t, sessions[0].State, "app:app-key")
	assert.Contains(t, sessions[0].State, "user:user-key")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: List app states fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	sessions, err := s.ListSessions(ctx, userKey)
	assert.Error(t, err)
	assert.Nil(t, sessions)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "", // Invalid: empty user ID
	}

	sessions, err := s.ListSessions(ctx, userKey)
	assert.Error(t, err)
	assert.Nil(t, sessions)
}

func TestAddTrackEvent_SessionNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "missing",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	// Mock: QueryRow returns ErrNoRows.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(sql.ErrNoRows)

	err = s.addTrackEvent(ctx, key, trackEvent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_ExpiredSessionWithTTL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"ttl"`),
		Timestamp: time.Now(),
	}

	sessState := SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: QueryRow returns expired session.
	expiredTime := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, expiredTime))

	// Mock: Transaction to update session and insert track event.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_track_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, "alpha",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = s.addTrackEvent(ctx, key, trackEvent)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_AppendError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}

	sessState := SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	err = s.addTrackEvent(ctx, key, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "track event is nil")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_UnmarshalStateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	// Invalid JSON for session state.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow([]byte("{invalid"), nil))

	err = s.addTrackEvent(ctx, key, trackEvent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session state failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_UpdateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	sessState := SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("update error"))
	mock.ExpectRollback()

	err = s.addTrackEvent(ctx, key, trackEvent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")
	assert.Contains(t, err.Error(), "store track event failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_InsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	sessState := SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_track_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, "alpha",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("insert error"))
	mock.ExpectRollback()

	err = s.addTrackEvent(ctx, key, trackEvent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert track event failed")
	assert.Contains(t, err.Error(), "store track event failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddEvent_SessionNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "nonexistent",
	}

	evt := event.New("inv-1", "author")

	// Mock: QueryRow returns ErrNoRows
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(sql.ErrNoRows)

	err = s.addEvent(ctx, key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddEvent_ExpiredSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		Done:    true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{Content: "test"}}},
	}
	evt.IsPartial = false

	sessState := SessionState{ID: key.SessionID, State: session.StateMap{}}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: QueryRow returns expired session
	expiredTime := time.Now().Add(-2 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, expiredTime))

	// Mock: Transaction to update session and insert event (will extend expiry)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = s.addEvent(ctx, key, evt)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddEvent_PartialEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{Object: model.ObjectTypeChatCompletion}
	evt.IsPartial = true // Partial event should not be inserted

	sessState := SessionState{ID: key.SessionID, State: session.StateMap{}}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: QueryRow
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	// Mock: Transaction to update session only (no event insert for partial)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// No event insert expectation
	mock.ExpectCommit()

	err = s.addEvent(ctx, key, evt)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshSessionTTL_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Update session TTL
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states")).
		WithArgs(
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = s.refreshSessionTTL(ctx, key)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSessionState(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDelete", true},
		{"HardDelete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete))
			ctx := context.Background()

			key := session.Key{
				AppName:   "test-app",
				UserID:    "user-123",
				SessionID: "session-456",
			}

			// Mock: Transaction for delete
			mock.ExpectBegin()
			if tt.softDelete {
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_summaries SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_events SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_track_events SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
			} else {
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_states")).
					WithArgs(key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_summaries")).
					WithArgs(key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_events")).
					WithArgs(key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_track_events")).
					WithArgs(key.AppName, key.UserID, key.SessionID).
					WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectCommit()

			err = s.deleteSessionState(ctx, key)
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGetTrackEvents_EmptyInputs(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	result, err := s.getTrackEvents(context.Background(), nil, nil, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetTrackEvents_SessionStatesCountMismatch(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{}, 0, time.Time{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "session states count mismatch")
}

func TestGetTrackEvents_InvalidTrackList(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte("{invalid"),
		},
	}

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 0, time.Time{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get track list failed")
}

func TestGetTrackEventsHelper_WithLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}

	event := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"limited"`),
		Timestamp: time.Now(),
	}
	eventBytes, _ := json.Marshal(event)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnRows(sqlmock.NewRows([]string{"event"}).AddRow(eventBytes))

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 1, time.Time{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	alpha := result[0][session.Track("alpha")]
	require.Len(t, alpha, 1)
	assert.Equal(t, json.RawMessage(`"limited"`), alpha[0].Payload)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTrackEventsHelper_NoLimit_ReversedOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}

	event1 := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"first"`),
		Timestamp: time.Now().Add(-time.Minute),
	}
	event2 := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"second"`),
		Timestamp: time.Now(),
	}
	event1Bytes, _ := json.Marshal(event1)
	event2Bytes, _ := json.Marshal(event2)

	// Rows come in reverse chronological order, but getTrackEvents should reverse them.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"event"}).
			AddRow(event2Bytes).
			AddRow(event1Bytes))

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	alpha := result[0][session.Track("alpha")]
	require.Len(t, alpha, 2)
	assert.Equal(t, json.RawMessage(`"first"`), alpha[0].Payload)
	assert.Equal(t, json.RawMessage(`"second"`), alpha[1].Payload)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTrackEventsHelper_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnError(fmt.Errorf("db error"))

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 1, time.Time{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "query track events failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTrackEventsHelper_UnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}

	// Invalid JSON for track event.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event FROM session_track_events")).
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnRows(sqlmock.NewRows([]string{"event"}).
			AddRow([]byte("{invalid")))

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 1, time.Time{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unmarshal track event failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCalculateExpiresAt(t *testing.T) {
	// Test with positive TTL
	ttl := 1 * time.Hour
	expiresAt := calculateExpiresAt(ttl)
	assert.NotNil(t, expiresAt)
	assert.True(t, expiresAt.After(time.Now()))

	// Test with zero TTL
	expiresAt = calculateExpiresAt(0)
	assert.Nil(t, expiresAt)

	// Test with negative TTL
	expiresAt = calculateExpiresAt(-1 * time.Hour)
	assert.Nil(t, expiresAt)
}

func TestMergeState_NilSession(t *testing.T) {
	appState := session.StateMap{"app-key": []byte(`"app-val"`)}
	userState := session.StateMap{"user-key": []byte(`"user-val"`)}

	result := mergeState(appState, userState, nil)
	assert.Nil(t, result)
}

func TestMergeState_NilState(t *testing.T) {
	appState := session.StateMap{"app-key": []byte(`"app-val"`)}
	userState := session.StateMap{"user-key": []byte(`"user-val"`)}

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
		State:   nil, // Nil state
	}

	result := mergeState(appState, userState, sess)
	assert.NotNil(t, result)
	assert.NotNil(t, result.State)
	assert.Contains(t, result.State, "app:app-key")
	assert.Contains(t, result.State, "user:user-key")
}
