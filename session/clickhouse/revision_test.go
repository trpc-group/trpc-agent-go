//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type revisionFixture struct {
	record    *sessionrevision.PersistedRecord
	active    *session.Session
	expiresAt *time.Time
	state     SessionState
	execs     []string
}

func newRevisionTestService(
	t *testing.T,
	record *sessionrevision.PersistedRecord,
	active *session.Session,
) (*Service, *revisionFixture) {
	t.Helper()
	fixture := &revisionFixture{
		record: record,
		active: active,
		state: SessionState{
			ID: active.ID, State: active.SnapshotState(),
			CreatedAt: active.CreatedAt, UpdatedAt: active.UpdatedAt,
		},
	}
	client := &mockClient{}
	client.queryFunc = func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "FROM session_revisions"):
			recordRaw, err := json.Marshal(fixture.record)
			require.NoError(t, err)
			snapshotRaw, err := sessionrevision.Snapshot(fixture.active)
			require.NoError(t, err)
			return newMockRows([][]any{{string(recordRaw), string(snapshotRaw), fixture.expiresAt}}), nil
		case strings.Contains(query, "toJSONString(state), created_at"):
			stateRaw, err := json.Marshal(fixture.state)
			require.NoError(t, err)
			return newMockRows([][]any{{string(stateRaw), fixture.state.CreatedAt}}), nil
		case strings.Contains(query, "toJSONString(state)"):
			stateRaw, err := json.Marshal(fixture.state)
			require.NoError(t, err)
			return newMockRows([][]any{{string(stateRaw)}}), nil
		default:
			return newMockRows(nil), nil
		}
	}
	client.execFunc = func(_ context.Context, query string, args ...any) error {
		fixture.execs = append(fixture.execs, query)
		if strings.Contains(query, "INSERT INTO session_revisions") {
			recordRaw, ok := args[6].(string)
			require.True(t, ok)
			var nextRecord sessionrevision.PersistedRecord
			require.NoError(t, json.Unmarshal([]byte(recordRaw), &nextRecord))
			fixture.record = &nextRecord

			snapshotRaw, ok := args[7].(string)
			require.True(t, ok)
			nextActive, err := sessionrevision.DecodeSnapshot([]byte(snapshotRaw))
			require.NoError(t, err)
			fixture.active = nextActive
			if args[9] != nil {
				fixture.expiresAt, _ = args[9].(*time.Time)
			}
		}
		return nil
	}
	return &Service{
		opts:                  defaultOptions,
		chClient:              client,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableSessionRevisions: "session_revisions",
		tableRevisionArchives: "session_revision_archives",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}, fixture
}

func revisionResponseEvent(requestID string) *event.Event {
	evt := event.New("invocation", "assistant")
	evt.RequestID = requestID
	evt.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleAssistant, Content: "response"},
	}}}
	return evt
}

func TestRevisionVersion(t *testing.T) {
	version, err := revisionVersion(3, 9)
	require.NoError(t, err)
	assert.Equal(t, uint64(3)<<32|9, version)
	_, err = revisionVersion(math.MaxUint32+1, 0)
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)
	_, err = revisionVersion(0, math.MaxUint32+1)
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)
}

func TestRevisionHeadReadWriteAndVerification(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, fixture := newRevisionTestService(t, &sessionrevision.PersistedRecord{Generation: 2, Head: 4}, active)

	head, ok, err := s.loadRevisionHead(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, uint64(2), head.record.Generation)

	head.record.Head++
	require.NoError(t, s.publishRevisionHead(context.Background(), key, head.record, head.session, nil, nil))
	require.NoError(t, s.verifyPublishedRevision(context.Background(), key, head.record))
	assert.Equal(t, uint64(5), fixture.record.Head)

	missing := *s
	missing.tableSessionRevisions = ""
	_, ok, err = missing.loadRevisionHead(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, ok)

	fixture.record.Generation++
	assert.ErrorIs(t, s.verifyPublishedRevision(context.Background(), key, head.record), sessionrevision.ErrStaleGeneration)
}

