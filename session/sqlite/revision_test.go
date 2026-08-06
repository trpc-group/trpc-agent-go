//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLatestTurnReplacementContract(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	replacementtest.Run(t, service)
}

func TestReplaceLatestTurnRestoresSQLiteProjection(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			db, _, cleanup := openTempSQLiteDB(t)
			t.Cleanup(cleanup)
			service, err := NewService(
				db,
				WithEnableAsyncPersist(async),
				WithAsyncPersisterNum(1),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			testSQLiteLatestTurnReplacement(t, service, async)
		})
	}
}

func TestDeleteSessionRemovesRevisionMetadata(t *testing.T) {
	for _, softDelete := range []bool{true, false} {
		name := "hard"
		if softDelete {
			name = "soft"
		}
		t.Run(name, func(t *testing.T) {
			db, _, cleanup := openTempSQLiteDB(t)
			t.Cleanup(cleanup)
			service, err := NewService(db, WithSoftDelete(softDelete))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })

			ctx := context.Background()
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "session",
			}
			sess, err := service.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			turnCtx := sessionrevision.ContextWithTurnStart(
				ctx,
				sessionrevision.TurnStart{
					RequestID: "request", InvocationID: "invocation",
				},
			)
			require.NoError(t, service.AppendEvent(
				turnCtx,
				sess,
				sqliteTestMessageEvent(
					"turn", "request", "invocation", "latest",
				),
			))
			require.NoError(t, service.AppendEvent(
				ctx,
				sess,
				sqliteTestCompletionEvent("request", "invocation"),
			))
			_, err = service.ReplaceLatestTurn(
				ctx,
				sessionrevision.LatestTurnReplacementRequest{
					Key:               key,
					ExpectedRequestID: "request",
					IdempotencyKey:    "replacement",
				},
			)
			require.NoError(t, err)

			assertRevisionMetadataRows(t, service, key, 1)

			require.NoError(t, service.DeleteSession(ctx, key))
			assertRevisionMetadataRows(t, service, key, 0)
		})
	}
}

func TestReplacementPreservesAndLaterWritesRefreshArchiveExpiration(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db, WithSessionTTL(time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	turnCtx := sessionrevision.ContextWithTurnStart(
		ctx,
		sessionrevision.TurnStart{
			RequestID: "request", InvocationID: "invocation",
		},
	)
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		sqliteTestMessageEvent("turn", "request", "invocation", "latest"),
	))
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		sqliteTestCompletionEvent("request", "invocation"),
	))
	before := sessionExpiration(t, service, key)

	result, err := service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, before, sessionExpiration(t, service, key))
	assert.Equal(t, before, archiveExpiration(t, service, key))

	require.NoError(t, service.AppendEvent(
		ctx,
		result.ActiveSession,
		sqliteTestMessageEvent("after", "after", "after-invocation", "after"),
	))
	after := sessionExpiration(t, service, key)
	assert.GreaterOrEqual(t, after, before)
	assert.Equal(t, after, archiveExpiration(t, service, key))
}

func TestRevisionTablesAreOptionalForLegacyDatabase(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	for _, table := range []string{
		service.tableSessionRevisions,
		service.tableRevisionArchives,
	} {
		_, err := service.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s", table))
		require.NoError(t, err)
	}

	loaded, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		sqliteTestMessageEvent("event", "request", "invocation", "content"),
	))
	_, err = service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnsupported)
	require.NoError(t, service.DeleteSession(ctx, key))
}

