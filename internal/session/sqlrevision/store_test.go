//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlrevision

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const storeTestDriverName = "sqlrevision-sqlite3"

func init() {
	sql.Register(storeTestDriverName, lockingSQLiteDriver{})
}

type lockingSQLiteDriver struct{}

func (lockingSQLiteDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite3.SQLiteDriver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &lockingSQLiteConn{Conn: conn}, nil
}

type lockingSQLiteConn struct {
	driver.Conn
}

func (c *lockingSQLiteConn) QueryContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, stripForUpdate(query), args)
}

func (c *lockingSQLiteConn) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, stripForUpdate(query), args)
}

func (c *lockingSQLiteConn) BeginTx(
	ctx context.Context,
	opts driver.TxOptions,
) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, driver.ErrSkip
	}
	return beginner.BeginTx(ctx, opts)
}

func stripForUpdate(query string) string {
	return strings.TrimSuffix(query, " FOR UPDATE")
}

func TestStoreRollingProjectionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openStoreTestDB(t)
	store := testStore()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	baseTime := time.Now().UTC().Truncate(time.Second)
	initialRecord := &sessionrevision.PersistedRecord{Generation: 3, Head: 4}
	stateRaw, err := sessionrevision.EncodeState(stateEnvelope{
		ID: key.SessionID,
		State: session.StateMap{
			"phase": []byte(`"initial"`),
		},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
	}, initialRecord)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO states (app_name, user_id, session_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, stateRaw, baseTime, baseTime,
	)
	require.NoError(t, err)

	before := storeTestEvent("before", "before-request", baseTime)
	beforeRaw, err := json.Marshal(before)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, beforeRaw, baseTime,
	)
	require.NoError(t, err)

	trackBefore := session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"phase":"before"}`),
		Timestamp: baseTime,
	}
	trackRaw, err := json.Marshal(trackBefore)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO tracks (app_name, user_id, session_id, track, event, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID,
		trackBefore.Track, trackRaw, baseTime,
	)
	require.NoError(t, err)

	summary := &session.Summary{Summary: "before", UpdatedAt: baseTime}
	summaryRaw, err := json.Marshal(summary)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO summaries (app_name, user_id, session_id, filter_key, summary, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID,
		"default", summaryRaw, baseTime,
	)
	require.NoError(t, err)

	active, expiresAt, err := store.LoadActive(ctx, db, key, false)
	require.NoError(t, err)
	assert.Nil(t, expiresAt)
	require.Len(t, active.Events, 1)
	require.Len(t, active.Tracks["trace"].Events, 1)
	assert.Equal(t, "before", active.Summaries["default"].Summary)

	generation, err := store.Generation(ctx, db, key)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), generation)
	generations, err := store.Generations(ctx, db, []session.Key{
		key,
		{AppName: "app", UserID: "user", SessionID: "missing"},
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(3), generations[key])
	assert.Zero(t, generations[session.Key{
		AppName: "app", UserID: "user", SessionID: "missing",
	}])

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	record := &sessionrevision.PersistedRecord{Generation: 3, Head: 4}
	latest := storeTestEvent("latest", "latest-request", baseTime.Add(time.Second))
	write := sessionrevision.Write{
		ExpectedGeneration:    3,
		HasExpectedGeneration: true,
		Start: &sessionrevision.TurnStart{
			RequestID: latest.RequestID, InvocationID: latest.InvocationID,
		},
	}
	require.NoError(t, store.ApplyEventWrite(
		ctx, tx, key, record, write, latest, true,
	))
	latestRaw, err := json.Marshal(latest)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, latestRaw, latest.Timestamp,
	)
	require.NoError(t, err)
	assert.True(t, sessionrevision.ProjectionInitialized(record))
	require.NotNil(t, record.Checkpoint)
	assert.Equal(t, latest.RequestID, record.Checkpoint.RequestID)

	// A corrupt historical row would fail a second bootstrap. The next start
	// succeeds because it reads only state and summaries once rolling metadata
	// has been established.
	_, err = tx.ExecContext(ctx, `UPDATE events SET event = 'not-json' WHERE id = 1`)
	require.NoError(t, err)
	record.Checkpoint.Terminal = true
	next := storeTestEvent("next", "next-request", baseTime.Add(2*time.Second))
	write.Start = &sessionrevision.TurnStart{
		RequestID: next.RequestID, InvocationID: next.InvocationID,
	}
	write.RequestID = next.RequestID
	require.NoError(t, store.ApplyEventWrite(
		ctx, tx, key, record, write, next, true,
	))
	assert.Equal(t, next.RequestID, record.Checkpoint.RequestID)

	trackAfter := &session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"phase":"after"}`),
		Timestamp: baseTime.Add(3 * time.Second),
	}
	require.NoError(t, store.ApplyTrackWrite(record, write, trackAfter))
	require.NoError(t, store.ApplyMutation(record, write))
	assert.ErrorIs(t, store.ApplyMutation(record, sessionrevision.Write{
		ExpectedGeneration: 4, HasExpectedGeneration: true,
	}), sessionrevision.ErrStaleGeneration)
}

