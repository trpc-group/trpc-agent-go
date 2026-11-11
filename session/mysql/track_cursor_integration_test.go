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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func cursorIntegrationService(t *testing.T, opts ...ServiceOpt) (*Service, *sql.DB) {
	t.Helper()
	dsn := mysqlIntegrationDSN(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prefix := prepareSummaryIntegrationSchema(t, db, dsn)
	options := []ServiceOpt{WithMySQLClientDSN(dsn), WithTablePrefix(prefix), WithEnableAsyncPersist(false)}
	s, err := NewService(append(options, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s, db
}

func TestTrackCursorPrecisionIntegration(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			s, db := cursorIntegrationService(t, WithEnableAsyncPersist(async))
			ctx := summaryIntegrationContext(t)
			key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
			sess, err := s.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			timestamp := time.Now().Truncate(time.Microsecond).Add(100 * time.Nanosecond)
			event := &session.TrackEvent{Track: "track", Payload: json.RawMessage(`"same"`), Timestamp: timestamp}
			require.NoError(t, s.AppendTrackEvent(ctx, sess, event))
			var first *session.TrackEvents
			require.Eventually(t, func() bool {
				first, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
				return err == nil && len(first.Events) == 1
			}, 3*time.Second, time.Millisecond)
			for i := 0; i < 2; i++ {
				require.NoError(t, s.AppendTrackEvent(ctx, sess, event))
			}
			var suffix *session.TrackEvents
			require.Eventually(t, func() bool {
				suffix, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*first.Cursor))
				return err == nil && len(suffix.Events) == 2
			}, 3*time.Second, time.Millisecond)
			var buckets int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT created_at) FROM "+s.tableSessionTracks).Scan(&buckets))
			require.Equal(t, 1, buckets, "exercise actual MySQL timestamp precision")
			legacy, err := s.GetTrackEvents(ctx, key, "track", session.WithEventTime(first.Events[0].Timestamp))
			require.NoError(t, err)
			require.Empty(t, legacy.Events, "the existing time predicate loses the suffix")
			backdated := *event
			backdated.Timestamp = timestamp.Add(-time.Hour)
			require.NoError(t, s.AppendTrackEvent(ctx, sess, &backdated))
			var backwards *session.TrackEvents
			require.Eventually(t, func() bool {
				backwards, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*suffix.Cursor))
				return err == nil && len(backwards.Events) == 1
			}, 3*time.Second, time.Millisecond)
			require.Equal(t, backdated.Timestamp, backwards.Events[0].Timestamp)
			empty, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*backwards.Cursor))
			require.NoError(t, err)
			require.Empty(t, empty.Events)
		})
	}
}

func TestTrackCursorTransactionVisibilityIntegration(t *testing.T) {
	s, db := cursorIntegrationService(t)
	ctx := summaryIntegrationContext(t)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	seed := &session.TrackEvent{Track: "track", Payload: json.RawMessage(`"seed"`), Timestamp: time.Now()}
	require.NoError(t, s.AppendTrackEvent(ctx, sess, seed))
	initial, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, _, err = loadSessionStateForUpdate(ctx, tx, s.tableSessionStates, key)
	require.NoError(t, err)
	first := *seed
	first.Payload = json.RawMessage(`"held-first"`)
	data, err := json.Marshal(first)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(app_name,user_id,session_id,track,event,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, s.tableSessionTracks),
		key.AppName, key.UserID, key.SessionID, first.Track, string(data), first.Timestamp, first.Timestamp)
	require.NoError(t, err)
	second := *seed
	second.Payload = json.RawMessage(`"blocked-second"`)
	done := make(chan error, 1)
	go func() { done <- s.AppendTrackEvent(ctx, sess, &second) }()
	still, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*initial.Cursor))
	require.NoError(t, err)
	require.Empty(t, still.Events, "neither uncommitted append is visible")
	select {
	case err := <-done:
		t.Fatalf("append passed held session lock: %v", err)
	default:
	}
	require.NoError(t, tx.Commit())
	require.NoError(t, <-done)
	read, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*still.Cursor))
	require.NoError(t, err)
	require.Len(t, read.Events, 2)
	require.Equal(t, first.Payload, read.Events[0].Payload)
	require.Equal(t, second.Payload, read.Events[1].Payload)
}