func TestReplaceLatestTurnRejectsInvalidRevisionRecords(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err = service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	setRecord := func(raw []byte) {
		_, err := service.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, record, updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(app_name, user_id, session_id) DO UPDATE SET
  record = excluded.record,
  updated_at = excluded.updated_at`,
				service.tableSessionRevisions,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
			raw,
			time.Now().UTC().UnixNano(),
		)
		require.NoError(t, err)
	}
	setJSONRecord := func(record sessionrevision.PersistedRecord) {
		raw, err := json.Marshal(record)
		require.NoError(t, err)
		setRecord(raw)
	}

	setRecord([]byte("not-json"))
	_, err = service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorContains(t, err, "decode revision metadata")

	tests := []struct {
		name   string
		record sessionrevision.PersistedRecord
		is     error
	}{
		{
			name: "missing checkpoint",
			is:   sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "request mismatch",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "other", Terminal: true, Snapshot: []byte(`{}`),
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "generation exhausted",
			record: sessionrevision.PersistedRecord{
				Generation: math.MaxInt64,
				Checkpoint: &sessionrevision.PersistedCheckpoint{
					RequestID: "request", Terminal: true, Snapshot: []byte(`{}`),
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "invalid checkpoint",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "request", Terminal: true, Snapshot: []byte("not-json"),
			}},
		},
		{
			name: "replay identity mismatch",
			record: sessionrevision.PersistedRecord{Replays: map[string]sessionrevision.PersistedReplay{
				"replacement": {RequestID: "other"},
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "replay generation mismatch",
			record: sessionrevision.PersistedRecord{
				Generation: 2,
				Replays: map[string]sessionrevision.PersistedReplay{
					"replacement": {RequestID: "request", Generation: 1},
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setJSONRecord(tt.record)
			_, err := service.ReplaceLatestTurn(
				ctx,
				sessionrevision.LatestTurnReplacementRequest{
					Key:               key,
					ExpectedRequestID: "request",
					IdempotencyKey:    "replacement",
				},
			)
			require.Error(t, err)
			if tt.is != nil {
				assert.ErrorIs(t, err, tt.is)
			}
		})
	}

	setJSONRecord(sessionrevision.PersistedRecord{
		Generation: 1,
		Head:       1,
		Replays: map[string]sessionrevision.PersistedReplay{
			"replacement": {RequestID: "request", Generation: 1, Head: 1},
		},
	})
	_, err = service.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET state = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			service.tableSessionStates,
		),
		[]byte("not-json"),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	require.NoError(t, err)
	_, err = service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorContains(t, err, "decode active session state")
}

func TestReplaceLatestTurnValidatesRequest(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	tests := []struct {
		name string
		req  sessionrevision.LatestTurnReplacementRequest
	}{
		{name: "invalid key"},
		{name: "missing expected request", req: sessionrevision.LatestTurnReplacementRequest{
			Key: key, IdempotencyKey: "replacement",
		}},
		{name: "missing idempotency key", req: sessionrevision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "request",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.ReplaceLatestTurn(ctx, tt.req)
			require.Error(t, err)
		})
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = service.ReplaceLatestTurn(
		cancelled,
		sessionrevision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "request",
			IdempotencyKey:    "replacement",
		},
	)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRevisionFlushHonorsCancellation(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	t.Run("event send", func(t *testing.T) {
		service := &Service{
			opts:           ServiceOpts{enableAsyncPersist: true},
			eventPairChans: []chan *sessionEventPair{make(chan *sessionEventPair)},
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := service.flushRevisionPersistence(ctx, key)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("event wait", func(t *testing.T) {
		eventCh := make(chan *sessionEventPair)
		service := &Service{
			opts:           ServiceOpts{enableAsyncPersist: true},
			eventPairChans: []chan *sessionEventPair{eventCh},
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-eventCh
			cancel()
		}()
		err := service.flushRevisionPersistence(ctx, key)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("track send", func(t *testing.T) {
		service := &Service{
			opts:            ServiceOpts{enableAsyncPersist: true},
			trackEventChans: []chan *trackEventPair{make(chan *trackEventPair)},
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := service.flushTrackPersistence(ctx, 0)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("track wait", func(t *testing.T) {
		trackCh := make(chan *trackEventPair)
		service := &Service{
			opts:            ServiceOpts{enableAsyncPersist: true},
			trackEventChans: []chan *trackEventPair{trackCh},
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-trackCh
			cancel()
		}()
		err := service.flushTrackPersistence(ctx, 0)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestReadRevisionHonorsCancellation(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.readRevision(
		ctx,
		service.db,
		session.Key{AppName: "app", UserID: "user", SessionID: "session"},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRevisionReadBoundaries(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "missing"}
	require.NoError(t, service.attachRevisionGeneration(ctx, key, nil))
	require.NoError(t, service.flushTrackPersistence(ctx, 0))

	tx, err := service.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, _, err = service.loadActiveSessionTx(ctx, tx, key)
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	require.NoError(t, tx.Rollback())

	require.NoError(t, service.Close())
	err = service.attachRevisionGeneration(
		ctx,
		key,
		session.NewSession(key.AppName, key.UserID, key.SessionID),
	)
	require.Error(t, err)
}

func sessionExpiration(t *testing.T, service *Service, key session.Key) int64 {
	t.Helper()
	var expiresAt int64
	require.NoError(t, service.db.QueryRowContext(
		context.Background(),
		fmt.Sprintf(
			`SELECT expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			service.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expiresAt))
	return expiresAt
}

func archiveExpiration(t *testing.T, service *Service, key session.Key) int64 {
	t.Helper()
	var expiresAt int64
	require.NoError(t, service.db.QueryRowContext(
		context.Background(),
		fmt.Sprintf(
			`SELECT expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			service.tableRevisionArchives,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expiresAt))
	return expiresAt
}

func assertRevisionMetadataRows(
	t *testing.T,
	service *Service,
	key session.Key,
	want int,
) {
	t.Helper()
	for _, table := range []string{
		service.tableSessionRevisions,
		service.tableRevisionArchives,
	} {
		var count int
		require.NoError(t, service.db.QueryRowContext(
			context.Background(),
			fmt.Sprintf(
				`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				table,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&count))
		assert.Equal(t, want, count, "table %s", table)
	}
}

