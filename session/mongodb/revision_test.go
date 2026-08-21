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

	s := &Service{}
	sess := session.NewSession("app", "user", "session")
	require.NoError(t, s.attachRevisionGeneration(sess, sessionStateDoc{Revision: raw}))
	generation, ok := sessionrevision.Generation(sess)
	assert.True(t, ok)
	assert.Equal(t, uint64(7), generation)
	require.NoError(t, s.attachRevisionGeneration(nil, sessionStateDoc{Revision: raw}))
	assert.ErrorContains(t, s.attachRevisionGeneration(
		sess, sessionStateDoc{Revision: []byte("{")},
	), "decode session revision")
}

func TestProjectionReadsFailClosedOnInvalidRevision(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		Revision: []byte("{"), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	t.Run("get", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		_, err := s.GetSession(context.Background(), key)
		assert.ErrorContains(t, err, "decode session revision")
	})

	t.Run("list", func(t *testing.T) {
		findCall := 0
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			findCall++
			if findCall == 1 {
				return mongo.NewCursorFromDocuments([]any{doc}, nil, nil)
			}
			return emptyCursor()
		}}
		s := newServiceForTest(t, mc)
		_, err := s.ListSessions(
			context.Background(),
			session.UserKey{AppName: key.AppName, UserID: key.UserID},
			session.WithListSessionOnlyMeta(),
		)
		assert.ErrorContains(t, err, "decode session revision")
	})
}

func TestGetSessionBracketsReplacedGeneration(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	raw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		Revision: raw, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	findOneCalls := 0
	mc := &mockClient{
		findOneFn: func(any) *mongo.SingleResult {
			findOneCalls++
			return mongo.NewSingleResultFromDocument(doc, nil, nil)
		},
		findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
	}
	s := newServiceForTest(t, mc)

	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	generation, ok := sessionrevision.Generation(got)
	require.True(t, ok)
	assert.Equal(t, uint64(2), generation)
	assert.GreaterOrEqual(t, findOneCalls, 4)
}

func TestGetSessionRereadsProjectionAfterFirstReplacement(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	initial := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	revisionRaw, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 1},
	)
	require.NoError(t, err)
	replaced := initial
	replaced.Revision = revisionRaw
	findOneCalls := 0
	mc := &mockClient{
		findOneFn: func(any) *mongo.SingleResult {
			findOneCalls++
			if findOneCalls == 1 {
				return mongo.NewSingleResultFromDocument(initial, nil, nil)
			}
			return mongo.NewSingleResultFromDocument(replaced, nil, nil)
		},
		findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
	}
	s := newServiceForTest(t, mc)

	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	generation, ok := sessionrevision.Generation(got)
	require.True(t, ok)
	assert.Equal(t, uint64(1), generation)
	assert.GreaterOrEqual(t, findOneCalls, 5)
}

func TestStabilizeListedRevisionProjectionsBatchesUnchangedSessions(t *testing.T) {
	userKey := session.UserKey{AppName: "app", UserID: "user"}
	id1 := primitive.NewObjectID()
	id2 := primitive.NewObjectID()
	raw1, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 3},
	)
	require.NoError(t, err)
	raw2, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 7},
	)
	require.NoError(t, err)
	docs := []sessionStateDoc{
		{DocumentID: id1, SessionID: "s1", Revision: raw1},
		{DocumentID: id2, SessionID: "s2", Revision: raw2},
	}
	mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
		return docsCursor([]any{docs[1], docs[0]})
	}}
	s := newServiceForTest(t, mc)
	listed := []*session.Session{
		session.NewSession(userKey.AppName, userKey.UserID, "s1"),
		session.NewSession(userKey.AppName, userKey.UserID, "s2"),
	}

	got, err := s.stabilizeListedRevisionProjections(
		context.Background(), userKey, listed, docs, false, 0, time.Time{},
	)
	require.NoError(t, err)
	assert.Equal(t, listed, got)
	assert.Len(t, mc.recorded(), 1)
	assert.Equal(t, "Find", mc.recorded()[0].name)
}

func TestStabilizeListedRevisionProjectionsReloadsOnlyChangedSession(t *testing.T) {
	userKey := session.UserKey{AppName: "app", UserID: "user"}
	id1 := primitive.NewObjectID()
	id2 := primitive.NewObjectID()
	raw1, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 3},
	)
	require.NoError(t, err)
	rawBefore, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 6},
	)
	require.NoError(t, err)
	rawAfter, err := encodeRevision(
		&sessionrevision.PersistedRecord{Generation: 7},
	)
	require.NoError(t, err)
	initialDocs := []sessionStateDoc{
		{DocumentID: id1, SessionID: "s1", Revision: raw1},
		{DocumentID: id2, SessionID: "s2", Revision: rawBefore},
	}
	currentDocs := []any{
		sessionStateDoc{DocumentID: id1, SessionID: "s1", Revision: raw1},
		sessionStateDoc{DocumentID: id2, SessionID: "s2", Revision: rawAfter},
	}
	currentS2 := sessionStateDoc{
		DocumentID: id2,
		AppName:    userKey.AppName,
		UserID:     userKey.UserID,
		SessionID:  "s2",
		State:      bson.M{"state": []byte("new")},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Revision:   rawAfter,
	}
	findCalls := 0
	findOneCalls := 0
	mc := &mockClient{
		findFn: func(any) (*mongo.Cursor, error) {
			findCalls++
			if findCalls == 1 {
				return docsCursor(currentDocs)
			}
			return emptyCursor()
		},
		findOneFn: func(any) *mongo.SingleResult {
			findOneCalls++
			return mongo.NewSingleResultFromDocument(currentS2, nil, nil)
		},
	}
	s := newServiceForTest(t, mc)
	listed := []*session.Session{
		session.NewSession(userKey.AppName, userKey.UserID, "s1"),
		session.NewSession(userKey.AppName, userKey.UserID, "s2"),
	}

	got, err := s.stabilizeListedRevisionProjections(
		context.Background(), userKey, listed, initialDocs, false, 0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Same(t, listed[0], got[0])
	assert.Equal(t, []byte("new"), got[1].State["state"])
	generation, ok := sessionrevision.Generation(got[1])
	require.True(t, ok)
	assert.Equal(t, uint64(7), generation)
	assert.Equal(t, 3, findOneCalls)
}

