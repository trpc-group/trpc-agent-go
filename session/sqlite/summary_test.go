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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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

func TestSessionSQLite_EnqueueSummaryJob_PersistsCopiedFullSummary(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(
		db,
		WithSummarizer(&fakeSummarizer{}),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	require.NotNil(t, svc.asyncWorker)
	svc.asyncWorker.Stop()
	svc.asyncWorker = nil

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s-provider-copy"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	ev := newUserEvent("hi")
	ev.FilterKey = "branch"
	require.NoError(t, svc.AppendEvent(ctx, sess, ev))

	require.NoError(t, svc.EnqueueSummaryJob(ctx, sess, "branch", false))

	branchText, ok := svc.GetSessionSummaryText(
		ctx,
		session.NewSession(
			sess.AppName,
			sess.UserID,
			sess.ID,
			session.WithSessionCreatedAt(sess.CreatedAt),
		),
		session.WithSummaryFilterKey("branch"),
	)
	require.True(t, ok)
	require.Equal(t, "summary", branchText)

	fullText, ok := svc.GetSessionSummaryText(
		ctx,
		session.NewSession(
			sess.AppName,
			sess.UserID,
			sess.ID,
			session.WithSessionCreatedAt(sess.CreatedAt),
		),
	)
	require.True(t, ok)
	require.Equal(t, "summary", fullText)
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