func TestStoreChildCleanupHelpers(t *testing.T) {
	ctx := context.Background()
	db := openStoreTestDB(t)
	store := testStore()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	now := time.Now().UTC()
	record := &sessionrevision.PersistedRecord{
		Head: 7,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "request", Boundary: []byte("boundary"),
		},
	}
	require.NoError(t, sessionrevision.InitializeProjection(
		record, session.NewSession(key.AppName, key.UserID, key.SessionID),
	))
	stateRaw, err := sessionrevision.EncodeState(stateEnvelope{
		ID: key.SessionID, State: session.StateMap{},
		CreatedAt: now, UpdatedAt: now,
	}, record)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO states (app_name, user_id, session_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, stateRaw, now, now,
	)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err := db.ExecContext(ctx, `
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, '{}', ?)`,
			key.AppName, key.UserID, key.SessionID, now.Add(time.Duration(i)*time.Second),
		)
		require.NoError(t, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.removeTailRows(
		ctx, tx, store.Tables.Events, key, []int64{2},
	))
	require.NoError(t, tx.Commit())
	assert.Equal(t, 1, storeTestRowCount(t, db, "events", "deleted_at IS NULL"))

	store.SoftDelete = true
	_, err = db.ExecContext(ctx, `
INSERT INTO tracks (
  app_name, user_id, session_id, track, event, created_at, expires_at
) VALUES (?, ?, ?, 'trace', '{}', ?, ?)`,
		key.AppName, key.UserID, key.SessionID, now, now.Add(-time.Second),
	)
	require.NoError(t, err)
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.removeTailRows(
		ctx, tx, store.Tables.Tracks, key, []int64{1},
	))
	require.NoError(t, tx.Commit())
	assert.Equal(t, 1, storeTestRowCount(t, db, "tracks", "deleted_at IS NOT NULL"))

	_, err = db.ExecContext(ctx, `
INSERT INTO tracks (
  app_name, user_id, session_id, track, event, created_at, expires_at
) VALUES (?, ?, ?, 'trace', '{}', ?, ?)`,
		key.AppName, key.UserID, key.SessionID, now, now.Add(-time.Second),
	)
	require.NoError(t, err)
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.InvalidateExpiredChildProjections(
		ctx, tx, store.Tables.Tracks, now, nil,
	))
	require.NoError(t, tx.Commit())
	invalidated, err := store.Read(ctx, db, key, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), invalidated.Head)
	assert.False(t, sessionrevision.ProjectionInitialized(invalidated))
	require.NotNil(t, invalidated.Checkpoint)
	assert.True(t, invalidated.Checkpoint.Hazard)

	missing := session.Key{AppName: "missing", UserID: "user", SessionID: "session"}
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.InvalidateProjection(ctx, tx, missing))
	require.NoError(t, tx.Commit())

	cleanupKeys := []session.Key{
		{AppName: "app-b", UserID: "user-a", SessionID: "session-a"},
		{AppName: "app-a", UserID: "user-b", SessionID: "session-a"},
		{AppName: "app-a", UserID: "user-a", SessionID: "session-b"},
		{AppName: "app-a", UserID: "user-a", SessionID: "session-a"},
	}
	for _, cleanupKey := range cleanupKeys {
		cleanupRecord := &sessionrevision.PersistedRecord{}
		require.NoError(t, sessionrevision.InitializeProjection(
			cleanupRecord,
			session.NewSession(
				cleanupKey.AppName, cleanupKey.UserID, cleanupKey.SessionID,
			),
		))
		insertStoreTestState(t, db, cleanupKey, now, cleanupRecord)
	}
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.InvalidateProjections(ctx, tx, []session.Key{
		cleanupKeys[0], cleanupKeys[2], cleanupKeys[1], cleanupKeys[3], cleanupKeys[2],
	}))
	require.NoError(t, tx.Commit())
	for _, cleanupKey := range cleanupKeys {
		cleanupRecord, err := store.Read(ctx, db, cleanupKey, false)
		require.NoError(t, err)
		assert.Equal(t, uint64(1), cleanupRecord.Head)
		assert.False(t, sessionrevision.ProjectionInitialized(cleanupRecord))
	}

	insertStoreTestTrack(t, db, cleanupKeys[2], &session.TrackEvent{
		Track: "trace", Timestamp: now.Add(-time.Second),
	})
	insertStoreTestTrack(t, db, cleanupKeys[0], &session.TrackEvent{
		Track: "trace", Timestamp: now.Add(-time.Second),
	})
	_, err = db.ExecContext(ctx, `UPDATE tracks SET expires_at = ?`, now.Add(-time.Second))
	require.NoError(t, err)
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.InvalidateExpiredChildProjections(
		ctx,
		tx,
		store.Tables.Tracks,
		now,
		&session.UserKey{AppName: cleanupKeys[2].AppName, UserID: cleanupKeys[2].UserID},
	))
	require.NoError(t, tx.Commit())
	filtered, err := store.Read(ctx, db, cleanupKeys[2], false)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), filtered.Head)
	unmatched, err := store.Read(ctx, db, cleanupKeys[0], false)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), unmatched.Head)

	restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
	restored.Summaries = map[string]*session.Summary{
		"default": {Summary: "restored", UpdatedAt: now},
	}
	currentSummary, err := json.Marshal(&session.Summary{
		Summary: "current", UpdatedAt: now,
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO summaries (
  app_name, user_id, session_id, filter_key, summary, updated_at
) VALUES (?, ?, ?, 'default', ?, ?)`,
		key.AppName, key.UserID, key.SessionID, currentSummary, now,
	)
	require.NoError(t, err)
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.replaceSummaries(
		ctx, tx, key, restored, time.Now().UTC(),
	))
	require.NoError(t, tx.Commit())
	assert.Equal(t, 1, storeTestRowCount(t, db, "summaries", "deleted_at IS NULL"))
}

func TestStoreRemoveTailRowsBatches(t *testing.T) {
	tests := []struct {
		name       string
		dialect    Dialect
		softDelete bool
	}{
		{name: "mysql hard delete", dialect: MySQL},
		{name: "mysql soft delete", dialect: MySQL, softDelete: true},
		{name: "postgres hard delete", dialect: PostgreSQL},
		{name: "postgres soft delete", dialect: PostgreSQL, softDelete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := testStore()
			store.Dialect = tt.dialect
			store.SoftDelete = tt.softDelete
			key := session.Key{
				AppName: "app", UserID: "user", SessionID: "session",
			}
			ids := make([]int64, tailDeleteBatchSize+1)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			exec := &recordingExecer{}
			require.NoError(t, store.removeTailRows(
				ctx, exec, store.Tables.Events, key, ids,
			))
			require.Len(t, exec.calls, 2)
			offset := 0
			if tt.softDelete {
				offset = 1
			}
			for i, wantArgs := range []int{
				tailDeleteBatchSize + 3 + offset,
				1 + 3 + offset,
			} {
				call := exec.calls[i]
				require.Len(t, call.args, wantArgs)
				if tt.dialect == MySQL {
					assert.Equal(t, wantArgs, strings.Count(call.statement, "?"))
					continue
				}
				assert.Contains(t, call.statement, store.bind(wantArgs))
				assert.NotContains(t, call.statement, store.bind(wantArgs+1))
			}
		})
	}
}

type recordedExecCall struct {
	statement string
	args      []any
}

type recordingExecer struct {
	calls []recordedExecCall
}

func (e *recordingExecer) ExecContext(
	_ context.Context,
	statement string,
	args ...any,
) (sql.Result, error) {
	e.calls = append(e.calls, recordedExecCall{
		statement: statement,
		args:      append([]any(nil), args...),
	})
	return nil, nil
}

func TestStoreRewind(t *testing.T) {
	ctx := context.Background()
	db := openStoreTestDB(t)
	store := testStore()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "replace"}
	baseTime := time.Now().UTC().Truncate(time.Second)
	expiresAt := baseTime.Add(24 * time.Hour)
	before := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionState(session.StateMap{"phase": []byte(`"before"`)}),
		session.WithSessionCreatedAt(baseTime),
		session.WithSessionUpdatedAt(baseTime),
	)
	beforeEvent := storeTestEvent("before", "before-request", baseTime)
	before.Events = []event.Event{*beforeEvent}
	beforeTrack := session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"phase":"before"}`),
		Timestamp: baseTime,
	}
	before.Tracks = map[session.Track]*session.TrackEvents{
		"trace": {Track: "trace", Events: []session.TrackEvent{beforeTrack}},
	}
	before.Summaries = map[string]*session.Summary{
		"default": {Summary: "before", UpdatedAt: baseTime},
	}
	boundary, err := sessionrevision.NewBoundary(before)
	require.NoError(t, err)
	record := &sessionrevision.PersistedRecord{
		Generation: 2,
		Head:       9,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "latest-request", InvocationID: "latest-invocation",
			Boundary: boundary, Terminal: true,
		},
	}
	currentState := stateEnvelope{
		ID: key.SessionID,
		State: session.StateMap{
			"phase": []byte(`"after"`),
		},
		CreatedAt: baseTime,
		UpdatedAt: baseTime.Add(time.Minute),
	}
	stateRaw, err := sessionrevision.EncodeState(currentState, record)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO states (
  app_name, user_id, session_id, state, created_at, updated_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, stateRaw,
		currentState.CreatedAt, currentState.UpdatedAt, expiresAt,
	)
	require.NoError(t, err)

	latestEvent := storeTestEvent(
		"latest", "latest-request", baseTime.Add(time.Minute),
	)
	for i, evt := range []*event.Event{beforeEvent, latestEvent} {
		raw, err := json.Marshal(evt)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, ?, ?)`,
			key.AppName, key.UserID, key.SessionID, raw,
			baseTime.Add(time.Duration(i)*time.Minute),
		)
		require.NoError(t, err)
	}
	latestTrack := session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"phase":"after"}`),
		Timestamp: baseTime.Add(time.Minute),
	}
	for _, trackEvent := range []session.TrackEvent{beforeTrack, latestTrack} {
		raw, err := json.Marshal(trackEvent)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
INSERT INTO tracks (app_name, user_id, session_id, track, event, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			key.AppName, key.UserID, key.SessionID,
			trackEvent.Track, raw, trackEvent.Timestamp,
		)
		require.NoError(t, err)
	}
	currentSummary := &session.Summary{
		Summary: "after", UpdatedAt: baseTime.Add(time.Minute),
	}
	currentSummaryRaw, err := json.Marshal(currentSummary)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO summaries (app_name, user_id, session_id, filter_key, summary, updated_at)
