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
	"os"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrimConversationsIntegration(t *testing.T) {
	for _, backend := range []struct {
		name, env string
		tdsql     bool
	}{
		{"mysql", "TRPC_AGENT_GO_MYSQL_TEST_DSN", false},
		{"tdsql", "TRPC_AGENT_GO_TDSQL_TEST_DSN", true},
	} {
		t.Run(backend.name, func(t *testing.T) {
			dsn := os.Getenv(backend.env)
			if dsn == "" {
				t.Skipf("set %s to run integration tests", backend.env)
			}
			cfg, err := drivermysql.ParseDSN(dsn)
			require.NoError(t, err)
			cfg.ParseTime, cfg.Loc = true, time.UTC
			for _, soft := range []bool{true, false} {
				t.Run(fmt.Sprintf("soft=%t", soft), func(t *testing.T) {
					svc, db := newTrimIntegrationService(t, cfg.FormatDSN(), backend.tdsql, soft)
					t.Run("event scope and unrelated data", func(t *testing.T) {
						testTrimIntegrationScope(t, svc, db, soft)
					})
					t.Run("complete large turn", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
						defer cancel()
						key := session.Key{AppName: "trim", UserID: "user", SessionID: "large"}
						requests := make([]string, 1002)
						requests[0] = "keep"
						for i := 1; i < len(requests); i++ {
							requests[i] = "trim"
						}
						_, events := seedTrimIntegrationSession(t, ctx, svc, key, requests...)
						deleted, err := svc.TrimConversations(ctx, key)
						require.NoError(t, err)
						require.Equal(t, events[1:], deleted)
						remaining, err := svc.GetSession(ctx, key)
						require.NoError(t, err)
						require.Equal(t, events[:1], remaining.Events)
					})
					t.Run("locking and cancellation", func(t *testing.T) {
						testTrimIntegrationLocking(t, svc, db)
					})
				})
			}
		})
	}
}

func newTrimIntegrationService(t *testing.T, dsn string, tdsql, soft bool) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	// Register cleanup before initialization so partially created tables are
	// removed too. The random prefix confines cleanup to this test's tables.
	prefix := "trim_" + uuid.NewString()[:8] + "_"
	var svc *Service
	t.Cleanup(func() {
		if svc != nil {
			require.NoError(t, svc.Close())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := len(tableDefs) - 1; i >= 0; i-- {
			_, err := db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", sqldb.BuildTableName(prefix, tableDefs[i].name)))
			require.NoError(t, err)
		}
		require.NoError(t, db.Close())
	})
	svc, err = NewService(WithMySQLClientDSN(dsn), WithTablePrefix(prefix),
		WithTDSQLSharding(tdsql), WithSoftDelete(soft), WithSessionEventLimit(0),
		WithSessionTTL(time.Hour))
	require.NoError(t, err)
	return svc, db
}

func seedTrimIntegrationSession(
	t *testing.T, ctx context.Context, svc *Service, key session.Key, requestIDs ...string,
) (*session.Session, []event.Event) {
	t.Helper()
	sess, err := svc.CreateSession(ctx, key, session.StateMap{"marker": []byte("unchanged")})
	require.NoError(t, err)
	events := trimTestEvents(requestIDs...)
	base := time.Now().UTC().Truncate(time.Second)
	for i := range events {
		events[i].Timestamp = base.Add(time.Duration(i) * time.Second)
		require.NoError(t, svc.AppendEvent(ctx, sess, &events[i]))
	}
	return sess, events
}