func TestRevisionIdentitiesHandleIncompleteBatch(t *testing.T) {
	userKey := session.UserKey{AppName: "app", UserID: "user"}
	listed := []*session.Session{
		session.NewSession(userKey.AppName, userKey.UserID, "s1"),
		session.NewSession(userKey.AppName, userKey.UserID, "s2"),
	}
	t.Run("missing", func(t *testing.T) {
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionStateDoc{SessionID: "s1"}})
		}}
		s := newServiceForTest(t, mc)
		identities, err := s.revisionIdentities(
			context.Background(), userKey, listed,
		)
		require.NoError(t, err)
		assert.Contains(t, identities, "s1")
		assert.NotContains(t, identities, "s2")
		ops := mc.recorded()
		require.Len(t, ops, 1)
		filter, ok := ops[0].filter.(bson.M)
		require.True(t, ok)
		assert.Contains(t, filter, "$or")
	})

	t.Run("duplicate", func(t *testing.T) {
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{
				sessionStateDoc{SessionID: "s1"},
				sessionStateDoc{SessionID: "s1"},
			})
		}}
		s := newServiceForTest(t, mc)
		_, err := s.revisionIdentities(
			context.Background(), userKey, listed,
		)
		assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
	})
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

func TestTurnStartUsesRollingProjectionAfterBootstrap(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	for _, tc := range []struct {
		name            string
		initialize      bool
		wantCollections []string
	}{
		{
			name: "bootstrap",
			wantCollections: []string{
				"session_events", "session_tracks", "session_summaries",
			},
		},
		{
			name:            "steady state",
			initialize:      true,
			wantCollections: []string{"session_summaries"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &sessionrevision.PersistedRecord{}
			if tc.initialize {
				require.NoError(t, sessionrevision.InitializeProjection(
					record,
					session.NewSession(key.AppName, key.UserID, key.SessionID),
				))
			}
			revisionRaw, err := encodeRevision(record)
			require.NoError(t, err)
			doc := sessionStateDoc{
				AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
				State: bson.M{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				Revision: revisionRaw,
			}
			mc := &mockClient{
				findOneFn: func(any) *mongo.SingleResult {
					return mongo.NewSingleResultFromDocument(doc, nil, nil)
				},
				findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
				transactionFn: func(fn func(mongo.SessionContext) error) error {
					return fn(mongo.NewSessionContext(context.Background(), nil))
				},
			}
			s := newServiceForTest(t, mc)
			evt := nonPartialResponseEvent(t)
			evt.RequestID = "request"
			require.NoError(t, s.persistEventWithRevision(
				context.Background(), key, evt, sessionrevision.Write{
					Start: &sessionrevision.TurnStart{
						RequestID: "request", InvocationID: evt.InvocationID,
					},
				},
			))

			var gotCollections []string
			var persistedRevision []byte
			for _, op := range mc.recorded() {
				if op.name == "Find" {
					gotCollections = append(gotCollections, op.coll)
				}
				if op.name == "UpdateOne" && op.coll == s.collSessionStates {
					set := op.update.(bson.M)["$set"].(bson.M)
					persistedRevision, _ = set["revision"].([]byte)
				}
			}
			assert.Equal(t, tc.wantCollections, gotCollections)
			require.NotEmpty(t, persistedRevision)
			persisted, err := decodeRevision(persistedRevision)
			require.NoError(t, err)
			assert.True(t, sessionrevision.ProjectionInitialized(persisted))
			assert.Equal(t, uint64(1), persisted.Projection.Events.Count)
			require.NotNil(t, persisted.Checkpoint)
			assert.False(t, persisted.Checkpoint.Hazard)
		})
	}
}

func TestTrackWriteAdvancesInitializedProjection(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	record := &sessionrevision.PersistedRecord{}
	require.NoError(t, sessionrevision.InitializeProjection(
		record, session.NewSession(key.AppName, key.UserID, key.SessionID),
	))
	revisionRaw, err := encodeRevision(record)
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		State: bson.M{}, Revision: revisionRaw,
	}
	var persistedRevision []byte
	mc := &mockClient{
		findOneFn: func(any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(doc, nil, nil)
		},
		updateOneFn: func(_, update any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			set := update.(bson.M)["$set"].(bson.M)
			persistedRevision, _ = set["revision"].([]byte)
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		},
		transactionFn: func(fn func(mongo.SessionContext) error) error {
			return fn(mongo.NewSessionContext(context.Background(), nil))
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.persistTrackEventWithRevision(
		context.Background(), key, &session.TrackEvent{
			Track: "trace", RequestID: "request", Payload: json.RawMessage(`{}`),
		}, sessionrevision.Write{},
	))
	require.NotEmpty(t, persistedRevision)
	persisted, err := decodeRevision(persistedRevision)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), persisted.Projection.Tracks["trace"].Count)
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
	boundary, err := sessionrevision.NewBoundary(restored)
	require.NoError(t, err)
	record := &sessionrevision.PersistedRecord{
		Generation: 3,
		Head:       9,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "old-invocation", Boundary: boundary,
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
	prefixRaw, err := json.Marshal(restored.Events[0])
	require.NoError(t, err)
	later := nonPartialResponseEvent(t)
	later.ID = "latest-turn-event"
	laterRaw, err := json.Marshal(later)
	require.NoError(t, err)
	eventDocs := []any{
		sessionEventDoc{
			ID:      primitive.NewObjectID(),
			AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
			EventID: restored.Events[0].ID, Event: prefixRaw,
		},
		sessionEventDoc{
			ID:      primitive.NewObjectID(),
			AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
			EventID: later.ID, Event: laterRaw,
		},
	}
	findCalls := 0
	mc.findFn = func(any) (*mongo.Cursor, error) {
		findCalls++
		if findCalls == 1 {
			return mongo.NewCursorFromDocuments(eventDocs, nil, nil)
		}
		if findCalls == 2 || findCalls == 5 {
			return mongo.NewCursorFromDocuments([]any{trackDoc}, nil, nil)
		}
		if findCalls == 4 {
			return mongo.NewCursorFromDocuments(eventDocs, nil, nil)
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
	var eventInserts int
	var persistedRevision []byte
	for _, op := range mc.recorded() {
		if op.name == "InsertOne" && op.coll == s.collSessionEvents {
			eventInserts++
		}
		if op.name == "UpdateOne" && op.coll == s.collSessionStates {
			set := op.update.(bson.M)["$set"].(bson.M)
			persistedRevision, _ = set["revision"].([]byte)
		}
	}
	assert.Zero(t, eventInserts, "the retained event prefix must not be rewritten")
	require.NotEmpty(t, persistedRevision)
	persisted, err := decodeRevision(persistedRevision)
	require.NoError(t, err)
	assert.True(t, sessionrevision.ProjectionInitialized(persisted))
	assert.Equal(t, uint64(1), persisted.Projection.Events.Count)
	assert.Equal(t, uint64(1), persisted.Projection.Tracks["trace"].Count)
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
	boundary, err := sessionrevision.NewBoundary(empty)
	require.NoError(t, err)
	limited := &sessionrevision.PersistedRecord{
		Generation: math.MaxUint64,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "old-request", InvocationID: "invocation", Boundary: boundary,
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

func TestRevisionEventFastPath(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	recordRaw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
	require.NoError(t, err)
	doc := sessionStateDoc{
		AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
		Revision: recordRaw,
	}
	partial := nonPartialResponseEvent(t)
	partial.IsPartial = true

	t.Run("avoids transaction", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.transactionFn = func(func(mongo.SessionContext) error) error {
			t.Fatal("fast path should not open a transaction")
			return nil
		}
		require.NoError(t, s.persistEventWithRevision(
			context.Background(), key, partial,
			sessionrevision.Write{HasExpectedGeneration: true, ExpectedGeneration: 2},
		))
		ops := mc.recorded()
		assert.Equal(t, []string{"FindOne", "UpdateOne"}, []string{ops[0].name, ops[1].name})
		filter := ops[1].filter.(bson.M)
		assert.Equal(t, recordRaw, filter["revision"])
	})

	t.Run("retries compare and swap", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		attempts := 0
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			attempts++
			if attempts == 1 {
				return &mongo.UpdateResult{}, nil
			}
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		}
		require.NoError(t, s.persistEventWithRevision(
			context.Background(), key, partial, sessionrevision.Write{},
		))
		assert.Equal(t, 2, attempts)
	})

	t.Run("falls back after bounded retries", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		attempts := 0
		transactions := 0
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			attempts++
			if attempts <= 8 {
				return &mongo.UpdateResult{}, nil
			}
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		}
		mc.transactionFn = func(fn func(mongo.SessionContext) error) error {
			transactions++
			return fn(mongo.NewSessionContext(context.Background(), nil))
		}
		require.NoError(t, s.persistEventWithRevision(
			context.Background(), key, partial, sessionrevision.Write{},
		))
		assert.Equal(t, 9, attempts)
		assert.Equal(t, 1, transactions)
	})
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
	newRestoreClient := func() *mockClient {
		calls := 0
		return &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return docsCursor([]any{sessionEventDoc{
					ID: primitive.NewObjectID(),
				}})
			}
			return emptyCursor()
		}}
	}

	t.Run("remove event tail", func(t *testing.T) {
		mc := newRestoreClient()
		mc.updateManyFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("delete failed")
		}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(context.Background(), key, restored, sessionStateDoc{}, record)
		assert.ErrorContains(t, err, "remove discarded event tail")
	})

	t.Run("soft delete projection", func(t *testing.T) {
		var filters []bson.M
		mc := newRestoreClient()
		mc.updateManyFn = func(filter, update any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			filters = append(filters, filter.(bson.M))
			set := update.(bson.M)["$set"].(bson.M)
			assert.NotNil(t, set["deleted_at"])
			return &mongo.UpdateResult{}, nil
		}
		s := newServiceForTest(t, mc)
		require.NoError(t, s.restoreRevisionProjection(
			context.Background(), key, restored, sessionStateDoc{}, record,
		))
		require.Len(t, filters, 2)
		for _, filter := range filters {
			assert.Equal(t, nil, filter["deleted_at"])
		}
	})

	t.Run("hard delete projection", func(t *testing.T) {
		mc := newRestoreClient()
		s := newServiceForTest(t, mc, func(opts *serviceOpts) {
			opts.softDelete = false
		})
		require.NoError(t, s.restoreRevisionProjection(
			context.Background(), key, restored, sessionStateDoc{}, record,
		))
		var deletes int
		for _, op := range mc.recorded() {
			if op.name == "DeleteMany" {
				deletes++
			}
		}
		assert.Equal(t, 2, deletes)
	})

	t.Run("missing state", func(t *testing.T) {
		mc := newRestoreClient()
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil
		}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(context.Background(), key, restored, sessionStateDoc{}, record)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("track encoding", func(t *testing.T) {
		withTrack := restored.Clone()
		withTrack.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{{
				Track: "trace", Payload: json.RawMessage{0xff},
			}}},
		}
		s := newServiceForTest(t, newRestoreClient())
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
		updateCalls := 0
		mc.updateManyFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			updateCalls++
			if updateCalls == 3 {
				return nil, errors.New("delete failed")
			}
			return &mongo.UpdateResult{}, nil
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
		mc := newRestoreClient()
		mc.insertOneFn = func(any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("insert failed")
		}
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
		s := newServiceForTest(t, newRestoreClient())
		require.NoError(t, s.restoreRevisionProjection(
			context.Background(), key, withNilEntries, sessionStateDoc{}, record,
		))
	})
}

