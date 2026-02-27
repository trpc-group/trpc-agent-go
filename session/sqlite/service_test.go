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
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func openTempSQLiteDB(t *testing.T) (*sql.DB, string, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-sess-*.db")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	db, err := sql.Open("sqlite3", f.Name())
	require.NoError(t, err)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return db, f.Name(), cleanup
}

type fakeSummarizer struct{}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return true
}

func (f *fakeSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	return "summary", nil
}

func (f *fakeSummarizer) SetPrompt(prompt string) {}

func (f *fakeSummarizer) SetModel(m model.Model) {}

func (f *fakeSummarizer) Metadata() map[string]any { return nil }

type denySummarizer struct{}

func (d *denySummarizer) ShouldSummarize(sess *session.Session) bool {
	return false
}

func (d *denySummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	return "no-op", nil
}

func (d *denySummarizer) SetPrompt(prompt string) {}

func (d *denySummarizer) SetModel(m model.Model) {}

func (d *denySummarizer) Metadata() map[string]any { return nil }

func newUserEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: content,
				},
			}},
		},
		Timestamp: time.Now(),
	}
}

func newAssistantEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			}},
		},
		Timestamp: time.Now(),
	}
}

func newTrackEvent(
	track session.Track,
	payload any,
) *session.TrackEvent {
	raw, _ := json.Marshal(payload)
	return &session.TrackEvent{
		Track:     track,
		Payload:   raw,
		Timestamp: time.Now(),
	}
}

func TestOptions_Coverage(t *testing.T) {
	opts := defaultOptions

	WithSessionEventLimit(123)(&opts)
	require.Equal(t, 123, opts.sessionEventLimit)

	WithAsyncPersisterNum(2)(&opts)
	require.Equal(t, 2, opts.asyncPersisterNum)
	WithAsyncPersisterNum(0)(&opts)
	require.Equal(t, defaultAsyncPersisterNum, opts.asyncPersisterNum)

	WithCleanupInterval(time.Second)(&opts)
	require.Equal(t, time.Second, opts.cleanupInterval)

	WithTablePrefix("t_")(&opts)
	require.Equal(t, "t_", opts.tablePrefix)

	WithTablePrefix("")(&opts)
	require.Empty(t, opts.tablePrefix)

	require.Panics(t, func() {
		WithTablePrefix("bad-prefix")(&opts)
	})

	appendHook := func(
		ctx *session.AppendEventContext,
		next func() error,
	) error {
		return next()
	}
	getHook := func(
		ctx *session.GetSessionContext,
		next func() (*session.Session, error),
	) (*session.Session, error) {
		return next()
	}

	WithAppendEventHook(appendHook)(&opts)
	require.Len(t, opts.appendEventHooks, 1)

	WithGetSessionHook(getHook)(&opts)
	require.Len(t, opts.getSessionHooks, 1)

	WithSummaryQueueSize(10)(&opts)
	require.Equal(t, 10, opts.summaryQueueSize)
	WithSummaryQueueSize(0)(&opts)
	require.Equal(t, defaultSummaryQueueSize, opts.summaryQueueSize)

	WithSummaryJobTimeout(5 * time.Second)(&opts)
	require.Equal(t, 5*time.Second, opts.summaryJobTimeout)
	WithSummaryJobTimeout(0)(&opts)
	require.Equal(t, 5*time.Second, opts.summaryJobTimeout)

	WithSkipDBInit(true)(&opts)
	require.True(t, opts.skipDBInit)
}

func TestSessionSQLite_CRUD(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	sess, err := svc.CreateSession(ctx, key, session.StateMap{"k": []byte("v")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	require.NoError(t, svc.UpdateAppState(ctx, key.AppName, session.StateMap{
		session.StateAppPrefix + "a": []byte("1"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, session.StateMap{
		session.StateUserPrefix + "u": []byte("2"),
	}))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	_, ok := got.GetState(session.StateAppPrefix + "a")
	require.True(t, ok)
	_, ok = got.GetState(session.StateUserPrefix + "u")
	require.True(t, ok)

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		"temp:x": []byte("t"),
	})
	require.NoError(t, err)

	got, err = svc.GetSession(ctx, key)
	require.NoError(t, err)
	v, ok := got.GetState("temp:x")
	require.True(t, ok)
	require.Equal(t, []byte("t"), v)
}

func TestSessionSQLite_AppendEvent_And_Summary(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	sum := &fakeSummarizer{}
	svc, err := NewService(
		db,
		WithSummarizer(sum),
		WithAsyncSummaryNum(0),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	userEvt := newUserEvent("hi")
	asstEvt := newAssistantEvent("hello")

	require.NoError(t, svc.AppendEvent(ctx, sess, userEvt))
	require.NoError(t, svc.AppendEvent(ctx, sess, asstEvt))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.GreaterOrEqual(t, len(got.Events), 1)

	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		got,
		session.SummaryFilterKeyAllContents,
		true,
	))

	fresh := session.NewSession(
		got.AppName,
		got.UserID,
		got.ID,
		session.WithSessionCreatedAt(got.CreatedAt),
	)
	text, ok := svc.GetSessionSummaryText(ctx, fresh)
	require.True(t, ok)
	require.Equal(t, "summary", text)
}

func TestSessionSQLite_AsyncPersist(t *testing.T) {
	db, path, cleanup := openTempSQLiteDB(t)
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

	userEvt := newUserEvent("hi")
	asstEvt := newAssistantEvent("hello")

	require.NoError(t, svc.AppendEvent(ctx, sess, userEvt))
	require.NoError(t, svc.AppendEvent(ctx, sess, asstEvt))
	require.NoError(t, svc.Close())

	db2, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc2, err := NewService(db2)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc2.Close()) }()

	got, err := svc2.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.GreaterOrEqual(t, len(got.Events), 2)
}

