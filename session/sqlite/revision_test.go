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
	"database/sql"
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

func TestRevisionProjectionReadFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	newService := func(t *testing.T) *Service {
		t.Helper()
		db, _, cleanup := openTempSQLiteDB(t)
		t.Cleanup(cleanup)
		service, err := NewService(db)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err = service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		return service
	}
	load := func(t *testing.T, service *Service) error {
		t.Helper()
		tx, err := service.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, _, err = service.loadActiveSessionTx(context.Background(), tx, key)
		return err
	}

	for name, table := range map[string]string{
		"events": "session_events", "tracks": "session_track_events",
		"summaries": "session_summaries",
	} {
		t.Run(name+" query", func(t *testing.T) {
			service := newService(t)
			_, err := service.db.ExecContext(
				context.Background(), fmt.Sprintf("DROP TABLE %s", table),
			)
			require.NoError(t, err)
			assert.Error(t, load(t, service))
		})
	}

	t.Run("event decode", func(t *testing.T) {
		service := newService(t)
		now := time.Now().UnixNano()
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`INSERT INTO %s (app_name, user_id, session_id, event, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, service.tableSessionEvents,
		), key.AppName, key.UserID, key.SessionID, []byte("{"), now, now)
		require.NoError(t, err)
		assert.Error(t, load(t, service))
	})

	t.Run("track decode", func(t *testing.T) {
		service := newService(t)
		now := time.Now().UnixNano()
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`INSERT INTO %s (app_name, user_id, session_id, track, event, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, service.tableSessionTracks,
		), key.AppName, key.UserID, key.SessionID, "trace", []byte("{"), now, now)
		require.NoError(t, err)
		assert.Error(t, load(t, service))
	})

	t.Run("summary decode", func(t *testing.T) {
		service := newService(t)
		now := time.Now().UnixNano()
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, service.tableSessionSummaries,
		), key.AppName, key.UserID, key.SessionID, "all", []byte("{"), now)
		require.NoError(t, err)
		assert.Error(t, load(t, service))
	})
}

func TestReplacementResultScopedStateFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	for name, table := range map[string]string{
		"app": "app_states", "user": "user_states",
	} {
		t.Run(name, func(t *testing.T) {
			db, _, cleanup := openTempSQLiteDB(t)
			t.Cleanup(cleanup)
			service, err := NewService(db)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			_, err = service.db.ExecContext(
				context.Background(), fmt.Sprintf("DROP TABLE %s", table),
			)
			require.NoError(t, err)
			_, err = service.replacementResultWithScopedState(
				context.Background(), key,
				session.NewSession(key.AppName, key.UserID, key.SessionID), false,
			)
			assert.Error(t, err)
		})
	}
}

func TestReplaceActiveSessionWriteFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	newService := func(t *testing.T) *Service {
		t.Helper()
		db, _, cleanup := openTempSQLiteDB(t)
		t.Cleanup(cleanup)
		service, err := NewService(db)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err = service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		return service
	}
	restore := func(t *testing.T, service *Service, restored *session.Session) error {
		t.Helper()
		tx, err := service.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		return service.replaceActiveSessionTx(
			context.Background(), tx, key, restored, sql.NullInt64{},
		)
	}
	newProjection := func() *session.Session {
		return session.NewSession(
			key.AppName, key.UserID, key.SessionID,
			session.WithSessionCreatedAt(time.Now()),
			session.WithSessionUpdatedAt(time.Now()),
		)
	}

	for name, table := range map[string]string{
		"state": "session_states", "events": "session_events",
		"tracks": "session_track_events", "summaries": "session_summaries",
	} {
		t.Run(name+" table", func(t *testing.T) {
			service := newService(t)
			_, err := service.db.ExecContext(
				context.Background(), fmt.Sprintf("DROP TABLE %s", table),
			)
			require.NoError(t, err)
			assert.Error(t, restore(t, service, newProjection()))
		})
	}

	t.Run("event insert", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_event BEFORE INSERT ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionEvents,
		))
		require.NoError(t, err)
		restored := newProjection()
		restored.Events = append(restored.Events, *sqliteTestMessageEvent(
			"event", "request", "invocation", "content",
		))
		assert.ErrorContains(t, restore(t, service, restored), "restore active event")
	})

	t.Run("track tail delete", func(t *testing.T) {
		service := newService(t)
		prefix := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":1}`),
			Timestamp: time.Now(),
		}
		tail := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":2}`),
			Timestamp: prefix.Timestamp.Add(time.Second),
		}
		for _, trackEvent := range []session.TrackEvent{prefix, tail} {
			raw, err := json.Marshal(trackEvent)
			require.NoError(t, err)
			_, err = service.db.ExecContext(
				context.Background(),
				fmt.Sprintf(
					`INSERT INTO %s (
  app_name, user_id, session_id, track, event, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					service.tableSessionTracks,
				),
				key.AppName, key.UserID, key.SessionID, trackEvent.Track, raw,
				trackEvent.Timestamp.UTC().UnixNano(),
				trackEvent.Timestamp.UTC().UnixNano(),
			)
			require.NoError(t, err)
		}
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_track BEFORE DELETE ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionTracks,
		))
		require.NoError(t, err)
		restored := newProjection()
		restored.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{prefix}},
		}
		assert.ErrorContains(t, restore(t, service, restored), "remove discarded track tail")
	})

	t.Run("summary insert", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_summary BEFORE INSERT ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionSummaries,
		))
		require.NoError(t, err)
		restored := newProjection()
		restored.Summaries = map[string]*session.Summary{
			"all": {Summary: "summary", UpdatedAt: time.Now()},
		}
		assert.ErrorContains(t, restore(t, service, restored), "restore active summary")
	})

	t.Run("projection edges", func(t *testing.T) {
		service := newService(t)
		restored := newProjection()
		evt := sqliteTestMessageEvent(
			"event", "request", "invocation", "content",
		)
		evt.Timestamp = time.Time{}
		restored.Events = append(restored.Events, *evt)
		restored.Tracks = map[session.Track]*session.TrackEvents{"nil": nil}
		restored.Summaries = map[string]*session.Summary{"nil": nil}
		require.NoError(t, restore(t, service, restored))
	})

	t.Run("event encoding", func(t *testing.T) {
		service := newService(t)
		restored := newProjection()
		evt := sqliteTestMessageEvent(
			"event", "request", "invocation", "content",
		)
		evt.Extensions = map[string]json.RawMessage{"invalid": {0xff}}
		restored.Events = append(restored.Events, *evt)
		assert.Error(t, restore(t, service, restored))
	})

	t.Run("track event encoding", func(t *testing.T) {
		service := newService(t)
		restored := newProjection()
		restored.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{{
				Track: "trace", Payload: json.RawMessage{0xff}, Timestamp: time.Now(),
			}}},
		}
		assert.Error(t, restore(t, service, restored))
	})
}

func TestReplaceLatestTurnDatabaseFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	req := sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
	}
	newService := func(t *testing.T) *Service {
		t.Helper()
		db, _, cleanup := openTempSQLiteDB(t)
		t.Cleanup(cleanup)
		service, err := NewService(db)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		turnCtx := sessionrevision.ContextWithTurnStart(
			context.Background(), sessionrevision.TurnStart{
				RequestID: "request", InvocationID: "invocation",
			},
		)
		require.NoError(t, service.AppendEvent(
			turnCtx, sess,
			sqliteTestMessageEvent("latest", "request", "invocation", "latest"),
		))
		return service
	}

	t.Run("load active state", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), "DROP TABLE session_states")
		require.NoError(t, err)
		_, err = service.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "load active session state")
	})

	t.Run("archive", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_archive BEFORE INSERT ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableRevisionArchives,
		))
		require.NoError(t, err)
		_, err = service.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "archive discarded revision")
	})

	t.Run("restore state", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_state BEFORE UPDATE ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionStates,
		))
		require.NoError(t, err)
		_, err = service.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "restore session state")
	})

	t.Run("revision metadata", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_revision BEFORE INSERT ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionRevisions,
		))
		require.NoError(t, err)
		_, err = service.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "store revision metadata")
	})
}

func TestRevisionMetadataWriteFailures(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err = service.db.ExecContext(
		context.Background(),
		fmt.Sprintf(
			`INSERT INTO %s
(app_name, user_id, session_id, generation, snapshot, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			service.tableRevisionArchives,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		0,
		[]byte(`{}`),
		time.Now().UnixNano(),
	)
	require.NoError(t, err)

	_, err = service.db.ExecContext(context.Background(), fmt.Sprintf(
		`CREATE TRIGGER fail_delete BEFORE DELETE ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableRevisionArchives,
	))
	require.NoError(t, err)
	err = service.deleteRevisionMetadata(context.Background(), service.db, key)
	assert.ErrorContains(t, err, "delete revision metadata")

	_, err = service.db.ExecContext(context.Background(), "DROP TABLE session_revisions")
	require.NoError(t, err)
	tx, err := service.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = service.writeRevisionTx(
		context.Background(), tx, key, &sessionrevision.PersistedRecord{}, nil,
	)
	assert.ErrorContains(t, err, "store revision metadata")
	require.NoError(t, tx.Rollback())
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
