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

func trimTestEvent(id, requestID string, timestamp time.Time) event.Event {
	return event.Event{
		ID: id, RequestID: requestID, Timestamp: timestamp,
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewUserMessage(id)}},
		},
	}
}

func trimTestEvents(requestIDs ...string) []event.Event {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	events := make([]event.Event, len(requestIDs))
	for i, requestID := range requestIDs {
		events[i] = trimTestEvent(fmt.Sprintf("e%d", i+1), requestID, base.Add(time.Duration(i)*time.Second))
	}
	return events
}

func expectTrimSession(mock sqlmock.Sqlmock, svc *Service, key session.Key) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery(fmt.Sprintf(`SELECT created_at, expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND deleted_at IS NULL FOR UPDATE`, svc.tableSessionStates)).
		WithArgs(key.AppName, key.UserID, key.SessionID)
}

func expectTrimEvents(mock sqlmock.Sqlmock, svc *Service, key session.Key, createdAt time.Time) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery(fmt.Sprintf(`SELECT id, event FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND created_at >= ? AND deleted_at IS NULL FOR UPDATE`, svc.tableSessionEvents)).
		WithArgs(key.AppName, key.UserID, key.SessionID, createdAt)
}

func trimTestRows(t *testing.T, events []event.Event) *sqlmock.Rows {
	t.Helper()
	rows := sqlmock.NewRows([]string{"id", "event"})
	for i, evt := range events {
		payload, err := json.Marshal(evt)
		require.NoError(t, err)
		rows.AddRow(int64(i+1), payload)
	}
	return rows
}

func expectTrimMutation(mock sqlmock.Sqlmock, svc *Service, key session.Key, ids []int) *sqlmock.ExpectedExec {
	query := "DELETE FROM " + svc.tableSessionEvents
	var args []driver.Value
	if svc.opts.softDelete {
		query = "UPDATE " + svc.tableSessionEvents + " SET deleted_at = ?"
		args = append(args, sqlmock.AnyArg())
	}
	query += " WHERE app_name = ? AND user_id = ? AND session_id = ?" +
		" AND deleted_at IS NULL AND id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	args = append(args, key.AppName, key.UserID, key.SessionID)
	for _, id := range ids {
		args = append(args, int64(id))
	}
	return mock.ExpectExec(query).WithArgs(args...)
}