VALUES (?, ?, ?, 'default', ?, ?)`,
		key.AppName, key.UserID, key.SessionID,
		currentSummaryRaw, currentSummary.UpdatedAt,
	)
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result, err := store.Rewind(
		ctx,
		tx,
		sessionrevision.RewindRequest{
			Key: key, TargetRequestID: "latest-request", ExpectedHeadRequestID: "latest-request",
			IdempotencyKey: "replacement-request",
		},
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.True(t, result.Applied)
	require.Len(t, result.ActiveSession.Events, 1)
	assert.Equal(t, "before", result.ActiveSession.Events[0].ID)
	assert.Equal(t, []byte(`"before"`), result.ActiveSession.SnapshotState()["phase"])
	assert.Equal(t, "before", result.ActiveSession.Summaries["default"].Summary)
	assert.Equal(t, 1, storeTestRowCount(t, db, "events", "deleted_at IS NULL"))
	assert.Equal(t, 1, storeTestRowCount(t, db, "tracks", "deleted_at IS NULL"))
	assert.Equal(t, 1, storeTestRowCount(t, db, "summaries", "deleted_at IS NULL"))

	persisted, err := store.Read(ctx, db, key, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), persisted.Generation)
	assert.Nil(t, persisted.Checkpoint)
	assert.True(t, sessionrevision.ProjectionInitialized(persisted))

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	replay, err := store.Rewind(
		ctx,
		tx,
		sessionrevision.RewindRequest{
			Key: key, TargetRequestID: "latest-request", ExpectedHeadRequestID: "latest-request",
			IdempotencyKey: "replacement-request",
		},
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.False(t, replay.Applied)
	replayGeneration, ok := sessionrevision.Generation(replay.ActiveSession)
	require.True(t, ok)
	assert.Equal(t, uint64(3), replayGeneration)
	_, replayExpiresAt, err := store.LoadActive(ctx, db, key, false)
	require.NoError(t, err)
	require.NotNil(t, replayExpiresAt)
	assert.Equal(t, expiresAt, *replayExpiresAt)

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = store.Rewind(
		ctx,
		tx,
		sessionrevision.RewindRequest{
			Key: key, TargetRequestID: "different-request", ExpectedHeadRequestID: "different-request",
			IdempotencyKey: "replacement-request",
		},
	)
	assert.ErrorIs(t, err, sessionrevision.ErrRewindConflict)
	require.NoError(t, tx.Rollback())
}

func TestStoreLoadActiveRejectsCorruptProjectionRows(t *testing.T) {
	ctx := context.Background()
	db := openStoreTestDB(t)
	store := testStore()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "corrupt"}

	generation, err := store.Generation(ctx, db, key)
	require.NoError(t, err)
	assert.Zero(t, generation)
	generations, err := store.Generations(ctx, db, nil)
	require.NoError(t, err)
	assert.Empty(t, generations)
	_, _, err = store.LoadActive(ctx, db, key, false)
	assert.ErrorIs(t, err, sessionrevision.ErrRewindUnavailable)

	now := time.Now().UTC().Truncate(time.Second)
	_, err = db.ExecContext(ctx, `
