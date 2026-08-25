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
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

func TestGetTrackEventPagePaginatesRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{"id", "event", "created_at"}).
		AddRow(int64(3), postgresTrackPageEventBytes(t, "newest", baseTime.Add(2*time.Second)), baseTime.Add(2*time.Second)).
		AddRow(int64(2), postgresTrackPageEventBytes(t, "middle", baseTime.Add(time.Second)), baseTime.Add(time.Second)).
		AddRow(int64(1), postgresTrackPageEventBytes(t, "oldest", baseTime), baseTime)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, event, created_at FROM session_track_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID, session.Track("alpha"), sqlmock.AnyArg(), 3).
		WillReturnRows(rows)

	page, err := s.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, page.Entries, 2)
	assert.True(t, page.HasMore)
	assert.Equal(t, session.Track("alpha"), page.Track)
	assert.Equal(t, json.RawMessage(`"middle"`), page.Entries[0].Event.Payload)
	assert.Equal(t, json.RawMessage(`"newest"`), page.Entries[1].Event.Payload)

	cursor, err := trackpage.Decode(page.Entries[0].Cursor)
	require.NoError(t, err)
	assert.Equal(t, trackEventPageCursorKindPostgres, cursor.Kind)
	assert.Equal(t, "2", cursor.ID)
	assert.Equal(t, trackpage.TimeToUnixNano(baseTime.Add(time.Second)), cursor.CreatedAt)

	nextRows := sqlmock.NewRows([]string{"id", "event", "created_at"}).
		AddRow(int64(1), postgresTrackPageEventBytes(t, "oldest", baseTime), baseTime)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, event, created_at FROM session_track_events")).
		WithArgs(
			key.AppName, key.UserID, key.SessionID, session.Track("alpha"),
			sqlmock.AnyArg(), baseTime.Add(time.Second), baseTime.Add(time.Second), int64(2), 3,
		).
		WillReturnRows(nextRows)

	next, err := s.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     page.Entries[0].Cursor,
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, next.Entries, 1)
	assert.False(t, next.HasMore)
	assert.Equal(t, json.RawMessage(`"oldest"`), next.Entries[0].Event.Payload)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTrackEventPageRejectsWrongCursorBinding(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	cursor, err := trackpage.CursorFor(
		trackEventPageCursorKindPostgres,
		key,
		"beta",
		time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC),
		"1",
	)
	require.NoError(t, err)

	_, err = s.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     cursor,
		EventLimit: 1,
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func postgresTrackPageEventBytes(t *testing.T, payload string, ts time.Time) []byte {
	t.Helper()
	event := session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"` + payload + `"`),
		Timestamp: ts,
	}
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return data
}