func TestTrackCursorLifecycleIntegration(t *testing.T) {
	for _, change := range []string{"delete", "recreate", "expire-anchor", "rewrite-incarnation"} {
		t.Run(change, func(t *testing.T) {
			s, db := cursorIntegrationService(t)
			ctx := summaryIntegrationContext(t)
			key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
			sess, err := s.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			require.NoError(t, s.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: time.Now()}))
			read, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			switch change {
			case "delete", "recreate":
				require.NoError(t, s.DeleteSession(ctx, key))
				if change == "recreate" {
					_, err = s.CreateSession(ctx, key, nil)
					require.NoError(t, err)
				}
			case "expire-anchor":
				_, err = db.ExecContext(ctx, "UPDATE "+s.tableSessionTracks+" SET expires_at = ?", time.Now().Add(-time.Hour))
				require.NoError(t, err)
			case "rewrite-incarnation":
				_, err = db.ExecContext(ctx, "UPDATE "+s.tableSessionStates+" SET expires_at = ?", time.Now().Add(-time.Hour))
				require.NoError(t, err)
				_, err = s.CreateSession(ctx, key, nil)
				require.NoError(t, err)
			}
			_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*read.Cursor))
			require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
		})
	}
}

func TestTrackCursorHooksIntegration(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "real"}
	alias := key
	alias.SessionID = "alias"
	var calls int
	denied := errors.New("denied")
	s, _ := cursorIntegrationService(t, WithGetSessionHook(func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		calls++
		if c.Key.SessionID == "denied" {
			return nil, denied
		}
		c.Key = key
		snapshot, err := next()
		if snapshot != nil && snapshot.Tracks["track"] != nil {
			for i := range snapshot.Tracks["track"].Events {
				snapshot.Tracks["track"].Events[i].Payload = json.RawMessage(`"redacted"`)
			}
		}
		return snapshot, err
	}))
	ctx := summaryIntegrationContext(t)
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, s.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Payload: json.RawMessage(`"secret"`), Timestamp: time.Now()}))
	read, err := s.GetTrackEvents(ctx, alias, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, read.Events, 1)
	require.Equal(t, json.RawMessage(`"redacted"`), read.Events[0].Payload)
	empty, err := s.GetTrackEvents(ctx, alias, "track", session.WithTrackCursor(*read.Cursor))
	require.NoError(t, err)
	require.Empty(t, empty.Events)
	require.Equal(t, 2, calls)
	alias.SessionID = "denied"
	_, err = s.GetTrackEvents(ctx, alias, "track", session.WithTrackCursor(""))
	require.ErrorIs(t, err, denied)
	require.Equal(t, 3, calls)
}

func TestTrackCursorLargePayloadIntegration(t *testing.T) {
	cfg, err := drivermysql.ParseDSN(mysqlIntegrationDSN(t))
	require.NoError(t, err)
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["sort_buffer_size"] = "32768"
	s, db := cursorIntegrationService(t, WithMySQLClientDSN(cfg.FormatDSN()))
	ctx := summaryIntegrationContext(t)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "large"}
	_, err = s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, _, err = loadSessionStateForUpdate(ctx, tx, s.tableSessionStates, key)
	require.NoError(t, err)
	stamp := time.Now()
	const count = 300
	for i := 0; i < count; i++ {
		payload, err := json.Marshal(fmt.Sprintf("%04d:%s", i, strings.Repeat("x", 32<<10)))
		require.NoError(t, err)
		data, err := json.Marshal(session.TrackEvent{Track: "track", Timestamp: stamp, Payload: payload})
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (app_name,user_id,session_id,track,event,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, s.tableSessionTracks),
			key.AppName, key.UserID, key.SessionID, "track", string(data), stamp, stamp)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	read, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Len(t, read.Events, count)
	for i, evt := range read.Events {
		var payload string
		require.NoError(t, json.Unmarshal(evt.Payload, &payload))
		require.True(t, strings.HasPrefix(payload, fmt.Sprintf("%04d:", i)))
	}
}
