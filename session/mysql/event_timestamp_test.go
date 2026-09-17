//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// These helpers use QueryMatcherEqual in their callers, so the assertions also
// protect the absence of JSON in the ordered query and of ORDER BY in the JSON
// query, as well as the shard key and soft-delete predicates.
func expectTimestampRefIDs(
	mock sqlmock.Sqlmock, key session.Key, after time.Time,
	before *eventRef, limit int,
) *sqlmock.ExpectedQuery {
	query := `SELECT id, created_at FROM session_events
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND created_at >= ? AND deleted_at IS NULL`
	args := []driver.Value{key.AppName, key.UserID, key.SessionID, after}
	if before != nil {
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, before.createdAt, before.createdAt, before.id)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return mock.ExpectQuery(query).WithArgs(args...)
}

func expectTimestampValues(mock sqlmock.Sqlmock, key session.Key, ids ...int64) *sqlmock.ExpectedQuery {
	placeholders := make([]string, len(ids))
	args := make([]driver.Value, 0, len(ids)+1)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, key.UserID)
	return mock.ExpectQuery(fmt.Sprintf(
		`SELECT id, JSON_UNQUOTE(JSON_EXTRACT(event, '$.timestamp')) FROM session_events
		WHERE id IN (%s) AND user_id = ? AND deleted_at IS NULL`,
		strings.Join(placeholders, ","),
	)).WithArgs(args...)
}

