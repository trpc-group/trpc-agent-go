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
	"strings"
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

func TestTurnStartReusesRollingProjection(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "rolling"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	firstCtx := sessionrevision.ContextWithTurnStart(
		ctx,
		sessionrevision.TurnStart{
			RequestID: "first", InvocationID: "first-invocation",
		},
	)
	require.NoError(t, service.AppendEvent(
		firstCtx,
		sess,
		sqliteTestMessageEvent(
			"first", "first", "first-invocation", "first",
		),
	))
	require.NoError(t, service.AppendEvent(
		ctx, sess, sqliteTestCompletionEvent("first", "first-invocation"),
	))
	record, err := service.readRevision(ctx, service.db, key)
	require.NoError(t, err)
	assert.True(t, sessionrevision.ProjectionInitialized(record))

	// A steady-state turn start must not read or decode historical payloads.
	// Replacement still performs a full authoritative verification and will
	// fail closed if persisted history was externally corrupted.
	_, err = service.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET event = ? WHERE app_name = ? AND user_id = ?
AND session_id = ?`,
			service.tableSessionEvents,
		),
		[]byte("{"), key.AppName, key.UserID, key.SessionID,
	)
	require.NoError(t, err)
	secondCtx := sessionrevision.ContextWithTurnStart(
		ctx,
		sessionrevision.TurnStart{
			RequestID: "second", InvocationID: "second-invocation",
		},
	)
	require.NoError(t, service.AppendEvent(
		secondCtx,
		sess,
		sqliteTestMessageEvent(
			"second", "second", "second-invocation", "second",
		),
	))
	record, err = service.readRevision(ctx, service.db, key)
	require.NoError(t, err)
	require.NotNil(t, record.Checkpoint)
	assert.Equal(t, "second", record.Checkpoint.RequestID)
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
				WithSessionTTL(time.Hour),
				WithEnableAsyncPersist(async),
				WithAsyncPersisterNum(1),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			testSQLiteLatestTurnReplacement(t, service, async)
		})
	}
}

func TestDeleteSessionRemovesEmbeddedRevisionMetadata(t *testing.T) {
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

			record, err := service.readRevision(ctx, service.db, key)
			require.NoError(t, err)
			assert.Equal(t, uint64(1), record.Generation)

			require.NoError(t, service.DeleteSession(ctx, key))
			record, err = service.readRevision(ctx, service.db, key)
			require.NoError(t, err)
			assert.Equal(t, &sessionrevision.PersistedRecord{}, record)
		})
	}
}

func TestReplacementPreservesEarlierSoftDeletedProjection(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "reused"}
	oldSession, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, service.AppendEvent(ctx, oldSession,
		sqliteTestMessageEvent("old-event", "old-request", "old-invocation", "old"),
	))
	oldSummary, err := json.Marshal(&session.Summary{
		Summary: "old summary", UpdatedAt: time.Now(),
	})
	require.NoError(t, err)
	_, err = service.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (
  app_name, user_id, session_id, filter_key, summary, updated_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		service.tableSessionSummaries,
	), key.AppName, key.UserID, key.SessionID, "old-filter", oldSummary,
		time.Now().UTC().UnixNano())
	require.NoError(t, err)
	require.NoError(t, service.DeleteSession(ctx, key))

	active, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, service.AppendEvent(ctx, active,
		sqliteTestMessageEvent("baseline", "baseline-request", "baseline-invocation", "baseline"),
	))
	turnCtx := sessionrevision.ContextWithTurnStart(ctx, sessionrevision.TurnStart{
		RequestID: "latest-request", InvocationID: "latest-invocation",
	})
	require.NoError(t, service.AppendEvent(turnCtx, active,
		sqliteTestMessageEvent("latest", "latest-request", "latest-invocation", "latest"),
	))
	require.NoError(t, service.AppendEvent(ctx, active,
		sqliteTestCompletionEvent("latest-request", "latest-invocation"),
	))
	_, err = service.ReplaceLatestTurn(ctx, sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "latest-request", IdempotencyKey: "replacement",
	})
	require.NoError(t, err)

	var oldEvents int
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND json_extract(CAST(event AS TEXT), '$.id') = ?
AND deleted_at IS NOT NULL`,
		service.tableSessionEvents,
	), key.AppName, key.UserID, key.SessionID, "old-event").Scan(&oldEvents))
	assert.Equal(t, 1, oldEvents)

	var oldSummaries int
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND filter_key = ? AND deleted_at IS NOT NULL`,
		service.tableSessionSummaries,
	), key.AppName, key.UserID, key.SessionID, "old-filter").Scan(&oldSummaries))
	assert.Equal(t, 1, oldSummaries)
}

func TestReplacementRetainsSQLiteEventPrefixRowsAndOrder(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db, WithSoftDelete(true))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	first := sqliteTestMessageEvent(
		"first", "first-request", "first-invocation", "first",
	)
	first.Timestamp = time.Unix(200, 0).UTC()
	second := sqliteTestMessageEvent(
		"second", "second-request", "second-invocation", "second",
	)
	second.Timestamp = time.Unix(100, 0).UTC()
	require.NoError(t, service.AppendEvent(ctx, sess, first))
	require.NoError(t, service.AppendEvent(ctx, sess, second))
	turnCtx := sessionrevision.ContextWithTurnStart(
		ctx,
		sessionrevision.TurnStart{
			RequestID: "latest-request", InvocationID: "latest-invocation",
		},
	)
	require.NoError(t, service.AppendEvent(
		turnCtx,
		sess,
		sqliteTestMessageEvent(
			"latest", "latest-request", "latest-invocation", "latest",
		),
	))
	require.NoError(t, service.AppendEvent(
		ctx,
		sess,
		sqliteTestCompletionEvent("latest-request", "latest-invocation"),
	))

	activeIDs := func() []int64 {
		rows, err := service.db.QueryContext(ctx, fmt.Sprintf(
			`SELECT id FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC`,
			service.tableSessionEvents,
		), key.AppName, key.UserID, key.SessionID)
		require.NoError(t, err)
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())
		return ids
	}
	before := activeIDs()
	require.Len(t, before, 3)

	_, err = service.ReplaceLatestTurn(
		ctx,
		sessionrevision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "latest-request", IdempotencyKey: "edit",
		},
	)
	require.NoError(t, err)
	after := activeIDs()
	assert.Equal(t, before[:2], after)

	rows, err := service.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC`,
		service.tableSessionEvents,
	), key.AppName, key.UserID, key.SessionID)
	require.NoError(t, err)
	defer rows.Close()
	var persistedEventIDs []string
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var evt event.Event
		require.NoError(t, json.Unmarshal(raw, &evt))
		persistedEventIDs = append(persistedEventIDs, evt.ID)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"first", "second"}, persistedEventIDs)

	var tombstones int
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NOT NULL`,
		service.tableSessionEvents,
	), key.AppName, key.UserID, key.SessionID).Scan(&tombstones))
	assert.Equal(t, 1, tombstones)
}

func TestReplacementPreservesAndLaterWritesRefreshSessionExpiration(t *testing.T) {
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

	require.NoError(t, service.AppendEvent(
		ctx,
		result.ActiveSession,
		sqliteTestMessageEvent("after", "after", "after-invocation", "after"),
	))
	after := sessionExpiration(t, service, key)
	assert.Greater(t, after, before)
}

func TestRevisionMetadataUsesExistingStateTable(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
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
		sqliteTestMessageEvent("event", "request", "invocation", "content"),
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

	var count int
	require.NoError(t, service.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table' AND name IN ('session_revisions', 'session_revision_archives')`,
	).Scan(&count))
	assert.Zero(t, count)
}