func TestSessionSQLite_HooksExecuted(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	var appendCalled bool
	var getCalled bool

	appendHook := func(
		ctx *session.AppendEventContext,
		next func() error,
	) error {
		appendCalled = true
		return next()
	}
	getHook := func(
		ctx *session.GetSessionContext,
		next func() (*session.Session, error),
	) (*session.Session, error) {
		getCalled = true
		return next()
	}

	svc, err := NewService(
		db,
		WithAppendEventHook(appendHook),
		WithGetSessionHook(getHook),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))
	_, err = svc.GetSession(ctx, key)
	require.NoError(t, err)

	require.True(t, appendCalled)
	require.True(t, getCalled)
}

func TestSessionSQLite_CreateSession_ExistsNotExpired(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, nil)
	require.Error(t, err)
}

func TestSessionSQLite_CreateSession_ExpiredOverwrite(t *testing.T) {
	const sessTTL = time.Hour

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSessionTTL(sessTTL))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	_, err = svc.CreateSession(ctx, key, session.StateMap{"a": []byte("1")})
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

	_, err = svc.CreateSession(ctx, key, session.StateMap{"a": []byte("2")})
	require.NoError(t, err)
}

func TestSessionSQLite_ListSessions_And_Delete_Soft(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "u1"}
	key1 := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s2"}

	s1, err := svc.CreateSession(ctx, key1, nil)
	require.NoError(t, err)
	s2, err := svc.CreateSession(ctx, key2, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, s1, newUserEvent("hi")))
	require.NoError(t, svc.AppendEvent(ctx, s2, newUserEvent("yo")))

	list, err := svc.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.NoError(t, svc.DeleteSession(ctx, key1))
	list, err = svc.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestSessionSQLite_DeleteSession_Hard(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(false))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))
	require.NoError(t, svc.DeleteSession(ctx, key))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)

	var count int
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableSessionStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSessionSQLite_DeleteAppAndUserState(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	appName := "app"
	userKey := session.UserKey{AppName: appName, UserID: "u1"}

	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	}))

	require.NoError(t, svc.DeleteAppState(ctx, appName, "k"))
	require.NoError(t, svc.DeleteUserState(ctx, userKey, "k"))

	appState, err := svc.ListAppStates(ctx, appName)
	require.NoError(t, err)
	_, ok := appState["k"]
	require.False(t, ok)

	userState, err := svc.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	_, ok = userState["k"]
	require.False(t, ok)
}

func TestSessionSQLite_AppendTrackEvent(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	trk := session.Track("t1")
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent(trk, 1)))
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent(trk, 2)))

	got, err := svc.GetSession(ctx, key, session.WithEventNum(1))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Contains(t, got.Tracks, trk)
	require.Len(t, got.Tracks[trk].Events, 1)
}

func TestSessionSQLite_AppendTrackEvent_AsyncPersist(t *testing.T) {
	db, path, cleanup := openTempSQLiteDB(t)
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

	trk := session.Track("t1")
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, newTrackEvent(trk, 1)))
	require.NoError(t, svc.Close())

	db2, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc2, err := NewService(db2)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc2.Close()) }()

	got, err := svc2.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Contains(t, got.Tracks, trk)
}

func TestSessionSQLite_RefreshTTL(t *testing.T) {
	const sessTTL = time.Hour

	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSessionTTL(sessTTL))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	var expires1 int64
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT expires_at FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expires1)
	require.NoError(t, err)

	_, err = svc.GetSession(ctx, key)
	require.NoError(t, err)

	var expires2 int64
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT expires_at FROM "+svc.tableSessionStates+
			" WHERE app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expires2)
	require.NoError(t, err)

	require.Greater(t, expires2, expires1)
}

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