func TestGetEventRefsWithTimestamp_TwoPhase(t *testing.T) {
	createdAt := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	eventTime := createdAt.Add(time.Minute + 123*time.Nanosecond)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	for _, withCursor := range []bool{false, true} {
		t.Run(fmt.Sprintf("cursor=%t", withCursor), func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()
			s := createTestService(t, db)
			var before *eventRef
			if withCursor {
				before = &eventRef{id: 5, createdAt: createdAt}
			}
			expectTimestampRefIDs(mock, key, createdAt, before, 4).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).
					AddRow(4, createdAt).AddRow(3, createdAt).
					AddRow(2, createdAt).AddRow(1, createdAt))
			// Deliberately return timestamps in a different order. ID 2 was
			// deleted after the ID read; IDs 1 and 3 are legacy timestamps.
			expectTimestampValues(mock, key, 4, 3, 2, 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}).
					AddRow(1, nil).AddRow(4, eventTime.Format(time.RFC3339Nano)).AddRow(3, ""))

			refs, err := s.getEventRefsWithTimestamp(context.Background(), key, createdAt, before, 4)
			require.NoError(t, err)
			require.Equal(t, []eventRef{
				{id: 4, createdAt: createdAt, eventTimestamp: eventTime},
				{id: 3, createdAt: createdAt, eventTimestamp: createdAt},
				{id: 2, createdAt: createdAt, timestampMissing: true},
				{id: 1, createdAt: createdAt, eventTimestamp: createdAt},
			}, refs)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGetEventRefsWithTimestamp_Empty(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	expectTimestampRefIDs(mock, key, time.Time{}, nil, 2500).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}))
	refs, err := s.getEventRefsWithTimestamp(context.Background(), key, time.Time{}, nil, 2500)
	require.NoError(t, err)
	require.Empty(t, refs)
	// In particular, no second query with an empty IN list is issued.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventRefsWithTimestamp_Errors(t *testing.T) {
	createdAt := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	queryErr := errors.New("query failed")
	for _, tc := range []struct {
		name      string
		phase     int
		queryErr  error
		rows      *sqlmock.Rows
		wantError string
	}{
		{name: "ID query", phase: 1, queryErr: queryErr},
		{name: "ID scan", phase: 1,
			rows:      sqlmock.NewRows([]string{"id", "created_at"}).AddRow("bad-id", createdAt),
			wantError: "Scan error"},
		{name: "ID iteration", phase: 1,
			rows:      sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, createdAt).RowError(0, queryErr),
			wantError: "query failed"},
		{name: "timestamp query", phase: 2, queryErr: queryErr},
		{name: "cancellation", phase: 2, queryErr: context.Canceled},
		{name: "timestamp scan", phase: 2,
			rows:      sqlmock.NewRows([]string{"id", "event_timestamp"}).AddRow("bad-id", nil),
			wantError: "Scan error"},
		{name: "timestamp iteration", phase: 2,
			rows:      sqlmock.NewRows([]string{"id", "event_timestamp"}).AddRow(1, nil).RowError(0, queryErr),
			wantError: "query failed"},
		{name: "invalid timestamp", phase: 2,
			rows:      sqlmock.NewRows([]string{"id", "event_timestamp"}).AddRow(1, "invalid"),
			wantError: "parse event timestamp failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()
			s := createTestService(t, db)
			query := expectTimestampRefIDs(mock, key, createdAt, nil, 1)
			if tc.phase == 2 {
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, createdAt))
				query = expectTimestampValues(mock, key, 1)
			}
			if tc.queryErr != nil {
				query.WillReturnError(tc.queryErr)
			} else {
				query.WillReturnRows(tc.rows)
			}
			refs, err := s.getEventRefsWithTimestamp(context.Background(), key, createdAt, nil, 1)
			require.ErrorContains(t, err, "batch get events failed")
			require.Nil(t, refs)
			if tc.queryErr != nil {
				require.ErrorIs(t, err, tc.queryErr)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGetRecentEventRefsAfterEventTime_ContinuesPastDeletedAndCoveredRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	createdAt := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	cutoff := createdAt.Add(time.Hour)
	expectTimestampRefIDs(mock, key, createdAt, nil, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(5, createdAt).AddRow(4, createdAt))
	// The entire first batch disappears between reads; it is not EOF.
	expectTimestampValues(mock, key, 5, 4).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}))
	expectTimestampRefIDs(mock, key, createdAt, &eventRef{id: 4, createdAt: createdAt}, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(3, createdAt).AddRow(2, createdAt))
	// ID 2 is already covered by the summary. ID 3 is exactly on the
	// inclusive boundary, despite its database creation time being earlier.
	expectTimestampValues(mock, key, 3, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}).
			AddRow(2, cutoff.Add(-time.Nanosecond).Format(time.RFC3339Nano)).
			AddRow(3, cutoff.Format(time.RFC3339Nano)))
	expectTimestampRefIDs(mock, key, createdAt, &eventRef{id: 2, createdAt: createdAt}, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, createdAt))
	expectTimestampValues(mock, key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}).
			AddRow(1, cutoff.Add(time.Minute).Format(time.RFC3339Nano)))

	refs, err := s.getRecentEventRefsAfterEventTime(context.Background(), key, createdAt, cutoff, 2)
	require.NoError(t, err)
	require.Equal(t, []eventRef{
		{id: 3, createdAt: createdAt, eventTimestamp: cutoff},
		{id: 1, createdAt: createdAt, eventTimestamp: cutoff.Add(time.Minute)},
	}, refs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLastUserEventBeforeRefs_TimestampBatchDeleted(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	createdAt := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	cutoff := createdAt.Add(time.Hour)
	before := eventRef{id: 3, createdAt: createdAt}
	expectTimestampRefIDs(mock, key, createdAt, &before, userAnchorSearchBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(2, createdAt))
	expectTimestampValues(mock, key, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}))
	expectTimestampRefIDs(mock, key, createdAt, &eventRef{id: 2, createdAt: createdAt}, userAnchorSearchBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, createdAt))
	expectTimestampValues(mock, key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_timestamp"}).AddRow(1, cutoff.Format(time.RFC3339Nano)))
	user := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "anchor"}}},
	})
	user.Timestamp = cutoff
	payload, err := json.Marshal(user)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT id, event FROM session_events WHERE id IN (?) AND user_id = ? AND deleted_at IS NULL`).
		WithArgs(int64(1), key.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event"}).AddRow(1, payload))
	got, ok, err := s.getLastUserEventBeforeRefs(context.Background(), key, createdAt, cutoff, []eventRef{before})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, user.ID, got.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