func TestUnsupportedRevisionSidecarIsReadOnly(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err = service.CreateSession(ctx, key, session.StateMap{"phase": []byte("before")})
	require.NoError(t, err)
	var raw []byte
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		service.tableSessionStates,
	), key.AppName, key.UserID, key.SessionID).Scan(&raw))
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &envelope))
	envelope["_trpcAgent"] = json.RawMessage(`{"version":2,"opaque":"future"}`)
	raw, err = json.Marshal(envelope)
	require.NoError(t, err)
	_, err = service.db.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET state = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		service.tableSessionStates,
	), raw, key.AppName, key.UserID, key.SessionID)
	require.NoError(t, err)

	got, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	phase, ok := got.GetState("phase")
	require.True(t, ok)
	assert.Equal(t, []byte("before"), phase)
	listed, err := service.ListSessions(ctx, session.UserKey{
		AppName: key.AppName, UserID: key.UserID,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)

	err = service.AppendEvent(ctx, got, sqliteTestMessageEvent(
		"write", "request", "invocation", "write",
	))
	assert.ErrorContains(t, err, "unsupported persisted version 2")
	var after []byte
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		service.tableSessionStates,
	), key.AppName, key.UserID, key.SessionID).Scan(&after))
	assert.JSONEq(t, string(raw), string(after))

	_, err = service.ReplaceLatestTurn(ctx, sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "request", IdempotencyKey: "replacement",
	})
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
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
	setRecord := func(record sessionrevision.PersistedRecord) {
		var raw []byte
		require.NoError(t, service.db.QueryRowContext(
			ctx,
			fmt.Sprintf(
				`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				service.tableSessionStates,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&raw))
		var state SessionState
		require.NoError(t, json.Unmarshal(raw, &state))
		encoded, err := sessionrevision.EncodeState(&state, &record)
		require.NoError(t, err)
		_, err = service.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET state = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				service.tableSessionStates,
			),
			encoded,
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		require.NoError(t, err)
	}

	var rawState map[string]json.RawMessage
	var raw []byte
	require.NoError(t, service.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			service.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &rawState))
	rawState["_trpcAgent"] = json.RawMessage(`"invalid"`)
	invalidMetadata, err := json.Marshal(rawState)
	require.NoError(t, err)
	_, err = service.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET state = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			service.tableSessionStates,
		),
		invalidMetadata,
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
	assert.ErrorContains(t, err, "decode session revision metadata")

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
				RequestID: "other", Terminal: true, Boundary: []byte(`{}`),
			}},
			is: sessionrevision.ErrLatestTurnReplacementConflict,
		},
		{
			name: "generation exhausted",
			record: sessionrevision.PersistedRecord{
				Generation: math.MaxInt64,
				Checkpoint: &sessionrevision.PersistedCheckpoint{
					RequestID: "request", Terminal: true, Boundary: []byte(`{}`),
				},
			},
			is: sessionrevision.ErrLatestTurnReplacementUnavailable,
		},
		{
			name: "invalid checkpoint",
			record: sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "request", Terminal: true, Boundary: []byte("not-json"),
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
			setRecord(tt.record)
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

	setRecord(sessionrevision.PersistedRecord{
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
	assert.ErrorContains(t, err, "decode session state")
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
		err := service.flushTrackPersistence(ctx, key)
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
		err := service.flushTrackPersistence(ctx, key)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRevisionFlushDoesNotMixSessionErrors(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(
		db,
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	keyA := session.Key{AppName: "app", UserID: "user", SessionID: "a"}
	keyB := session.Key{AppName: "app", UserID: "user", SessionID: "b"}
	sessA, err := service.CreateSession(ctx, keyA, nil)
	require.NoError(t, err)
	_, err = service.CreateSession(ctx, keyB, nil)
	require.NoError(t, err)
	_, err = service.db.ExecContext(
		ctx,
		fmt.Sprintf("DROP TABLE %s", service.tableSessionEvents),
	)
	require.NoError(t, err)

	require.NoError(t, service.AppendEvent(
		ctx,
		sessA,
		sqliteTestMessageEvent("event", "request", "invocation", "content"),
	))
	require.NoError(t, service.flushRevisionPersistence(ctx, keyB))
	assert.ErrorContains(t, service.flushRevisionPersistence(ctx, keyA), "no such table")
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

func TestRevisionProjectionBoundaries(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	t.Cleanup(cleanup)
	service, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "missing"}
	require.NoError(t, service.flushTrackPersistence(ctx, key))

	tx, err := service.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, _, err = service.loadActiveSessionTx(ctx, tx, key)
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	require.NoError(t, tx.Rollback())

	require.NoError(t, service.Close())
	_, err = service.readRevision(ctx, service.db, key)
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

func TestRemoveActiveTailRowsBatches(t *testing.T) {
	for _, softDelete := range []bool{false, true} {
		name := "hard delete"
		if softDelete {
			name = "soft delete"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			service := &Service{opts: defaultOptions}
			service.opts.softDelete = softDelete
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "session",
			}
			ids := make([]int64, revisionTailDeleteBatchSize+1)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			exec := &sqliteRecordingExecer{}
			require.NoError(t, service.removeActiveTailRowsTx(
				ctx, exec, "session_events", key, ids,
			))
			require.Len(t, exec.calls, 2)
			offset := 0
			if softDelete {
				offset = 1
			}
			for i, wantArgs := range []int{
				revisionTailDeleteBatchSize + 3 + offset,
				1 + 3 + offset,
			} {
				call := exec.calls[i]
				require.Len(t, call.args, wantArgs)
				assert.Equal(t, wantArgs, strings.Count(call.statement, "?"))
			}
		})
	}
}

type sqliteExecCall struct {
	statement string
	args      []any
}

type sqliteRecordingExecer struct {
	calls []sqliteExecCall
}

func (e *sqliteRecordingExecer) ExecContext(
	_ context.Context,
	statement string,
	args ...any,
) (sql.Result, error) {
	e.calls = append(e.calls, sqliteExecCall{
		statement: statement,
		args:      append([]any(nil), args...),
	})
	return nil, nil
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
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		for i := 0; i < 2; i++ {
			require.NoError(t, service.AppendEvent(
				context.Background(),
				sess,
				sqliteTestMessageEvent(
					fmt.Sprintf("discarded-%d", i),
					"request",
					"invocation",
					"discarded",
				),
			))
		}
		return service
	}
	restore := func(t *testing.T, service *Service, restored *session.Session) error {
		t.Helper()
		tx, err := service.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		return service.replaceActiveSessionTx(
			context.Background(), tx, key, restored,
			&sessionrevision.PersistedRecord{}, sql.NullInt64{},
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

	t.Run("event tail delete", func(t *testing.T) {
		service := newService(t)
		_, err := service.db.ExecContext(context.Background(), fmt.Sprintf(
			`CREATE TRIGGER fail_event BEFORE UPDATE OF deleted_at ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionEvents,
		))
		require.NoError(t, err)
		assert.ErrorContains(
			t,
			restore(t, service, newProjection()),
			"remove discarded event tail",
		)
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
			`CREATE TRIGGER fail_track BEFORE UPDATE OF deleted_at ON %s
BEGIN SELECT RAISE(ABORT, 'fail'); END`, service.tableSessionTracks,
		))
		require.NoError(t, err)
		restored := newProjection()
		restored.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{prefix}},
		}
		assert.ErrorContains(t, restore(t, service, restored), "remove discarded track tail")
	})

	t.Run("track checkpoint longer than active projection", func(t *testing.T) {
		service := newService(t)
		prefix := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":1}`),
			Timestamp: time.Now(),
		}
		raw, err := json.Marshal(prefix)
		require.NoError(t, err)
		_, err = service.db.ExecContext(
			context.Background(),
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, track, event, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				service.tableSessionTracks,
			),
			key.AppName, key.UserID, key.SessionID, prefix.Track, raw,
			prefix.Timestamp.UTC().UnixNano(),
			prefix.Timestamp.UTC().UnixNano(),
		)
		require.NoError(t, err)
		restored := newProjection()
		restored.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{
				prefix,
				{
					Track: "trace", Payload: json.RawMessage(`{"step":2}`),
					Timestamp: prefix.Timestamp.Add(time.Second),
				},
			}},
		}
		err = restore(t, service, restored)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
		assert.ErrorContains(t, err, "checkpoint prefix has 2 events")
	})

	t.Run("track checkpoint differs from active projection", func(t *testing.T) {
		service := newService(t)
		active := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":1}`),
			Timestamp: time.Now(),
		}
		raw, err := json.Marshal(active)
		require.NoError(t, err)
		_, err = service.db.ExecContext(
			context.Background(),
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, track, event, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				service.tableSessionTracks,
			),
			key.AppName, key.UserID, key.SessionID, active.Track, raw,
			active.Timestamp.UTC().UnixNano(),
			active.Timestamp.UTC().UnixNano(),
		)
		require.NoError(t, err)
		restored := newProjection()
		restored.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{{
				Track: "trace", Payload: json.RawMessage(`{"step":"different"}`),
				Timestamp: active.Timestamp,
			}}},
		}
		err = restore(t, service, restored)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
		assert.ErrorContains(t, err, "checkpoint prefix differs at event 0")
	})

	t.Run("track projection contains invalid event", func(t *testing.T) {
		service := newService(t)
		now := time.Now().UTC().UnixNano()
		_, err := service.db.ExecContext(
			context.Background(),
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, track, event, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				service.tableSessionTracks,
			),
			key.AppName, key.UserID, key.SessionID, "trace", []byte("{"), now, now,
		)
		require.NoError(t, err)
		assert.Error(t, restore(t, service, newProjection()))
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
		assert.ErrorContains(t, err, "get revision metadata")
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
	var summariesWithoutTTL int
	require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL AND expires_at IS NULL`,
		service.tableSessionSummaries,
	), key.AppName, key.UserID, key.SessionID).Scan(&summariesWithoutTTL))
	assert.Equal(t, 1, summariesWithoutTTL)
	if service.opts.softDelete {
		var tombstonedTracks int
		require.NoError(t, service.db.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NOT NULL`,
			service.tableSessionTracks,
		), key.AppName, key.UserID, key.SessionID).Scan(&tombstonedTracks))
		assert.Equal(t, 1, tombstonedTracks)
	}

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