func TestSessionSQLite_EnqueueSummaryJob_And_Fallback(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSummarizer(&fakeSummarizer{}),
		WithAsyncSummaryNum(0),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	// Force sync processing for deterministic testing.
	require.NotNil(t, svc.asyncWorker)
	svc.asyncWorker.Stop()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	require.NoError(t, svc.EnqueueSummaryJob(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))

	fresh := session.NewSession(
		sess.AppName,
		sess.UserID,
		sess.ID,
		session.WithSessionCreatedAt(sess.CreatedAt),
	)

	text, ok := svc.GetSessionSummaryText(
		ctx,
		fresh,
		session.WithSummaryFilterKey("branch"),
	)
	require.True(t, ok)
	require.Equal(t, "summary", text)
}

func TestSessionSQLite_UpdateAppAndUserState_UpdateExisting(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	appName := "app"
	userKey := session.UserKey{AppName: appName, UserID: "u1"}

	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v1"),
	}))
	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v2"),
	}))
	appState, err := svc.ListAppStates(ctx, appName)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), appState["k"])

	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("u1"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("u2"),
	}))
	userState, err := svc.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	require.Equal(t, []byte("u2"), userState["k"])
}

func TestSessionSQLite_DeleteAppAndUserState_Hard(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(false))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	appName := "app"
	userKey := session.UserKey{AppName: appName, UserID: "u1"}

	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	}))

	require.NoError(t, svc.DeleteAppState(ctx, appName, "k"))
	require.NoError(t, svc.DeleteUserState(ctx, userKey, "k"))

	var count int
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableAppStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableUserStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSessionSQLite_DeleteState_ValidationErrors(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()

	err = svc.DeleteAppState(ctx, "", "k")
	require.ErrorIs(t, err, session.ErrAppNameRequired)

	err = svc.DeleteAppState(ctx, "app", "")
	require.Error(t, err)

	err = svc.DeleteUserState(ctx, session.UserKey{}, "k")
	require.Error(t, err)

	err = svc.DeleteUserState(
		ctx,
		session.UserKey{AppName: "app", UserID: "u1"},
		"",
	)
	require.Error(t, err)
}

func TestSessionSQLite_UpdateSessionState_ValidationErrors(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	})
	require.Error(t, err)

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	})
	require.Error(t, err)
}

func TestSessionSQLite_UpdateSessionState_SessionNotFound(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		"k": []byte("v"),
	})
	require.Error(t, err)
}

func TestSessionSQLite_EnqueueSummaryJob_NoWorker_Sync(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	require.NotNil(t, svc.asyncWorker)
	svc.asyncWorker.Stop()
	svc.asyncWorker = nil

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	require.NoError(t, svc.EnqueueSummaryJob(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))

	fresh := session.NewSession(
		sess.AppName,
		sess.UserID,
		sess.ID,
		session.WithSessionCreatedAt(sess.CreatedAt),
	)

	text, ok := svc.GetSessionSummaryText(
		ctx,
		fresh,
		session.WithSummaryFilterKey("branch"),
	)
	require.True(t, ok)
	require.Equal(t, "summary", text)
}

func TestSessionSQLite_EnqueueSummaryJob_NoSummarizer_NoOp(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.EnqueueSummaryJob(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))
}

func TestSessionSQLite_EnqueueSummaryJob_NilSession_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	err = svc.EnqueueSummaryJob(
		ctx,
		nil,
		session.SummaryFilterKeyAllContents,
		true,
	)
	require.ErrorIs(t, err, session.ErrNilSession)
}

func TestSessionSQLite_GetSession_LoadsAndFiltersSummaries(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		true,
	))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotEmpty(t, got.Summaries)

	got.SummariesMu.RLock()
	sum := got.Summaries[session.SummaryFilterKeyAllContents]
	got.SummariesMu.RUnlock()
	require.NotNil(t, sum)
	require.Equal(t, "summary", sum.Summary)

	staleNs := sess.CreatedAt.Add(-time.Second).UTC().UnixNano()
	_, err = svc.db.ExecContext(
		ctx,
		"UPDATE "+svc.tableSessionSummaries+
			" SET updated_at = ? WHERE app_name = ? AND user_id = ?"+
			" AND session_id = ?",
		staleNs,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	require.NoError(t, err)

	got, err = svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Summaries, 0)
}

func TestSessionSQLite_GetSummariesList_EmptyInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	out, err := svc.getSummariesList(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Nil(t, out)
}

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

func TestSessionSQLite_CreateSessionSummary_NoSummarizer_NoOp(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	err = svc.CreateSessionSummary(
		context.Background(),
		nil,
		session.SummaryFilterKeyAllContents,
		true,
	)
	require.NoError(t, err)
}

