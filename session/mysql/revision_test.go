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

func TestRevisionStoreGenerations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	s.opts.softDelete = true

	store := s.revisionStore()
	assert.Equal(t, "session_states", store.Tables.States)
	assert.Equal(t, "session_events", store.Tables.Events)
	assert.True(t, store.SoftDelete)
	s.opts.softDelete = false
	assert.False(t, s.revisionStore().SoftDelete)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	state7 := revisionState(t, key.SessionID, 7)
	state8 := revisionState(t, key.SessionID, 8)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(string(state8)))
	mock.ExpectCommit()
	generation, err := s.revisionGeneration(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), generation)
	require.NoError(t, mock.ExpectationsWereMet())

	missing := session.Key{AppName: "app", UserID: "user", SessionID: "missing"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT app_name, user_id, session_id, state FROM session_states").
		WithArgs(
			key.AppName, key.UserID, key.SessionID,
			missing.AppName, missing.UserID, missing.SessionID,
		).
		WillReturnRows(sqlmock.NewRows(
			[]string{"app_name", "user_id", "session_id", "state"},
		).AddRow(key.AppName, key.UserID, key.SessionID, string(state7)))
	mock.ExpectCommit()
	generations, err := s.revisionGenerations(
		context.Background(), []session.Key{key, missing},
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), generations[key])
	assert.Zero(t, generations[missing])
	require.NoError(t, mock.ExpectationsWereMet())

	generations, err = s.revisionGenerations(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, generations)
}

func revisionState(t *testing.T, sessionID string, generation uint64) []byte {
	t.Helper()
	raw, err := sessionrevision.EncodeState(SessionState{
		ID: sessionID, State: session.StateMap{},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, &sessionrevision.PersistedRecord{Generation: generation})
	require.NoError(t, err)
	return raw
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
	s.trackEventChans = nil
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
	require.NoError(t, s.flushEventPersistence(context.Background(), key))
	require.NoError(t, s.flushTrackPersistence(context.Background(), key))
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
		t, s.flushTrackPersistence(context.Background(), key), "track persist failed",
	)

	trackWaitCtx, cancelTrackWait := context.WithCancel(context.Background())
	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair)}
	go func() {
		<-s.trackEventChans[0]
		cancelTrackWait()
	}()
	assert.ErrorIs(
		t, s.flushTrackPersistence(trackWaitCtx, key), context.Canceled,
	)

	trackSendCtx, cancelTrackSend := context.WithCancel(context.Background())
	cancelTrackSend()
	s.trackEventChans = []chan *trackEventPair{make(chan *trackEventPair)}
	assert.ErrorIs(
		t, s.flushTrackPersistence(trackSendCtx, key), context.Canceled,
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

func TestReplaceLatestTurnValidation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)

	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{})
	assert.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummaryWithRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db, WithSummarizer(&fakeSummarizer{allow: true, out: "summary"}))
	sess := session.NewSession("app", "user", "session")
	sessionrevision.SetGeneration(sess, 0)
	stateRaw, err := json.Marshal(SessionState{
		ID: sess.ID, State: session.StateMap{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(sess.AppName, sess.UserID, sess.ID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).AddRow(string(stateRaw), nil))
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sess.AppName, sess.UserID, sess.ID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs(sess.AppName, sess.UserID, sess.ID, "all", sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "all", true))
	require.NoError(t, mock.ExpectationsWereMet())
}
