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

	s.opts.enableAsyncPersist = false
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))
	s.opts.enableAsyncPersist = true

	waitCtx, cancelWait := context.WithCancel(context.Background())
	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	go func() {
		<-s.eventPairChans[0]
		cancelWait()
	}()
	assert.ErrorIs(t, s.flushRevisionPersistence(waitCtx, key), context.Canceled)
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

func TestRevisionHeadFailurePaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	t.Run("scan", func(t *testing.T) {
		s := &Service{
			chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
				return &mockRows{data: [][]any{{"record"}}, current: -1, scanFunc: func(...any) error {
					return errors.New("scan failed")
				}}, nil
			}},
			tableSessionRevisions: "session_revisions",
		}
		_, _, err := s.loadRevisionHead(context.Background(), key)
		assert.ErrorContains(t, err, "scan failed")
	})

	t.Run("snapshot", func(t *testing.T) {
		s := &Service{
			chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
				return newMockRows([][]any{{`{}`, `{`, (*time.Time)(nil)}}), nil
			}},
			tableSessionRevisions: "session_revisions",
		}
		_, _, err := s.loadRevisionHead(context.Background(), key)
		assert.ErrorContains(t, err, "decode session revision snapshot")
	})

	t.Run("publish version", func(t *testing.T) {
		s := &Service{chClient: &mockClient{}, tableSessionRevisions: "session_revisions"}
		err := s.publishRevisionHead(context.Background(), key,
			&sessionrevision.PersistedRecord{Generation: math.MaxUint32 + 1},
			session.NewSession(key.AppName, key.UserID, key.SessionID), nil, nil)
		assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("publish exec", func(t *testing.T) {
		s := &Service{
			chClient: &mockClient{execFunc: func(context.Context, string, ...any) error {
				return errors.New("exec failed")
			}},
			tableSessionRevisions: "session_revisions",
		}
		err := s.publishRevisionHead(context.Background(), key,
			&sessionrevision.PersistedRecord{},
			session.NewSession(key.AppName, key.UserID, key.SessionID), nil, nil)
		assert.ErrorContains(t, err, "publish session revision")
	})

	t.Run("publish snapshot", func(t *testing.T) {
		s := &Service{chClient: &mockClient{}, tableSessionRevisions: "session_revisions"}
		err := s.publishRevisionHead(
			context.Background(), key, &sessionrevision.PersistedRecord{}, nil, nil, nil,
		)
		assert.ErrorIs(t, err, session.ErrNilSession)
	})

	t.Run("verify load", func(t *testing.T) {
		s := &Service{
			chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
				return nil, errors.New("verify failed")
			}},
			tableSessionRevisions: "session_revisions",
		}
		err := s.verifyPublishedRevision(
			context.Background(), key, &sessionrevision.PersistedRecord{},
		)
		assert.ErrorContains(t, err, "verify failed")
	})
}

func TestRevisionWriteConflicts(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, _ := newRevisionTestService(t,
		&sessionrevision.PersistedRecord{Generation: 2}, active)
	write := sessionrevision.Write{HasExpectedGeneration: true, ExpectedGeneration: 1}

	assert.ErrorIs(t, s.addEventWithRevision(
		context.Background(), key, revisionResponseEvent("request"), write,
	), sessionrevision.ErrStaleGeneration)
	staleCtx := sessionrevision.ContextWithGeneration(context.Background(), 1)
	assert.ErrorIs(t, s.UpdateSessionState(
		staleCtx, key, session.StateMap{"key": []byte("value")},
	), sessionrevision.ErrStaleGeneration)
	assert.ErrorIs(t, s.publishSummaryRevision(
		context.Background(), key, "all", &session.Summary{}, write,
	), sessionrevision.ErrStaleGeneration)
}

func TestReplaceLatestTurnFailurePaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	req := session.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)

	s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	_, err := s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{})
	assert.Error(t, err)
	_, err = s.ReplaceLatestTurn(context.Background(), req)
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)

	s.tableRevisionArchives = ""
	_, err = s.ReplaceLatestTurn(context.Background(), req)
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnsupported)

	snapshot, err := sessionrevision.Snapshot(active)
	require.NoError(t, err)
	record := &sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
		RequestID: "old-request", InvocationID: "invocation", Snapshot: snapshot,
	}}
	s, _ = newRevisionTestService(t, record, active)
	client := s.chClient.(*mockClient)
	client.execFunc = func(context.Context, string, ...any) error {
		return errors.New("archive failed")
	}
	_, err = s.ReplaceLatestTurn(context.Background(), req)
	assert.ErrorContains(t, err, "archive discarded revision")

	t.Run("load head", func(t *testing.T) {
		s := &Service{
			chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
				return nil, errors.New("head failed")
			}},
			tableSessionRevisions: "session_revisions",
			tableRevisionArchives: "session_revision_archives",
		}
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "head failed")
	})

	t.Run("replay conflict", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{
			Replays: map[string]sessionrevision.PersistedReplay{
				"new-request": {RequestID: "different"},
			},
		}
		s, _ := newRevisionTestService(t, record, active)
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorIs(t, err, session.ErrLatestTurnReplacementConflict)
	})

	t.Run("checkpoint decode", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{
			Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "old-request", Snapshot: []byte("{"),
			},
		}
		s, _ := newRevisionTestService(t, record, active)
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "decode latest-turn checkpoint")
	})

	t.Run("publish replacement", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{
			Checkpoint: &sessionrevision.PersistedCheckpoint{
				RequestID: "old-request", Snapshot: snapshot,
			},
		}
		s, _ := newRevisionTestService(t, record, active)
		client := s.chClient.(*mockClient)
		baseExec := client.execFunc
		client.execFunc = func(ctx context.Context, query string, args ...any) error {
			if strings.Contains(query, "INSERT INTO session_revisions") {
				return errors.New("publish failed")
			}
			return baseExec(ctx, query, args...)
		}
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "publish failed")
	})
}

func TestRevisionScopedStateFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)

	t.Run("app state", func(t *testing.T) {
		s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
		client := s.chClient.(*mockClient)
		baseQuery := client.queryFunc
		client.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			if strings.Contains(query, "FROM app_states") {
				return nil, errors.New("app state failed")
			}
			return baseQuery(ctx, query, args...)
		}
		_, err := s.getSession(context.Background(), key, 0, time.Time{})
		assert.ErrorContains(t, err, "app state failed")
		_, err = s.replacementResultWithScopedState(context.Background(), key, active)
		assert.ErrorContains(t, err, "app state failed")
		_, err = s.overlayRevisionHeads(context.Background(), []*session.Session{active.Clone()}, false, 0, time.Time{})
		assert.ErrorContains(t, err, "app state failed")
	})

	t.Run("user state", func(t *testing.T) {
		s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
		client := s.chClient.(*mockClient)
		baseQuery := client.queryFunc
		client.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			if strings.Contains(query, "FROM user_states") {
				return nil, errors.New("user state failed")
			}
			return baseQuery(ctx, query, args...)
		}
		_, err := s.getSession(context.Background(), key, 0, time.Time{})
		assert.ErrorContains(t, err, "user state failed")
		_, err = s.replacementResultWithScopedState(context.Background(), key, active)
		assert.ErrorContains(t, err, "user state failed")
		_, err = s.overlayRevisionHeads(context.Background(), []*session.Session{active.Clone()}, false, 0, time.Time{})
		assert.ErrorContains(t, err, "user state failed")
	})
}

func TestRevisionOperationFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)

	s := &Service{
		chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return nil, errors.New("head failed")
		}},
		tableSessionRevisions: "session_revisions",
	}
	assert.ErrorContains(t, s.addEventWithRevision(
		context.Background(), key, revisionResponseEvent("request"), sessionrevision.Write{},
	), "head failed")
	assert.ErrorContains(t, s.updateSessionStateWithRevision(
		context.Background(), key, session.StateMap{"key": []byte("value")},
	), "head failed")
	assert.ErrorContains(t, s.publishSummaryRevision(
		context.Background(), key, "all", &session.Summary{}, sessionrevision.Write{},
	), "head failed")
	_, err := s.getSession(context.Background(), key, 0, time.Time{})
	assert.ErrorContains(t, err, "head failed")
	_, err = s.overlayRevisionHeads(
		context.Background(), []*session.Session{active}, false, 0, time.Time{},
	)
	assert.ErrorContains(t, err, "head failed")

	s, _ = newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	client := s.chClient.(*mockClient)
	client.execFunc = func(context.Context, string, ...any) error {
		return errors.New("publish failed")
	}
	assert.ErrorContains(t, s.publishSummaryRevision(
		context.Background(), key, "all", &session.Summary{}, sessionrevision.Write{},
	), "publish failed")
	assert.ErrorContains(t, s.UpdateSessionState(
		context.Background(), key, session.StateMap{"key": []byte("value")},
	), "publish failed")

	s.opts.enableAsyncPersist = true
	_, err = s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	})
	assert.ErrorContains(t, err, "not initialized")
}

func TestDeleteRevisionHeadFailurePaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{
		chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return newMockRows(nil), nil
		}},
		tableSessionRevisions: "session_revisions",
	}
	require.NoError(t, s.deleteRevisionHead(context.Background(), key))

	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, _ = newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	client := s.chClient.(*mockClient)
	baseExec := client.execFunc
	client.execFunc = func(ctx context.Context, query string, args ...any) error {
		if strings.Contains(query, "ALTER TABLE") {
			return errors.New("delete failed")
		}
		return baseExec(ctx, query, args...)
	}
	assert.ErrorContains(t, s.deleteRevisionHead(context.Background(), key), "delete session revision archives")

	s, _ = newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	client = s.chClient.(*mockClient)
	client.execFunc = func(context.Context, string, ...any) error {
		return errors.New("publish failed")
	}
	assert.ErrorContains(t, s.deleteRevisionHead(context.Background(), key), "publish failed")
}

func TestAuthoritativeRevisionLegacyFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{
		chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return newMockRows(nil), nil
		}},
		tableSessionRevisions: "session_revisions", tableSessionStates: "session_states",
		tableSessionEvents: "session_events", tableSessionSummaries: "session_summaries",
	}
	_, err := s.authoritativeRevisionSession(context.Background(), key)
	assert.ErrorContains(t, err, "session not found")

	unchanged := session.NewSession(key.AppName, key.UserID, key.SessionID)
	result, err := s.overlayRevisionHeads(
		context.Background(), []*session.Session{unchanged}, true, 0, time.Time{},
	)
	require.NoError(t, err)
	assert.Same(t, unchanged, result[0])

	s.chClient = &mockClient{queryFunc: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM session_revisions") {
			return newMockRows(nil), nil
		}
		return nil, errors.New("legacy failed")
	}}
	_, err = s.authoritativeRevisionSession(context.Background(), key)
	assert.ErrorContains(t, err, "legacy failed")
}

func TestRevisionPublishFailurePaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)

	t.Run("event legacy write", func(t *testing.T) {
		s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
		client := s.chClient.(*mockClient)
		client.execFunc = func(context.Context, string, ...any) error {
			return errors.New("event failed")
		}
		assert.ErrorContains(t, s.addEventWithRevision(
			context.Background(), key, revisionResponseEvent("request"), sessionrevision.Write{},
		), "event failed")
	})

	t.Run("event revision", func(t *testing.T) {
		s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
		client := s.chClient.(*mockClient)
		baseExec := client.execFunc
		client.execFunc = func(ctx context.Context, query string, args ...any) error {
			if strings.Contains(query, "INSERT INTO session_revisions") {
				return errors.New("revision failed")
			}
			return baseExec(ctx, query, args...)
		}
		assert.ErrorContains(t, s.addEventWithRevision(
			context.Background(), key, revisionResponseEvent("request"), sessionrevision.Write{},
		), "revision failed")
	})

	t.Run("state revision", func(t *testing.T) {
		s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
		client := s.chClient.(*mockClient)
		baseExec := client.execFunc
		client.execFunc = func(ctx context.Context, query string, args ...any) error {
			if strings.Contains(query, "INSERT INTO session_revisions") {
				return errors.New("revision failed")
			}
			return baseExec(ctx, query, args...)
		}
		assert.ErrorContains(t, s.UpdateSessionState(
			context.Background(), key, session.StateMap{"key": []byte("value")},
		), "revision failed")
	})
}

func TestRevisionBackedServiceLifecycle(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)

	created, err := s.CreateSession(
		context.Background(), key, session.StateMap{"created": []byte("yes")},
	)
	require.NoError(t, err)
	generation, ok := sessionrevision.Generation(created)
	assert.True(t, ok)
	assert.Zero(t, generation)

	client := s.chClient.(*mockClient)
	baseQuery := client.queryFunc
	client.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "SELECT session_id, toJSONString(state)") {
			stateRaw, marshalErr := json.Marshal(SessionState{
				ID: key.SessionID, State: session.StateMap{"listed": []byte("yes")},
				CreatedAt: active.CreatedAt, UpdatedAt: active.UpdatedAt,
			})
			require.NoError(t, marshalErr)
			return newMockRows([][]any{{key.SessionID, string(stateRaw), active.CreatedAt, active.UpdatedAt}}), nil
		}
		return baseQuery(ctx, query, args...)
	}
	sessions, err := s.ListSessions(context.Background(), session.UserKey{
		AppName: key.AppName, UserID: key.UserID,
	})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, []byte("yes"), sessions[0].State["created"])

	require.NoError(t, s.DeleteSession(context.Background(), key))
}

func TestRevisionAsyncWorkerBarrier(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	s, _ := newRevisionTestService(t, &sessionrevision.PersistedRecord{}, active)
	s.opts.enableAsyncPersist = true
	s.opts.asyncPersisterNum = 1
	s.opts.batchSize = 2
	s.opts.batchTimeout = time.Hour
	s.startAsyncPersistWorker()
	s.eventPairChans[0] <- &sessionEventPair{
		key: key, event: revisionResponseEvent("request"),
	}
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))
	close(s.eventPairChans[0])
	s.persistWg.Wait()

	s = &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true, asyncPersisterNum: 1,
			batchSize: 2, batchTimeout: time.Hour,
		},
		chClient: &mockClient{queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return nil, errors.New("persist failed")
		}},
		tableSessionRevisions: "session_revisions",
	}
	s.startAsyncPersistWorker()
	s.eventPairChans[0] <- &sessionEventPair{
		key: key, event: revisionResponseEvent("request"),
	}
	assert.ErrorContains(t, s.flushRevisionPersistence(context.Background(), key), "persist failed")
	close(s.eventPairChans[0])
	s.persistWg.Wait()
}
