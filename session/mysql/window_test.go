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
	mock.ExpectQuery("SELECT id, event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "u2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "event", "created_at"}).
			AddRow(int64(3), mysqlWindowEventBytes(t, "u2", model.RoleUser, "three"), base.Add(2*time.Minute)))
	mock.ExpectQuery("SELECT id, event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), base.Add(2*time.Minute), int64(3), eventWindowBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event", "created_at"}).
			AddRow(int64(2), mysqlWindowEventBytes(t, "a1", model.RoleAssistant, "two"), base.Add(time.Minute)).
			AddRow(int64(1), mysqlWindowEventBytes(t, "u1", model.RoleUser, "one"), base))
	mock.ExpectQuery("SELECT id, event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), base.Add(2*time.Minute), int64(3), eventWindowBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event", "created_at"}).
			AddRow(int64(4), mysqlWindowToolEventBytes(t, "t1", "calc", "four"), base.Add(3*time.Minute)).
			AddRow(int64(5), mysqlWindowEventBytes(t, "u3", model.RoleUser, "five"), base.Add(4*time.Minute)))

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
	mock.ExpectQuery("SELECT id, event, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, "missing").
		WillReturnRows(sqlmock.NewRows([]string{"id", "event", "created_at"}))

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
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
