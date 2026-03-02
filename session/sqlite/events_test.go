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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSQLite_AppendEvent_ContextDone(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	svc.opts.enableAsyncPersist = true
	svc.eventPairChans = []chan *sessionEventPair{
		make(chan *sessionEventPair),
	}

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = svc.AppendEvent(ctx, sess, newUserEvent("hi"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestSessionSQLite_AppendEvent_SendOnClosedChannel_NoPanic(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.Close())

	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))
}

func TestSessionSQLite_AppendTrackEvent_ContextDone(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	svc.opts.enableAsyncPersist = true
	svc.trackEventChans = []chan *trackEventPair{
		make(chan *trackEventPair),
	}

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSessionSQLite_AppendTrackEvent_ClosedChan_NoPanic(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.Close())

	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))
}

func TestSessionSQLite_AppendTrackEvent_NilEvent_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	err = svc.AppendTrackEvent(ctx, sess, nil)
	require.Error(t, err)
}

func TestSessionSQLite_EventLimit_DefaultApplied(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSessionEventLimit(1))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("a")))
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("b")))
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("c")))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Events, 1)
	require.Equal(t, "c", got.Events[0].Choices[0].Message.Content)
}

func TestSessionSQLite_AppendEvent_SessionNotFound_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	sess := session.NewSession("app", "u1", "s1")
	err = svc.AppendEvent(context.Background(), sess, newUserEvent("hi"))
	require.Error(t, err)
}

func TestSessionSQLite_AppendTrackEvent_SessionNotFound_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	sess := session.NewSession("app", "u1", "s1")
	err = svc.AppendTrackEvent(
		context.Background(),
		sess,
		newTrackEvent("t1", 1),
	)
	require.Error(t, err)
}

func TestSessionSQLite_AppendEvent_ResponseNil_SkipsInsert(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, sess, &event.Event{
		Timestamp: time.Now(),
	}))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Events, 0)
}

func TestSessionSQLite_AppendEvent_ExpiredSession_ExtendsTTL(t *testing.T) {
	const sessTTL = time.Hour

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSessionTTL(sessTTL))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

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

	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	var expires sql.NullInt64
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT expires_at FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expires)
	require.NoError(t, err)
	require.True(t, expires.Valid)
	require.Greater(t, expires.Int64, expiredNs)
}

func TestSessionSQLite_AppendTrack_Expired_ExtendsTTL(t *testing.T) {
	const sessTTL = time.Hour

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSessionTTL(sessTTL))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

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

	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))

	var expires sql.NullInt64
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT expires_at FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expires)
	require.NoError(t, err)
	require.True(t, expires.Valid)
	require.Greater(t, expires.Int64, expiredNs)
}

func TestSessionSQLite_AppendEvent_CorruptState_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+" SET state = ?",
		[]byte("bad-json"),
	)
	require.NoError(t, err)

	err = svc.AppendEvent(ctx, sess, newUserEvent("hi"))
	require.Error(t, err)
}

func TestSessionSQLite_AppendTrack_CorruptState_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+" SET state = ?",
		[]byte("bad-json"),
	)
	require.NoError(t, err)

	err = svc.AppendTrackEvent(ctx, sess, newTrackEvent("t1", 1))
	require.Error(t, err)
}

func TestSessionSQLite_GetSession_CorruptEvent_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	_, err = svc.db.ExecContext(
		ctx,
		"INSERT INTO "+svc.tableSessionEvents+
			" (app_name, user_id, session_id, event, created_at,"+
			" updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, NULL)",
		key.AppName,
		key.UserID,
		key.SessionID,
		[]byte("bad-json"),
		time.Now().UTC().UnixNano(),
		time.Now().UTC().UnixNano(),
	)
	require.NoError(t, err)

	_, err = svc.GetSession(ctx, key)
	require.Error(t, err)
}

func TestSessionSQLite_GetSession_CorruptTrackEvent_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))

	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionTracks+" SET event = ?",
		[]byte("bad-json"),
	)
	require.NoError(t, err)

	_, err = svc.GetSession(ctx, key)
	require.Error(t, err)
}

func TestSessionSQLite_GetSession_CorruptTrackIndex_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendTrackEvent(
		ctx,
		sess,
		newTrackEvent("t1", 1),
	))

	var stateBytes []byte
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT state FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&stateBytes)
	require.NoError(t, err)

	var st SessionState
	require.NoError(t, json.Unmarshal(stateBytes, &st))
	if st.State == nil {
		st.State = make(session.StateMap)
	}
	st.State["tracks"] = []byte("bad-json")

	updated, err := json.Marshal(&st)
	require.NoError(t, err)

	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionStates+
			" SET state = ? WHERE app_name = ? AND user_id = ?"+
			" AND session_id = ?",
		updated,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	require.NoError(t, err)

	_, err = svc.GetSession(ctx, key)
	require.Error(t, err)
}
