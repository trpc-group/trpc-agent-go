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

func TestSessionSQLite_DeleteSession_InvalidKey_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	err = svc.DeleteSession(context.Background(), session.Key{})
	require.Error(t, err)
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

func TestSessionSQLite_NewService_NilDB_Error(t *testing.T) {
	_, err := NewService(nil)
	require.Error(t, err)
}