func TestSessionSQLite_CreateSessionSummary_NilSession_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	err = svc.CreateSessionSummary(
		context.Background(),
		nil,
		session.SummaryFilterKeyAllContents,
		true,
	)
	require.ErrorIs(t, err, session.ErrNilSession)
}

func TestSessionSQLite_CreateSummary_InvalidKey_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	invalid := session.NewSession("", "u1", "s1")
	err = svc.CreateSessionSummary(
		context.Background(),
		invalid,
		session.SummaryFilterKeyAllContents,
		true,
	)
	require.Error(t, err)
}

func TestSessionSQLite_CreateSummary_Denied_NoPersist(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&denySummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("hi")))

	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		sess,
		session.SummaryFilterKeyAllContents,
		false,
	))

	fresh := session.NewSession(
		sess.AppName,
		sess.UserID,
		sess.ID,
		session.WithSessionCreatedAt(sess.CreatedAt),
	)
	_, ok := svc.GetSessionSummaryText(ctx, fresh)
	require.False(t, ok)
}

func TestSessionSQLite_GetSessionSummaryText_InMemory(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	sess := session.NewSession("app", "u1", "s1")
	sess.SummariesMu.Lock()
	sess.Summaries[session.SummaryFilterKeyAllContents] = &session.Summary{
		Summary:   "mem-summary",
		UpdatedAt: sess.CreatedAt.Add(time.Second).UTC(),
	}
	sess.SummariesMu.Unlock()

	text, ok := svc.GetSessionSummaryText(
		context.Background(),
		sess,
		session.WithSummaryFilterKey("branch"),
	)
	require.True(t, ok)
	require.Equal(t, "mem-summary", text)
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

func TestSessionSQLite_DeleteSession_InvalidKey_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	err = svc.DeleteSession(context.Background(), session.Key{})
	require.Error(t, err)
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

func TestSessionSQLite_ListSessions_EmptyEvents(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSummarizer(&fakeSummarizer{}))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "u1"}

	key1 := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s2"}

	s1, err := svc.CreateSession(ctx, key1, nil)
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key2, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendEvent(ctx, s1, newUserEvent("hi")))
	require.NoError(t, svc.CreateSessionSummary(
		ctx,
		s1,
		session.SummaryFilterKeyAllContents,
		true,
	))

	list, err := svc.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, list, 2)

	for _, sess := range list {
		if sess.ID == key2.SessionID {
			require.Len(t, sess.Events, 0)
			require.Nil(t, sess.Summaries)
		}
	}
}

func TestSessionSQLite_CreateSession_GenerateID_NilState(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1"}
	state := session.StateMap{
		"k1": []byte("v1"),
		"k2": nil,
	}
	sess, err := svc.CreateSession(ctx, key, state)
	require.NoError(t, err)
	require.NotEmpty(t, sess.ID)

	v, ok := sess.GetState("k1")
	require.True(t, ok)
	require.Equal(t, []byte("v1"), v)

	v, ok = sess.GetState("k2")
	require.True(t, ok)
	require.Nil(t, v)
}

func TestSessionSQLite_CreateSession_InvalidKey_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.CreateSession(
		context.Background(),
		session.Key{AppName: "", UserID: "u1", SessionID: "s1"},
		nil,
	)
	require.Error(t, err)
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

func TestSessionSQLite_GetSession_CorruptState_Error(t *testing.T) {
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
		"UPDATE "+svc.tableSessionStates+" SET state = ?",
		[]byte("bad-json"),
	)
	require.NoError(t, err)

	_, err = svc.GetSession(ctx, key)
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

func TestSessionSQLite_GetEventsList_EmptyInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	out, err := svc.getEventsList(
		context.Background(),
		nil,
		nil,
		0,
		time.Time{},
	)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestSessionSQLite_GetTrackEvents_EmptyInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	out, err := svc.getTrackEvents(
		context.Background(),
		nil,
		nil,
		0,
		time.Time{},
	)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestSessionSQLite_GetTrackEvents_Mismatch_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.getTrackEvents(
		context.Background(),
		[]session.Key{{AppName: "app", UserID: "u1", SessionID: "s1"}},
		nil,
		0,
		time.Time{},
	)
	require.Error(t, err)
}

func TestSessionSQLite_GetSessionSummaryText_InvalidInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, ok := svc.GetSessionSummaryText(context.Background(), nil)
	require.False(t, ok)

	invalid := session.NewSession("", "", "s1")
	_, ok = svc.GetSessionSummaryText(context.Background(), invalid)
	require.False(t, ok)
}

func TestSessionSQLite_NewService_NilDB_Error(t *testing.T) {
	_, err := NewService(nil)
	require.Error(t, err)
}
