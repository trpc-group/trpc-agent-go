//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func revisionTestService(t *testing.T, doc sessionStateDoc) (*Service, *mockClient) {
	t.Helper()
	mc := &mockClient{
		findOneFn: func(any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(doc, nil, nil)
		},
		findFn: func(any) (*mongo.Cursor, error) {
			return emptyCursor()
		},
		transactionFn: func(fn func(mongo.SessionContext) error) error {
			return fn(mongo.NewSessionContext(context.Background(), nil))
		},
	}
	s := newServiceForTest(t, mc)
	s.collRevisionArchives = "session_revision_archives"
	return s, mc
}

func TestRevisionEncodingAndGeneration(t *testing.T) {
	record, err := decodeRevision(nil)
	require.NoError(t, err)
	assert.Zero(t, record.Generation)

	record.Generation = 7
	raw, err := encodeRevision(record)
	require.NoError(t, err)
	decoded, err := decodeRevision(raw)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), decoded.Generation)
	_, err = decodeRevision([]byte("{"))
	assert.ErrorContains(t, err, "decode session revision")

	assert.ErrorIs(t, checkRevisionGeneration(record, sessionrevision.Write{
		HasExpectedGeneration: true,
		ExpectedGeneration:    6,
	}), sessionrevision.ErrStaleGeneration)
	assert.NoError(t, checkRevisionGeneration(record, sessionrevision.Write{
		HasExpectedGeneration: true,
		ExpectedGeneration:    7,
	}))

	s := &Service{collRevisionArchives: "archives"}
	sess := session.NewSession("app", "user", "session")
	s.attachRevisionGeneration(sess, sessionStateDoc{Revision: raw})
	generation, ok := sessionrevision.Generation(sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(7), generation)
	s.attachRevisionGeneration(nil, sessionStateDoc{Revision: raw})
}

func TestPrepareRevisionEventMutation(t *testing.T) {
	s := &Service{opts: defaultOptions}
	evt := nonPartialResponseEvent(t)
	evt.StateDelta = session.StateMap{
		"local":                             []byte("session"),
		session.StateAppPrefix + "shared":   []byte("app"),
		session.StateUserPrefix + "private": []byte("user"),
	}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	mutation, err := s.prepareRevisionEventMutation(key, evt, time.Now())
	require.NoError(t, err)
	require.NotNil(t, mutation.eventDoc)
	assert.Equal(t, []byte("session"), mutation.stateSet["state.local"])
	assert.Equal(t, []byte("app"), mutation.appState["shared"])
	assert.Equal(t, []byte("user"), mutation.userState["private"])
	assert.Equal(t, evt.ID, mutation.eventDoc.EventID)

	empty, err := s.prepareRevisionEventMutation(key, nil, time.Now())
	require.NoError(t, err)
	assert.Nil(t, empty.eventDoc)

	invalid := nonPartialResponseEvent(t)
	invalid.Extensions = map[string]json.RawMessage{"invalid": json.RawMessage(`{`)}
	_, err = s.prepareRevisionEventMutation(key, invalid, time.Now())
	assert.ErrorContains(t, err, "marshal event")
}