func TestTrimConversations(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	type testCase struct {
		name    string
		events  []event.Event
		options []TrimConversationOption
		wantIDs []int
	}
	cases := []testCase{
		{name: "default", events: trimTestEvents("A", "B", "B"), wantIDs: []int{2, 3}},
		{name: "zero", events: trimTestEvents("A", "B", "B"), options: []TrimConversationOption{WithCount(0)}, wantIDs: []int{2, 3}},
		{name: "negative", events: trimTestEvents("A", "B", "B"), options: []TrimConversationOption{WithCount(-2)}, wantIDs: []int{2, 3}},
		{name: "last option wins", events: trimTestEvents("A", "B", "C"), options: []TrimConversationOption{WithCount(1), WithCount(2)}, wantIDs: []int{2, 3}},
		{name: "excess count", events: trimTestEvents("A", "B"), options: []TrimConversationOption{WithCount(int(^uint(0) >> 1))}, wantIDs: []int{1, 2}},
		{name: "empty", events: nil},
		{name: "no request IDs", events: trimTestEvents("", "")},
		{name: "skip empty request IDs", events: trimTestEvents("A", "", "B", ""), wantIDs: []int{3}},
		{name: "interleaved one", events: trimTestEvents("A", "B", "A"), wantIDs: []int{1, 3}},
		{name: "interleaved two", events: trimTestEvents("A", "B", "A", "C", "B"), options: []TrimConversationOption{WithCount(2)}, wantIDs: []int{2, 4, 5}},
		{
			name: "event timestamps determine selection and return order",
			events: []event.Event{
				trimTestEvent("e1", "A", base.Add(3*time.Second)),
				trimTestEvent("e2", "B", base.Add(2*time.Second)),
				trimTestEvent("e3", "A", base.Add(time.Second)),
			},
			wantIDs: []int{3, 1},
		},
		{
			name: "timestamp ties use event ID",
			events: []event.Event{
				trimTestEvent("z", "A", base), trimTestEvent("a", "B", base),
			},
			wantIDs: []int{1},
		},
		{
			name: "duplicate event IDs use row ID",
			events: []event.Event{
				trimTestEvent("same", "A", base), trimTestEvent("same", "B", base),
			},
			wantIDs: []int{2},
		},
	}
	for _, count := range []int{100, 101, 201, 1001} {
		requests := make([]string, count+1)
		requests[0] = "older"
		ids := make([]int, count)
		for i := 0; i < count; i++ {
			requests[i+1] = "recent"
			ids[i] = i + 2
		}
		cases = append(cases, testCase{
			name: fmt.Sprintf("complete turn with %d events", count), events: trimTestEvents(requests...), wantIDs: ids,
		})
	}
	requests, ids := make([]string, 120), make([]int, 120)
	for i := range requests {
		requests[i] = "A"
		if i >= 60 {
			requests[i] = "B"
		}
		ids[i] = i + 1
	}
	cases = append(cases, testCase{
		name: "two turns with 60 events each", events: trimTestEvents(requests...),
		options: []TrimConversationOption{WithCount(2)}, wantIDs: ids,
	})
	for _, softDelete := range []bool{true, false} {
		for _, tdsql := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("soft=%t/tdsql=%t/%s", softDelete, tdsql, tc.name), func(t *testing.T) {
					db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
					require.NoError(t, err)
					defer db.Close()
					svc := createTestService(t, db, WithSoftDelete(softDelete), WithTDSQLSharding(tdsql),
						WithSessionEventLimit(1), WithGetSessionHook(func(*session.GetSessionContext, func() (*session.Session, error)) (*session.Session, error) {
							t.Fatal("trim must not invoke GetSession hooks")
							return nil, nil
						}))
					// Check scoped SQL also works with configured table names.
					svc.tableSessionStates = "tenant_session_states"
					svc.tableSessionEvents = "tenant_session_events"
					key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
					mock.ExpectBegin()
					expectTrimSession(mock, svc, key).WillReturnRows(sqlmock.NewRows([]string{"created_at", "expires_at"}).AddRow(base, nil))
					expectTrimEvents(mock, svc, key, base).WillReturnRows(trimTestRows(t, tc.events)).RowsWillBeClosed()
					// The bound IDs are explicit expectations, independent of selection logic.
					for start := 0; start < len(tc.wantIDs); start += 500 {
						batch := tc.wantIDs[start:min(start+500, len(tc.wantIDs))]
						expectTrimMutation(mock, svc, key, batch).WillReturnResult(sqlmock.NewResult(0, int64(len(batch))))
					}
					mock.ExpectCommit()
					deleted, err := svc.TrimConversations(context.Background(), key, tc.options...)
					require.NoError(t, err)
					var want []event.Event
					for _, id := range tc.wantIDs {
						want = append(want, tc.events[id-1])
					}
					require.Equal(t, want, deleted)
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		}
	}
}

