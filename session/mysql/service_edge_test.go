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

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
				AddRow("app1", "user1", "session1", []byte("invalid-event-json")))

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

		mock.ExpectQuery("SELECT `key`, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT `key`, value FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

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

		mock.ExpectQuery("SELECT `key`, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT `key`, value FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

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

// TestAddEvent tests AddEvent error scenarios
func TestAddEvent(t *testing.T) {
	t.Run("QueryError", func(t *testing.T) {
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

		mock.ExpectQuery("SELECT state, expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnError(fmt.Errorf("database error"))

		err = s.addEvent(context.Background(), key, evt)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get session state failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

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

		evt := event.New("inv-1", "test-author")

		rows := sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow([]byte("invalid-json"), sql.NullTime{Valid: false})

		mock.ExpectQuery("SELECT state, expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(rows)

		err = s.addEvent(context.Background(), key, evt)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal session state failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestRefreshSessionTTL tests RefreshSessionTTL error scenario
func TestRefreshSessionTTL(t *testing.T) {
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

		rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow("app1", "user1", "sess1", []byte("invalid-json"))

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs("app1", "user1", "sess1").
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

		rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow(keys[0].AppName, keys[0].UserID, keys[0].SessionID, evt1Bytes).
			AddRow(keys[0].AppName, keys[0].UserID, keys[0].SessionID, evt2Bytes).
			AddRow(keys[0].AppName, keys[0].UserID, keys[0].SessionID, evt3Bytes)

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs(keys[0].AppName, keys[0].UserID, keys[0].SessionID).
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

		rows := sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow("app1", "user1", "sess1", evt1Bytes).
			AddRow("app1", "user1", "sess1", evt2Bytes).
			AddRow("app1", "user1", "sess1", evt3Bytes)

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs("app1", "user1", "sess1").
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

		mock.ExpectQuery("SELECT app_name, user_id, session_id, event FROM session_events").
			WithArgs("app1", "user1", "sess1").
			WillReturnRows(rows)

		result, err := s.getEventsList(context.Background(), keys, 0, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "batch get events failed")
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

// TestCreateSession tests CreateSession error scenarios
func TestCreateSession(t *testing.T) {
	t.Run("CheckExistingError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		mock.ExpectQuery("SELECT expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.CreateSession(context.Background(), key, session.StateMap{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "check existing session failed")
	})

	t.Run("InsertError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		mock.ExpectQuery("SELECT expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"expires_at"}))

		mock.ExpectExec("INSERT INTO session_states").
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.CreateSession(context.Background(), key, session.StateMap{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "create session failed")
	})

	t.Run("ListAppStatesError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		mock.ExpectQuery("SELECT expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"expires_at"}))

		mock.ExpectExec("INSERT INTO session_states").
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT `key`, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.CreateSession(context.Background(), key, session.StateMap{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "list app states failed")
	})

	t.Run("ListUserStatesError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		key := session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "session-456",
		}

		mock.ExpectQuery("SELECT expires_at FROM session_states").
			WithArgs(key.AppName, key.UserID, key.SessionID).
			WillReturnRows(sqlmock.NewRows([]string{"expires_at"}))

		mock.ExpectExec("INSERT INTO session_states").
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT `key`, value FROM app_states").
			WithArgs(key.AppName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

		mock.ExpectQuery("SELECT `key`, value FROM user_states").
			WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.CreateSession(context.Background(), key, session.StateMap{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "list user states failed")
	})
}

// TestCleanupExpired tests cleanup error scenarios
func TestCleanupExpired(t *testing.T) {
	t.Run("AppStates_SoftDeleteError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		s.opts.softDelete = true

		mock.ExpectExec("UPDATE app_states SET deleted_at").
			WillReturnError(fmt.Errorf("database error"))

		s.cleanupExpiredAppStates(context.Background(), time.Now())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("UserStates_SoftDeleteError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		s.opts.softDelete = true

		mock.ExpectExec("UPDATE user_states SET deleted_at").
			WillReturnError(fmt.Errorf("database error"))

		s.cleanupExpiredUserStates(context.Background(), time.Now())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Sessions_SoftDeleteError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		s.opts.softDelete = true

		mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
			WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
				AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE session_states SET deleted_at").
			WillReturnError(fmt.Errorf("database error"))
		mock.ExpectRollback()

		s.cleanupExpiredSessions(context.Background(), time.Now())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestInvalidKeyValidation tests invalid key validation
func TestInvalidKeyValidation(t *testing.T) {
	t.Run("DeleteSession_InvalidKey", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		err = s.DeleteSession(context.Background(), session.Key{
			AppName:   "test-app",
			UserID:    "user-123",
			SessionID: "",
		})
		assert.Error(t, err)
	})

	t.Run("DeleteAppState_InvalidKey", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		err = s.DeleteAppState(context.Background(), "", "")
		assert.Error(t, err)
	})

	t.Run("ListAppStates_InvalidKey", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		_, err = s.ListAppStates(context.Background(), "")
		assert.Error(t, err)
	})
}

// TestDeleteAppState_EdgeCases tests DeleteAppState error scenarios
func TestDeleteAppState_EdgeCases(t *testing.T) {
	t.Run("ExecError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)
		s.opts.softDelete = true

		mock.ExpectExec("UPDATE app_states SET deleted_at").
			WithArgs(sqlmock.AnyArg(), "test-app", "").
			WillReturnError(fmt.Errorf("database error"))

		err = s.DeleteAppState(context.Background(), "test-app", "")
		assert.Error(t, err)
	})
}

// TestListAppStates tests ListAppStates error scenarios
func TestListAppStates(t *testing.T) {
	t.Run("QueryError", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db)

		mock.ExpectQuery("SELECT state FROM app_states").
			WithArgs("test-app", sqlmock.AnyArg()).
			WillReturnError(fmt.Errorf("database error"))

		_, err = s.ListAppStates(context.Background(), "test-app")
		assert.Error(t, err)
	})
}