INSERT INTO states (app_name, user_id, session_id, state, created_at, updated_at)
VALUES (?, ?, ?, '{', ?, ?)`, key.AppName, key.UserID, key.SessionID, now, now)
	require.NoError(t, err)
	_, err = store.Read(ctx, db, key, false)
	require.Error(t, err)
	_, _, err = store.LoadActive(ctx, db, key, false)
	require.Error(t, err)

	stateRaw, err := sessionrevision.EncodeState(stateEnvelope{
		ID: key.SessionID, State: session.StateMap{},
		CreatedAt: now, UpdatedAt: now,
	}, &sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE states SET state = ? WHERE session_id = ?`,
		stateRaw, key.SessionID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, 'not-json', ?)`, key.AppName, key.UserID, key.SessionID, now)
	require.NoError(t, err)
	_, _, err = store.LoadActive(ctx, db, key, false)
	require.Error(t, err)

	eventRaw, err := json.Marshal(storeTestEvent("event", "request", now))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE events SET event = ?`, eventRaw)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO tracks (app_name, user_id, session_id, track, event, created_at)
VALUES (?, ?, ?, 'trace', 'not-json', ?)`, key.AppName, key.UserID, key.SessionID, now)
	require.NoError(t, err)
	_, _, err = store.LoadActive(ctx, db, key, false)
	require.Error(t, err)

	trackRaw, err := json.Marshal(&session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"valid":true}`), Timestamp: now,
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE tracks SET event = ?`, trackRaw)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO summaries (
  app_name, user_id, session_id, filter_key, summary, updated_at
) VALUES (?, ?, ?, 'default', 'not-json', ?)`,
		key.AppName, key.UserID, key.SessionID, now)
	require.NoError(t, err)
	_, _, err = store.LoadActive(ctx, db, key, false)
	require.Error(t, err)

	summaryRaw, err := json.Marshal(&session.Summary{Summary: "valid", UpdatedAt: now})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE summaries SET summary = ?`, summaryRaw)
	require.NoError(t, err)
	active, expiresAt, err := store.LoadActive(ctx, db, key, true)
	require.NoError(t, err)
	assert.Nil(t, expiresAt)
	require.Len(t, active.Events, 1)
	require.Len(t, active.Tracks["trace"].Events, 1)
	assert.Equal(t, "valid", active.Summaries["default"].Summary)

	withoutTracks := store
	withoutTracks.Tables.Tracks = ""
	active, _, err = withoutTracks.LoadActive(ctx, db, key, false)
	require.NoError(t, err)
	assert.Empty(t, active.Tracks)
}

func TestStoreRewindGuards(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "guard"}

	t.Run("generation exhausted", func(t *testing.T) {
		db := openStoreTestDB(t)
		store := testStore()
		boundary, err := sessionrevision.NewBoundary(
			session.NewSession(key.AppName, key.UserID, key.SessionID),
		)
		require.NoError(t, err)
		insertStoreTestState(t, db, key, now, &sessionrevision.PersistedRecord{
			Generation: math.MaxInt64,
			Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "latest", Boundary: boundary,
			},
		})
		insertStoreTestEvent(t, db, key, storeTestEvent("latest", "latest", now))
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = store.Rewind(ctx, tx,
			sessionrevision.RewindRequest{
				Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest", IdempotencyKey: "replace",
			})
		assert.ErrorIs(t, err, sessionrevision.ErrRewindUnavailable)
		require.NoError(t, tx.Rollback())
	})

	t.Run("invalid boundary", func(t *testing.T) {
		db := openStoreTestDB(t)
		store := testStore()
		insertStoreTestState(t, db, key, now, &sessionrevision.PersistedRecord{
			Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "latest", Boundary: []byte("not-json"),
			},
		})
		insertStoreTestEvent(t, db, key, storeTestEvent("latest", "latest", now))
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = store.Rewind(ctx, tx,
			sessionrevision.RewindRequest{
				Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest", IdempotencyKey: "replace",
			})
		require.ErrorContains(t, err, "restore rewind boundary")
		require.NoError(t, tx.Rollback())
	})

	t.Run("expired session", func(t *testing.T) {
		db := openStoreTestDB(t)
		store := testStore()
		boundary, err := sessionrevision.NewBoundary(
			session.NewSession(key.AppName, key.UserID, key.SessionID),
		)
		require.NoError(t, err)
		insertStoreTestState(t, db, key, now, &sessionrevision.PersistedRecord{
			Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "latest", Boundary: boundary,
			},
		})
		_, err = db.ExecContext(
			ctx,
			`UPDATE states SET expires_at = ? WHERE session_id = ?`,
			now.Add(-time.Second),
			key.SessionID,
		)
		require.NoError(t, err)
		insertStoreTestEvent(t, db, key, storeTestEvent("latest", "latest", now))
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = store.Rewind(ctx, tx, sessionrevision.RewindRequest{
			Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replace",
		})
		assert.ErrorIs(t, err, sessionrevision.ErrRewindUnavailable)
		require.NoError(t, tx.Rollback())
	})
}

func TestStoreTrimPrefixGuards(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "prefix"}

	t.Run("event tail required", func(t *testing.T) {
		db := openStoreTestDB(t)
		store := testStore()
		insertStoreTestEvent(t, db, key, storeTestEvent("event", "request", now))
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		err = store.trimEventTail(ctx, tx, key, 1, time.Now().UTC())
		assert.ErrorIs(t, err, sessionrevision.ErrRewindUnavailable)
		require.NoError(t, tx.Rollback())
	})

	for _, test := range []struct {
		name     string
		restored map[session.Track]*session.TrackEvents
	}{
		{
			name: "prefix longer than active",
			restored: map[session.Track]*session.TrackEvents{"trace": {
				Track: "trace",
				Events: []session.TrackEvent{
					{Track: "trace", Payload: json.RawMessage(`"request"`), Timestamp: now},
					{Track: "trace", Payload: json.RawMessage(`"other"`), Timestamp: now.Add(time.Second)},
				},
			}},
		},
		{
			name: "prefix differs",
			restored: map[session.Track]*session.TrackEvents{"trace": {
				Track: "trace", Events: []session.TrackEvent{{
					Track: "trace", Payload: json.RawMessage(`"different"`), Timestamp: now,
				}},
			}},
		},
		{
			name: "prefix track missing",
			restored: map[session.Track]*session.TrackEvents{"missing": {
				Track: "missing", Events: []session.TrackEvent{{
					Track: "missing", Payload: json.RawMessage(`"request"`), Timestamp: now,
				}},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openStoreTestDB(t)
			store := testStore()
			trackEvent := &session.TrackEvent{
				Track: "trace", Payload: json.RawMessage(`"request"`), Timestamp: now,
			}
			insertStoreTestTrack(t, db, key, trackEvent)
			restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
			restored.Tracks = test.restored
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			err = store.trimTrackTails(ctx, tx, key, restored, time.Now().UTC())
			assert.ErrorIs(t, err, sessionrevision.ErrRewindUnavailable)
			require.NoError(t, tx.Rollback())
		})
	}

	db := openStoreTestDB(t)
	store := testStore()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.removeTailRows(ctx, tx, store.Tables.Events, key, nil))
	require.NoError(t, tx.Rollback())
}

func insertStoreTestState(
	t *testing.T,
	db *sql.DB,
	key session.Key,
	timestamp time.Time,
	record *sessionrevision.PersistedRecord,
) {
	t.Helper()
	raw, err := sessionrevision.EncodeState(stateEnvelope{
		ID: key.SessionID, State: session.StateMap{},
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}, record)
	require.NoError(t, err)
	_, err = db.Exec(`
