//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_GetEventWindow(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	ctx := context.Background()
	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	for _, evt := range []event.Event{
		sqliteWindowEvent("u1", model.RoleUser, "one"),
		sqliteWindowEvent("a1", model.RoleAssistant, "two"),
		sqliteWindowToolEvent("t1", "calc", "three"),
		sqliteWindowEvent("u2", model.RoleUser, "four"),
	} {
		evt := evt
		require.NoError(t, svc.AppendEvent(ctx, sess, &evt))
	}

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "t1",
		Before:        2,
		After:         1,
		Roles: []model.Role{
			model.RoleUser,
			model.RoleAssistant,
			model.RoleTool,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "a1", "t1", "u2"}, sqliteWindowIDs(got))
	require.False(t, got.Entries[0].CreatedAt.IsZero())
}

func TestService_GetEventWindowNoActiveSession(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	ctx := context.Background()
	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key: session.Key{
			AppName:   "app",
			UserID:    "user",
			SessionID: "missing",
		},
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowValidation(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key: key,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event id is required")

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Before:        -1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "before >= 0")
}

func TestService_GetEventWindowAnchorFilteredByRole(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	ctx := context.Background()
	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "filtered"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := sqliteWindowEvent("anchor", model.RoleAssistant, "answer")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Roles:         []model.Role{model.RoleUser},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowUnmarshalError(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	ctx := context.Background()
	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "bad-json"}
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	now := time.Now().UTC().UnixNano()
	_, err = db.ExecContext(ctx,
		`INSERT INTO session_events
		(app_name, user_id, session_id, event, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, []byte(`{"id":"anchor"`), now, now)
	require.NoError(t, err)

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal event window entry")
}

func TestService_GetEventWindowIgnoresMalformedRowOutsideWindow(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	ctx := context.Background()
	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "bad-outside"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := sqliteWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	now := time.Now().UTC().Add(time.Hour).UnixNano()
	_, err = db.ExecContext(ctx,
		`INSERT INTO session_events
		(app_name, user_id, session_id, event, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, []byte("{bad-json"), now, now)
	require.NoError(t, err)

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"anchor"}, sqliteWindowIDs(got))
}

func sqliteWindowEvent(id string, role model.Role, content string) event.Event {
	return event.Event{
		ID:        id,
		Timestamp: time.Unix(int64(len(id)), 0).UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    role,
					Content: content,
				},
			}},
		},
	}
}

func sqliteWindowToolEvent(id, name, content string) event.Event {
	evt := sqliteWindowEvent(id, model.RoleTool, content)
	evt.Response.Choices[0].Message.ToolID = "call-" + id
	evt.Response.Choices[0].Message.ToolName = name
	return evt
}

func sqliteWindowIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}
