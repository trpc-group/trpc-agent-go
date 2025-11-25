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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestBuildConnString_Complete(t *testing.T) {
	opts := ServiceOpts{
		host:     "testhost",
		port:     5433,
		user:     "testuser",
		password: "testpass",
		database: "testdb",
		sslMode:  "require",
	}
	connString := buildConnString(opts)
	assert.Contains(t, connString, "host=testhost")
	assert.Contains(t, connString, "port=5433")
	assert.Contains(t, connString, "dbname=testdb")
	assert.Contains(t, connString, "user=testuser")
	assert.Contains(t, connString, "password=testpass")
	assert.Contains(t, connString, "sslmode=require")
}

func TestGetSession_RefreshTTL(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL: time.Hour,
	})
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock get session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	// Mock list app states
	appStatesRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appStatesRows)

	// Mock list user states
	userStatesRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userStatesRows)

	// Mock get events
	eventsRows := sqlmock.NewRows([]string{"event"})
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WillReturnRows(eventsRows)

	// Mock get summaries
	summariesRows := sqlmock.NewRows([]string{"filter_key", "summary"})
	mock.ExpectQuery("SELECT filter_key, summary FROM session_summaries").
		WillReturnRows(summariesRows)

	// Mock refresh TTL
	mock.ExpectExec("UPDATE session_states").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	sess, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
}

func TestDeleteSessionState_HardDelete(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		softDelete: boolPtr(false),
	})
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectBegin()

	// Mock hard delete session state
	mock.ExpectExec("DELETE FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock hard delete session summaries
	mock.ExpectExec("DELETE FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock hard delete session events
	mock.ExpectExec("DELETE FROM session_events").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 5))
	// Mock hard delete session tracks.
	mock.ExpectExec("DELETE FROM session_track_events").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 5))

	mock.ExpectCommit()

	err := s.DeleteSession(context.Background(), key)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_WithEventLimit(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionEventLimit: 5,
		softDelete:        boolPtr(true),
	})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test-invocation", "test-author")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "response",
				},
			},
		},
	}

	// Mock get session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, nil)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	// Mock transaction
	mock.ExpectBegin()

	// Mock update session state
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock insert event
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_ToExpiredSession(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL: time.Hour,
	})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test-invocation", "test-author")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: "response"}},
		},
	}

	// Mock get session state - expired
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	pastTime := time.Now().Add(-2 * time.Hour)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, pastTime)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	// Mock transaction
	mock.ExpectBegin()

	// Mock update session state (extends TTL)
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock insert event
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetSession tests various GetSession error scenarios
func TestGetSession(t *testing.T) {
	t.Run("UnmarshalStateError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user1",
			SessionID: "sess1",
		}

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
	})

	t.Run("QueryEventUnmarshalError", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user1",
			SessionID: "sess1",
		}

		stateData := SessionState{
			State:     session.StateMap{"key": []byte("value")},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		stateBytes, _ := json.Marshal(stateData)

		mock.ExpectQuery("SELECT .+ FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
				AddRow(stateBytes, stateData.CreatedAt, stateData.UpdatedAt))

		mock.ExpectQuery("SELECT .+ FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT .+ FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}).
				AddRow("session1", []byte("invalid-event-json")))

		sess, err := s.getSession(context.Background(), key, 0, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, sess)
		assert.Contains(t, err.Error(), "get events failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("QueryError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.GetSession(context.Background(), key)
		assert.Error(t, err)
	})

	t.Run("WithRefresh", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db, WithSessionTTL(1*time.Hour))
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		sessState := SessionState{
			ID:        key.SessionID,
			State:     session.StateMap{},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		stateBytes, _ := json.Marshal(sessState)

		mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
				AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

		mock.ExpectQuery("SELECT key, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT key, value FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}))

		mock.ExpectExec("UPDATE session_states").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		sess, err := s.GetSession(context.Background(), key)
		assert.NoError(t, err)
		assert.NotNil(t, sess)
	})

	t.Run("RefreshTTLError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db, WithSessionTTL(1*time.Hour))
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		sessState := SessionState{
			ID:        key.SessionID,
			State:     session.StateMap{},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		stateBytes, _ := json.Marshal(sessState)

		mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
				AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

		mock.ExpectQuery("SELECT key, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT key, value FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}))

		mock.ExpectExec("UPDATE session_states").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
			WillReturnError(fmt.Errorf("database error"))

		sess, err := s.GetSession(context.Background(), key)
		assert.NoError(t, err)
		assert.NotNil(t, sess)
	})
}