// TestUpdateUserState_WithStatePrefix tests UpdateUserState with state prefixed keys
func TestUpdateUserState_WithStatePrefix(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithUserStateTTL(1*time.Hour))

	userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}
	state := session.StateMap{
		session.StateUserPrefix + "key1": []byte(`"value1"`),
	}

	mock.ExpectExec("REPLACE INTO user_states").
		WithArgs("test-app", "user-123", "key1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.UpdateUserState(context.Background(), userKey, state)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUserStates_Empty tests successful list of empty user states
func TestListUserStates_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}

	mock.ExpectQuery("SELECT `key`, value FROM user_states").
		WithArgs("test-app", "user-123", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	states, err := s.ListUserStates(context.Background(), userKey)
	assert.NoError(t, err)
	assert.Len(t, states, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteUserState_HardDeleteError tests hard delete error scenario
func TestDeleteUserState_HardDeleteError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}

	mock.ExpectExec("DELETE FROM user_states").
		WithArgs("test-app", "user-123", "key1").
		WillReturnError(fmt.Errorf("database error"))

	err = s.DeleteUserState(context.Background(), userKey, "key1")
	assert.Error(t, err)
}

// TestAppendEvent_InvalidSessionKey tests append event with invalid session key
func TestAppendEvent_InvalidSessionKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	sess := &session.Session{
		AppName: "",
		UserID:  "user-123",
		ID:      "session-456",
	}

	evt := &event.Event{ID: "inv-1"}
	err = s.AppendEvent(context.Background(), sess, evt)
	assert.Error(t, err)
}

// TestDeleteSession_ExecError tests delete session with database exec error
func TestDeleteSession_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = true

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectExec("UPDATE session_states SET deleted_at = ?").
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("database error"))

	err = s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
}

// TestDeleteSession_HardDeleteWithError tests hard delete error scenario
func TestDeleteSession_HardDeleteWithError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectExec("DELETE FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("database error"))

	err = s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
}

// TestCleanupExpiredForUser_SoftDelete tests cleanup expired sessions for a user
func TestCleanupExpiredForUser_SoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = true

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mock.ExpectExec("UPDATE session_states SET deleted_at = ?").
		WithArgs(sqlmock.AnyArg(), userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Should not panic
	s.cleanupExpiredForUser(context.Background(), userKey)
}

// TestCleanupExpiredForUser_HardDelete tests cleanup with hard delete
func TestCleanupExpiredForUser_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mock.ExpectExec("DELETE FROM session_states").
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Should not panic
	s.cleanupExpiredForUser(context.Background(), userKey)
}

// TestClose_WithAsyncWorkers tests closing service with async workers
func TestClose_WithAsyncWorkers(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithEnableAsyncPersist(true), WithAsyncPersisterNum(2))
	s.startAsyncPersistWorker()
	s.startAsyncSummaryWorker()

	err = s.Close()
	assert.NoError(t, err)
}

// TestClose_WithCleanupRoutine tests closing service with cleanup routine
func TestClose_WithCleanupRoutine(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(5*time.Minute))
	s.startCleanupRoutine()

	err = s.Close()
	assert.NoError(t, err)

	// Calling close twice should not panic
	err = s.Close()
	assert.NoError(t, err)
}

// TestCreateSession_ExistingWithoutExpiry tests creating session when an existing non-expiring session exists
func TestCreateSession_ExistingWithoutExpiry(t *testing.T) {
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

	// Mock: Check existing session - returns a row with NULL expires_at (no expiration)
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}).AddRow(nil))

	_, err = s.CreateSession(ctx, key, session.StateMap{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session already exists and has not expired")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteUserState_KeyRequired tests delete user state with empty key
func TestDeleteUserState_KeyRequired(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}

	err = s.DeleteUserState(ctx, userKey, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

// TestDeleteSession_HardDeleteSummariesError tests hard delete when summaries deletion fails
func TestDeleteSession_HardDeleteSummariesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM session_summaries").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("summaries delete error"))
	mock.ExpectRollback()

	err = s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete session state failed")
}

// TestDeleteSession_HardDeleteEventsError tests hard delete when events deletion fails
func TestDeleteSession_HardDeleteEventsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = false

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM session_summaries").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("events delete error"))
	mock.ExpectRollback()

	err = s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete session state failed")
}

// TestDeleteSession_SoftDeleteSummariesError tests soft delete when summaries fails
func TestDeleteSession_SoftDeleteSummariesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	s.opts.softDelete = true

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE session_summaries SET deleted_at").
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("summaries update error"))
	mock.ExpectRollback()

	err = s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
}