func TestTrimConversations_InactiveSession(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()
			svc := createTestService(t, db)
			key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
			rows := sqlmock.NewRows([]string{"created_at", "expires_at"})
			if !missing {
				rows.AddRow(time.Now().Add(-time.Hour), time.Now().Add(-time.Minute))
			}
			mock.ExpectBegin()
			expectTrimSession(mock, svc, key).WillReturnRows(rows)
			mock.ExpectCommit()
			deleted, err := svc.TrimConversations(context.Background(), key)
			require.NoError(t, err)
			require.Nil(t, deleted)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTrimConversations_InvalidKeyAndCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := createTestService(t, db)
	for _, key := range []session.Key{
		{}, {UserID: "user", SessionID: "session"},
		{AppName: "app", SessionID: "session"}, {AppName: "app", UserID: "user"},
	} {
		deleted, err := svc.TrimConversations(context.Background(), key)
		require.Equal(t, key.CheckSessionKey(), err)
		require.Nil(t, deleted)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deleted, err := svc.TrimConversations(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "session"})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTrimConversations_Failures(t *testing.T) {
	for _, stage := range []string{"begin", "lock", "read", "scan", "decode", "iterate", "delete", "second batch", "commit", "cancel read"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()
			svc := createTestService(t, db)
			key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
			base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			cause := errors.New("injected failure")
			if stage == "cancel read" {
				cause = context.Canceled
			}
			begin := mock.ExpectBegin()
			if stage == "begin" {
				begin.WillReturnError(cause)
			} else {
				lock := expectTrimSession(mock, svc, key)
				if stage == "lock" {
					lock.WillReturnError(cause)
				} else {
					lock.WillReturnRows(sqlmock.NewRows([]string{"created_at", "expires_at"}).AddRow(base, nil))
					read := expectTrimEvents(mock, svc, key, base)
					if stage == "read" || stage == "cancel read" {
						read.WillReturnError(cause)
					} else {
						events := trimTestEvents("A")
						if stage == "second batch" {
							requests := make([]string, 501)
							for i := range requests {
								requests[i] = "A"
							}
							events = trimTestEvents(requests...)
						}
						rows := trimTestRows(t, events)
						switch stage {
						case "scan":
							rows = sqlmock.NewRows([]string{"id", "event"}).AddRow("invalid row ID", "{}")
						case "decode":
							rows = sqlmock.NewRows([]string{"id", "event"}).AddRow(1, "invalid JSON")
						case "iterate":
							rows.RowError(0, cause)
						}
						read.WillReturnRows(rows).RowsWillBeClosed()
						if stage == "delete" || stage == "commit" {
							mutation := expectTrimMutation(mock, svc, key, []int{1})
							if stage == "delete" {
								mutation.WillReturnError(cause)
							} else {
								mutation.WillReturnResult(sqlmock.NewResult(0, 1))
							}
						}
						if stage == "second batch" {
							ids := make([]int, 500)
							for i := range ids {
								ids[i] = i + 1
							}
							expectTrimMutation(mock, svc, key, ids).WillReturnResult(sqlmock.NewResult(0, 500))
							expectTrimMutation(mock, svc, key, []int{501}).WillReturnError(cause)
						}
					}
				}
				if stage == "commit" {
					mock.ExpectCommit().WillReturnError(cause)
				} else {
					mock.ExpectRollback()
				}
			}
			deleted, err := svc.TrimConversations(context.Background(), key)
			require.Error(t, err)
			require.Nil(t, deleted)
			switch stage {
			case "scan":
				require.ErrorContains(t, err, "scan trim event")
			case "decode":
				require.ErrorContains(t, err, "unmarshal trim event")
				var syntaxErr *json.SyntaxError
				require.ErrorAs(t, err, &syntaxErr)
			default:
				require.ErrorIs(t, err, cause)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTrimConversations_DoesNotDrainAsyncQueue(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()
	svc := createTestService(t, db, WithEnableAsyncPersist(true))
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	events := trimTestEvents("persisted", "queued")
	queue := make(chan *sessionEventPair, 1)
	pending := &sessionEventPair{key: key, event: &events[1]}
	queue <- pending
	svc.eventPairChans = []chan *sessionEventPair{queue}
	base := events[0].Timestamp
	mock.ExpectBegin()
	expectTrimSession(mock, svc, key).WillReturnRows(sqlmock.NewRows([]string{"created_at", "expires_at"}).AddRow(base, nil))
	expectTrimEvents(mock, svc, key, base).WillReturnRows(trimTestRows(t, events[:1]))
	expectTrimMutation(mock, svc, key, []int{1}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deleted, err := svc.TrimConversations(ctx, key)
	require.NoError(t, err)
	require.Equal(t, events[:1], deleted)
	require.Len(t, queue, 1)
	require.Same(t, pending, <-queue)
	require.NoError(t, mock.ExpectationsWereMet())
}
