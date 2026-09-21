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
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func TestService_GetEventWindowMySQLIntegration(t *testing.T) {
	dsn := mysqlIntegrationDSN(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	prefix := prepareSummaryIntegrationSchema(t, db, dsn)
	svc := newSummaryIntegrationService(t, dsn, prefix)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	eventTable := sqldb.BuildTableName(prefix, sqldb.TableNameSessionEvents)
	stateTable := sqldb.BuildTableName(prefix, sqldb.TableNameSessionStates)
	base := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)

	type row struct {
		id      string
		offset  int
		role    model.Role
		deleted bool
	}
	numberedIDs := func(prefix string, count int) []string {
		ids := make([]string, count)
		for i := range ids {
			ids[i] = fmt.Sprintf("%s-%02d", prefix, i)
		}
		return ids
	}
	tests := []struct {
		name          string
		before, after int
		roles         []model.Role
		makeRows      func() []row
		anchor        string
		want          []string
	}{
		{
			name:   "same timestamp preserves insertion order on both sides",
			before: 1,
			after:  1,
			makeRows: func() []row {
				return []row{
					{id: "same-before", offset: 2, role: model.RoleUser},
					{id: "same-anchor", offset: 2, role: model.RoleUser},
					{id: "same-after", offset: 2, role: model.RoleUser},
				}
			},
			anchor: "same-anchor",
			want:   []string{"same-before", "same-anchor", "same-after"},
		},
		{
			name:   "both sides cross a 64 row timestamp batch",
			before: 65,
			after:  65,
			makeRows: func() []row {
				rows := make([]row, 0, 131)
				for i := 0; i < 65; i++ {
					rows = append(rows, row{id: fmt.Sprintf("before-%02d", i), offset: 1, role: model.RoleUser})
				}
				rows = append(rows, row{id: "batch-anchor", offset: 2, role: model.RoleUser})
				for i := 0; i < 65; i++ {
					rows = append(rows, row{id: fmt.Sprintf("after-%02d", i), offset: 3, role: model.RoleUser})
				}
				return rows
			},
			anchor: "batch-anchor",
			want:   append(append(numberedIDs("before", 65), "batch-anchor"), numberedIDs("after", 65)...),
		},
		{
			name:   "role filter crosses same timestamp batches on both sides",
			before: 1,
			after:  1,
			roles:  []model.Role{model.RoleAssistant},
			makeRows: func() []row {
				rows := []row{
					{id: "before-far", offset: 1, role: model.RoleAssistant},
					{id: "before-near", offset: 2, role: model.RoleAssistant},
				}
				for i := 0; i < 64; i++ {
					rows = append(rows, row{id: fmt.Sprintf("before-user-%02d", i), offset: 2, role: model.RoleUser})
				}
				rows = append(rows, row{id: "role-anchor", offset: 3, role: model.RoleAssistant})
				for i := 0; i < 64; i++ {
					rows = append(rows, row{id: fmt.Sprintf("after-user-%02d", i), offset: 4, role: model.RoleUser})
				}
				rows = append(rows,
					row{id: "after-near", offset: 4, role: model.RoleAssistant},
					row{id: "after-far", offset: 5, role: model.RoleAssistant},
				)
				return rows
			},
			anchor: "role-anchor",
			want:   []string{"before-near", "role-anchor", "after-near"},
		},
		{
			name:   "more than a batch at the anchor timestamp on both sides",
			before: 70,
			after:  70,
			makeRows: func() []row {
				var rows []row
				for i := 0; i < 141; i++ {
					rows = append(rows, row{id: fmt.Sprintf("tie-%02d", i), offset: 2, role: model.RoleUser})
				}
				return rows
			},
			anchor: "tie-70",
			want:   numberedIDs("tie", 141),
		},
		{
			name:   "incomplete boundary timestamp must not end a short batch",
			before: 1,
			after:  1,
			roles:  []model.Role{model.RoleAssistant},
			makeRows: func() []row {
				var rows []row
				for i := 0; i < 70; i++ {
					rows = append(rows, row{id: fmt.Sprintf("far-before-%02d", i), offset: 1, role: model.RoleAssistant})
				}
				rows = append(rows,
					row{id: "near-before", offset: 2, role: model.RoleUser},
					row{id: "boundary-anchor", offset: 3, role: model.RoleAssistant},
					row{id: "near-after", offset: 4, role: model.RoleUser},
				)
				for i := 0; i < 70; i++ {
					rows = append(rows, row{id: fmt.Sprintf("far-after-%02d", i), offset: 5, role: model.RoleAssistant})
				}
				return rows
			},
			anchor: "boundary-anchor",
			want:   []string{"far-before-69", "boundary-anchor", "far-after-00"},
		},
		{
			name:   "distinct timestamps remain ordered",
			before: 1,
			after:  1,
			makeRows: func() []row {
				return []row{
					{id: "distinct-before", offset: 1, role: model.RoleUser},
					{id: "distinct-anchor", offset: 2, role: model.RoleUser},
					{id: "distinct-after", offset: 3, role: model.RoleUser},
				}
			},
			anchor: "distinct-anchor",
			want:   []string{"distinct-before", "distinct-anchor", "distinct-after"},
		},
		{
			name: "zero bounds return only the anchor",
			makeRows: func() []row {
				return []row{{id: "zero-anchor", offset: 2, role: model.RoleUser}}
			},
			anchor: "zero-anchor",
			want:   []string{"zero-anchor"},
		},
		{
			name:   "old generations and soft deleted neighbors are excluded",
			before: 10,
			after:  10,
			makeRows: func() []row {
				return []row{
					{id: "old-generation", offset: -1, role: model.RoleUser},
					{id: "live-before", offset: 1, role: model.RoleUser},
					{id: "deleted-before", offset: 2, role: model.RoleUser, deleted: true},
					{id: "generation-anchor", offset: 3, role: model.RoleUser},
					{id: "deleted-after", offset: 4, role: model.RoleUser, deleted: true},
					{id: "live-after", offset: 5, role: model.RoleUser},
				}
			},
			anchor: "generation-anchor",
			want:   []string{"live-before", "generation-anchor", "live-after"},
		},
	}
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := session.Key{
				AppName:   "window-app",
				UserID:    "window-user",
				SessionID: fmt.Sprintf("window-session-%d", index),
			}
			_, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, fmt.Sprintf(
				"UPDATE `%s` SET created_at=?, updated_at=? WHERE app_name=? AND user_id=? AND session_id=? AND deleted_at IS NULL",
				stateTable,
			), base, base, key.AppName, key.UserID, key.SessionID)
			require.NoError(t, err)
			insert := func(fixture row) {
				var deletedAt any
				if fixture.deleted {
					deletedAt = base.Add(time.Hour)
				}
				_, err := db.ExecContext(ctx, fmt.Sprintf(
					"INSERT INTO `%s` (app_name,user_id,session_id,event,created_at,deleted_at) VALUES (?,?,?,?,?,?)",
					eventTable,
				), key.AppName, key.UserID, key.SessionID,
					mysqlWindowEventBytes(t, fixture.id, fixture.role, fixture.id),
					base.Add(time.Duration(fixture.offset)*time.Second), deletedAt)
				require.NoError(t, err)
			}
			for _, fixture := range tc.makeRows() {
				insert(fixture)
			}
			got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
				Key:           key,
				AnchorEventID: tc.anchor,
				Before:        tc.before,
				After:         tc.after,
				Roles:         tc.roles,
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, mysqlWindowIDs(got))
		})
	}
}

