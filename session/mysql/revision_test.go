//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRevisionStoreAndAttachGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	s.tableSessionRevisions = "session_revisions"
	s.tableRevisionArchives = "session_revision_archives"

	store := s.revisionStore()
	assert.Equal(t, "session_states", store.Tables.States)
	assert.Equal(t, "session_revision_archives", store.Tables.Archives)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT record FROM session_revisions").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"record"}).AddRow(`{"generation":7,"head":1}`))
	mock.ExpectCommit()
	require.NoError(t, s.attachRevisionGeneration(context.Background(), key, sess))
	generation, ok := sessionrevision.Generation(sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(7), generation)
	require.NoError(t, mock.ExpectationsWereMet())

	s.tableSessionRevisions = ""
	require.NoError(t, s.attachRevisionGeneration(context.Background(), key, sess))
}

func TestRevisionFlushBarriers(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{opts: ServiceOpts{enableAsyncPersist: true}}
	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair), make(chan *trackEventPair)}
	go func() {
		pair := <-s.eventPairChans[0]
		pair.done <- nil
	}()
	for _, ch := range s.trackEventChans {
		go func(ch chan *trackEventPair) {
			pair := <-ch
			pair.done <- nil
		}(ch)
	}
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))

	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	go func() {
		pair := <-s.eventPairChans[0]
		pair.done <- errors.New("persist failed")
	}()
	assert.ErrorContains(t, s.flushRevisionPersistence(context.Background(), key), "persist failed")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, s.flushRevisionPersistence(ctx, key), context.Canceled)

	s.opts.enableAsyncPersist = false
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))
	require.NoError(t, s.flushTrackPersistence(context.Background()))
	s.opts.enableAsyncPersist = true

	eventWaitCtx, cancelEventWait := context.WithCancel(context.Background())
	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	go func() {
		<-s.eventPairChans[0]
		cancelEventWait()
	}()
	assert.ErrorIs(
		t, s.flushRevisionPersistence(eventWaitCtx, key), context.Canceled,
	)

	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair)}
	go func() {
		pair := <-s.trackEventChans[0]
		pair.done <- errors.New("track persist failed")
	}()
	assert.ErrorContains(
		t, s.flushTrackPersistence(context.Background()), "track persist failed",
	)

	trackWaitCtx, cancelTrackWait := context.WithCancel(context.Background())
	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair)}
	go func() {
		<-s.trackEventChans[0]
		cancelTrackWait()
	}()
	assert.ErrorIs(
		t, s.flushTrackPersistence(trackWaitCtx), context.Canceled,
	)

	trackSendCtx, cancelTrackSend := context.WithCancel(context.Background())
	cancelTrackSend()
	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair)}
	assert.ErrorIs(
		t, s.flushTrackPersistence(trackSendCtx), context.Canceled,
	)
}

func TestReplacementResultWithScopedState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	mock.ExpectQuery("SELECT `key`, value FROM app_states").
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("shared", []byte("app")))
	mock.ExpectQuery("SELECT `key`, value FROM user_states").
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("private", []byte("user")))
	result, err := s.replacementResultWithScopedState(context.Background(), key,
		&sessionrevision.LatestTurnReplacementResult{ActiveSession: session.NewSession(
			key.AppName, key.UserID, key.SessionID,
		)})
	require.NoError(t, err)
	assert.Equal(t, []byte("app"), result.ActiveSession.State[session.StateAppPrefix+"shared"])
	assert.Equal(t, []byte("user"), result.ActiveSession.State[session.StateUserPrefix+"private"])

	mock.ExpectQuery("SELECT `key`, value FROM app_states").
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnError(errors.New("app state failed"))
	_, err = s.replacementResultWithScopedState(context.Background(), key, result)
	assert.ErrorContains(t, err, "app state failed")

	mock.ExpectQuery("SELECT `key`, value FROM app_states").
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT `key`, value FROM user_states").
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnError(errors.New("user state failed"))
	_, err = s.replacementResultWithScopedState(context.Background(), key, result)
	assert.ErrorContains(t, err, "user state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceLatestTurnValidationAndUnsupportedStore(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)

	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{})
	assert.Error(t, err)

	s.tableSessionRevisions = "session_revisions"
	mock.ExpectBegin()
	mock.ExpectRollback()
	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{
		Key:               session.Key{AppName: "app", UserID: "user", SessionID: "session"},
		ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	})
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummaryWithRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db, WithSummarizer(&fakeSummarizer{allow: true, out: "summary"}))
	s.tableSessionRevisions = "session_revisions"
	s.tableRevisionArchives = "session_revision_archives"
	sess := session.NewSession("app", "user", "session")
	stateRaw, err := json.Marshal(SessionState{
		ID: sess.ID, State: session.StateMap{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(sess.AppName, sess.UserID, sess.ID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).AddRow(string(stateRaw), nil))
	mock.ExpectQuery("SELECT record FROM session_revisions").
		WithArgs(sess.AppName, sess.UserID, sess.ID).
		WillReturnRows(sqlmock.NewRows([]string{"record"}).AddRow(`{"generation":0,"head":0}`))
	mock.ExpectExec("INSERT INTO session_revisions").
		WithArgs(sess.AppName, sess.UserID, sess.ID, sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs(sess.AppName, sess.UserID, sess.ID, "all", sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "all", true))
	require.NoError(t, mock.ExpectationsWereMet())
}
