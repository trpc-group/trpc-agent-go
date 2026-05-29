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

	// Mock: Query event refs (empty)
	expectLimitedEventRefs(mock, key, sessState.CreatedAt, defaultSessionEventLimit)

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotNil(t, sess.Events)
	assert.Empty(t, sess.Events)
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
	eventCreatedAt := time.Now()
	expectLimitedEventRefs(mock, key, sessState.CreatedAt, 10, eventRef{id: 1, createdAt: eventCreatedAt})
	expectEventsByRefs(mock, key, limitedEventRow{id: 1, event: eventBytes, createdAt: eventCreatedAt})

	// Mock: Batch load summaries with data
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sess, err := s.GetSession(ctx, key, session.WithEventNum(10))
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Len(t, sess.Events, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithLimitFetchesUserAnchor(t *testing.T) {
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
		ID:        key.SessionID,
		State:     session.StateMap{},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	anchor := event.NewResponseEvent("inv-1", "author1", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "user"}}},
	})
	evt2 := event.NewResponseEvent("inv-2", "author1", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "assistant-1"}}},
	})
	evt3 := event.NewResponseEvent("inv-3", "author1", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "assistant-2"}}},
	})
	anchorBytes, _ := json.Marshal(anchor)
	evt2Bytes, _ := json.Marshal(evt2)
	evt3Bytes, _ := json.Marshal(evt3)
	anchorCreatedAt := sessState.CreatedAt.Add(time.Minute)
	evt2CreatedAt := sessState.CreatedAt.Add(2 * time.Minute)
	evt3CreatedAt := sessState.CreatedAt.Add(3 * time.Minute)

	expectLimitedEventRefs(
		mock,
		key,
		sessState.CreatedAt,
		2,
		eventRef{id: 3, createdAt: evt3CreatedAt},
		eventRef{id: 2, createdAt: evt2CreatedAt},
	)
	expectEventsByRefs(
		mock,
		key,
		limitedEventRow{id: 3, event: evt3Bytes, createdAt: evt3CreatedAt},
		limitedEventRow{id: 2, event: evt2Bytes, createdAt: evt2CreatedAt},
	)
	expectPreviousEventRefs(
		mock,
		key,
		sessState.CreatedAt,
		&eventRef{id: 2, createdAt: evt2CreatedAt},
		eventRef{id: 1, createdAt: anchorCreatedAt},
	)
	expectEventsByRefs(
		mock,
		key,
		limitedEventRow{id: 1, event: anchorBytes, createdAt: anchorCreatedAt},
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sess, err := s.GetSession(ctx, key, session.WithEventNum(2))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 3)
	assert.Equal(t, "inv-1", sess.Events[0].InvocationID)
	assert.Equal(t, "inv-2", sess.Events[1].InvocationID)
	assert.Equal(t, "inv-3", sess.Events[2].InvocationID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithEventTimeFiltersEventTimestamp(t *testing.T) {
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

	afterTime := time.Now().Add(-time.Hour)
	sessState := SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{},
		CreatedAt: afterTime.Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	anchor := event.NewResponseEvent("inv-anchor", "author", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "anchor"}}},
	})
	anchor.Timestamp = afterTime.Add(-30 * time.Minute)
	oldByEventTime := event.NewResponseEvent("inv-old", "author", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "old"}}},
	})
	oldByEventTime.Timestamp = afterTime.Add(-time.Minute)
	newByEventTime := event.NewResponseEvent("inv-new", "author", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "new"}}},
	})
	newByEventTime.Timestamp = afterTime.Add(time.Minute)
	anchorBytes, _ := json.Marshal(anchor)
	oldBytes, _ := json.Marshal(oldByEventTime)
	newBytes, _ := json.Marshal(newByEventTime)
	anchorCreatedAt := afterTime.Add(-30 * time.Minute)
	oldCreatedAt := afterTime.Add(time.Minute)
	newCreatedAt := afterTime.Add(2 * time.Minute)

	expectFullSessionEventsList(
		mock,
		key,
		limitedEventRow{id: 1, event: anchorBytes, createdAt: anchorCreatedAt},
		limitedEventRow{id: 2, event: newBytes, createdAt: newCreatedAt},
		limitedEventRow{id: 3, event: oldBytes, createdAt: oldCreatedAt},
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sess, err := s.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, "inv-anchor", sess.Events[0].InvocationID)
	assert.Equal(t, "inv-new", sess.Events[1].InvocationID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithEventTimeUsesLoadedUserAnchor(t *testing.T) {
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

	afterTime := time.Now().Add(-time.Hour)
	sessState := SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{},
		CreatedAt: afterTime.Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	userAnchor := event.NewResponseEvent("inv-user", "author", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "user"}}},
	})
	userAnchor.Timestamp = afterTime.Add(-time.Minute)
	assistant := event.NewResponseEvent("inv-assistant", "author", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "assistant"}}},
	})
	assistant.Timestamp = afterTime.Add(time.Minute)
	userBytes, _ := json.Marshal(userAnchor)
	assistantBytes, _ := json.Marshal(assistant)
	userCreatedAt := afterTime.Add(time.Minute)
	assistantCreatedAt := afterTime.Add(2 * time.Minute)

	expectFullSessionEventsList(
		mock,
		key,
		limitedEventRow{id: 1, event: userBytes, createdAt: userCreatedAt},
		limitedEventRow{id: 2, event: assistantBytes, createdAt: assistantCreatedAt},
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sess, err := s.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, "inv-user", sess.Events[0].InvocationID)
	assert.Equal(t, "inv-assistant", sess.Events[1].InvocationID)
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

	// Mock: Query event refs (empty).
	expectLimitedEventRefs(mock, key, sessState.CreatedAt, defaultSessionEventLimit)

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

