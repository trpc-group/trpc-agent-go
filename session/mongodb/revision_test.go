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

	result, err := s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{
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
	result, err := s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{
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
	_, err = s.ReplaceLatestTurn(context.Background(), session.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "old-request", IdempotencyKey: "another-request",
	})
	assert.ErrorIs(t, err, session.ErrLatestTurnReplacementUnavailable)
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
