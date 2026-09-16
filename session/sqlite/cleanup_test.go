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
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSQLite_CleanupExpiredData_Soft(t *testing.T) {
	const (
		sessTTL = time.Hour
		appTTL  = time.Hour
		userTTL = time.Hour
	)

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSessionTTL(sessTTL),
		WithAppStateTTL(appTTL),
		WithUserStateTTL(userTTL),
		WithSummarizer(&fakeSummarizer{}),
		WithAsyncSummaryNum(0),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))
	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))
	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))

	require.NoError(t, svc.UpdateAppState(ctx, key.AppName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	}))

	expiredNs := time.Now().Add(-sessTTL).UTC().UnixNano()
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+
			" SET expires_at = ? WHERE app_name = ? AND user_id = ?"+
			" AND session_id = ?",
		expiredNs,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	require.NoError(t, err)

	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableAppStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableUserStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)

	svc.cleanupExpiredData(ctx)

	for _, table := range []string{
		svc.tableSessionStates,
		svc.tableSessionEvents,
		svc.tableSessionTracks,
		svc.tableSessionSummaries,
	} {
		var deleted sql.NullInt64
		err := svc.db.QueryRowContext(
			ctx,
			"SELECT deleted_at FROM "+table+" LIMIT 1",
		).Scan(&deleted)
		require.NoError(t, err)
		require.True(t, deleted.Valid)
	}
}

func TestSessionSQLite_CleanupExpiredData_Hard(t *testing.T) {
	const sessTTL = time.Hour

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSessionTTL(sessTTL),
		WithSoftDelete(false),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	expiredNs := time.Now().Add(-sessTTL).UTC().UnixNano()
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+
			" SET expires_at = ? WHERE app_name = ? AND user_id = ?"+
			" AND session_id = ?",
		expiredNs,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	require.NoError(t, err)

	svc.cleanupExpiredData(ctx)

	var count int
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableSessionStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSessionSQLite_CleanupExpiredData_Hard_AppUser(t *testing.T) {
	const (
		sessTTL = time.Hour
		appTTL  = time.Hour
		userTTL = time.Hour
	)

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSessionTTL(sessTTL),
		WithAppStateTTL(appTTL),
		WithUserStateTTL(userTTL),
		WithSoftDelete(false),
		WithSummarizer(&fakeSummarizer{}),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))
	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))
	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))
	require.NoError(t, svc.UpdateAppState(ctx, key.AppName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	}))

	expiredNs := time.Now().Add(-sessTTL).UTC().UnixNano()
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableAppStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableUserStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)

	svc.cleanupExpiredData(ctx)

	for _, table := range []string{
		svc.tableSessionStates,
		svc.tableSessionEvents,
		svc.tableSessionTracks,
		svc.tableSessionSummaries,
		svc.tableAppStates,
		svc.tableUserStates,
	} {
		var count int
		err := svc.db.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+table,
		).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 0, count)
	}
}

func TestSessionSQLite_CleanupExpiredData_TrackTTL(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()
	svc, err := NewService(db, WithTrackEventTTL(time.Hour))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent("expired", 1)))
	setSQLiteTrackExpires(t, svc, ctx, key, "expired", time.Now().Add(-time.Hour).UTC().UnixNano())
	svc.cleanupExpiredData(ctx)
	deleted := sqliteTrackDeletedAt(t, svc, ctx, key, "expired")
	require.True(t, deleted.Valid)
}