// TestListSessions tests ListSessions error scenarios
func TestListSessions(t *testing.T) {
	t.Run("UnmarshalStateError", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		userKey := session.UserKey{
			AppName: "test-app",
			UserID:  "user1",
		}

		mock.ExpectQuery("SELECT .+ FROM app_states").
			WithArgs(userKey.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT .+ FROM user_states").
			WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

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
	})

	t.Run("InvalidUserKey", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		userKey := session.UserKey{
			AppName: "test-app",
			UserID:  "",
		}

		_, err = s.ListSessions(context.Background(), userKey)
		assert.Error(t, err)
	})
}

// TestGetEventsList tests GetEventsList scenarios
func TestGetEventsList(t *testing.T) {
	t.Run("EmptySessionKeys", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		result, err := s.getEventsList(context.Background(), []session.Key{}, 0, time.Time{})
		assert.NoError(t, err)
		assert.Nil(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("UnmarshalError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		keys := []session.Key{
			{AppName: "app1", UserID: "user1", SessionID: "sess1"},
		}

		rows := sqlmock.NewRows([]string{"session_id", "event"}).
			AddRow("sess1", []byte("invalid-json"))

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs("app1", "user1", sqlmock.AnyArg()).
			WillReturnRows(rows)

		result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unmarshal event failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("WithLimit", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		keys := []session.Key{
			{AppName: "app1", UserID: "user1", SessionID: "sess1"},
		}

		evt1 := event.NewResponseEvent("inv-1", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "user message",
					},
				},
			},
		})
		evt2 := event.NewResponseEvent("inv-2", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "user message - 1",
					},
				},
			},
		})
		evt3 := event.NewResponseEvent("inv-3", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "assistant-1 message",
					},
				},
			},
		})

		evt1Bytes, _ := json.Marshal(evt1)
		evt2Bytes, _ := json.Marshal(evt2)
		evt3Bytes, _ := json.Marshal(evt3)

		rows := sqlmock.NewRows([]string{"session_id", "event"}).
			AddRow(keys[0].SessionID, evt1Bytes).
			AddRow(keys[0].SessionID, evt2Bytes).
			AddRow(keys[0].SessionID, evt3Bytes)

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs(keys[0].AppName, keys[0].UserID, sqlmock.AnyArg()).
			WillReturnRows(rows)

		result, err := s.getEventsList(context.Background(), keys, 2, time.Time{})
		assert.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0], 2)
		assert.Equal(t, "inv-2", result[0][0].InvocationID)
		assert.Equal(t, "inv-3", result[0][1].InvocationID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("NoLimit", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		keys := []session.Key{
			{AppName: "app1", UserID: "user1", SessionID: "sess1"},
		}

		evt1 := event.NewResponseEvent("inv-1", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "user message",
					},
				},
			},
		})
		evt2 := event.NewResponseEvent("inv-2", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "assistant-1 message",
					},
				},
			},
		})
		evt3 := event.NewResponseEvent("inv-3", "author1", &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "assistant-2 message",
					},
				},
			},
		})

		evt1Bytes, _ := json.Marshal(evt1)
		evt2Bytes, _ := json.Marshal(evt2)
		evt3Bytes, _ := json.Marshal(evt3)

		rows := sqlmock.NewRows([]string{"session_id", "event"}).
			AddRow("sess1", evt1Bytes).
			AddRow("sess1", evt2Bytes).
			AddRow("sess1", evt3Bytes)

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs("app1", "user1", sqlmock.AnyArg()).
			WillReturnRows(rows)

		result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
		assert.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0], 3)
		assert.Equal(t, "inv-1", result[0][0].InvocationID)
		assert.Equal(t, "inv-2", result[0][1].InvocationID)
		assert.Equal(t, "inv-3", result[0][2].InvocationID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ScanError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		keys := []session.Key{
			{AppName: "app1", UserID: "user1", SessionID: "sess1"},
		}

		rows := sqlmock.NewRows([]string{"app_name", "user_id"}).
			AddRow("app1", "user1")

		mock.ExpectQuery("SELECT session_id, event FROM session_events").
			WithArgs("app1", "user1", sqlmock.AnyArg()).
			WillReturnRows(rows)

		result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unmarshal event failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestGetSummariesList tests GetSummariesList error scenario
func TestGetSummariesList(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	keys := []session.Key{
		{AppName: "app1", UserID: "user1", SessionID: "sess1"},
	}

	rows := sqlmock.NewRows([]string{"session_id", "filter_key", "summary"}).
		AddRow("sess1", "filter1", []byte("invalid-json"))

	mock.ExpectQuery("SELECT session_id, filter_key, summary FROM session_summaries").
		WithArgs("app1", "user1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := s.getSummariesList(context.Background(), keys)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unmarshal summary failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