func testSQLiteLatestTurnReplacement(t *testing.T, service *Service, async bool) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, session.StateMap{"phase": []byte("before")})
	require.NoError(t, err)
	baseline := sqliteTestMessageEvent("before", "request-before", "invocation-before", "before")
	baseline.Response.Choices[0].Message.Role = model.RoleUser
	require.NoError(t, service.AppendEvent(ctx, sess, baseline))
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		Payload:   []byte(`{"value":"before"}`),
		Timestamp: time.Now().Add(-time.Second),
	}))
	sess.Summaries = map[string]*session.Summary{
		"": {Summary: "before", UpdatedAt: time.Now().Add(-time.Second)},
	}
	summaryRaw, err := json.Marshal(sess.Summaries[""])
	require.NoError(t, err)
	_, err = service.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
  app_name, user_id, session_id, filter_key, summary, updated_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
			service.tableSessionSummaries,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		"",
		summaryRaw,
		sess.Summaries[""].UpdatedAt.UTC().UnixNano(),
	)
	require.NoError(t, err)
	require.NoError(t, service.UpdateAppState(
		ctx,
		key.AppName,
		session.StateMap{"theme": []byte("dark")},
	))
	require.NoError(t, service.UpdateUserState(
		ctx,
		session.UserKey{AppName: key.AppName, UserID: key.UserID},
		session.StateMap{"locale": []byte("en")},
	))

	turnCtx := sessionrevision.ContextWithTurnStart(ctx, sessionrevision.TurnStart{
		RequestID:    "request-latest",
		InvocationID: "invocation-latest",
	})
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		sqliteTestMessageEvent("latest", "request-latest", "invocation-latest", "latest"),
	))
	stateEvent := sqliteTestMessageEvent("state", "request-latest", "invocation-latest", "state")
	stateEvent.StateDelta = session.StateMap{"phase": []byte("after")}
	require.NoError(t, service.AppendEvent(ctx, sess, stateEvent))
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "ui",
		RequestID: "request-latest",
		Payload:   []byte(`{"value":"latest"}`),
		Timestamp: time.Now(),
	}))
	require.NoError(t, service.AppendEvent(ctx, sess, sqliteTestCompletionEvent(
		"request-latest",
		"invocation-latest",
	)))

	result, err := sessionrevision.ReplaceLatestTurn(ctx, service, sessionrevision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request-latest",
		IdempotencyKey:    "replacement-1",
	})
	require.NoError(t, err)
	assert.True(t, result.Applied)
	require.Len(t, result.ActiveSession.Events, 1)
	assert.Equal(t, "before", result.ActiveSession.Events[0].ID)
	phase, ok := result.ActiveSession.GetState("phase")
	require.True(t, ok)
	assert.Equal(t, []byte("before"), phase)
	appTheme, ok := result.ActiveSession.GetState(session.StateAppPrefix + "theme")
	require.True(t, ok)
	assert.Equal(t, []byte("dark"), appTheme)
	userLocale, ok := result.ActiveSession.GetState(session.StateUserPrefix + "locale")
	require.True(t, ok)
	assert.Equal(t, []byte("en"), userLocale)
	trackEvents, err := result.ActiveSession.GetTrackEvents("ui")
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	assert.JSONEq(t, `{"value":"before"}`, string(trackEvents.Events[0].Payload))
	require.Contains(t, result.ActiveSession.Summaries, "")
	assert.Equal(t, "before", result.ActiveSession.Summaries[""].Summary)

	replayed, err := sessionrevision.ReplaceLatestTurn(ctx, service, sessionrevision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "request-latest",
		IdempotencyKey:    "replacement-1",
	})
	require.NoError(t, err)
	assert.False(t, replayed.Applied)

	err = service.AppendEvent(ctx, sess, sqliteTestMessageEvent(
		"stale",
		"request-stale",
		"invocation-stale",
		"stale",
	))
	if async {
		require.NoError(t, err)
		assert.ErrorIs(
			t,
			service.flushRevisionPersistence(ctx, key),
			sessionrevision.ErrStaleGeneration,
		)
	} else {
		assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
	}
	active, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, active.Events, 1)
	assert.Equal(t, "before", active.Events[0].ID)
}

func sqliteTestMessageEvent(id, requestID, invocationID, content string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "author", &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, Content: content},
		}},
	})
	evt.ID = id
	evt.RequestID = requestID
	return evt
}

func sqliteTestCompletionEvent(requestID, invocationID string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "app", &model.Response{
		Done:   true,
		Object: model.ObjectTypeRunnerCompletion,
	})
	evt.RequestID = requestID
	return evt
}
