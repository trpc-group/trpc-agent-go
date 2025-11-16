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
	mock.ExpectQuery("SELECT event FROM session_events").
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

	require.NoError(t, mock.ExpectationsWereMet())
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
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock enforce event limit (soft delete old events)
	mock.ExpectExec("UPDATE session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(), 5).
		WillReturnResult(sqlmock.NewResult(0, 0))

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
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}