func TestExpiredHistoryInvalidatesProjectionInTransaction(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	active := session.NewSession(key.AppName, key.UserID, key.SessionID)
	active.Events = append(active.Events, *nonPartialResponseEvent(t))
	record := &sessionrevision.PersistedRecord{
		Head: 5,
		Checkpoint: &sessionrevision.PersistedCheckpoint{
			RequestID: "request", InvocationID: "invocation",
		},
	}
	require.NoError(t, sessionrevision.InitializeProjection(record, active))
	revisionRaw, err := encodeRevision(record)
	require.NoError(t, err)
	doc := sessionStateDoc{
		DocumentID: primitive.NewObjectID(), Revision: revisionRaw,
	}
	var inTransaction bool
	var persistedRevision []byte
	mc := &mockClient{
		findFn: func(any) (*mongo.Cursor, error) {
			require.True(t, inTransaction)
			return docsCursor([]any{doc})
		},
		updateOneFn: func(_, update any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			require.True(t, inTransaction)
			set := update.(bson.M)["$set"].(bson.M)
			persistedRevision, _ = set["revision"].([]byte)
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		},
		updateManyFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			require.True(t, inTransaction)
			return &mongo.UpdateResult{MatchedCount: 1}, nil
		},
		transactionFn: func(fn func(mongo.SessionContext) error) error {
			inTransaction = true
			defer func() { inTransaction = false }()
			return fn(mongo.NewSessionContext(context.Background(), nil))
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.discardExpiredRevisionDocuments(
		context.Background(), s.collSessionEvents, "events", bson.A{
			bson.M{
				"app_name": key.AppName, "user_id": key.UserID,
				"session_id": key.SessionID,
			},
		}, time.Now(),
	))
	require.NotEmpty(t, persistedRevision)
	persisted, err := decodeRevision(persistedRevision)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), persisted.Head)
	require.NotNil(t, persisted.Checkpoint)
	assert.True(t, persisted.Checkpoint.Hazard)
	assert.False(t, sessionrevision.ProjectionInitialized(persisted))
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
	boundary, err := sessionrevision.NewBoundary(active)
	require.NoError(t, err)
	checkpoint := &sessionrevision.PersistedCheckpoint{
		RequestID: "old-request", InvocationID: "invocation", Boundary: boundary,
	}

	s, _ := revisionTestService(t, sessionStateDoc{})
	_, err = s.ReplaceLatestTurn(context.Background(), sessionrevision.LatestTurnReplacementRequest{})
	assert.Error(t, err)

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
			RequestID: "old-request", InvocationID: "invocation", Boundary: []byte("{"),
		}}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, _ := revisionTestService(t, sessionStateDoc{Revision: raw})
		_, err := s.ReplaceLatestTurn(context.Background(), req)
		assert.ErrorContains(t, err, "restore latest-turn boundary")
	})

	t.Run("restore", func(t *testing.T) {
		record := &sessionrevision.PersistedRecord{Checkpoint: checkpoint}
		raw, encodeErr := encodeRevision(record)
		require.NoError(t, encodeErr)
		s, mc := revisionTestService(t, sessionStateDoc{Revision: raw})
		later := nonPartialResponseEvent(t)
		laterRaw, marshalErr := json.Marshal(later)
		require.NoError(t, marshalErr)
		findCall := 0
		mc.findFn = func(any) (*mongo.Cursor, error) {
			findCall++
			if findCall == 1 || findCall == 4 {
				return mongo.NewCursorFromDocuments([]any{sessionEventDoc{
					ID:      primitive.NewObjectID(),
					AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
					EventID: later.ID, Event: laterRaw,
				}}, nil, nil)
			}
			return emptyCursor()
		}
		mc.updateManyFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
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

func TestRevisionIdentityReadBoundaries(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	listed := []*session.Session{
		session.NewSession(key.AppName, key.UserID, key.SessionID),
	}

	t.Run("single read", func(t *testing.T) {
		mc := &mockClient{findOneFn: func(any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(
				bson.D{}, errors.New("identity failed"), nil,
			)
		}}
		s := newServiceForTest(t, mc)
		_, err := s.revisionIdentity(ctx, key)
		assert.ErrorContains(t, err, "read session revision")

		mc.findOneFn = func(any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(
				sessionStateDoc{Revision: []byte("{")}, nil, nil,
			)
		}
		_, err = s.revisionIdentity(ctx, key)
		assert.ErrorContains(t, err, "decode session revision")
	})

	t.Run("empty batch", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			t.Fatal("empty batch must not query MongoDB")
			return nil, nil
		}})
		identities, err := s.revisionIdentities(ctx, userKey, nil)
		require.NoError(t, err)
		assert.Empty(t, identities)
	})

	for name, findFn := range map[string]func(any) (*mongo.Cursor, error){
		"query": func(any) (*mongo.Cursor, error) {
			return nil, errors.New("batch failed")
		},
		"cursor": func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{bson.M{"_id": "not-an-object-id"}})
		},
		"revision": func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionStateDoc{
				SessionID: key.SessionID, Revision: []byte("{"),
			}})
		},
	} {
		t.Run("batch "+name, func(t *testing.T) {
			s := newServiceForTest(t, &mockClient{findFn: findFn})
			_, err := s.revisionIdentities(ctx, userKey, listed)
			assert.Error(t, err)
		})
	}
}

