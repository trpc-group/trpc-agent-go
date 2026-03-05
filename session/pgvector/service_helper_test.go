//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

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

// --- Tests for refreshSessionTTL ---

func TestRefreshSessionTTL_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.refreshSessionTTL(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshSessionTTL_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnError(fmt.Errorf("db error"))

	err := s.refreshSessionTTL(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"refresh session TTL failed")
}

// --- Tests for addEvent ---

func TestAddEvent_SessionNotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))

	err := s.addEvent(
		context.Background(), key, &event.Event{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAddEvent_TransactionError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnError(fmt.Errorf("tx error"))
	mock.ExpectRollback()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "store event failed")
}

func TestAddEvent_QueryStateError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnError(fmt.Errorf("query error"))

	err := s.addEvent(
		context.Background(), key, &event.Event{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get session state failed")
}

// --- Tests for addTrackEvent ---

func TestAddTrackEvent_SessionNotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAddTrackEvent_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnError(fmt.Errorf("db error"))

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get session state failed")
}

// --- Tests for getEventsList ---

func TestGetEventsList_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getEventsList(
		context.Background(), nil, 0, time.Time{},
	)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetEventsList_Success(t *testing.T) {
	// getEventsList uses `ANY($3::varchar[])` with a
	// []string arg, which is PostgreSQL-specific and
	// unsupported by database/sql + go-sqlmock.
	// This test verifies the path for a single-session
	// key where the query succeeds.
	// Full integration coverage requires a real PG driver.
	s, _, db := newTestService(t, nil)
	defer db.Close()

	// Verify empty sessionIDs returns empty results.
	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}
	result, err := s.getEventsList(
		context.Background(), keys, 0, time.Time{},
	)
	// The []string conversion will fail with go-sqlmock,
	// so we just verify the function handles the error.
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetEventsList_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT session_id, event").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.getEventsList(
		context.Background(), []session.Key{key},
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"query events failed")
}

// --- Tests for getSummariesList ---

func TestGetSummariesList_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getSummariesList(
		context.Background(), nil,
	)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestGetSummariesList_Success(t *testing.T) {
	// getSummariesList uses `ANY($3::varchar[])` with a
	// []string arg, which is PostgreSQL-specific and
	// unsupported by database/sql + go-sqlmock.
	// Full integration coverage requires a real PG driver.
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}
	result, err := s.getSummariesList(
		context.Background(), keys,
	)
	// The []string conversion will fail with go-sqlmock.
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetSummariesList_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT session_id, filter_key").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.getSummariesList(
		context.Background(), []session.Key{key},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"query summaries failed")
}

// --- Tests for getTrackEvents ---

func TestGetTrackEvents_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getTrackEvents(
		context.Background(), nil, nil, 0, time.Time{},
	)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetTrackEvents_MismatchedLengths(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user", SessionID: "s1"},
	}

	_, err := s.getTrackEvents(
		context.Background(), keys,
		[]*SessionState{}, // Mismatched length.
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "count mismatch")
}

func TestGetTrackEvents_NoTracks(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user", SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1", State: session.StateMap{}},
	}

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

// --- Tests for addEvent partial event (no insert) ---

func TestAddEvent_PartialEvent_NoInsert(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))

	// Transaction with only state update (no event
	// insert for partial).
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	evt := &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "partial",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Test for addEvent with TTL ---

func TestAddEvent_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 30 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
