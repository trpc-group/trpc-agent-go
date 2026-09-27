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
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func TestEventTimestampSortMemoryIntegration(t *testing.T) {
	cfg, err := drivermysql.ParseDSN(mysqlIntegrationDSN(t))
	require.NoError(t, err)
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	// Apply to every connection, not just the connection used to seed data.
	cfg.Params["sort_buffer_size"] = "262144"
	dsn := cfg.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	table := fmt.Sprintf("it_sort_%x", time.Now().UnixNano())
	// Deliberately omit an ordering index so the regression exercises filesort.
	_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		id BIGINT NOT NULL, app_name VARCHAR(64) NOT NULL,
		user_id VARCHAR(64) NOT NULL, session_id VARCHAR(64) NOT NULL,
		created_at DATETIME(6) NOT NULL, deleted_at DATETIME(6) NULL,
		event JSON NOT NULL, PRIMARY KEY (id, user_id)
	) ENGINE=InnoDB`, table))
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s", table))
		require.NoError(t, err)
	})
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	createdAt := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	cutoff := createdAt.Add(time.Hour)
	for id := 1; id <= 4; id++ {
		role := model.RoleAssistant
		if id == 1 {
			role = model.RoleUser
		}
		evt := event.NewResponseEvent(fmt.Sprintf("inv-%d", id), "author", &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: role, Content: strings.Repeat("x", 512*1024)}}},
		})
		evt.Timestamp = cutoff.Add(time.Duration(id) * time.Minute)
		if id == 2 {
			evt.Timestamp = cutoff.Add(-time.Minute)
		}
		payload, err := json.Marshal(evt)
		require.NoError(t, err)
		// Equal created_at values also exercise the ID tie-breaker.
		_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
			(id, app_name, user_id, session_id, created_at, event)
			VALUES (?, ?, ?, ?, ?, ?)`, table),
			id, key.AppName, key.UserID, key.SessionID, createdAt, payload)
		require.NoError(t, err)
	}
	client, err := storage.GetClientBuilder()(storage.WithClientBuilderDSN(dsn))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	s := &Service{mysqlClient: client, tableSessionEvents: table}
	for _, limit := range []int{1, 2500} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			// Record the old query as a control. Some MySQL versions use a
			// different filesort strategy and may not exhibit error 1038.
			legacyQuery := fmt.Sprintf(`SELECT id, created_at,
				JSON_UNQUOTE(JSON_EXTRACT(event, '$.timestamp')) FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ?
				AND created_at >= ? AND deleted_at IS NULL
				ORDER BY created_at DESC, id DESC LIMIT ?`, table)
			err := client.Query(ctx, func(rows *sql.Rows) error {
				var id int64
				var created time.Time
				var timestamp sql.NullString
				return rows.Scan(&id, &created, &timestamp)
			}, legacyQuery, key.AppName, key.UserID, key.SessionID, createdAt, limit)
			if err != nil {
				var mysqlErr *drivermysql.MySQLError
				require.ErrorAs(t, err, &mysqlErr)
				require.EqualValues(t, 1038, mysqlErr.Number)
				t.Log("original query reproduced MySQL error 1038")
			} else {
				t.Log("original query did not exhaust sort memory on this server")
			}
			refs, err := s.getEventRefsWithTimestamp(ctx, key, createdAt, nil, limit)
			require.NoError(t, err)
			require.Len(t, refs, min(limit, 4))
			for i, ref := range refs {
				require.EqualValues(t, 4-i, ref.id)
				require.False(t, ref.timestampMissing)
				wantTime := cutoff.Add(time.Duration(ref.id) * time.Minute)
				if ref.id == 2 {
					wantTime = cutoff.Add(-time.Minute)
				}
				require.True(t, ref.eventTimestamp.Equal(wantTime))
			}
		})
	}
	// Exercise the full bounded restore path: filter by event time (not
	// created_at), retain the user anchor, and materialize in ascending order.
	events, err := s.getLimitedSessionEvents(ctx, key, createdAt, 2500, time.Time{}, cutoff)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Len(t, events[0], 3)
	for i, id := range []int{1, 3, 4} {
		require.Equal(t, fmt.Sprintf("inv-%d", id), events[0][i].InvocationID)
	}
}