func TestListedRevisionStabilizationFailsClosed(t *testing.T) {
	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}
	listed := []*session.Session{
		session.NewSession(userKey.AppName, userKey.UserID, "session"),
	}
	idBefore := primitive.NewObjectID()
	idAfter := primitive.NewObjectID()
	beforeRaw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 1})
	require.NoError(t, err)
	afterRaw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
	require.NoError(t, err)
	initial := sessionStateDoc{
		DocumentID: idBefore, SessionID: "session", Revision: beforeRaw,
	}
	current := sessionStateDoc{
		DocumentID: idAfter, SessionID: "session", Revision: afterRaw,
	}

	t.Run("batch read", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return nil, errors.New("identity failed")
		}})
		_, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, []sessionStateDoc{initial}, false, 0, time.Time{},
		)
		assert.ErrorContains(t, err, "read session revisions")
	})

	t.Run("deleted before identity read", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return emptyCursor()
		}})
		stabilized, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, []sessionStateDoc{initial}, false, 0, time.Time{},
		)
		require.NoError(t, err)
		assert.Empty(t, stabilized)
	})

	t.Run("duplicate initial identity", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{initial})
		}})
		_, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, []sessionStateDoc{initial, initial}, false, 0, time.Time{},
		)
		assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
	})

	t.Run("invalid initial revision", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{initial})
		}})
		bad := initial
		bad.Revision = []byte("{")
		_, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, []sessionStateDoc{bad}, false, 0, time.Time{},
		)
		assert.ErrorContains(t, err, "decode session revision")
	})

	t.Run("listed session absent from initial snapshot", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{initial})
		}})
		_, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, nil, false, 0, time.Time{},
		)
		assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
	})

	t.Run("expired during reload", func(t *testing.T) {
		findOneCalls := 0
		mc := &mockClient{
			findFn: func(any) (*mongo.Cursor, error) {
				return docsCursor([]any{current})
			},
			findOneFn: func(any) *mongo.SingleResult {
				findOneCalls++
				if findOneCalls == 2 {
					return mongo.NewSingleResultFromDocument(
						bson.D{}, mongo.ErrNoDocuments, nil,
					)
				}
				return mongo.NewSingleResultFromDocument(current, nil, nil)
			},
		}
		s := newServiceForTest(t, mc)
		stabilized, err := s.stabilizeListedRevisionProjections(
			ctx, userKey, listed, []sessionStateDoc{initial}, false, 0, time.Time{},
		)
		require.NoError(t, err)
		assert.Empty(t, stabilized)
	})

	t.Run("identity never stabilizes", func(t *testing.T) {
		findOneCalls := 0
		mc := &mockClient{
			findOneFn: func(any) *mongo.SingleResult {
				findOneCalls++
				doc := current
				if findOneCalls%3 == 1 {
					doc.DocumentID = primitive.NewObjectID()
					doc.Revision = beforeRaw
				} else if findOneCalls%3 == 0 {
					doc.DocumentID = primitive.NewObjectID()
					doc.Revision = afterRaw
				}
				return mongo.NewSingleResultFromDocument(doc, nil, nil)
			},
			findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
		}
		s := newServiceForTest(t, mc)
		_, err := s.loadStableRevisionProjection(
			ctx,
			session.Key{AppName: userKey.AppName, UserID: userKey.UserID, SessionID: "session"},
			0,
			time.Time{},
		)
		assert.ErrorIs(t, err, sessionrevision.ErrStaleProjection)
		assert.Equal(t, 9, findOneCalls)
	})
}