func TestRevisionMutations(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		State: bson.M{"original": []byte("value")}, CreatedAt: time.Now(),
		UpdatedAt: time.Now(), Revision: recordRaw,
	}
	s, mc := revisionTestService(t, doc)
	ctx := context.Background()

	evt := nonPartialResponseEvent(t)
	mutation, err := s.prepareRevisionEventMutation(key, evt, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		return s.persistRevisionEvent(sc, key, evt, sessionrevision.Write{}, mutation)
	}, nil))
	require.NoError(t, s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		return s.persistRevisionEvent(sc, key, evt, sessionrevision.Write{
			Start: &sessionrevision.TurnStart{
				RequestID: "request", InvocationID: "invocation",
			},
		}, mutation)
	}, nil))

	trackEvent := &session.TrackEvent{
		Track: "trace", RequestID: "request", Payload: json.RawMessage(`{"ok":true}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, s.persistTrackEventWithRevision(
		ctx, key, trackEvent, sessionrevision.Write{},
	))
	require.NoError(t, s.updateSessionStateWithRevision(
		ctx, key, session.StateMap{"updated": []byte("yes")},
	))
	require.NoError(t, s.persistSummaryWithRevision(
		ctx, key, bson.M{"filter_key": "all"}, bson.M{"$set": bson.M{"summary": "text"}},
		sessionrevision.Write{},
	))

	var inserts, updates int
	for _, op := range mc.recorded() {
		switch op.name {
		case "InsertOne":
			inserts++
		case "UpdateOne":
			updates++
		}
	}
	assert.GreaterOrEqual(t, inserts, 2)
	assert.GreaterOrEqual(t, updates, 5)
}

func TestReplaceLatestTurnRestoresCompleteProjection(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	createdAt := time.Now().Add(-time.Hour)
	restored := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(session.StateMap{"before": []byte("turn")}),
		session.WithSessionCreatedAt(createdAt),
		session.WithSessionUpdatedAt(createdAt.Add(time.Minute)),
	)
	evt := nonPartialResponseEvent(t)
	evt.ID = "prefix-event"
	evt.Timestamp = time.Time{}
	restored.Events = append(restored.Events, *evt)
	restored.Tracks = map[session.Track]*session.TrackEvents{
		"trace": {
			Track: "trace",
			Events: []session.TrackEvent{{
				Track: "trace", RequestID: "old-request", Payload: json.RawMessage(`{"step":1}`),
			}},
		},
	}
	restored.Summaries = map[string]*session.Summary{
		"all": {Summary: "before turn", UpdatedAt: createdAt},
	}
	snapshot, err := sessionrevision.Snapshot(restored)
	require.NoError(t, err)
	record := &sessionrevision.PersistedRecord{
		Generation: 3,
		Head:       9,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "old-invocation", Snapshot: snapshot,
		},
	}
	recordRaw, err := encodeRevision(record)
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		State: bson.M{"after": []byte("turn")}, CreatedAt: createdAt,
		UpdatedAt: time.Now(), Revision: recordRaw,
	}
	s, mc := revisionTestService(t, doc)
	trackRaw, err := json.Marshal(restored.Tracks["trace"].Events[0])
	require.NoError(t, err)
	trackDoc := sessionTrackDoc{
		ID: primitive.NewObjectID(), AppName: key.AppName, UserID: key.UserID,
		SessionID: key.SessionID, Track: "trace", Event: trackRaw,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	findCalls := 0
	mc.findFn = func(any) (*mongo.Cursor, error) {
		findCalls++
		if findCalls == 2 || findCalls == 4 {
			return mongo.NewCursorFromDocuments([]any{trackDoc}, nil, nil)
		}
		return emptyCursor()
	}

	result, err := s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Applied)
	assert.Equal(t, []byte("turn"), result.ActiveSession.State["before"])
	assert.Len(t, result.ActiveSession.Events, 1)
	assert.Len(t, result.ActiveSession.Tracks["trace"].Events, 1)
	assert.Equal(t, "before turn", result.ActiveSession.Summaries["all"].Summary)
	generation, ok := sessionrevision.Generation(result.ActiveSession)
	assert.True(t, ok)
	assert.Equal(t, uint64(4), generation)

	var archived bool
	for _, op := range mc.recorded() {
		if op.name == "InsertOne" && op.coll == "session_revision_archives" {
			archived = true
		}
	}
	assert.True(t, archived)
}

func TestLoadActiveRevisionSessionWithCompleteProjection(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	evt := nonPartialResponseEvent(t)
	eventRaw, err := json.Marshal(evt)
	require.NoError(t, err)
	trackEvent := session.TrackEvent{
		Track: "trace", RequestID: "request", Payload: json.RawMessage(`{"step":1}`),
		Timestamp: time.Now(),
	}
	trackRaw, err := json.Marshal(trackEvent)
	require.NoError(t, err)
	summary := session.Summary{Summary: "summary", UpdatedAt: time.Now()}
	summaryRaw, err := json.Marshal(summary)
	require.NoError(t, err)

	findCall := 0
	mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
		findCall++
		switch findCall {
		case 1:
			return docsCursor([]any{sessionEventDoc{Event: eventRaw}})
		case 2:
			return docsCursor([]any{sessionTrackDoc{Track: "trace", Event: trackRaw}})
		default:
			return docsCursor([]any{sessionSummaryDoc{FilterKey: "all", Summary: summaryRaw}})
		}
	}}
	s := newServiceForTest(t, mc)
	doc := sessionStateDoc{
		State: bson.M{"state": []byte("value")}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	active, err := s.loadActiveRevisionSession(context.Background(), key, doc)
	require.NoError(t, err)
	assert.Len(t, active.Events, 1)
	assert.Len(t, active.Tracks["trace"].Events, 1)
	assert.Equal(t, "summary", active.Summaries["all"].Summary)
}

func TestReplaceLatestTurnReplayAndGenerationLimit(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	doc := sessionStateDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID}
	replayRecord := &sessionrevision.PersistedRecord{
		Generation: 4, Head: 8,
		Replays: map[string]sessionrevision.PersistedReplay{
			"new-request": {RequestID: "old-request", Generation: 4, Head: 8},
		},
	}
	var err error
	doc.Revision, err = encodeRevision(replayRecord)
	require.NoError(t, err)
	s, _ := revisionTestService(t, doc)
	result, err := s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	})
	require.NoError(t, err)
	assert.False(t, result.Applied)

	empty := session.NewSession(key.AppName, key.UserID, key.SessionID)
	snapshot, err := sessionrevision.Snapshot(empty)
	require.NoError(t, err)
	limited := &sessionrevision.PersistedRecord{
		Generation: math.MaxUint64,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "invocation", Snapshot: snapshot,
		},
	}
	doc.Revision, err = encodeRevision(limited)
	require.NoError(t, err)
	s, _ = revisionTestService(t, doc)
	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "another-request",
	})
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
}

func TestFlushRevisionPersistence(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	s := &Service{opts: serviceOpts{enableAsyncPersist: true}}
	assert.ErrorContains(t, s.flushRevisionPersistence(context.Background(), key), "not initialized")

	s.persistChans = []chan *persistJob{make(chan *persistJob)}
	go func() {
		job := <-s.persistChans[0]
		job.done <- nil
	}()
	require.NoError(t, s.flushRevisionPersistence(context.Background(), key))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, s.flushRevisionPersistence(ctx, key), context.Canceled)

	ctx, cancel = context.WithCancel(context.Background())
	s.persistChans = []chan *persistJob{make(chan *persistJob)}
	go func() {
		<-s.persistChans[0]
		cancel()
	}()
	assert.ErrorIs(t, s.flushRevisionPersistence(ctx, key), context.Canceled)
}

func TestRevisionMutationErrors(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
	require.NoError(t, err)
	doc := sessionStateDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, Revision: recordRaw}
	s, mc := revisionTestService(t, doc)

	write := sessionrevision.Write{HasExpectedGeneration: true, ExpectedGeneration: 1}
	err = s.persistEventWithRevision(context.Background(), key, nil, write)
	assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)

	mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
		return nil, errors.New("update failed")
	}
	err = s.updateSessionStateWithRevision(context.Background(), key, session.StateMap{"key": []byte("value")})
	assert.ErrorContains(t, err, "update failed")
}

func TestLoadActiveRevisionSessionErrors(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	doc := sessionStateDoc{}

	t.Run("events query", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return nil, errors.New("events failed")
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.ErrorContains(t, err, "load active events")
	})

	t.Run("event decode", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionEventDoc{Event: []byte("{")}})
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.Error(t, err)
	})

	t.Run("event document", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{bson.M{"event": 42}})
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.Error(t, err)
	})

	t.Run("tracks query", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return emptyCursor()
			}
			return nil, errors.New("tracks failed")
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.ErrorContains(t, err, "load active tracks")
	})

	t.Run("summaries query", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls < 3 {
				return emptyCursor()
			}
			return nil, errors.New("summaries failed")
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.ErrorContains(t, err, "load active summaries")
	})

	t.Run("track decode", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return emptyCursor()
			}
			return docsCursor([]any{sessionTrackDoc{Track: "trace", Event: []byte("{")}})
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.Error(t, err)
	})

	t.Run("summary decode", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls < 3 {
				return emptyCursor()
			}
			return docsCursor([]any{sessionSummaryDoc{FilterKey: "all", Summary: []byte("{")}})
		}})
		_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
		assert.Error(t, err)
	})

	for name, failAt := range map[string]int{
		"event cursor": 1, "track cursor": 2, "summary cursor": 3,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
				calls++
				if calls == failAt {
					return mongo.NewCursorFromDocuments(
						nil, errors.New("cursor failed"), nil,
					)
				}
				return emptyCursor()
			}})
			_, err := s.loadActiveRevisionSession(context.Background(), key, doc)
			assert.ErrorContains(t, err, "cursor failed")
		})
	}
}

func TestRestoreRevisionProjectionErrors(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
	record := &sessionrevision.PersistedRecord{}

	t.Run("clear projection", func(t *testing.T) {
		mc := &mockClient{deleteManyFn: func(any) (*mongo.DeleteResult, error) {
			return nil, errors.New("delete failed")
		}}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(context.Background(), key, restored, sessionStateDoc{}, record)
		assert.ErrorContains(t, err, "clear discarded session projection")
	})

	t.Run("missing state", func(t *testing.T) {
		mc := &mockClient{updateOneFn: func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(context.Background(), key, restored, sessionStateDoc{}, record)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("restore event", func(t *testing.T) {
		withEvent := restored.Clone()
		withEvent.Events = append(withEvent.Events, *nonPartialResponseEvent(t))
		mc := &mockClient{insertOneFn: func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("insert failed")
		}}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(context.Background(), key, withEvent, sessionStateDoc{}, record)
		assert.ErrorContains(t, err, "restore event")
	})

	t.Run("event encoding", func(t *testing.T) {
		withEvent := restored.Clone()
		evt := nonPartialResponseEvent(t)
		evt.Extensions = map[string]json.RawMessage{"invalid": {0xff}}
		withEvent.Events = append(withEvent.Events, *evt)
		s := newServiceForTest(t, &mockClient{})
		err := s.restoreRevisionProjection(
			context.Background(), key, withEvent, sessionStateDoc{}, record,
		)
		assert.Error(t, err)
	})

	t.Run("track encoding", func(t *testing.T) {
		withTrack := restored.Clone()
		withTrack.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{{
				Track: "trace", Payload: json.RawMessage{0xff},
			}}},
		}
		s := newServiceForTest(t, &mockClient{})
		err := s.restoreRevisionProjection(
			context.Background(), key, withTrack, sessionStateDoc{}, record,
		)
		assert.Error(t, err)
	})

	t.Run("remove track tail", func(t *testing.T) {
		withTrack := restored.Clone()
		prefix := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":1}`),
		}
		tail := session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{"step":2}`),
		}
		withTrack.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{prefix}},
		}
		prefixRaw, err := json.Marshal(prefix)
		require.NoError(t, err)
		tailRaw, err := json.Marshal(tail)
		require.NoError(t, err)
		mc := &mockClient{
			findFn: func(any) (*mongo.Cursor, error) {
				return mongo.NewCursorFromDocuments([]any{
					sessionTrackDoc{ID: primitive.NewObjectID(), Track: "trace", Event: prefixRaw},
					sessionTrackDoc{ID: primitive.NewObjectID(), Track: "trace", Event: tailRaw},
				}, nil, nil)
			},
		}
		deleteCalls := 0
		mc.deleteManyFn = func(any) (*mongo.DeleteResult, error) {
			deleteCalls++
			if deleteCalls == 3 {
				return nil, errors.New("delete failed")
			}
			return &mongo.DeleteResult{}, nil
		}
		s := newServiceForTest(t, mc)
		err = s.restoreRevisionProjection(
			context.Background(), key, withTrack, sessionStateDoc{}, record,
		)
		assert.ErrorContains(t, err, "remove discarded track tail")
	})

	t.Run("restore summary", func(t *testing.T) {
		withSummary := restored.Clone()
		withSummary.Summaries = map[string]*session.Summary{
			"all": {Summary: "summary", UpdatedAt: time.Now()},
		}
		mc := &mockClient{insertOneFn: func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("insert failed")
		}}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(
			context.Background(), key, withSummary, sessionStateDoc{}, record,
		)
		assert.ErrorContains(t, err, "restore summary")
	})

	t.Run("nil projection entries", func(t *testing.T) {
		withNilEntries := session.NewSession(key.AppName, key.UserID, key.SessionID)
		withNilEntries.Tracks = map[session.Track]*session.TrackEvents{"nil": nil}
		withNilEntries.Summaries = map[string]*session.Summary{"nil": nil}
		s := newServiceForTest(t, &mockClient{})
		require.NoError(t, s.restoreRevisionProjection(
			context.Background(), key, withNilEntries, sessionStateDoc{}, record,
		))
	})
}

func TestRevisionWriteFailurePaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	doc := sessionStateDoc{Revision: recordRaw}
	evt := nonPartialResponseEvent(t)

	t.Run("event encoding", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		invalid := nonPartialResponseEvent(t)
		invalid.Extensions = map[string]json.RawMessage{"invalid": {0xff}}
		err := s.persistEventWithRevision(
			context.Background(), key, invalid, sessionrevision.Write{},
		)
		assert.ErrorContains(t, err, "marshal event")
	})

	t.Run("event scoped state", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("scoped state failed")
		}
		withState := nonPartialResponseEvent(t)
		withState.StateDelta = session.StateMap{
			session.StateAppPrefix + "shared": []byte("value"),
		}
		err := s.persistEventWithRevision(
			context.Background(), key, withState, sessionrevision.Write{},
		)
		assert.ErrorContains(t, err, "scoped state failed")
	})

	t.Run("event state update", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("state update failed")
		}
		err := s.persistEventWithRevision(
			context.Background(), key, evt, sessionrevision.Write{},
		)
		assert.ErrorContains(t, err, "update session state")
	})

	t.Run("event state missing", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}
		err := s.persistEventWithRevision(context.Background(), key, evt, sessionrevision.Write{})
		assert.ErrorIs(t, err, errSessionNotFound)
	})

	t.Run("event insert", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.insertOneFn = func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("insert failed")
		}
		err := s.persistEventWithRevision(context.Background(), key, evt, sessionrevision.Write{})
		assert.ErrorContains(t, err, "insert event")
	})

	t.Run("nil track", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		err := s.persistTrackEventWithRevision(context.Background(), key, nil, sessionrevision.Write{})
		assert.ErrorContains(t, err, "track event is nil")
	})

	t.Run("invalid track", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		err := s.persistTrackEventWithRevision(context.Background(), key, &session.TrackEvent{
			Track: "trace", Payload: json.RawMessage(`{`),
		}, sessionrevision.Write{})
		assert.ErrorContains(t, err, "marshal track event")
	})

	t.Run("transaction", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.transactionFn = func(func(mongo.SessionContext) error) error {
			return errors.New("transaction failed")
		}
		_, err := s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
		})
		assert.ErrorContains(t, err, "replace latest turn: transaction failed")
	})

	t.Run("state missing after update", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}
		err := s.updateSessionStateWithRevision(
			context.Background(), key, session.StateMap{"key": []byte("value")},
		)
		assert.ErrorContains(t, err, "session not found")
	})
}

func TestRevisionDocumentReadFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	trackEvent := &session.TrackEvent{Track: "trace", Payload: json.RawMessage(`{}`)}
	operations := map[string]func(*Service) error{
		"event": func(s *Service) error {
			return s.persistEventWithRevision(context.Background(), key, nil, sessionrevision.Write{})
		},
		"track": func(s *Service) error {
			return s.persistTrackEventWithRevision(context.Background(), key, trackEvent, sessionrevision.Write{})
		},
		"state": func(s *Service) error {
			return s.updateSessionStateWithRevision(context.Background(), key, session.StateMap{"key": []byte("value")})
		},
		"summary": func(s *Service) error {
			return s.persistSummaryWithRevision(context.Background(), key, bson.M{}, bson.M{}, sessionrevision.Write{})
		},
	}
	for name, operation := range operations {
		operation := operation
		t.Run(name+" not found", func(t *testing.T) {
			mc := &mockClient{
				findOneFn: func(any) *mongo.SingleResult {
					return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
				},
				transactionFn: func(fn func(mongo.SessionContext) error) error {
					return fn(mongo.NewSessionContext(context.Background(), nil))
				},
			}
			s := newServiceForTest(t, mc)
			s.collRevisionArchives = "session_revision_archives"
			assert.Error(t, operation(s))
		})
		t.Run(name+" read error", func(t *testing.T) {
			mc := &mockClient{
				findOneFn: func(any) *mongo.SingleResult {
					return mongo.NewSingleResultFromDocument(bson.D{}, errors.New("read failed"), nil)
				},
				transactionFn: func(fn func(mongo.SessionContext) error) error {
					return fn(mongo.NewSessionContext(context.Background(), nil))
				},
			}
			s := newServiceForTest(t, mc)
			s.collRevisionArchives = "session_revision_archives"
			assert.ErrorContains(t, operation(s), "read failed")
		})
		t.Run(name+" invalid revision", func(t *testing.T) {
			doc := sessionStateDoc{Revision: []byte("{")}
			s, _ := revisionTestService(t, doc)
			assert.ErrorContains(t, operation(s), "decode session revision")
		})
	}
}

func TestTrackAndSummaryWriteFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	doc := sessionStateDoc{Revision: recordRaw}
	trackEvent := &session.TrackEvent{Track: "trace", Payload: json.RawMessage(`{}`)}

	t.Run("track state missing", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}
		assert.ErrorIs(t, s.persistTrackEventWithRevision(
			context.Background(), key, trackEvent, sessionrevision.Write{},
		), errSessionNotFound)
	})

	t.Run("track update", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("update failed")
		}
		assert.ErrorContains(t, s.persistTrackEventWithRevision(
			context.Background(), key, trackEvent, sessionrevision.Write{},
		), "update session state")
	})

	t.Run("track insert", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.insertOneFn = func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("insert failed")
		}
		assert.ErrorContains(t, s.persistTrackEventWithRevision(
			context.Background(), key, trackEvent, sessionrevision.Write{},
		), "insert track event")
	})

	t.Run("summary state missing", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}
		assert.ErrorIs(t, s.persistSummaryWithRevision(
			context.Background(), key, bson.M{}, bson.M{}, sessionrevision.Write{},
		), errSessionNotFound)
	})

	t.Run("summary update", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		calls := 0
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("summary failed")
			}
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		}
		assert.ErrorContains(t, s.persistSummaryWithRevision(
			context.Background(), key, bson.M{}, bson.M{}, sessionrevision.Write{},
		), "summary failed")
	})

	t.Run("summary revision update", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("revision failed")
		}
		assert.ErrorContains(t, s.persistSummaryWithRevision(
			context.Background(), key, bson.M{}, bson.M{}, sessionrevision.Write{},
		), "revision failed")
	})

	t.Run("stale writes", func(t *testing.T) {
		staleRaw, encodeErr := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
		require.NoError(t, encodeErr)
		staleDoc := sessionStateDoc{Revision: staleRaw}
		write := sessionrevision.Write{HasExpectedGeneration: true, ExpectedGeneration: 1}
		s, _ := revisionTestService(t, staleDoc)
		assert.ErrorIs(t, s.persistTrackEventWithRevision(
			context.Background(), key, trackEvent, write,
		), sessionrevision.ErrStaleGeneration)
		assert.ErrorIs(t, s.persistSummaryWithRevision(
			context.Background(), key, bson.M{}, bson.M{}, write,
		), sessionrevision.ErrStaleGeneration)
		staleCtx := sessionrevision.ContextWithGeneration(context.Background(), 1)
		assert.ErrorIs(t, s.updateSessionStateWithRevision(
			staleCtx, key, session.StateMap{},
		), sessionrevision.ErrStaleGeneration)
	})
}

func TestPublicRevisionMutationPaths(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		State: bson.M{}, CreatedAt: time.Now(), UpdatedAt: time.Now(), Revision: recordRaw,
	}
	s, _ := revisionTestService(t, doc)
	s.opts.summarizer = &stubSummarizer{text: "summary"}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

	require.NoError(t, s.AppendEvent(context.Background(), sess, nonPartialResponseEvent(t)))
	require.NoError(t, s.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{
		Track: "trace", Payload: json.RawMessage(`{"step":1}`), Timestamp: time.Now(),
	}))
	require.NoError(t, s.UpdateSessionState(
		context.Background(), key, session.StateMap{"updated": []byte("yes")},
	))
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "all", true))
}

func TestReplaceLatestTurnProtocolFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	req := sessionrevision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "new-request",
	}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	snapshot, err := sessionrevision.Snapshot(active)
	require.NoError(t, err)
	checkpoint := &sessionrevision.PersistedCheckpoint{
		RequestID: "old-request", InvocationID: "invocation", Snapshot: snapshot,
	}

	s, _ := revisionTestService(t, sessionStateDoc{})
	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{})
	assert.Error(t, err)
	s.collRevisionArchives = ""
	_, err = s.ReplaceLatestTurn(context.Background(), req)
	assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnsupported)

	t.Run("flush", func(t *testing.T) {
		s, _ := revisionTestService(t, sessionStateDoc{})
		s.opts.enableAsyncPersist = true
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "not initialized")
	})

	for name, findErr := range map[string]error{
		"state missing": mongo.ErrNoDocuments, "state read": errors.New("read failed"),
	} {
		t.Run(name, func(t *testing.T) {
			s, mc := revisionTestService(t, sessionStateDoc{})
			mc.findOneFn = func(any) *mongo.SingleResult {
				return mongo.NewSingleResultFromDocument(bson.D{}, findErr, nil)
			}
			_, err := s.ReplaceLatestTurn(context.Background(), req)
			assert.Error(t, err)
		})
	}

	t.Run("invalid revision", func(t *testing.T) {
		s, _ := revisionTestService(t, sessionStateDoc{Revision: []byte("{")})
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "decode session revision")
	})

	t.Run("active projection", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{Checkpoint: checkpoint}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, mc := revisionTestService(t, sessionStateDoc{Revision: raw})
		mc.findFn = func(any) (*mongo.Cursor, error) {
			return nil, errors.New("projection failed")
		}
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "load active events")
	})

	t.Run("checkpoint unavailable", func(t *testing.T) {
		raw, encodeErr := encodeRevision(&sessionrevision.PersistedRecord{})
		require.NoError(t, encodeErr)
		s, _ := revisionTestService(t, sessionStateDoc{Revision: raw})
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("replay conflict", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{
			Generation: 2, Head: 3,
			Replays: map[string]sessionrevision.PersistedReplay{
				"new-request": {RequestID: "different", Generation: 2, Head: 3},
			},
		}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, _ := revisionTestService(t, sessionStateDoc{Revision: raw})
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementConflict)
	})

	t.Run("checkpoint decode", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "invocation", Snapshot: []byte("{"),
		}}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, _ := revisionTestService(t, sessionStateDoc{Revision: raw})
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "decode latest-turn checkpoint")
	})

	t.Run("archive", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{Checkpoint: checkpoint}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, mc := revisionTestService(t, sessionStateDoc{Revision: raw})
		mc.insertOneFn = func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("archive failed")
		}
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "archive discarded revision")
	})

	t.Run("restore", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{Checkpoint: checkpoint}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, mc := revisionTestService(t, sessionStateDoc{Revision: raw})
		mc.deleteManyFn = func(any) (*mongo.DeleteResult, error) {
			return nil, errors.New("restore failed")
		}
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "restore failed")
	})

	for name, failAt := range map[string]int{"app state": 4, "user state": 5} {
		t.Run(name, func(t *testing.T) {
			record := &sessionrevision.PersistedRecord{
				Generation: 1,
				Replays: map[string]sessionrevision.PersistedReplay{
					"new-request": {RequestID: "old-request", Generation: 1},
				},
			}
			raw, encodeErr := encodeRevision(record)
			require.NoError(t, encodeErr)
			s, mc := revisionTestService(t, sessionStateDoc{Revision: raw})
			calls := 0
			mc.findFn = func(any) (*mongo.Cursor, error) {
				calls++
				if calls == failAt {
					return nil, errors.New("scoped state failed")
				}
				return emptyCursor()
			}
			_, err := s.ReplaceLatestTurn(context.Background(), req)
			assert.ErrorContains(t, err, "scoped state failed")
		})
	}
}