func TestSessionSQLite_CleanupExpiredTrackEvents(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{name: "soft delete", softDelete: true},
		{name: "hard delete", softDelete: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _, cleanup := openTempSQLiteDB(t)
			defer cleanup()
			svc, err := NewService(
				db,
				WithTrackEventTTL(time.Hour),
				WithSoftDelete(tt.softDelete),
			)
			require.NoError(t, err)
			defer func() { require.NoError(t, svc.Close()) }()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent("expired", 1)))
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent("fresh", 2)))
			now := time.Now().UTC()
			setSQLiteTrackExpires(t, svc, ctx, key, "expired", now.Add(-time.Hour).UnixNano())
			setSQLiteTrackExpires(t, svc, ctx, key, "fresh", now.Add(time.Hour).UnixNano())
			svc.cleanupExpiredTrackEvents(ctx, now)
			if tt.softDelete {
				require.True(t, sqliteTrackDeletedAt(t, svc, ctx, key, "expired").Valid)
				require.False(t, sqliteTrackDeletedAt(t, svc, ctx, key, "fresh").Valid)
				return
			}
			require.Equal(t, 0, sqliteTrackCount(t, svc, ctx, key, "expired"))
			require.Equal(t, 1, sqliteTrackCount(t, svc, ctx, key, "fresh"))
		})
	}
}

func TestSessionSQLite_CleanupExpiredSessionsKeepsTrackEventsWithTrackTTL(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()
	svc, err := NewService(
		db,
		WithSessionTTL(time.Hour),
		WithTrackEventTTL(time.Hour),
		WithSoftDelete(false),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent("fresh", 1)))
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+" SET expires_at = ?",
		time.Now().Add(-time.Hour).UTC().UnixNano(),
	)
	require.NoError(t, err)
	setSQLiteTrackExpires(t, svc, ctx, key, "fresh", time.Now().Add(time.Hour).UTC().UnixNano())
	svc.cleanupExpiredData(ctx)
	require.Equal(t, 0, sqliteSessionCount(t, svc, ctx, key))
	require.Equal(t, 1, sqliteTrackCount(t, svc, ctx, key, "fresh"))
}

func TestSessionSQLite_StartCleanupRoutine_NoInterval(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	require.Nil(t, svc.cleanupTicker)
	svc.startCleanupRoutine()
	require.Nil(t, svc.cleanupTicker)
}

func setSQLiteTrackExpires(
	t *testing.T,
	svc *Service,
	ctx context.Context,
	key session.Key,
	track session.Track,
	expiresAt int64,
) {
	t.Helper()
	_, err := svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionTracks+
			" SET expires_at = ? WHERE app_name = ? AND user_id = ?"+
			" AND session_id = ? AND track = ?",
		expiresAt,
		key.AppName,
		key.UserID,
		key.SessionID,
		track,
	)
	require.NoError(t, err)
}

func sqliteTrackDeletedAt(
	t *testing.T,
	svc *Service,
	ctx context.Context,
	key session.Key,
	track session.Track,
) sql.NullInt64 {
	t.Helper()
	var deleted sql.NullInt64
	err := svc.db.QueryRowContext(
		ctx,
		"SELECT deleted_at FROM "+svc.tableSessionTracks+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?"+
			" AND track = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
		track,
	).Scan(&deleted)
	require.NoError(t, err)
	return deleted
}

func sqliteTrackCount(
	t *testing.T,
	svc *Service,
	ctx context.Context,
	key session.Key,
	track session.Track,
) int {
	t.Helper()
	var count int
	err := svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableSessionTracks+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?"+
			" AND track = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
		track,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func sqliteSessionCount(
	t *testing.T,
	svc *Service,
	ctx context.Context,
	key session.Key,
) int {
	t.Helper()
	var count int
	err := svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func TestSessionSQLite_CleanupRoutine_Tick(t *testing.T) {
	const (
		sessTTL  = time.Second
		interval = 10 * time.Millisecond
	)

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSessionTTL(sessTTL),
		WithCleanupInterval(interval),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	expiredNs := time.Now().Add(-sessTTL).UTC().UnixNano()
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+" SET expires_at = ?",
		expiredNs,
	)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		var count int
		err := svc.db.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+svc.tableSessionStates+
				" WHERE deleted_at IS NULL",
		).Scan(&count)
		if err != nil {
			return false
		}
		return count == 0
	}, time.Second, 10*time.Millisecond)
}