func TestRevisionTailVerificationFailsClosed(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	t.Run("event query", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return nil, errors.New("events failed")
		}})
		assert.ErrorContains(t, s.trimRevisionEventTail(ctx, key, 0), "lock active events")
	})

	t.Run("event document", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{bson.M{"_id": "not-an-object-id"}})
		}})
		assert.Error(t, s.trimRevisionEventTail(ctx, key, 0))
	})

	t.Run("event cursor", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return mongo.NewCursorFromDocuments(nil, errors.New("cursor failed"), nil)
		}})
		assert.ErrorContains(t, s.trimRevisionEventTail(ctx, key, 0), "cursor failed")
	})

	t.Run("event prefix not followed by a tail", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionEventDoc{ID: primitive.NewObjectID()}})
		}})
		assert.ErrorIs(
			t, s.trimRevisionEventTail(ctx, key, 1),
			sessionrevision.ErrLatestTurnReplacementUnavailable,
		)
	})

	track := session.Track("trace")
	prefix := session.TrackEvent{
		Track: track, RequestID: "request", Payload: json.RawMessage(`{"step":1}`),
	}
	prefixRaw, err := json.Marshal(prefix)
	require.NoError(t, err)

	t.Run("track query", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return nil, errors.New("tracks failed")
		}})
		assert.ErrorContains(
			t, s.trimRevisionTrackTails(ctx, key, session.NewSession("app", "user", "session")),
			"lock active tracks",
		)
	})

	t.Run("track document", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{bson.M{"_id": "not-an-object-id"}})
		}})
		assert.Error(t, s.trimRevisionTrackTails(
			ctx, key, session.NewSession("app", "user", "session"),
		))
	})

	t.Run("track payload", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionTrackDoc{
				ID: primitive.NewObjectID(), Track: track, Event: []byte("{"),
			}})
		}})
		assert.Error(t, s.trimRevisionTrackTails(
			ctx, key, session.NewSession("app", "user", "session"),
		))
	})

	t.Run("track cursor", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return mongo.NewCursorFromDocuments(nil, errors.New("cursor failed"), nil)
		}})
		assert.ErrorContains(t, s.trimRevisionTrackTails(
			ctx, key, session.NewSession("app", "user", "session"),
		), "cursor failed")
	})

	for _, tc := range []struct {
		name           string
		activeEvents   []session.TrackEvent
		restoredEvents []session.TrackEvent
	}{
		{
			name:           "active prefix too short",
			activeEvents:   []session.TrackEvent{prefix},
			restoredEvents: []session.TrackEvent{prefix, prefix},
		},
		{
			name: "active prefix differs",
			activeEvents: []session.TrackEvent{{
				Track: track, RequestID: "different", Payload: json.RawMessage(`{"step":1}`),
			}},
			restoredEvents: []session.TrackEvent{prefix},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var docs []any
			for _, activeEvent := range tc.activeEvents {
				raw, marshalErr := json.Marshal(activeEvent)
				require.NoError(t, marshalErr)
				docs = append(docs, sessionTrackDoc{
					ID: primitive.NewObjectID(), Track: track, Event: raw,
				})
			}
			s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
				return docsCursor(docs)
			}})
			restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
			restored.Tracks = map[session.Track]*session.TrackEvents{
				track: {Track: track, Events: tc.restoredEvents},
			}
			err := s.trimRevisionTrackTails(ctx, key, restored)
			assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
		})
	}

	t.Run("checkpoint track is entirely missing", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return emptyCursor()
		}})
		restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
		restored.Tracks = map[session.Track]*session.TrackEvents{
			track: {Track: track, Events: []session.TrackEvent{prefix}},
		}
		err := s.trimRevisionTrackTails(ctx, key, restored)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("exact track prefix needs no mutation", func(t *testing.T) {
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionTrackDoc{
				ID: primitive.NewObjectID(), Track: track, Event: prefixRaw,
			}})
		}}
		s := newServiceForTest(t, mc)
		restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
		restored.Tracks = map[session.Track]*session.TrackEvents{
			track: {Track: track, Events: []session.TrackEvent{prefix}},
		}
		require.NoError(t, s.trimRevisionTrackTails(ctx, key, restored))
		for _, op := range mc.recorded() {
			assert.NotEqual(t, "UpdateMany", op.name)
			assert.NotEqual(t, "DeleteMany", op.name)
		}
	})
}