func TestGetSession_WithTTL(t *testing.T) {
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

	// Mock: Query event refs and user anchor with TTL lower bound.
	expectLimitedEventRefs(mock, key, sessState.CreatedAt, defaultSessionEventLimit)
	expectNoUserAnchor(mock, key, sessState.CreatedAt)

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotNil(t, sess.Events)
	assert.Empty(t, sess.Events)
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, app_name, user_id, session_id, event, created_at FROM")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "app_name", "user_id", "session_id", "event", "created_at"}))

	// Mock: Batch load summaries (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, app_name, user_id, session_id, event, created_at FROM")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "app_name", "user_id", "session_id", "event", "created_at"}))

	// Mock: Batch load summaries (empty).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

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

func TestListSessions_WithListSessionOnlyMeta(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	tracks, err := json.Marshal([]session.Track{"alpha"})
	require.NoError(t, err)
	sessState := SessionState{
		ID: "session-1",
		State: session.StateMap{
			"key1":   []byte(`"value1"`),
			"tracks": tracks,
		},
	}
	stateBytes, err := json.Marshal(sessState)
	require.NoError(t, err)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", stateBytes, now, now))

	sessions, err := s.ListSessions(ctx, userKey, session.WithListSessionOnlyMeta())
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].ID)
	assert.Empty(t, sessions[0].Events)
	assert.Nil(t, sessions[0].Tracks)
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, app_name, user_id, session_id, event, created_at FROM")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "app_name", "user_id", "session_id", "event", "created_at"}))

	// Mock: Batch load summaries
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// Verify app state and user state are merged
	assert.Contains(t, sessions[0].State, "app:app-key")
	assert.Contains(t, sessions[0].State, "user:user-key")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_WithListSessionPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	sessState := SessionState{ID: "session-2"}
	stateBytes, _ := json.Marshal(sessState)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg(), 1, 1).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-2", stateBytes, time.Now(), time.Now()))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, app_name, user_id, session_id, event, created_at FROM")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event", "created_at"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary, updated_at FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary", "updated_at"}))

	sessions, err := s.ListSessions(ctx, userKey, session.WithListSessionPage(1, 1))
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "session-2", sessions[0].ID)
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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, expiredTime))
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

	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))
	mock.ExpectRollback()

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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow([]byte("{invalid"), nil))
	mock.ExpectRollback()

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

	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))
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

	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))
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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, expiredTime))
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
	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// No event insert expectation
	mock.ExpectCommit()

	err = s.addEvent(ctx, key, evt)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_PreservesExistingSkillMarker(t *testing.T) {
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
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	markerKey := "temp:skill:loaded_by_agent:llmagent_xxx/wesee-title-producer"

	sessState := SessionState{
		ID: key.SessionID,
		State: session.StateMap{
			markerKey: []byte("1"),
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	stateBytes, err := json.Marshal(sessState)
	require.NoError(t, err)

	trackEvent := &session.TrackEvent{
		Track:     "agui",
		Payload:   json.RawMessage(`{"delta":"hi"}`),
		Timestamp: time.Now(),
	}

	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(
			&sessionStateJSONMatcher{
				t:          t,
				expectedID: key.SessionID,
				expectedState: session.StateMap{
					markerKey: []byte("1"),
				},
				createdAt:  createdAt,
				previousAt: updatedAt,
			},
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_track_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, "agui",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = s.addTrackEvent(ctx, key, trackEvent)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
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
