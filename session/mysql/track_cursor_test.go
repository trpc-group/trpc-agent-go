//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackcursor"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var cursorTestKey = session.Key{AppName: "app", UserID: "user", SessionID: "session"}
var cursorTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)

// cursorSnapshotClient verifies the transaction contract independently of SQL
// expectations. A plain transaction would not provide the same read snapshot.
type cursorSnapshotClient struct {
	*mockMySQLClient
	t *testing.T
}

func (c *cursorSnapshotClient) Transaction(ctx context.Context, fn storage.TxFunc, opts ...storage.TxOption) error {
	options := &sql.TxOptions{}
	for _, opt := range opts {
		opt(options)
	}
	require.True(c.t, options.ReadOnly)
	require.Equal(c.t, sql.LevelRepeatableRead, options.Isolation)
	return c.mockMySQLClient.Transaction(ctx, fn, opts...)
}

func newCursorUnitService(t *testing.T, opts ...ServiceOpt) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	s := createTestService(t, db, opts...)
	s.mysqlClient = &cursorSnapshotClient{mockMySQLClient: &mockMySQLClient{db: db}, t: t}
	return s, mock
}

func cursorStateRows(t *testing.T, id int64) *sqlmock.Rows {
	t.Helper()
	data, err := json.Marshal(SessionState{ID: cursorTestKey.SessionID, CreatedAt: cursorTestCreated})
	require.NoError(t, err)
	return sqlmock.NewRows([]string{"id", "state", "created_at"}).AddRow(id, data, cursorTestCreated.Truncate(time.Microsecond))
}

func expectCursorState(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("SELECT id, state, created_at FROM session_states").
		WithArgs(cursorTestKey.AppName, cursorTestKey.UserID, cursorTestKey.SessionID, sqlmock.AnyArg())
}

func expectCursorIDs(mock sqlmock.Sqlmock, after uint64) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("SELECT id FROM session_track_events.*id >.*ORDER BY id ASC").
		WithArgs(cursorTestKey.AppName, cursorTestKey.UserID, cursorTestKey.SessionID, "track", after, sqlmock.AnyArg())
}

func cursorEventBytes(t *testing.T, timestamp time.Time) []byte {
	t.Helper()
	data, err := json.Marshal(session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(`"same"`)})
	require.NoError(t, err)
	return data
}

