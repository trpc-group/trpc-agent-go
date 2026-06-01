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

func TestAddEvent_StoresAppendTimeAsTimestampUTC(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sessState := &SessionState{ID: key.SessionID, State: session.StateMap{}}
	stateBytes, err := json.Marshal(sessState)
	require.NoError(t, err)

	// event.Timestamp is logical event time; created_at stores the append time.
	// Keep eventTime outside the append window so regressions cannot pass.
	eventTime := time.Now().In(time.FixedZone("UTC+8", 8*60*60)).Add(-24 * time.Hour)
	beforeAppend := time.Now().UTC()

	expectLoadSessionStateForUpdate(mock, key).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), utcTimeArg{}, sqlmock.AnyArg(),
			key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg(),
			appendTimeUTCArg{notBefore: beforeAppend}, utcTimeArg{}).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	evt := &event.Event{
		Timestamp: eventTime,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}

	err = s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTrackCreatedAtUTCUsesTimestampOrFallback(t *testing.T) {
	fallback := time.Date(
		2026, 5, 31, 20, 25, 0, 0,
		time.FixedZone("UTC+8", 8*60*60),
	)
	eventTime := fallback.Add(time.Minute)

	assert.Equal(t, fallback.UTC(), trackEventCreatedAtUTC(nil, fallback))
	assert.Equal(
		t,
		eventTime.UTC(),
		trackEventCreatedAtUTC(&session.TrackEvent{Timestamp: eventTime}, fallback),
	)
}
