//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_GetEventWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "u2").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowEventBytes(t, "u2", model.RoleUser, "three"), base.Add(2*time.Minute)))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), eventWindowBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowEventBytes(t, "a1", model.RoleAssistant, "two"), base.Add(time.Minute)).
			AddRow(mysqlWindowEventBytes(t, "u1", model.RoleUser, "one"), base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), eventWindowBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowToolEventBytes(t, "t1", "calc", "four"), base.Add(3*time.Minute)).
			AddRow(mysqlWindowEventBytes(t, "u3", model.RoleUser, "five"), base.Add(4*time.Minute)))

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "u2",
		Before:        1,
		After:         1,
		Roles:         []model.Role{model.RoleUser},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, mysqlWindowIDs(got))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowAnchorNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "missing").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowValidation(t *testing.T) {
	svc := createTestService(t, nil)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	_, err := svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           session.Key{UserID: "user", SessionID: "sess"},
		AnchorEventID: "anchor",
	})
	require.Error(t, err)

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key: key,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event id is required")

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		After:         -1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "before >= 0")
}

func TestService_GetEventWindowAnchorOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "anchor").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowEventBytes(t, "anchor", model.RoleUser, "one"), base))

	got, err := svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: " anchor ",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"anchor"}, mysqlWindowIDs(got))
	require.Equal(t, "anchor", got.AnchorEventID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowNoActiveSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowActiveSessionQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnError(errors.New("query failed"))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load active session")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowAnchorFilteredByRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "a1").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowEventBytes(t, "a1", model.RoleAssistant, "one"), base))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "a1",
		Roles:         []model.Role{model.RoleUser},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowNeighborQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "anchor").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow(mysqlWindowEventBytes(t, "anchor", model.RoleUser, "one"), base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base, eventWindowBatchSize).
		WillReturnError(errors.New("neighbors failed"))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Before:        1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load event window neighbors")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_GetEventWindowUnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "bad").
		WillReturnRows(sqlmock.NewRows([]string{"event", "created_at"}).
			AddRow([]byte("{bad-json"), base))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "bad",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal event window entry")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReverseWindowEntries(t *testing.T) {
	entries := []session.EventWindowEntry{
		{Event: event.Event{ID: "first"}},
		{Event: event.Event{ID: "second"}},
	}
	reverseWindowEntries(entries)
	require.Equal(t, []string{"second", "first"}, []string{
		entries[0].Event.ID,
		entries[1].Event.ID,
	})
}

func mysqlWindowEventBytes(
	t *testing.T,
	id string,
	role model.Role,
	content string,
) []byte {
	t.Helper()
	evt := event.Event{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    role,
					Content: content,
				},
			}},
		},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	return data
}

func mysqlWindowToolEventBytes(
	t *testing.T,
	id string,
	toolName string,
	content string,
) []byte {
	t.Helper()
	evt := event.Event{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  content,
					ToolID:   "call-" + id,
					ToolName: toolName,
				},
			}},
		},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	return data
}

func mysqlWindowIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}