func TestService_GetEventWindowLargeTiesMySQLIntegration(t *testing.T) {
	dsn := mysqlIntegrationDSN(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	prefix := prepareSummaryIntegrationSchema(t, db, dsn)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, "SET SESSION sort_buffer_size = 32768")
	require.NoError(t, err)
	// Reuse this connection so every window query has the restricted sort buffer.
	svc := &Service{
		mysqlClient:        storagemysql.WrapSQLDB(db),
		tableSessionStates: sqldb.BuildTableName(prefix, sqldb.TableNameSessionStates),
		tableSessionEvents: sqldb.BuildTableName(prefix, sqldb.TableNameSessionEvents),
	}
	base := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (app_name,user_id,session_id,state,created_at) VALUES (?,?,?,? ,?)", svc.tableSessionStates), "app", "user", "ties", "{}", base)
	require.NoError(t, err)
	for i := 0; i < 140; i++ {
		_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (app_name,user_id,session_id,event,created_at) VALUES (?,?,?,?,?)", svc.tableSessionEvents),
			"app", "user", "ties", mysqlWindowEventBytes(t, "duplicate", model.RoleUser, fmt.Sprintf("%03d", i)+strings.Repeat("x", 32768)), base)
		require.NoError(t, err)
	}
	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           session.Key{AppName: "app", UserID: "user", SessionID: "ties"},
		AnchorEventID: "duplicate", Before: 1, After: 130,
	})
	require.NoError(t, err)
	require.Len(t, got.Entries, 131)
	for i, entry := range got.Entries {
		// The first duplicate is the anchor; subsequent rows are ordered by id.
		require.True(t, strings.HasPrefix(entry.Event.Choices[0].Message.Content, fmt.Sprintf("%03d", i)))
	}
}

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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "u2", key.AppName, key.UserID, key.SessionID, base, "u2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(3), base.Add(2*time.Minute), mysqlWindowEventBytes(t, "u2", model.RoleUser, "three")))
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), base.Add(2*time.Minute), int64(3), eventWindowBatchSize+1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(2), base.Add(time.Minute), mysqlWindowEventBytes(t, "a1", model.RoleAssistant, "two")).
			AddRow(int64(1), base, mysqlWindowEventBytes(t, "u1", model.RoleUser, "one")))
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base.Add(2*time.Minute), base.Add(2*time.Minute), int64(3), eventWindowBatchSize+1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(4), base.Add(3*time.Minute), mysqlWindowToolEventBytes(t, "t1", "calc", "four")).
			AddRow(int64(5), base.Add(4*time.Minute), mysqlWindowEventBytes(t, "u3", model.RoleUser, "five")))

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