func TestExpiredRevisionCleanupFailureBoundaries(t *testing.T) {
	ctx := context.Background()
	group := bson.M{"app_name": "app", "user_id": "user", "session_id": "session"}
	groups := bson.A{group}
	now := time.Now()

	t.Run("empty invalidation", func(t *testing.T) {
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			t.Fatal("empty invalidation must not query MongoDB")
			return nil, nil
		}}
		s := newServiceForTest(t, mc)
		require.NoError(t, s.invalidateRevisionProjections(ctx, nil))
	})

	for name, findFn := range map[string]func(any) (*mongo.Cursor, error){
		"query": func(any) (*mongo.Cursor, error) {
			return nil, errors.New("revision query failed")
		},
		"cursor": func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{bson.M{"_id": "not-an-object-id"}})
		},
		"decode": func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{sessionStateDoc{Revision: []byte("{")}})
		},
	} {
		t.Run("invalidate "+name, func(t *testing.T) {
			s := newServiceForTest(t, &mockClient{findFn: findFn})
			assert.Error(t, s.invalidateRevisionProjections(ctx, groups))
		})
	}

	revisionRaw, err := encodeRevision(&sessionrevision.PersistedRecord{})
	require.NoError(t, err)
	doc := sessionStateDoc{DocumentID: primitive.NewObjectID(), Revision: revisionRaw}

	for _, tc := range []struct {
		name     string
		updateFn func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error)
		target   error
	}{
		{
			name: "update error",
			updateFn: func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
				return nil, errors.New("revision update failed")
			},
		},
		{
			name: "compare and swap lost",
			updateFn: func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
				return &mongo.UpdateResult{}, nil
			},
			target: sessionrevision.ErrStaleProjection,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := &mockClient{
				findFn: func(any) (*mongo.Cursor, error) {
					return docsCursor([]any{doc})
				},
				updateOneFn: tc.updateFn,
			}
			s := newServiceForTest(t, mc)
			err := s.invalidateRevisionProjections(ctx, groups)
			assert.Error(t, err)
			if tc.target != nil {
				assert.ErrorIs(t, err, tc.target)
			}
		})
	}

	t.Run("invalidation aborts deletion", func(t *testing.T) {
		mc := &mockClient{
			findFn: func(any) (*mongo.Cursor, error) {
				return nil, errors.New("invalidate failed")
			},
			transactionFn: func(fn func(mongo.SessionContext) error) error {
				return fn(mongo.NewSessionContext(context.Background(), nil))
			},
		}
		s := newServiceForTest(t, mc)
		err := s.discardExpiredRevisionDocuments(
			ctx, s.collSessionEvents, "events", groups, now,
		)
		assert.ErrorContains(t, err, "invalidate expired events projections")
		for _, op := range mc.recorded() {
			assert.NotEqual(t, "UpdateMany", op.name)
			assert.NotEqual(t, "DeleteMany", op.name)
		}
	})

	t.Run("soft delete", func(t *testing.T) {
		mc := &mockClient{
			findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
			updateManyFn: func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
				return nil, errors.New("soft delete failed")
			},
			transactionFn: func(fn func(mongo.SessionContext) error) error {
				return fn(mongo.NewSessionContext(context.Background(), nil))
			},
		}
		s := newServiceForTest(t, mc)
		err := s.discardExpiredRevisionDocuments(
			ctx, s.collSessionEvents, "events", groups, now,
		)
		assert.ErrorContains(t, err, "soft delete expired events")
	})

	for name, deleteErr := range map[string]error{
		"success": nil,
		"failure": errors.New("hard delete failed"),
	} {
		t.Run("hard delete "+name, func(t *testing.T) {
			mc := &mockClient{
				findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
				deleteManyFn: func(any) (*mongo.DeleteResult, error) {
					return &mongo.DeleteResult{}, deleteErr
				},
				transactionFn: func(fn func(mongo.SessionContext) error) error {
					return fn(mongo.NewSessionContext(context.Background(), nil))
				},
			}
			s := newServiceForTest(t, mc, func(opts *serviceOpts) {
				opts.softDelete = false
			})
			err := s.discardExpiredRevisionDocuments(
				ctx, s.collSessionTracks, "tracks", groups, now,
			)
			if deleteErr == nil {
				require.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, "hard delete expired tracks")
			}
		})
	}
}

func TestRevisionMutationProjectionAndCancellationFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	record := &sessionrevision.PersistedRecord{}
	require.NoError(t, sessionrevision.InitializeProjection(
		record, session.NewSession(key.AppName, key.UserID, key.SessionID),
	))
	record.Projection.Version++
	raw, err := encodeRevision(record)
	require.NoError(t, err)
	doc := sessionStateDoc{Revision: raw, State: bson.M{}}

	t.Run("event projection version", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		err := s.persistEventWithRevision(
			context.Background(), key, nonPartialResponseEvent(t), sessionrevision.Write{},
		)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("track projection version", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		err := s.persistTrackEventWithRevision(
			context.Background(), key,
			&session.TrackEvent{Track: "trace", Payload: json.RawMessage(`{}`)},
			sessionrevision.Write{},
		)
		assert.ErrorIs(t, err, sessionrevision.ErrLatestTurnReplacementUnavailable)
	})

	t.Run("track index is corrupt", func(t *testing.T) {
		valid := &sessionrevision.PersistedRecord{}
		require.NoError(t, sessionrevision.InitializeProjection(
			valid, session.NewSession(key.AppName, key.UserID, key.SessionID),
		))
		validRaw, encodeErr := encodeRevision(valid)
		require.NoError(t, encodeErr)
		s, _ := revisionTestService(t, sessionStateDoc{
			Revision: validRaw, State: bson.M{"tracks": []byte("{")},
		})
		err := s.persistTrackEventWithRevision(
			context.Background(), key,
			&session.TrackEvent{Track: "trace", Payload: json.RawMessage(`{}`)},
			sessionrevision.Write{},
		)
		assert.ErrorContains(t, err, "decode track index")
	})

	t.Run("fast path cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		partial := nonPartialResponseEvent(t)
		partial.IsPartial = true
		s, _ := revisionTestService(t, sessionStateDoc{})
		assert.ErrorIs(
			t,
			s.persistEventWithRevision(ctx, key, partial, sessionrevision.Write{}),
			context.Canceled,
		)
	})

	t.Run("fast path update", func(t *testing.T) {
		validRaw, encodeErr := encodeRevision(&sessionrevision.PersistedRecord{})
		require.NoError(t, encodeErr)
		partial := nonPartialResponseEvent(t)
		partial.IsPartial = true
		s, mc := revisionTestService(t, sessionStateDoc{Revision: validRaw})
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("compare and swap failed")
		}
		err := s.persistEventWithRevision(
			context.Background(), key, partial, sessionrevision.Write{},
		)
		assert.ErrorContains(t, err, "update session state")
	})
}

func TestStableRevisionProjectionReadFailures(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	raw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 1})
	require.NoError(t, err)
	doc := sessionStateDoc{
		DocumentID: primitive.NewObjectID(), AppName: key.AppName,
		UserID: key.UserID, SessionID: key.SessionID, Revision: raw,
	}

	t.Run("before identity", func(t *testing.T) {
		s := newServiceForTest(t, &mockClient{findOneFn: func(any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(
				bson.D{}, errors.New("before failed"), nil,
			)
		}})
		_, err := s.loadStableRevisionProjection(ctx, key, 0, time.Time{})
		assert.ErrorContains(t, err, "before failed")
	})

	t.Run("projection", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findOneFn: func(any) *mongo.SingleResult {
			calls++
			if calls == 2 {
				return mongo.NewSingleResultFromDocument(
					bson.D{}, errors.New("projection failed"), nil,
				)
			}
			return mongo.NewSingleResultFromDocument(doc, nil, nil)
		}})
		_, err := s.loadStableRevisionProjection(ctx, key, 0, time.Time{})
		assert.ErrorContains(t, err, "projection failed")
	})

	t.Run("after identity", func(t *testing.T) {
		calls := 0
		mc := &mockClient{
			findOneFn: func(any) *mongo.SingleResult {
				calls++
				if calls == 3 {
					return mongo.NewSingleResultFromDocument(
						bson.D{}, errors.New("after failed"), nil,
					)
				}
				return mongo.NewSingleResultFromDocument(doc, nil, nil)
			},
			findFn: func(any) (*mongo.Cursor, error) { return emptyCursor() },
		}
		s := newServiceForTest(t, mc)
		_, err := s.loadStableRevisionProjection(ctx, key, 0, time.Time{})
		assert.ErrorContains(t, err, "after failed")
	})
}

