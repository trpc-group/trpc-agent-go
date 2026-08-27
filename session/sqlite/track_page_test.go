//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

func TestSessionSQLite_GetTrackEventPagePaginatesRows(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, sqliteTrackPageEvent("oldest", baseTime)))
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, sqliteTrackPageEvent("middle", baseTime.Add(time.Second))))
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, sqliteTrackPageEvent("newest", baseTime.Add(2*time.Second))))

	page, err := svc.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, page.Entries, 2)
	require.True(t, page.HasMore)
	require.Equal(t, session.Track("alpha"), page.Track)
	require.Equal(t, json.RawMessage(`"middle"`), page.Entries[0].Event.Payload)
	require.Equal(t, json.RawMessage(`"newest"`), page.Entries[1].Event.Payload)

	cursor, err := trackpage.Decode(page.Entries[0].Cursor)
	require.NoError(t, err)
	require.Equal(t, trackEventPageCursorKindSQLite, cursor.Kind)
	require.Equal(t, trackpage.TimeToUnixNano(baseTime.Add(time.Second)), cursor.CreatedAt)

	next, err := svc.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     page.Entries[0].Cursor,
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, next.Entries, 1)
	require.False(t, next.HasMore)
	require.Equal(t, json.RawMessage(`"oldest"`), next.Entries[0].Event.Payload)
}

func TestSessionSQLite_GetTrackEventPageRejectsWrongCursorBinding(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	cursor, err := trackpage.CursorForUnixNano(
		trackEventPageCursorKindSQLite,
		key,
		"beta",
		trackpage.TimeToUnixNano(time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)),
		"1",
	)
	require.NoError(t, err)

	_, err = svc.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     cursor,
		EventLimit: 1,
	})
	require.Error(t, err)
}

func TestSessionSQLite_GetTrackEventPageRejectsInvalidRequest(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:   session.Key{AppName: "app", UserID: "user", SessionID: "session"},
		Track: "alpha",
	})
	require.Error(t, err)
}

func TestSessionSQLite_GetTrackEventPageRejectsNonNumericCursorID(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	cursor, err := trackpage.CursorForUnixNano(
		trackEventPageCursorKindSQLite,
		key,
		"alpha",
		trackpage.TimeToUnixNano(time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)),
		"bad",
	)
	require.NoError(t, err)

	_, err = svc.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     cursor,
		EventLimit: 1,
	})
	require.Error(t, err)
}

func TestSessionSQLite_GetTrackEventPageRejectsMalformedEvent(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, sqliteTrackPageEvent("bad", time.Now())))

	_, err = svc.db.ExecContext(ctx, "UPDATE "+svc.tableSessionTracks+" SET event = ?", []byte("{"))
	require.NoError(t, err)

	_, err = svc.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 1,
	})
	require.Error(t, err)
}

func sqliteTrackPageEvent(payload string, ts time.Time) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"` + payload + `"`),
		Timestamp: ts,
	}
}