func TestTrackCursorReadsAppendOrderAcrossBatches(t *testing.T) {
	s, mock := newCursorUnitService(t)
	mock.ExpectBegin()
	expectCursorState(mock).WillReturnRows(cursorStateRows(t, 42))
	ids := sqlmock.NewRows([]string{"id"})
	for id := 1; id <= 130; id++ {
		ids.AddRow(id)
	}
	expectCursorIDs(mock, 0).WillReturnRows(ids)
	for first := 1; first <= 130; first += 128 {
		last := min(first+127, 130)
		args := []driver.Value{cursorTestKey.AppName, cursorTestKey.UserID, cursorTestKey.SessionID, "track"}
		rows := sqlmock.NewRows([]string{"id", "event"})
		for id := first; id <= last; id++ {
			args = append(args, int64(id))
		}
		// Payload lookups need not return rows in cursor order. Equal
		// timestamps must still represent separate appends.
		for id := last; id >= first; id-- {
			data := cursorEventBytes(t, cursorTestCreated)
			var event session.TrackEvent
			require.NoError(t, json.Unmarshal(data, &event))
			event.Payload = json.RawMessage(fmt.Sprint(id))
			data, err := json.Marshal(event)
			require.NoError(t, err)
			rows.AddRow(id, data)
		}
		mock.ExpectQuery("SELECT id, event FROM session_track_events.*id IN").WithArgs(args...).WillReturnRows(rows)
	}
	mock.ExpectCommit()
	first, err := s.GetTrackEvents(context.Background(), cursorTestKey, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Len(t, first.Events, 130)
	for i, event := range first.Events {
		require.Equal(t, json.RawMessage(fmt.Sprint(i+1)), event.Payload)
	}
	position, err := trackcursor.Decode(*first.Cursor, "mysql/session_track_events", cursorTestKey, "track")
	require.NoError(t, err)
	require.Equal(t, uint64(130), position.Position)
	require.Equal(t, "42/"+cursorTestCreated.Format(time.RFC3339Nano), position.Generation)

	mock.ExpectBegin()
	expectCursorState(mock).WillReturnRows(cursorStateRows(t, 42))
	mock.ExpectQuery("SELECT COUNT.*FROM session_track_events").
		WithArgs(uint64(130), cursorTestKey.AppName, cursorTestKey.UserID, cursorTestKey.SessionID, "track", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	expectCursorIDs(mock, 130).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(131))
	mock.ExpectQuery("SELECT id, event FROM session_track_events.*id IN").
		WithArgs(cursorTestKey.AppName, cursorTestKey.UserID, cursorTestKey.SessionID, "track", uint64(131)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event"}).AddRow(131, cursorEventBytes(t, cursorTestCreated.Add(-time.Hour))))
	mock.ExpectCommit()
	next, err := s.GetTrackEvents(context.Background(), cursorTestKey, "track", session.WithTrackCursor(*first.Cursor))
	require.NoError(t, err)
	require.Len(t, next.Events, 1)
	require.Equal(t, cursorTestCreated.Add(-time.Hour), next.Events[0].Timestamp)
	require.NotEqual(t, *first.Cursor, *next.Cursor)
}

func TestTrackCursorStorageFailuresDoNotAdvance(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	for _, mode := range []string{"begin", "session query", "state scan", "state json", "duplicate session", "id query", "id scan", "id row error", "payload query", "payload scan", "payload json", "missing payload", "commit"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := newCursorUnitService(t)
			if mode == "begin" {
				mock.ExpectBegin().WillReturnError(storageErr)
			} else {
				mock.ExpectBegin()
				state := expectCursorState(mock)
				switch mode {
				case "session query":
					state.WillReturnError(storageErr)
				case "state scan":
					state.WillReturnRows(sqlmock.NewRows([]string{"id", "state", "created_at"}).AddRow("bad", `{}`, cursorTestCreated))
				case "state json":
					state.WillReturnRows(sqlmock.NewRows([]string{"id", "state", "created_at"}).AddRow(42, `{`, cursorTestCreated))
				case "duplicate session":
					state.WillReturnRows(cursorStateRows(t, 42).AddRow(43, `{}`, cursorTestCreated))
				default:
					state.WillReturnRows(cursorStateRows(t, 42))
					ids := expectCursorIDs(mock, 0)
					switch mode {
					case "id query":
						ids.WillReturnError(storageErr)
					case "id scan":
						ids.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bad"))
					case "id row error":
						ids.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, storageErr))
					case "commit":
						ids.WillReturnRows(sqlmock.NewRows([]string{"id"}))
					default:
						ids.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
						payload := mock.ExpectQuery("SELECT id, event FROM session_track_events.*id IN")
						switch mode {
						case "payload query":
							payload.WillReturnError(storageErr)
						case "payload scan":
							payload.WillReturnRows(sqlmock.NewRows([]string{"id", "event"}).AddRow("bad", `{}`))
						case "payload json":
							payload.WillReturnRows(sqlmock.NewRows([]string{"id", "event"}).AddRow(1, `{`))
						case "missing payload":
							payload.WillReturnRows(sqlmock.NewRows([]string{"id", "event"}))
						}
					}
				}
				if mode == "commit" {
					mock.ExpectCommit().WillReturnError(storageErr)
				} else {
					mock.ExpectRollback()
				}
			}
			got, err := s.GetTrackEvents(context.Background(), cursorTestKey, "track", session.WithTrackCursor(""))
			require.Error(t, err)
			require.Nil(t, got, "a failed read must not expose a partial result or new cursor")
			if mode == "duplicate session" || mode == "missing payload" {
				require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
			}
		})
	}
}