func TestRevisionBackedReadsAndWrites(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(session.StateMap{"initial": []byte("value")}),
	)
	active.Events = append(active.Events, *revisionResponseEvent("existing"))
	s, fixture := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	ctx := context.Background()

	loaded, err := s.getSession(ctx, key, 1, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), loaded.State["initial"])
	overlaid, err := s.overlayRevisionHeads(ctx, []*session.Session{active.Clone(), nil}, true, 0, time.Time{})
	require.NoError(t, err)
	assert.Empty(t, overlaid[0].Events)
	assert.Nil(t, overlaid[1])

	turnCtx := sessionrevision.ContextWithTurnStart(ctx, sessionrevision.TurnStart{
		RequestID: "request", InvocationID: "invocation",
	})
	require.NoError(t, s.AppendEvent(turnCtx, active, revisionResponseEvent("request")))
	assert.NotNil(t, fixture.record.Checkpoint)
	require.NoError(t, s.UpdateSessionState(
		ctx, key, session.StateMap{"updated": []byte("yes")},
	))
	require.NoError(t, s.publishSummaryRevision(
		ctx, key, "all", &session.Summary{Summary: "summary", UpdatedAt: time.Now()},
		sessionrevision.Write{},
	))
	require.NoError(t, s.publishSummaryRevision(
		ctx, key, "all", nil, sessionrevision.Write{},
	))
	assert.NotEmpty(t, fixture.execs)
	require.NoError(t, s.flushEventBatch(nil))
	require.NoError(t, s.flushEventBatch([]*sessionEventPair{{
		key: key, event: revisionResponseEvent("batched"),
	}}))
}

func TestReplaceLatestTurnAndReplay(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	before := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(session.StateMap{"before": []byte("turn")}),
	)
	before.Events = append(before.Events, *revisionResponseEvent("prefix"))
	snapshot, err := sessionrevision.Snapshot(before)
	require.NoError(t, err)
	after := before.Clone()
	after.State["after"] = []byte("turn")
	after.Events = append(after.Events, *revisionResponseEvent("old-request"))
	record := &sessionrevision.PersistedRecord{
		Generation: 5,
		Head:       8,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "old-invocation", Snapshot: snapshot,
		},
	}
	s, fixture := newRevisionTestService(t, record, after)
	req := session.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	}

	result, err := s.ReplaceLatestTurn(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.Applied)
	assert.Equal(t, []byte("turn"), result.ActiveSession.State["before"])
	assert.NotContains(t, result.ActiveSession.State, "after")
	assert.Equal(t, uint64(6), fixture.record.Generation)

	replayed, err := s.ReplaceLatestTurn(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, replayed.Applied)
	assert.Equal(t, uint64(6), fixture.record.Generation)

	limited := &sessionrevision.PersistedRecord{
		Generation: math.MaxUint32,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "old-invocation", Snapshot: snapshot,
		},
	}
	s, _ = newRevisionTestService(t, limited, after)
	_, err = s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "another-request",
	})
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)
}

func TestDeleteRevisionHead(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, fixture := newRevisionTestService(t, &sessionrevision.PersistedRecord{Head: 1}, active)
	require.NoError(t, s.deleteRevisionHead(context.Background(), key))
	assert.Equal(t, uint64(2), fixture.record.Head)
	assert.True(t, strings.Contains(fixture.execs[len(fixture.execs)-1], "ALTER TABLE"))

	fixture.record.Head = math.MaxUint32
	assert.ErrorIs(t, s.deleteRevisionHead(context.Background(), key), session.ErrLatestTurnReplacementUnavailable)
}

func TestFlushRevisionPersistenceErrorsAndBarrier(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{opts: ServiceOpts{enableAsyncPersist: true}}
	assert.ErrorContains(t, s.flushRevisionPersistence(context.Background(), key), "not initialized")

	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	go func() {
		pair := <-s.eventPairChans[0]
		pair.done <- nil
	}()
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, s.flushRevisionPersistence(ctx, key), context.Canceled)
}

func TestLoadRevisionHeadErrors(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{
		chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return nil, errors.New("query failed")
		}},
		tableSessionRevisions: "session_revisions",
	}
	_, _, err := s.loadRevisionHead(context.Background(), key)
	assert.ErrorContains(t, err, "load session revision")

	s.chClient = &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{", "{}", (*time.Time)(nil)}}), nil
	}}
	_, _, err = s.loadRevisionHead(context.Background(), key)
	assert.ErrorContains(t, err, "decode session revision")
}

func TestRevisionCleanupPaths(t *testing.T) {
	client := &mockClient{}
	var execs int
	client.execFunc = func(context.Context, string, ...any) error {
		execs++
		return nil
	}
	s := &Service{
		opts: ServiceOpts{
			sessionTTL: time.Hour, userStateTTL: time.Hour,
			deletedRetention: time.Hour,
		},
		chClient: client, tableSessionStates: "session_states",
		tableSessionEvents: "session_events", tableSessionSummaries: "session_summaries",
		tableSessionRevisions: "session_revisions", tableRevisionArchives: "session_revision_archives",
		tableAppStates: "app_states", tableUserStates: "user_states",
	}
	s.cleanupExpiredData(context.Background())
	s.cleanupDeletedData(context.Background(), time.Now())
	assert.GreaterOrEqual(t, execs, 8)
}