INSERT INTO states (app_name, user_id, session_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID, raw, timestamp, timestamp,
	)
	require.NoError(t, err)
}

func insertStoreTestEvent(
	t *testing.T,
	db *sql.DB,
	key session.Key,
	evt *event.Event,
) {
	t.Helper()
	raw, err := json.Marshal(evt)
	require.NoError(t, err)
	_, err = db.Exec(`
INSERT INTO events (app_name, user_id, session_id, event, created_at)
VALUES (?, ?, ?, ?, ?)`, key.AppName, key.UserID, key.SessionID, raw, evt.Timestamp)
	require.NoError(t, err)
}

func insertStoreTestTrack(
	t *testing.T,
	db *sql.DB,
	key session.Key,
	trackEvent *session.TrackEvent,
) {
	t.Helper()
	raw, err := json.Marshal(trackEvent)
	require.NoError(t, err)
	_, err = db.Exec(`
INSERT INTO tracks (app_name, user_id, session_id, track, event, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		key.AppName, key.UserID, key.SessionID,
		trackEvent.Track, raw, trackEvent.Timestamp,
	)
	require.NoError(t, err)
}

func storeTestEvent(
	id string,
	requestID string,
	timestamp time.Time,
) *event.Event {
	return &event.Event{
		ID: id, RequestID: requestID, InvocationID: requestID + "-invocation",
		Timestamp: timestamp,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: id},
		}}},
	}
}

func testStore() Store {
	return Store{
		Dialect: MySQL,
		Tables: Tables{
			States: "states", Events: "events",
			Tracks: "tracks", Summaries: "summaries",
		},
	}
}

func openStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(storeTestDriverName, ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for _, statement := range []string{
		`CREATE TABLE states (
  app_name TEXT, user_id TEXT, session_id TEXT, state BLOB,
  created_at DATETIME, updated_at DATETIME, expires_at DATETIME,
  deleted_at DATETIME
)`,
		`CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT, user_id TEXT, session_id TEXT, event BLOB,
  created_at DATETIME, expires_at DATETIME, deleted_at DATETIME
)`,
		`CREATE TABLE tracks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT, user_id TEXT, session_id TEXT, track TEXT, event BLOB,
  created_at DATETIME, expires_at DATETIME, deleted_at DATETIME
)`,
		`CREATE TABLE summaries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT, user_id TEXT, session_id TEXT, filter_key TEXT,
  summary BLOB, updated_at DATETIME, expires_at DATETIME,
  deleted_at DATETIME
)`,
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	return db
}

func storeTestRowCount(
	t *testing.T,
	db *sql.DB,
	table string,
	predicate string,
) int {
	t.Helper()
	var count int
	// #nosec G201 -- table and predicate are test constants.
	err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + predicate).Scan(&count)
	require.NoError(t, err)
	return count
}