func TestService_QueryWindowNeighborBatchSQLAndRowIDOrder(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	sessionCreatedAt := base.Add(-time.Hour)
	cursorID := int64(10)

	type metadataRow struct {
		rowID   int64
		content string
	}
	tests := []struct {
		name       string
		before     bool
		comparator string
		orderBy    string
		metadata   []metadataRow
	}{
		{
			name:       "after uses ascending created_at and id",
			comparator: `(created_at > ? OR (created_at = ? AND id > ?))`,
			orderBy:    `ORDER BY created_at ASC`,
			metadata: []metadataRow{
				{rowID: 11, content: "after-first"},
				{rowID: 12, content: "after-second"},
			},
		},
		{
			name:       "before uses descending created_at and id",
			before:     true,
			comparator: `(created_at < ? OR (created_at = ? AND id < ?))`,
			orderBy:    `ORDER BY created_at DESC`,
			metadata: []metadataRow{
				{rowID: 9, content: "before-near"},
				{rowID: 8, content: "before-far"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			svc := createTestService(t, db)
			metadataQuery := fmt.Sprintf(`SELECT id, created_at, event FROM session_events
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at >= ?
AND deleted_at IS NULL
AND %s
%s
LIMIT ?`, tc.comparator, tc.orderBy)
			metadataRows := sqlmock.NewRows([]string{"id", "created_at", "event"})
			for index := len(tc.metadata) - 1; index >= 0; index-- {
				// Time-only SQL does not guarantee the order of equal-time rows.
				row := tc.metadata[index]
				metadataRows.AddRow(row.rowID, base, mysqlWindowEventBytes(t, "duplicate-event-id", model.RoleUser, row.content))
			}
			mock.ExpectQuery("^"+regexp.QuoteMeta(metadataQuery)+"$").
				WithArgs(key.AppName, key.UserID, key.SessionID, sessionCreatedAt, base, base, cursorID, eventWindowBatchSize+1).
				WillReturnRows(metadataRows)

			got, more, err := svc.queryWindowNeighborBatch(ctx, key, sessionCreatedAt, base, cursorID, tc.before)
			require.NoError(t, err)
			require.False(t, more)
			require.Len(t, got, len(tc.metadata))
			for index, row := range tc.metadata {
				require.Equal(t, row.content, got[index].entry.Event.Response.Choices[0].Message.Content)
				require.Equal(t, "duplicate-event-id", got[index].entry.Event.ID)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestService_GetEventWindowAdvancesCursorAfterMissingPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := createTestService(t, db)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	anchorTime := base.Add(2 * time.Minute)
	firstBatchTime := base.Add(3 * time.Minute)

	mock.ExpectQuery("SELECT created_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(base))
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "anchor", key.AppName, key.UserID, key.SessionID, base, "anchor").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(100), anchorTime, mysqlWindowEventBytes(t, "anchor", model.RoleUser, "anchor")))

	prefix := sqlmock.NewRows([]string{"id", "created_at", "event"})
	for id := int64(101); id <= int64(101+eventWindowBatchSize); id++ {
		prefix.AddRow(id, firstBatchTime, mysqlWindowEventBytes(t, "deleted", model.RoleUser, "deleted"))
	}
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, anchorTime, anchorTime, int64(100), eventWindowBatchSize+1).
		WillReturnRows(prefix)
	firstBatch := sqlmock.NewRows([]string{"id", "created_at"})
	payloadArgs := make([]driver.Value, 0, eventWindowBatchSize+1)
	for id := int64(101); id < int64(101+eventWindowBatchSize); id++ {
		firstBatch.AddRow(id, firstBatchTime)
		payloadArgs = append(payloadArgs, id)
	}
	payloadArgs = append(payloadArgs, key.UserID)
	mock.ExpectQuery("SELECT id, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, firstBatchTime, eventWindowBatchSize).
		WillReturnRows(firstBatch)
	// The rows were selected as metadata, then soft-deleted before the payload
	// query. They must still move the (created_at, id) cursor to id 164.
	mock.ExpectQuery("SELECT id, event FROM session_events").
		WithArgs(payloadArgs...).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event"}))
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, firstBatchTime, firstBatchTime, int64(164), eventWindowBatchSize+1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(165), base.Add(4*time.Minute), mysqlWindowEventBytes(t, "after", model.RoleUser, "after")))

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		After:         1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"anchor", "after"}, mysqlWindowIDs(got))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_QueryWindowTimestampBatch(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, sameTime := range []bool{false, true} {
			t.Run(fmt.Sprintf("before=%t/sameTime=%t", before, sameTime), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				svc := createTestService(t, db)
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
				base := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
				cursor := base.Add(-time.Second)
				if sameTime {
					cursor = base
				}
				query := `SELECT id, created_at FROM session_events
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at = ? AND deleted_at IS NULL`
				args := []driver.Value{key.AppName, key.UserID, key.SessionID, base}
				ids := []int64{11, 12}
				if sameTime {
					if before {
						query += ` AND id < ?`
					} else {
						query += ` AND id > ?`
					}
					args = append(args, int64(10))
				}
				if before {
					query += ` ORDER BY id DESC LIMIT ?`
					ids = []int64{9, 8}
				} else {
					query += ` ORDER BY id ASC LIMIT ?`
				}
				args = append(args, eventWindowBatchSize)
				mock.ExpectQuery("^" + regexp.QuoteMeta(query) + "$").WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(ids[0], base).AddRow(ids[1], base))
				mock.ExpectQuery("^"+regexp.QuoteMeta(`SELECT id, event FROM session_events WHERE id IN (?,?) AND user_id = ? AND deleted_at IS NULL`)+"$").
					WithArgs(ids[0], ids[1], key.UserID).
					WillReturnRows(sqlmock.NewRows([]string{"id", "event"}).
						AddRow(ids[1], mysqlWindowEventBytes(t, "duplicate", model.RoleUser, "second")).
						AddRow(ids[0], mysqlWindowEventBytes(t, "duplicate", model.RoleUser, "first")))
				got, err := svc.queryWindowTimestampBatch(context.Background(), key, base, cursor, 10, before)
				require.NoError(t, err)
				require.Len(t, got, 2)
				require.Equal(t, "first", got[0].entry.Event.Choices[0].Message.Content)
				require.Equal(t, "second", got[1].entry.Event.Choices[0].Message.Content)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestService_QueryWindowNeighborBatchDisappearingTimestamp(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	prefix := sqlmock.NewRows([]string{"id", "created_at", "event"})
	for id := int64(11); id < 76; id++ {
		prefix.AddRow(id, base, mysqlWindowEventBytes(t, "deleted", model.RoleUser, "deleted"))
	}
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base, base, int64(10), eventWindowBatchSize+1).
		WillReturnRows(prefix)
	mock.ExpectQuery("SELECT id, created_at FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, int64(10), eventWindowBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}))
	got, more, err := svc.queryWindowNeighborBatch(context.Background(), key, base, base, 10, false)
	require.NoError(t, err)
	require.True(t, more)
	require.Len(t, got, 1)
	require.Greater(t, got[0].rowID, int64(10))
	require.Equal(t, base, got[0].entry.CreatedAt)
	require.Empty(t, got[0].entry.Event.ID)
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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "missing", key.AppName, key.UserID, key.SessionID, base, "missing").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}))

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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "anchor", key.AppName, key.UserID, key.SessionID, base, "anchor").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(1), base, mysqlWindowEventBytes(t, "anchor", model.RoleUser, "one")))

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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "a1", key.AppName, key.UserID, key.SessionID, base, "a1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(1), base, mysqlWindowEventBytes(t, "a1", model.RoleAssistant, "one")))

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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "anchor", key.AppName, key.UserID, key.SessionID, base, "anchor").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(1), base, mysqlWindowEventBytes(t, "anchor", model.RoleUser, "one")))
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events").
		WithArgs(key.AppName, key.UserID, key.SessionID, base, base, base, int64(1), eventWindowBatchSize+1).
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
	mock.ExpectQuery("SELECT id, created_at, event FROM session_events WHERE user_id").
		WithArgs(key.UserID, key.AppName, key.UserID, key.SessionID, base, "bad", key.AppName, key.UserID, key.SessionID, base, "bad").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "event"}).
			AddRow(int64(1), base, []byte("{bad-json")))

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