func testTrimIntegrationScope(t *testing.T, svc *Service, db *sql.DB, soft bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := session.Key{AppName: "trim", UserID: "user", SessionID: "scope"}
	sess, events := seedTrimIntegrationSession(t, ctx, svc, key, "A", "B", "A", "C", "B", "")
	otherKeys := []session.Key{
		{AppName: key.AppName, UserID: "other-user", SessionID: key.SessionID},
		{AppName: key.AppName, UserID: key.UserID, SessionID: "other-session"},
		{AppName: "other-app", UserID: key.UserID, SessionID: key.SessionID},
	}
	for _, other := range otherKeys {
		seedTrimIntegrationSession(t, ctx, svc, other, "B")
	}
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: "agui", Payload: json.RawMessage(`{"message":"keep"}`), Timestamp: time.Now().UTC(),
	}))
	sum := &session.Summary{Summary: "keep summary", UpdatedAt: time.Now().UTC()}
	payload, err := json.Marshal(sum)
	require.NoError(t, err)
	_, err = svc.upsertSessionSummary(ctx, key, "", payload, sum.UpdatedAt)
	require.NoError(t, err)
	require.NoError(t, svc.UpdateSessionState(ctx, key, session.StateMap{"marker": []byte("latest state")}))

	var createdAt, expiresAt time.Time
	err = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT created_at, expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, svc.tableSessionStates),
		key.AppName, key.UserID, key.SessionID).Scan(&createdAt, &expiresAt)
	require.NoError(t, err)
	// A surviving row from a previous incarnation must not become a candidate,
	// even if its event timestamp is newer than all current events.
	stale := trimTestEvent("previous-incarnation", "stale", time.Now().UTC().Add(time.Hour))
	payload, err = json.Marshal(stale)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(app_name, user_id, session_id, event, created_at) VALUES (?, ?, ?, ?, ?)`, svc.tableSessionEvents),
		key.AppName, key.UserID, key.SessionID, string(payload), createdAt.Add(-time.Second))
	require.NoError(t, err)

	before, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	deleted, err := svc.TrimConversations(ctx, key, WithCount(2))
	require.NoError(t, err)
	require.Equal(t, []event.Event{events[1], events[3], events[4]}, deleted)
	after, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, []event.Event{events[0], events[2], events[5]}, after.Events)
	require.Equal(t, events, before.Events, "an already loaded Session must not be mutated")
	require.Equal(t, before.State, after.State)
	require.Equal(t, before.Summaries, after.Summaries)
	require.Equal(t, before.Tracks, after.Tracks)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)
	require.Equal(t, before.CreatedAt, after.CreatedAt)
	var afterExpiresAt time.Time
	err = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, svc.tableSessionStates),
		key.AppName, key.UserID, key.SessionID).Scan(&afterExpiresAt)
	require.NoError(t, err)
	require.Equal(t, expiresAt, afterExpiresAt)
	for _, other := range otherKeys {
		remaining, err := svc.GetSession(ctx, other)
		require.NoError(t, err)
		require.Len(t, remaining.Events, 1)
	}
	var total, active int
	err = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*), SUM(deleted_at IS NULL) FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?`, svc.tableSessionEvents),
		key.AppName, key.UserID, key.SessionID).Scan(&total, &active)
	require.NoError(t, err)
	require.Equal(t, 4, active, "three retained events plus the previous incarnation")
	if soft {
		require.Equal(t, 7, total)
	} else {
		require.Equal(t, 4, total)
	}
}

func testTrimIntegrationLocking(t *testing.T, svc *Service, db *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key := session.Key{AppName: "trim", UserID: "user", SessionID: "locking"}
	_, events := seedTrimIntegrationSession(t, ctx, svc, key, "A", "B")
	lock, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer lock.Rollback()
	var id int64
	err = lock.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL FOR UPDATE`, svc.tableSessionStates),
		key.AppName, key.UserID, key.SessionID).Scan(&id)
	require.NoError(t, err)
	blockedCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	deleted, err := svc.TrimConversations(blockedCtx, key)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, deleted)
	require.NoError(t, lock.Rollback())
	remaining, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, events, remaining.Events)

	type result struct {
		events []event.Event
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			events, err := svc.TrimConversations(ctx, key)
			results <- result{events: events, err: err}
		}()
	}
	close(start)
	var requestIDs []string
	for i := 0; i < 2; i++ {
		res := <-results
		require.NoError(t, res.err)
		require.Len(t, res.events, 1)
		requestIDs = append(requestIDs, res.events[0].RequestID)
	}
	require.ElementsMatch(t, []string{"A", "B"}, requestIDs)
	remaining, err = svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Empty(t, remaining.Events)
}