func TestTransactionalRevisionEventReadFailures(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	evt := nonPartialResponseEvent(t)
	raw, err := encodeRevision(&sessionrevision.PersistedRecord{Generation: 2})
	require.NoError(t, err)
	doc := sessionStateDoc{Revision: raw}

	for name, tc := range map[string]struct {
		result   *mongo.SingleResult
		expected error
	}{
		"not found": {
			result:   mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil),
			expected: errSessionNotFound,
		},
		"read": {
			result: mongo.NewSingleResultFromDocument(bson.D{}, errors.New("read failed"), nil),
		},
		"revision": {
			result: mongo.NewSingleResultFromDocument(
				sessionStateDoc{Revision: []byte("{")}, nil, nil,
			),
		},
	} {
		t.Run(name, func(t *testing.T) {
			mc := &mockClient{
				findOneFn: func(any) *mongo.SingleResult { return tc.result },
				transactionFn: func(fn func(mongo.SessionContext) error) error {
					return fn(mongo.NewSessionContext(context.Background(), nil))
				},
			}
			s := newServiceForTest(t, mc)
			err := s.persistEventWithRevision(ctx, key, evt, sessionrevision.Write{})
			assert.Error(t, err)
			if tc.expected != nil {
				assert.ErrorIs(t, err, tc.expected)
			}
		})
	}

	t.Run("generation", func(t *testing.T) {
		s, _ := revisionTestService(t, doc)
		err := s.persistEventWithRevision(ctx, key, evt, sessionrevision.Write{
			HasExpectedGeneration: true, ExpectedGeneration: 1,
		})
		assert.ErrorIs(t, err, sessionrevision.ErrStaleGeneration)
	})

	t.Run("turn bootstrap", func(t *testing.T) {
		s, mc := revisionTestService(t, doc)
		mc.findFn = func(any) (*mongo.Cursor, error) {
			return nil, errors.New("history failed")
		}
		err := s.persistEventWithRevision(ctx, key, evt, sessionrevision.Write{
			Start: &sessionrevision.TurnStart{
				RequestID: "request", InvocationID: "invocation",
			},
		})
		assert.ErrorContains(t, err, "load active events")
	})
}

func TestRevisionDocumentDecodeAndRestoreFailures(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	t.Run("track document", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return emptyCursor()
			}
			return docsCursor([]any{bson.M{"event": 42}})
		}})
		_, err := s.loadActiveRevisionSession(ctx, key, sessionStateDoc{})
		assert.Error(t, err)
	})

	t.Run("summary document", func(t *testing.T) {
		calls := 0
		s := newServiceForTest(t, &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls < 3 {
				return emptyCursor()
			}
			return docsCursor([]any{bson.M{"summary": 42}})
		}})
		_, err := s.loadActiveRevisionSession(ctx, key, sessionStateDoc{})
		assert.Error(t, err)
	})

	newRestoreClient := func() *mockClient {
		calls := 0
		return &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return docsCursor([]any{sessionEventDoc{ID: primitive.NewObjectID()}})
			}
			return emptyCursor()
		}}
	}
	restored := session.NewSession(key.AppName, key.UserID, key.SessionID)
	record := &sessionrevision.PersistedRecord{}

	t.Run("clear summaries", func(t *testing.T) {
		mc := newRestoreClient()
		updates := 0
		mc.updateManyFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			updates++
			if updates == 2 {
				return nil, errors.New("summaries failed")
			}
			return &mongo.UpdateResult{}, nil
		}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(ctx, key, restored, sessionStateDoc{}, record)
		assert.ErrorContains(t, err, "clear discarded session summaries")
	})

	t.Run("restore state", func(t *testing.T) {
		mc := newRestoreClient()
		mc.updateOneFn = func(any, any, []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("state failed")
		}
		s := newServiceForTest(t, mc)
		err := s.restoreRevisionProjection(ctx, key, restored, sessionStateDoc{}, record)
		assert.ErrorContains(t, err, "restore session state")
	})

	t.Run("remove track tail", func(t *testing.T) {
		prefix := session.TrackEvent{
			Track: "trace", RequestID: "request", Payload: json.RawMessage(`{"step":1}`),
		}
		tail := session.TrackEvent{
			Track: "trace", RequestID: "request", Payload: json.RawMessage(`{"step":2}`),
		}
		prefixRaw, marshalErr := json.Marshal(prefix)
		require.NoError(t, marshalErr)
		tailRaw, marshalErr := json.Marshal(tail)
		require.NoError(t, marshalErr)
		mc := &mockClient{findFn: func(any) (*mongo.Cursor, error) {
			return docsCursor([]any{
				sessionTrackDoc{ID: primitive.NewObjectID(), Track: "trace", Event: prefixRaw},
				sessionTrackDoc{ID: primitive.NewObjectID(), Track: "trace", Event: tailRaw},
			})
		}}
		s := newServiceForTest(t, mc)
		projection := session.NewSession(key.AppName, key.UserID, key.SessionID)
		projection.Tracks = map[session.Track]*session.TrackEvents{
			"trace": {Track: "trace", Events: []session.TrackEvent{prefix}},
		}
		require.NoError(t, s.trimRevisionTrackTails(ctx, key, projection))
		ops := mc.recorded()
		require.Len(t, ops, 2)
		assert.Equal(t, "UpdateMany", ops[1].name)
	})
}