func TestTrackCursorSessionAndAnchorInvalidation(t *testing.T) {
	for _, mode := range []string{"absent initial session", "empty track", "deleted session", "recreated session", "expired anchor", "anchor query error"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := newCursorUnitService(t)
			position, err := trackcursor.Decode("", "mysql/session_track_events", cursorTestKey, "track")
			require.NoError(t, err)
			if mode != "absent initial session" && mode != "empty track" {
				position.Generation = "42/" + cursorTestCreated.Format(time.RFC3339Nano)
				position.Position = 7
			}
			mock.ExpectBegin()
			switch mode {
			case "absent initial session", "deleted session":
				expectCursorState(mock).WillReturnRows(sqlmock.NewRows([]string{"id", "state", "created_at"}))
			case "recreated session":
				expectCursorState(mock).WillReturnRows(cursorStateRows(t, 43))
			default:
				expectCursorState(mock).WillReturnRows(cursorStateRows(t, 42))
				if mode == "empty track" {
					expectCursorIDs(mock, 0).WillReturnRows(sqlmock.NewRows([]string{"id"}))
				} else {
					anchor := mock.ExpectQuery("SELECT COUNT.*FROM session_track_events")
					if mode == "expired anchor" {
						anchor.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					} else {
						anchor.WillReturnError(errors.New("anchor unavailable"))
					}
				}
			}
			valid := mode == "absent initial session" || mode == "empty track"
			if valid {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			got, err := s.GetTrackEvents(context.Background(), cursorTestKey, "track", session.WithTrackCursor(trackcursor.Encode(position)))
			if valid {
				require.NoError(t, err)
				require.Empty(t, got.Events)
				require.NotNil(t, got.Cursor)
			} else {
				require.Error(t, err)
				require.Nil(t, got)
				if mode != "anchor query error" {
					require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
				}
			}
		})
	}
}

func TestTrackCursorPolicyCannotBypassValidation(t *testing.T) {
	denied := errors.New("permission denied")
	for _, mode := range []string{"invalid token", "event filter", "denied", "hook filter", "projection missing", "projection failure"} {
		t.Run(mode, func(t *testing.T) {
			var options []ServiceOpt
			if mode != "invalid token" && mode != "event filter" {
				options = append(options, WithGetSessionHook(func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
					if mode == "denied" {
						return nil, denied
					}
					if mode == "hook filter" {
						c.Options.EventNum = -1
					}
					return next()
				}))
			}
			s, mock := newCursorUnitService(t, options...)
			cursor := ""
			if mode == "invalid token" {
				cursor = "not a cursor"
			}
			opts := []session.Option{session.WithTrackCursor(cursor)}
			if mode == "event filter" {
				opts = append(opts, session.WithEventNum(1))
			}
			if mode == "projection missing" || mode == "projection failure" {
				mock.ExpectBegin()
				expectCursorState(mock).WillReturnRows(cursorStateRows(t, 42))
				projection := mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states")
				if mode == "projection missing" {
					projection.WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}))
				} else {
					projection.WillReturnError(denied)
				}
				mock.ExpectRollback()
			}
			got, err := s.GetTrackEvents(context.Background(), cursorTestKey, "track", opts...)
			require.Error(t, err)
			require.Nil(t, got)
			if mode == "denied" || mode == "projection failure" {
				require.ErrorIs(t, err, denied)
			}
		})
	}
}

func TestCursorTransactionReadOnlyAndEarlyStop(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	client := cursorTransaction{tx: tx}
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
	seen := 0
	err = client.Query(context.Background(), func(*sql.Rows) error { seen++; return storage.ErrBreak }, "SELECT id")
	require.NoError(t, err)
	require.Equal(t, 1, seen)
	_, err = client.Exec(context.Background(), "DELETE FROM session_states")
	require.ErrorContains(t, err, "read-only")
	err = client.Transaction(context.Background(), func(*sql.Tx) error { t.Fatal("nested transaction ran"); return nil })
	require.ErrorContains(t, err, "nested")
	require.NoError(t, client.Close())
	mock.ExpectCommit()
	require.NoError(t, tx.Commit(), "closing the view must leave the owning transaction usable")
	require.NoError(t, mock.ExpectationsWereMet())
}
