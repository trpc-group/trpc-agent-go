//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

// -- CreateSession ----------------------------------------------------------

func insertedSessionStateDoc(t *testing.T, mc *mockClient) sessionStateDoc {
	t.Helper()
	for _, op := range mc.recorded() {
		if op.name == "InsertOne" && op.coll == "session_states" {
			doc, ok := op.doc.(sessionStateDoc)
			require.True(t, ok, "InsertOne doc should be sessionStateDoc")
			return doc
		}
	}
	t.Fatalf("InsertOne on session_states not found")
	return sessionStateDoc{}
}

func TestCreateSession_AssignsUUIDWhenSessionIDEmpty(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	sess, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u"},
		session.StateMap{"k": []byte("v")})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotEmpty(t, sess.ID)

	ops := mc.recorded()
	require.NotEmpty(t, ops)
	doc := insertedSessionStateDoc(t, mc)
	assert.Equal(t, "app", doc.AppName)
	assert.Equal(t, "u", doc.UserID)
	assert.NotEmpty(t, doc.SessionID)
	assert.Nil(t, doc.ExpiresAt, "no TTL configured -> expires_at is nil")
}

func TestCreateSession_TTLSetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{})
	require.NoError(t, err)

	doc := insertedSessionStateDoc(t, mc)
	require.NotNil(t, doc.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *doc.ExpiresAt, 5*time.Second)
}

func TestCreateSession_StateKeysAreEncoded(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"a.b": []byte("v"), "c$d": []byte("w")})
	require.NoError(t, err)

	doc := insertedSessionStateDoc(t, mc)
	// Encoded keys: a.b -> a\db, c$d -> c\sd
	_, hasDot := doc.State["a.b"]
	_, hasDollar := doc.State["c$d"]
	assert.False(t, hasDot, "raw '.' key must not appear in BSON state field")
	assert.False(t, hasDollar, "raw '$' key must not appear in BSON state field")
	assert.NotNil(t, doc.State[`a\db`])
	assert.NotNil(t, doc.State[`c\sd`])
}

func TestCreateSession_DuplicateKeyMapsToFriendlyError(t *testing.T) {
	mc := &mockClient{
		insertOneFn: func(_ any) (*mongo.InsertOneResult, error) {
			return nil, mongo.WriteException{
				WriteErrors: mongo.WriteErrors{{Code: 11000}},
			}
		},
	}
	s := newServiceForTest(t, mc)

	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session already exists")
}

func TestCreateSession_ExistingNotExpiredReturnsExists(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(sessionStateDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				ExpiresAt: &expiresAt,
			}, nil, nil)
		},
	}
	s := newServiceForTest(t, mc)

	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	for _, op := range mc.recorded() {
		assert.NotEqual(t, "InsertOne", op.name)
	}
}

func TestCreateSession_ReplacesExpiredSession(t *testing.T) {
	expiresAt := time.Now().Add(-time.Hour)
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(sessionStateDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				ExpiresAt: &expiresAt,
			}, nil, nil)
		},
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	s := newServiceForTest(t, mc)

	sess, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"fresh": []byte("state")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	var sawTransaction, sawInsert bool
	var deletedCollections []string
	for _, op := range mc.recorded() {
		switch op.name {
		case "Transaction":
			sawTransaction = true
		case "UpdateOne", "UpdateMany":
			if op.coll == "session_states" || op.coll == "session_events" ||
				op.coll == "session_tracks" || op.coll == "session_summaries" {
				deletedCollections = append(deletedCollections, op.coll)
			}
		case "InsertOne":
			if op.coll == "session_states" {
				sawInsert = true
			}
		}
	}
	assert.True(t, sawTransaction)
	assert.True(t, sawInsert)
	assert.ElementsMatch(t, []string{
		"session_states",
		"session_events",
		"session_tracks",
		"session_summaries",
	}, deletedCollections)
}

func TestCreateSession_ReplacesExpiredSessionHardDeleteUsesActiveStateFilter(t *testing.T) {
	expiresAt := time.Now().Add(-time.Hour)
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(sessionStateDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				ExpiresAt: &expiresAt,
			}, nil, nil)
		},
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.softDelete = false })

	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"fresh": []byte("state")})
	require.NoError(t, err)

	var stateDeleteFilter bson.M
	for _, op := range mc.recorded() {
		if op.name == "DeleteOne" && op.coll == "session_states" {
			stateDeleteFilter = op.filter.(bson.M)
			break
		}
	}
	require.NotNil(t, stateDeleteFilter)
	assert.Equal(t, nil, stateDeleteFilter["deleted_at"])
}

func TestCreateSession_RejectsMissingAppName(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	_, err := s.CreateSession(context.Background(), session.Key{UserID: "u"}, nil)
	require.Error(t, err)
	assert.Empty(t, mc.recorded(), "no client calls should happen on validation failure")
}

// -- GetSession -------------------------------------------------------------

func TestGetSession_NotFoundReturnsNilNoError(t *testing.T) {
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
		},
	}
	s := newServiceForTest(t, mc)
	sess, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestGetSession_DecodesStateAndMergesAppUser(t *testing.T) {
	stored := sessionStateDoc{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		State:     bson.M{`a\db`: []byte("v1")},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}

	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(stored, nil, nil)
		},
		findFn: func(_ any) (*mongo.Cursor, error) {
			// Two Find calls: app_states and user_states. The fake doesn't
			// distinguish; return a cursor that yields one stateKVDoc each
			// time, with an alternating value to exercise the merge.
			return docsCursor([]any{
				stateKVDoc{AppName: "app", Key: "globalKey", Value: []byte("g")},
			})
		},
	}
	s := newServiceForTest(t, mc)

	sess, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Decoded state key (with '.' restored).
	v, ok := sess.GetState("a.b")
	require.True(t, ok)
	assert.Equal(t, []byte("v1"), v)

	// app: prefix from app state merge.
	v, ok = sess.GetState("app:globalKey")
	require.True(t, ok)
	assert.Equal(t, []byte("g"), v)

	// user: prefix from user state merge.
	v, ok = sess.GetState("user:globalKey")
	require.True(t, ok)
	assert.Equal(t, []byte("g"), v)
}

func TestGetSession_LoadsEventsAndSummaries(t *testing.T) {
	now := time.Now()
	stored := sessionStateDoc{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		State:     bson.M{},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	e1 := event.Event{
		ID:           "e1",
		InvocationID: "i",
		Author:       "user",
		Timestamp:    now.Add(-time.Minute),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}
	e2 := event.Event{
		ID:           "e2",
		InvocationID: "i",
		Author:       "assistant",
		Timestamp:    now,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "hi"},
		}}},
	}
	e1Bytes, err := json.Marshal(e1)
	require.NoError(t, err)
	e2Bytes, err := json.Marshal(e2)
	require.NoError(t, err)
	summaryBytes, err := json.Marshal(&session.Summary{Summary: "short", UpdatedAt: now})
	require.NoError(t, err)

	findCalls := 0
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(stored, nil, nil)
		},
		findFn: func(filter any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1, 2: // app_states, user_states.
				return emptyCursor()
			case 3: // session_events.
				f := filter.(bson.M)
				assert.NotContains(t, f, "$or", "session_events reads must not filter by per-row expires_at")
				return docsCursor([]any{
					sessionEventDoc{AppName: "app", UserID: "u", SessionID: "s", Event: e1Bytes, CreatedAt: e1.Timestamp},
					sessionEventDoc{AppName: "app", UserID: "u", SessionID: "s", Event: e2Bytes, CreatedAt: e2.Timestamp},
				})
			case 4: // session_summaries.
				return docsCursor([]any{
					sessionSummaryDoc{AppName: "app", UserID: "u", SessionID: "s", Summary: summaryBytes, UpdatedAt: now},
				})
			default:
				return emptyCursor()
			}
		},
	}
	s := newServiceForTest(t, mc)

	sess, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, "e1", sess.Events[0].ID)
	assert.Equal(t, "e2", sess.Events[1].ID)
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "short", text)
}

func TestGetSession_EventPagePath(t *testing.T) {
	now := time.Now()
	stored := sessionStateDoc{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		State:     bson.M{},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	e1 := event.Event{ID: "e1", InvocationID: "i", Author: "user", Timestamp: now.Add(-time.Minute)}
	e2 := event.Event{ID: "e2", InvocationID: "i", Author: "assistant", Timestamp: now}
	e1Bytes, err := json.Marshal(e1)
	require.NoError(t, err)
	e2Bytes, err := json.Marshal(e2)
	require.NoError(t, err)

	findCalls := 0
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(stored, nil, nil)
		},
		findFn: func(filter any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1, 2: // app_states, user_states.
				return emptyCursor()
			case 3: // paged events are read newest-first by query, then reversed.
				f := filter.(bson.M)
				assert.NotContains(t, f, "$or", "paged session_events reads must not filter by per-row expires_at")
				return docsCursor([]any{
					sessionEventDoc{AppName: "app", UserID: "u", SessionID: "s", Event: e2Bytes, CreatedAt: e2.Timestamp},
					sessionEventDoc{AppName: "app", UserID: "u", SessionID: "s", Event: e1Bytes, CreatedAt: e1.Timestamp},
				})
			case 4: // summaries.
				return emptyCursor()
			default:
				return emptyCursor()
			}
		},
	}
	s := newServiceForTest(t, mc)

	sess, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.WithGetSessionEventPage(0, 10))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, "e1", sess.Events[0].ID)
	assert.Equal(t, "e2", sess.Events[1].ID)
}

// -- ListSessions -----------------------------------------------------------

func TestListSessions_AppliesPagination(t *testing.T) {
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			return emptyCursor()
		},
	}

	s := newServiceForTest(t, mc)
	_, err := s.ListSessions(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"},
		session.WithListSessionPage(10, 5))
	require.NoError(t, err)

	// Three Find calls expected: session_states (paged), app_states, user_states.
	var findCalls int
	for _, op := range mc.recorded() {
		if op.name == "Find" {
			findCalls++
		}
	}
	assert.Equal(t, 3, findCalls)
}

func TestListSessions_DecodesAndMergesEachSession(t *testing.T) {
	now := time.Now()
	docs := []any{
		sessionStateDoc{AppName: "app", UserID: "u", SessionID: "s1", State: bson.M{"k": []byte("v1")}, CreatedAt: now, UpdatedAt: now},
		sessionStateDoc{AppName: "app", UserID: "u", SessionID: "s2", State: bson.M{"k": []byte("v2")}, CreatedAt: now, UpdatedAt: now},
	}

	calls := 0
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return docsCursor(docs)
			}
			// Subsequent Find calls (app_states, user_states, events,
			// summaries) yield empty.
			return emptyCursor()
		},
	}
	s := newServiceForTest(t, mc)

	got, err := s.ListSessions(context.Background(), session.UserKey{AppName: "app", UserID: "u"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "s1", got[0].ID)
	assert.Equal(t, "s2", got[1].ID)
}

func TestListSessions_OnlyMetaSkipsEventsAndSummaries(t *testing.T) {
	now := time.Now()
	docs := []any{
		sessionStateDoc{AppName: "app", UserID: "u", SessionID: "s1", State: bson.M{}, CreatedAt: now, UpdatedAt: now},
	}
	calls := 0
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			calls++
			if calls == 1 {
				return docsCursor(docs)
			}
			return emptyCursor()
		},
	}
	s := newServiceForTest(t, mc)

	got, err := s.ListSessions(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"},
		session.WithListSessionOnlyMeta())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Events)
	assert.Equal(t, 3, calls, "session/app/user state reads only; no event/summary reads")
}

func TestListSessions_NonMetaLoadsEventsAndSummaries(t *testing.T) {
	now := time.Now()
	evt := event.Event{
		ID:           "e1",
		InvocationID: "i",
		Author:       "user",
		Timestamp:    now,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}
	evtBytes, err := json.Marshal(evt)
	require.NoError(t, err)

	findCalls := 0
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1: // session_states.
				return docsCursor([]any{
					sessionStateDoc{
						AppName:   "app",
						UserID:    "u",
						SessionID: "s",
						State:     bson.M{},
						CreatedAt: now.Add(-time.Hour),
						UpdatedAt: now,
					},
				})
			case 2, 3: // app_states, user_states.
				return emptyCursor()
			case 4: // session_events.
				return docsCursor([]any{
					sessionEventDoc{
						AppName:   "app",
						UserID:    "u",
						SessionID: "s",
						Event:     evtBytes,
						CreatedAt: evt.Timestamp,
					},
				})
			case 5: // session_summaries.
				return emptyCursor()
			default:
				return emptyCursor()
			}
		},
	}
	s := newServiceForTest(t, mc)

	got, err := s.ListSessions(context.Background(), session.UserKey{AppName: "app", UserID: "u"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Events, 1)
	assert.Equal(t, "e1", got[0].Events[0].ID)
}

// -- DeleteSession ----------------------------------------------------------

func TestDeleteSession_SoftDeleteStampsDeletedAt(t *testing.T) {
	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	s := newServiceForTest(t, mc) // default: softDelete=true

	require.NoError(t, s.DeleteSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"}))

	ops := mc.recorded()
	require.Len(t, ops, 5)
	assert.Equal(t, "Transaction", ops[0].name)
	assert.Equal(t, "UpdateOne", ops[1].name)
	assert.Equal(t, "session_states", ops[1].coll)
	assert.Equal(t, "UpdateMany", ops[2].name)
	assert.Equal(t, "session_events", ops[2].coll)
	assert.Equal(t, "UpdateMany", ops[3].name)
	assert.Equal(t, "session_tracks", ops[3].coll)
	assert.Equal(t, "UpdateMany", ops[4].name)
	assert.Equal(t, "session_summaries", ops[4].coll)

	for _, op := range ops[1:] {
		upd, ok := op.update.(bson.M)
		require.True(t, ok)
		set, ok := upd["$set"].(bson.M)
		require.True(t, ok)
		_, hasDeletedAt := set["deleted_at"]
		assert.True(t, hasDeletedAt, "soft delete should $set deleted_at")
	}
}

func TestDeleteSession_HardDeleteFanOut(t *testing.T) {
	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.softDelete = false })

	require.NoError(t, s.DeleteSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"}))

	ops := mc.recorded()
	require.Len(t, ops, 5)
	assert.Equal(t, "Transaction", ops[0].name)
	assert.Equal(t, "DeleteOne", ops[1].name)
	assert.Equal(t, "session_states", ops[1].coll)
	stateFilter := ops[1].filter.(bson.M)
	assert.Equal(t, nil, stateFilter["deleted_at"])
	assert.Equal(t, "DeleteMany", ops[2].name)
	assert.Equal(t, "session_events", ops[2].coll)
	assert.Equal(t, "DeleteMany", ops[3].name)
	assert.Equal(t, "session_tracks", ops[3].coll)
	assert.Equal(t, "DeleteMany", ops[4].name)
	assert.Equal(t, "session_summaries", ops[4].coll)
}

func TestDeleteSession_TransactionErrorPropagates(t *testing.T) {
	want := errors.New("tx failed")
	mc := &mockClient{
		transactionFn: func(_ storage.TxFunc) error {
			return want
		},
	}
	s := newServiceForTest(t, mc)

	err := s.DeleteSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

// -- App state --------------------------------------------------------------

func TestUpdateAppState_PerKeyUpsert_StripsAppPrefix(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateAppState(context.Background(), "app",
		session.StateMap{
			"app:foo": []byte("v1"),
			"bar":     []byte("v2"),
		}))

	ops := mc.recorded()
	require.Len(t, ops, 2)

	for _, op := range ops {
		assert.Equal(t, "UpdateOne", op.name)
		assert.Equal(t, "app_states", op.coll)
		filter := op.filter.(bson.M)
		assert.Equal(t, "app", filter["app_name"])
		// Both keys should appear without the "app:" prefix.
		k := filter["key"].(string)
		assert.False(t, strings.HasPrefix(k, "app:"), "app: prefix must be stripped before write")
	}
}

func TestUpdateAppState_TTLSetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.appStateTTL = time.Hour })

	require.NoError(t, s.UpdateAppState(context.Background(), "app",
		session.StateMap{"foo": []byte("v")}))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	upd := ops[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	exp, ok := set["expires_at"].(*time.Time)
	require.True(t, ok)
	require.NotNil(t, exp)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *exp, 5*time.Second)
}

func TestUpdateAppState_NoTTLUnsetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateAppState(context.Background(), "app",
		session.StateMap{"foo": []byte("v")}))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	assert.NotContains(t, set, "expires_at")
	unset := upd["$unset"].(bson.M)
	assert.Contains(t, unset, "expires_at")
}

func TestUpdateAppState_RejectsEmptyAppName(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	err := s.UpdateAppState(context.Background(), "", session.StateMap{"k": []byte("v")})
	assert.ErrorIs(t, err, session.ErrAppNameRequired)
}

func TestListAppStates_DecodesValues(t *testing.T) {
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			return docsCursor([]any{
				stateKVDoc{AppName: "app", Key: "k1", Value: []byte("v1")},
				stateKVDoc{AppName: "app", Key: "k2", Value: []byte("v2")},
			})
		},
	}
	s := newServiceForTest(t, mc)

	out, err := s.ListAppStates(context.Background(), "app")
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), out["k1"])
	assert.Equal(t, []byte("v2"), out["k2"])
}

func TestDeleteAppState_SoftAndHard(t *testing.T) {
	t.Run("soft", func(t *testing.T) {
		mc := &mockClient{}
		s := newServiceForTest(t, mc)
		require.NoError(t, s.DeleteAppState(context.Background(), "app", "k"))
		assert.Equal(t, "UpdateOne", mc.recorded()[0].name)
	})
	t.Run("hard", func(t *testing.T) {
		mc := &mockClient{}
		s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.softDelete = false })
		require.NoError(t, s.DeleteAppState(context.Background(), "app", "k"))
		assert.Equal(t, "DeleteOne", mc.recorded()[0].name)
		filter := mc.recorded()[0].filter.(bson.M)
		assert.Equal(t, nil, filter["deleted_at"])
	})
}

func TestDeleteAppState_RejectsBlankKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	require.Error(t, s.DeleteAppState(context.Background(), "app", ""))
}

// -- User state -------------------------------------------------------------

func TestUpdateUserState_StripsUserPrefix(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"},
		session.StateMap{"user:foo": []byte("v")}))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	filter := ops[0].filter.(bson.M)
	assert.Equal(t, "foo", filter["key"], "user: prefix must be stripped")
}

func TestUpdateUserState_NoTTLUnsetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"},
		session.StateMap{"foo": []byte("v")}))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	assert.NotContains(t, set, "expires_at")
	unset := upd["$unset"].(bson.M)
	assert.Contains(t, unset, "expires_at")
}

func TestListUserStates_RejectsBadKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	_, err := s.ListUserStates(context.Background(), session.UserKey{AppName: ""})
	require.Error(t, err)
}

func TestDeleteUserState_HardDelete(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.softDelete = false })
	require.NoError(t, s.DeleteUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"}, "k"))
	assert.Equal(t, "DeleteOne", mc.recorded()[0].name)
	filter := mc.recorded()[0].filter.(bson.M)
	assert.Equal(t, nil, filter["deleted_at"])
}

// -- UpdateSessionState (D4=B: dot-notation $set) ---------------------------

func TestUpdateSessionState_DotNotationPerKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"foo": []byte("v"), "bar.baz": []byte("w")}))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	upd := ops[0].update.(bson.M)
	set := upd["$set"].(bson.M)

	// foo writes to "state.foo"; bar.baz encodes the dot, writes to "state.bar\db".
	_, hasFoo := set["state.foo"]
	_, hasEncoded := set[`state.bar\dbaz`]
	assert.True(t, hasFoo)
	assert.True(t, hasEncoded)
	assert.Contains(t, set, "updated_at")
}

func TestUpdateSessionState_RejectsAppPrefix(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	err := s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"app:foo": []byte("v")})
	require.Error(t, err)
	assert.Empty(t, mc.recorded(), "no client call should happen when validation rejects")
}

func TestUpdateSessionState_RejectsUserPrefix(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	err := s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"user:foo": []byte("v")})
	require.Error(t, err)
}

func TestUpdateSessionState_TTLBumpsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	require.NoError(t, s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"foo": []byte("v")}))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	exp, ok := set["expires_at"].(*time.Time)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *exp, 5*time.Second)
}

func TestUpdateSessionState_NoTTLUnsetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.NoError(t, s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"foo": []byte("v")}))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	assert.NotContains(t, set, "expires_at")
	unset := upd["$unset"].(bson.M)
	assert.Contains(t, unset, "expires_at")
}

func TestUpdateSessionState_NoMatchReturnsSessionNotFound(t *testing.T) {
	mc := &mockClient{
		updateOneFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil // MatchedCount: 0
		},
	}
	s := newServiceForTest(t, mc)

	err := s.UpdateSessionState(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		session.StateMap{"foo": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// -- AppendEvent ----------------------------------------------------------

// nonPartialResponseEvent constructs an event that AppendEvent will treat as
// "persistable" — Response set, IsPartial=false, IsValidContent()=true. This
// drives the transactional path that inserts a session_events document.
func nonPartialResponseEvent(t *testing.T) *event.Event {
	t.Helper()
	e := event.New("test-invocation", "test-author")
	e.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}},
		},
	}
	e.IsPartial = false
	return e
}

func TestAppendEvent_PersistableGoesThroughTransaction(t *testing.T) {
	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	s := newServiceForTest(t, mc)
	sess := newSessionForTest("app", "u", "s")
	evt := nonPartialResponseEvent(t)

	require.NoError(t, s.AppendEvent(context.Background(), sess, evt))

	// Expect: Transaction wrapper recorded plus the inner UpdateOne + InsertOne.
	var sawTransaction, sawUpdate, sawInsert bool
	for _, op := range mc.recorded() {
		switch op.name {
		case "Transaction":
			sawTransaction = true
		case "UpdateOne":
			if op.coll == "session_states" {
				sawUpdate = true
				upd := op.update.(bson.M)
				unset := upd["$unset"].(bson.M)
				assert.Contains(t, unset, "expires_at")
			}
		case "InsertOne":
			if op.coll == "session_events" {
				sawInsert = true
				doc := op.doc.(*sessionEventDoc)
				assert.Equal(t, evt.ID, doc.EventID)
			}
		}
	}
	assert.True(t, sawTransaction, "expected Transaction call")
	assert.True(t, sawUpdate, "expected UpdateOne on session_states")
	assert.True(t, sawInsert, "expected InsertOne on session_events")
}

func TestAppendEvent_StateDeltaOnly_NoTransactionNoEventInsert(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	sess := newSessionForTest("app", "u", "s")
	e := &event.Event{
		StateDelta: map[string][]byte{"k1": []byte("v1")},
	}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	ops := mc.recorded()
	// State-delta-only events: a single UpdateOne, no transaction, no event insert.
	require.Len(t, ops, 1)
	assert.Equal(t, "UpdateOne", ops[0].name)
	assert.Equal(t, "session_states", ops[0].coll)
	upd := ops[0].update.(bson.M)
	unset := upd["$unset"].(bson.M)
	assert.Contains(t, unset, "expires_at")

	// In-memory session is updated.
	v, ok := sess.GetState("k1")
	require.True(t, ok)
	assert.Equal(t, []byte("v1"), v)
}

func TestAppendEvent_StateDeltaUsesDotNotation(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	sess := newSessionForTest("app", "u", "s")
	e := &event.Event{
		StateDelta: map[string][]byte{
			"plain":    []byte("v1"),
			"with.dot": []byte("v2"),
		},
	}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	_, hasPlain := set["state.plain"]
	_, hasEncoded := set[`state.with\ddot`]
	assert.True(t, hasPlain)
	assert.True(t, hasEncoded, "key with '.' must be encoded into BSON dot path")
}

func TestAppendEvent_RejectsBadSessionKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	// AppName empty makes CheckSessionKey fail.
	sess := newSessionForTest("", "u", "s")
	require.Error(t, s.AppendEvent(context.Background(), sess, &event.Event{}))
	assert.Empty(t, mc.recorded(), "no client traffic on validation failure")
}

func TestAppendEvent_NoMatchingSessionReturnsNotFound(t *testing.T) {
	mc := &mockClient{
		updateOneFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return &mongo.UpdateResult{}, nil // MatchedCount=0
		},
	}
	s := newServiceForTest(t, mc)

	sess := newSessionForTest("app", "u", "s")
	err := s.AppendEvent(context.Background(), sess,
		&event.Event{StateDelta: map[string][]byte{"k": []byte("v")}})
	require.Error(t, err)
	assert.ErrorIs(t, err, errSessionNotFound)
}

func TestAppendEvent_RunsHookChain(t *testing.T) {
	called := false
	hook := session.AppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
		called = true
		return next()
	})
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.appendEventHooks = []session.AppendEventHook{hook}
	})
	sess := newSessionForTest("app", "u", "s")
	require.NoError(t, s.AppendEvent(context.Background(), sess,
		&event.Event{StateDelta: map[string][]byte{"k": []byte("v")}}))
	assert.True(t, called)
}

func TestAppendEvent_RejectsNilSession(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	require.ErrorIs(t, s.AppendEvent(context.Background(), nil, nil), session.ErrNilSession)
}

func TestAppendEvent_AsyncPathDispatchesToChan(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.enableAsyncPersist = true
		o.asyncPersisterNum = 1
	})
	s.persistChans = []chan *persistJob{make(chan *persistJob, 1)}
	sess := newSessionForTest("app", "u", "s")
	evt := nonPartialResponseEvent(t)

	require.NoError(t, s.AppendEvent(context.Background(), sess, evt))
	require.Len(t, s.persistChans[0], 1)
	job := <-s.persistChans[0]
	assert.Equal(t, session.Key{AppName: "app", UserID: "u", SessionID: "s"}, job.key)
	assert.Same(t, evt, job.event)
	assert.Nil(t, job.trackEvent)
	assert.Empty(t, mc.recorded(), "async enqueue should not persist synchronously")
}

func TestAsyncPersist_EventAndTrackShareSessionQueue(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.enableAsyncPersist = true
		o.asyncPersisterNum = 2
	})
	s.persistChans = []chan *persistJob{
		make(chan *persistJob, 2),
		make(chan *persistJob, 2),
	}
	sess := newSessionForTest("app", "u", "s")
	evt := nonPartialResponseEvent(t)
	trackEvt := trackEventForTest("alpha", `"payload"`, time.Now())

	require.NoError(t, s.AppendEvent(context.Background(), sess, evt))
	require.NoError(t, s.AppendTrackEvent(context.Background(), sess, trackEvt))

	index := sessionPersistIndex(session.Key{AppName: "app", UserID: "u", SessionID: "s"}, len(s.persistChans))
	otherIndex := (index + 1) % len(s.persistChans)
	require.Len(t, s.persistChans[index], 2)
	require.Len(t, s.persistChans[otherIndex], 0)
	eventJob := <-s.persistChans[index]
	trackJob := <-s.persistChans[index]
	assert.Same(t, evt, eventJob.event)
	assert.Nil(t, eventJob.trackEvent)
	assert.Same(t, trackEvt, trackJob.trackEvent)
	assert.Nil(t, trackJob.event)
}

func TestClose_DrainsAsyncWorker(t *testing.T) {
	var inserts atomic.Int64
	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
		insertOneFn: func(_ any) (*mongo.InsertOneResult, error) {
			inserts.Add(1)
			return &mongo.InsertOneResult{}, nil
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.enableAsyncPersist = true
		o.asyncPersisterNum = 2
	})
	s.startAsyncPersistWorker()
	sess := newSessionForTest("app", "u", "s")
	const n = 5
	for i := 0; i < n; i++ {
		require.NoError(t, s.AppendEvent(context.Background(), sess, nonPartialResponseEvent(t)))
	}

	require.NoError(t, s.Close())
	assert.Equal(t, int64(n), inserts.Load())
}

func TestClose_AfterCloseAppendEventDoesNotPanic(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.enableAsyncPersist = true
		o.asyncPersisterNum = 1
	})
	s.persistChans = []chan *persistJob{make(chan *persistJob, 1)}
	require.NoError(t, s.Close())

	sess := newSessionForTest("app", "u", "s")
	require.NotPanics(t, func() {
		err := s.AppendEvent(context.Background(), sess, nonPartialResponseEvent(t))
		require.NoError(t, err)
	})
}

// -- Track events -----------------------------------------------------------

func trackEventForTest(track session.Track, payload string, ts time.Time) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     track,
		Payload:   json.RawMessage(payload),
		Timestamp: ts,
	}
}

func mustTrackIndex(t *testing.T, tracks []session.Track) []byte {
	t.Helper()
	b, err := json.Marshal(tracks)
	require.NoError(t, err)
	return b
}

func TestAppendTrackEvent_UsesTransactionAndSessionTracks(t *testing.T) {
	now := time.Now()
	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(sessionStateDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				State:     bson.M{},
				CreatedAt: now,
				UpdatedAt: now,
			}, nil, nil)
		},
	}
	s := newServiceForTest(t, mc)
	sess := newSessionForTest("app", "u", "s")
	evt := trackEventForTest("alpha", `"payload"`, now)

	require.NoError(t, s.AppendTrackEvent(context.Background(), sess, evt))

	ops := mc.recorded()
	var sawTransaction, sawStateUpdate, sawTrackInsert bool
	for _, op := range ops {
		switch op.name {
		case "Transaction":
			sawTransaction = true
		case "UpdateOne":
			if op.coll == "session_states" {
				sawStateUpdate = true
				upd := op.update.(bson.M)
				set := upd["$set"].(bson.M)
				assert.Contains(t, set, "state.tracks")
				unset := upd["$unset"].(bson.M)
				assert.Contains(t, unset, "expires_at")
			}
		case "InsertOne":
			if op.coll == "session_tracks" {
				sawTrackInsert = true
				doc := op.doc.(sessionTrackDoc)
				assert.Equal(t, session.Track("alpha"), doc.Track)
				var persisted session.TrackEvent
				require.NoError(t, json.Unmarshal(doc.Event, &persisted))
				assert.Equal(t, evt.Track, persisted.Track)
			}
		}
	}
	assert.True(t, sawTransaction)
	assert.True(t, sawStateUpdate)
	assert.True(t, sawTrackInsert)
	tracks, err := session.TracksFromState(sess.State)
	require.NoError(t, err)
	assert.Equal(t, []session.Track{"alpha"}, tracks)
}

func TestAppendTrackEvent_AsyncPathDispatchesToChan(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.enableAsyncPersist = true
		o.asyncPersisterNum = 2
	})
	s.persistChans = []chan *persistJob{
		make(chan *persistJob, 2),
		make(chan *persistJob, 2),
	}
	sess := newSessionForTest("app", "u", "s")
	evt := trackEventForTest("alpha", `"payload"`, time.Now())
	evtOtherTrack := trackEventForTest("beta", `"payload"`, time.Now())

	require.NoError(t, s.AppendTrackEvent(context.Background(), sess, evt))
	require.NoError(t, s.AppendTrackEvent(context.Background(), sess, evtOtherTrack))
	index := session.HashString("app:u:s") % len(s.persistChans)
	otherIndex := (index + 1) % len(s.persistChans)
	require.Len(t, s.persistChans[index], 2)
	require.Len(t, s.persistChans[otherIndex], 0)
	job := <-s.persistChans[index]
	assert.Equal(t, session.Key{AppName: "app", UserID: "u", SessionID: "s"}, job.key)
	assert.Same(t, evt, job.trackEvent)
	assert.Nil(t, job.event)
	job = <-s.persistChans[index]
	assert.Equal(t, session.Key{AppName: "app", UserID: "u", SessionID: "s"}, job.key)
	assert.Same(t, evtOtherTrack, job.trackEvent)
	assert.Nil(t, job.event)
	assert.Empty(t, mc.recorded(), "async track enqueue should not persist synchronously")
}

func TestGetSession_LoadsTrackEvents(t *testing.T) {
	now := time.Now()
	trackEvent := trackEventForTest("alpha", `"payload"`, now)
	eventBytes, err := json.Marshal(trackEvent)
	require.NoError(t, err)
	stored := sessionStateDoc{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		State:     bson.M{"tracks": mustTrackIndex(t, []session.Track{"alpha"})},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}

	findCalls := 0
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(stored, nil, nil)
		},
		findFn: func(_ any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1, 2, 3: // app_states, user_states, session_events.
				return emptyCursor()
			case 4: // session_tracks.
				return docsCursor([]any{
					sessionTrackDoc{
						AppName:   "app",
						UserID:    "u",
						SessionID: "s",
						Track:     "alpha",
						Event:     eventBytes,
						CreatedAt: now,
						UpdatedAt: now,
					},
				})
			default:
				return emptyCursor()
			}
		},
	}
	s := newServiceForTest(t, mc)

	sess, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Contains(t, sess.Tracks, session.Track("alpha"))
	require.Len(t, sess.Tracks["alpha"].Events, 1)
	assert.Equal(t, json.RawMessage(`"payload"`), sess.Tracks["alpha"].Events[0].Payload)
}

func TestGetTrackEvents_BatchesSessionsAndTracks(t *testing.T) {
	now := time.Now()
	alphaOld := trackEventForTest("alpha", `"old"`, now.Add(-2*time.Minute))
	alphaNew := trackEventForTest("alpha", `"new"`, now.Add(-time.Minute))
	beta := trackEventForTest("beta", `"beta"`, now)
	alphaOldBytes, err := json.Marshal(alphaOld)
	require.NoError(t, err)
	alphaNewBytes, err := json.Marshal(alphaNew)
	require.NoError(t, err)
	betaBytes, err := json.Marshal(beta)
	require.NoError(t, err)

	findCalls := 0
	mc := &mockClient{
		findFn: func(filter any) (*mongo.Cursor, error) {
			findCalls++
			f := filter.(bson.M)
			assert.Equal(t, bson.M{"$in": []string{"s1", "s2"}}, f["session_id"])
			assert.Contains(t, f, "track")
			return docsCursor([]any{
				sessionTrackDoc{SessionID: "s1", Track: "alpha", Event: alphaNewBytes, CreatedAt: alphaNew.Timestamp},
				sessionTrackDoc{SessionID: "s1", Track: "alpha", Event: alphaOldBytes, CreatedAt: alphaOld.Timestamp},
				sessionTrackDoc{SessionID: "s2", Track: "beta", Event: betaBytes, CreatedAt: beta.Timestamp},
			})
		},
	}
	s := newServiceForTest(t, mc)

	got, err := s.getTrackEvents(context.Background(),
		[]session.Key{
			{AppName: "app", UserID: "u", SessionID: "s1"},
			{AppName: "app", UserID: "u", SessionID: "s2"},
		},
		[]sessionStateDoc{
			{State: bson.M{"tracks": mustTrackIndex(t, []session.Track{"alpha"})}},
			{State: bson.M{"tracks": mustTrackIndex(t, []session.Track{"beta"})}},
		},
		1,
		time.Time{},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, findCalls)
	require.Len(t, got[0]["alpha"], 1)
	assert.Equal(t, json.RawMessage(`"new"`), got[0]["alpha"][0].Payload)
	require.Len(t, got[1]["beta"], 1)
	assert.Equal(t, json.RawMessage(`"beta"`), got[1]["beta"][0].Payload)
}

func TestCleanupExpiredTracks_UsesSessionGroupCleanup(t *testing.T) {
	now := time.Now()
	mc := &mockClient{
		aggregateFn: func(pipeline any) (*mongo.Cursor, error) {
			stages := pipeline.(bson.A)
			require.Len(t, stages, 3)
			group := stages[1].(bson.M)["$group"].(bson.M)
			assert.Contains(t, group, "max_updated_at")
			return docsCursor([]any{
				bson.M{"_id": bson.M{"app_name": "app", "user_id": "u", "session_id": "s"}},
			})
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	require.NoError(t, s.cleanupExpiredTracks(context.Background(), now))

	ops := mc.recorded()
	require.Len(t, ops, 2)
	assert.Equal(t, "Aggregate", ops[0].name)
	assert.Equal(t, "session_tracks", ops[0].coll)
	assert.Equal(t, "UpdateMany", ops[1].name)
	assert.Equal(t, "session_tracks", ops[1].coll)
}

// -- WindowService ----------------------------------------------------------

func windowEventForTest(id string, role model.Role, content string, ts time.Time) event.Event {
	return event.Event{
		ID:        id,
		Author:    string(role),
		Timestamp: ts,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: role, Content: content},
		}}},
	}
}

func TestGetEventWindow_LoadsOrderedEntries(t *testing.T) {
	now := time.Now()
	sessionCreatedAt := now.Add(-10 * time.Minute)
	evts := []event.Event{
		windowEventForTest("u1", model.RoleUser, "one", now.Add(-3*time.Minute)),
		windowEventForTest("a1", model.RoleAssistant, "two", now.Add(-2*time.Minute)),
		windowEventForTest("u2", model.RoleUser, "three", now.Add(-time.Minute)),
	}
	ids := []primitive.ObjectID{
		primitive.NewObjectID(),
		primitive.NewObjectID(),
		primitive.NewObjectID(),
	}
	docs := make([]any, 0, len(evts))
	for i, evt := range evts {
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		docs = append(docs, sessionEventDoc{
			ID:        ids[i],
			AppName:   "app",
			UserID:    "u",
			SessionID: "s",
			EventID:   evt.ID,
			Event:     b,
			CreatedAt: evt.Timestamp,
			UpdatedAt: evt.Timestamp,
		})
	}
	findCalls := 0
	mc := &mockClient{
		findOneFn: func(filter any) *mongo.SingleResult {
			f := filter.(bson.M)
			if f["event_id"] == "a1" {
				createdAtFilter, ok := f["created_at"].(bson.M)
				require.True(t, ok)
				gotCreatedAt, ok := createdAtFilter["$gte"].(time.Time)
				require.True(t, ok)
				assert.WithinDuration(t, sessionCreatedAt, gotCreatedAt, time.Millisecond)
				return mongo.NewSingleResultFromDocument(docs[1], nil, nil)
			}
			return mongo.NewSingleResultFromDocument(sessionStateDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				CreatedAt: sessionCreatedAt,
				UpdatedAt: now,
			}, nil, nil)
		},
		findFn: func(filter any) (*mongo.Cursor, error) {
			findCalls++
			f := filter.(bson.M)
			assert.Equal(t, nil, f["deleted_at"])
			switch findCalls {
			case 1:
				assert.Contains(t, f, "$or")
				return docsCursor([]any{docs[0]})
			case 2:
				assert.Contains(t, f, "$or")
				return docsCursor([]any{docs[2]})
			default:
				return emptyCursor()
			}
		},
	}
	s := newServiceForTest(t, mc)

	got, err := s.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		AnchorEventID: "a1",
		Before:        1,
		After:         1,
	})
	require.NoError(t, err)
	require.Len(t, got.Entries, 3)
	assert.Equal(t, "u1", got.Entries[0].Event.ID)
	assert.Equal(t, "a1", got.Entries[1].Event.ID)
	assert.Equal(t, "u2", got.Entries[2].Event.ID)
	assert.False(t, got.Entries[1].CreatedAt.IsZero())
	assert.Equal(t, 2, findCalls)
}

func TestGetEventWindow_RejectsInvalidRequestBeforeQuery(t *testing.T) {
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			t.Fatal("FindOne should not run for invalid window request")
			return nil
		},
	}
	s := newServiceForTest(t, mc)

	_, err := s.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key: session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor event id is required")

	_, err = s.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		AnchorEventID: "a1",
		Before:        -1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before >= 0")
}

func TestGetEventWindow_MissingActiveSessionReturnsAnchorNotFound(t *testing.T) {
	mc := &mockClient{
		findOneFn: func(filter any) *mongo.SingleResult {
			f := filter.(bson.M)
			assert.Contains(t, f, "$or", "active session lookup must honor session expiry")
			return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
		},
		findFn: func(_ any) (*mongo.Cursor, error) {
			t.Fatal("event lookup should not run when active session is missing")
			return nil, nil
		},
	}
	s := newServiceForTest(t, mc)

	_, err := s.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           session.Key{AppName: "app", UserID: "u", SessionID: "s"},
		AnchorEventID: "a1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor event not found: a1")
}

func TestCreateSessionSummary_NoOpWithoutSummarizer(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	// No summarizer configured -> no error, no client traffic.
	require.NoError(t, s.CreateSessionSummary(context.Background(),
		newSessionForTest("app", "u", "s"), "", false))
	assert.Empty(t, mc.recorded())
}

func TestEnqueueSummaryJob_NoOpWithoutSummarizer(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.EnqueueSummaryJob(context.Background(),
		newSessionForTest("app", "u", "s"), "", false))
	assert.Empty(t, mc.recorded())
}

func TestGetSessionSummaryText_NilSession(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	got, ok := s.GetSessionSummaryText(context.Background(), nil)
	assert.False(t, ok)
	assert.Empty(t, got)
}

// -- Close ------------------------------------------------------------------

func TestClose_DelegatesToClient(t *testing.T) {
	var calls int
	mc := &mockClient{
		closeFn: func() error {
			calls++
			return nil
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	assert.Equal(t, 1, calls)
}

func TestCleanupExpiredEvents_AggregationIntegrity(t *testing.T) {
	now := time.Now()
	mc := &mockClient{
		aggregateFn: func(pipeline any) (*mongo.Cursor, error) {
			stages := pipeline.(bson.A)
			require.Len(t, stages, 3)
			match := stages[0].(bson.M)["$match"].(bson.M)
			assert.Equal(t, nil, match["deleted_at"])
			group := stages[1].(bson.M)["$group"].(bson.M)
			assert.Contains(t, group, "max_updated_at")
			return docsCursor([]any{
				bson.M{"_id": bson.M{"app_name": "app", "user_id": "u1", "session_id": "s1"}},
				bson.M{"_id": bson.M{"app_name": "app", "user_id": "u2", "session_id": "s2"}},
			})
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	require.NoError(t, s.cleanupExpiredEvents(context.Background(), now))

	ops := mc.recorded()
	require.Len(t, ops, 2)
	assert.Equal(t, "Aggregate", ops[0].name)
	assert.Equal(t, "UpdateMany", ops[1].name)
	filter := ops[1].filter.(bson.M)
	or := filter["$or"].(bson.A)
	require.Len(t, or, 2)
	assert.Contains(t, or, bson.M{"app_name": "app", "user_id": "u1", "session_id": "s1"})
	assert.Contains(t, or, bson.M{"app_name": "app", "user_id": "u2", "session_id": "s2"})
	assert.Equal(t, nil, filter["deleted_at"])
	updatedAt := filter["updated_at"].(bson.M)
	assert.Equal(t, now.Add(-time.Hour), updatedAt["$lte"])
	upd := ops[1].update.(bson.M)
	set := upd["$set"].(bson.M)
	assert.Equal(t, now, set["deleted_at"])
}

func TestCleanupExpiredEvents_NoMatchIsNoOp(t *testing.T) {
	mc := &mockClient{
		aggregateFn: func(_ any) (*mongo.Cursor, error) {
			return emptyCursor()
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	require.NoError(t, s.cleanupExpiredEvents(context.Background(), time.Now()))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	assert.Equal(t, "Aggregate", ops[0].name)
}

func TestCleanupExpired_NoSessionTTLIsNoOp(t *testing.T) {
	mc := &mockClient{
		aggregateFn: func(_ any) (*mongo.Cursor, error) {
			t.Fatal("Aggregate should not be called when sessionTTL is zero")
			return nil, nil
		},
	}
	s := newServiceForTest(t, mc)

	s.cleanupExpired()
	assert.Empty(t, mc.recorded())
}

func TestCleanupExpired_CleansEventsAndTracks(t *testing.T) {
	var collections []string
	var mc *mockClient
	mc = &mockClient{
		aggregateFn: func(_ any) (*mongo.Cursor, error) {
			ops := mc.recorded()
			collections = append(collections, ops[len(ops)-1].coll)
			return emptyCursor()
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.sessionTTL = time.Hour })

	s.cleanupExpired()

	assert.Equal(t, []string{"session_events", "session_tracks"}, collections)
}

func TestCleanupExpiredEvents_HardDeletePath(t *testing.T) {
	mc := &mockClient{
		aggregateFn: func(_ any) (*mongo.Cursor, error) {
			return docsCursor([]any{
				bson.M{"_id": bson.M{"app_name": "app", "user_id": "u", "session_id": "s"}},
			})
		},
	}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.sessionTTL = time.Hour
		o.softDelete = false
	})

	require.NoError(t, s.cleanupExpiredEvents(context.Background(), time.Now()))

	ops := mc.recorded()
	require.Len(t, ops, 2)
	assert.Equal(t, "Aggregate", ops[0].name)
	assert.Equal(t, "DeleteMany", ops[1].name)
	assert.Equal(t, "session_events", ops[1].coll)
}

func TestCleanupTicker_StartStop(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.sessionTTL = time.Hour
		o.cleanupInterval = time.Hour
	})

	s.startCleanupRoutine()
	require.NotNil(t, s.cleanupTicker)
	s.stopCleanupRoutine()
	assert.Nil(t, s.cleanupTicker)
}

// -- buildClientOpts (NewService precondition logic) ------------------------

func TestBuildClientOpts_URITakesPrecedence(t *testing.T) {
	opts := defaultOptions
	opts.uri = "mongodb://example/db"
	opts.instanceName = "anything"

	got, err := buildClientOpts(opts)
	require.NoError(t, err)
	require.NotEmpty(t, got)
}

func TestBuildClientOpts_RequiresOneOfURIOrInstance(t *testing.T) {
	_, err := buildClientOpts(defaultOptions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithMongoClientURI or WithMongoInstance")
}

func TestBuildClientOpts_UnknownInstance(t *testing.T) {
	opts := defaultOptions
	opts.instanceName = "definitely-not-registered-xyz"
	_, err := buildClientOpts(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestNewService_ProbesTransactionSupport(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	mc := &mockClient{
		transactionFn: func(fn storage.TxFunc) error {
			return fn(nil)
		},
	}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	s, err := NewService(WithMongoClientURI("mongodb://example"))
	require.NoError(t, err)
	require.NotNil(t, s)

	var sawTransaction, sawProbeFind bool
	for _, op := range mc.recorded() {
		if op.name == "Transaction" {
			sawTransaction = true
		}
		if op.name == "FindOne" && op.coll == "session_states" {
			sawProbeFind = true
			assert.Equal(t, bson.M{}, op.filter)
		}
	}
	assert.True(t, sawTransaction, "NewService must fail fast by probing transactions")
	assert.True(t, sawProbeFind, "transaction probe should perform a harmless read")
}

func TestNewService_SkipDBInitSkipsTransactionProbe(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	mc := &mockClient{
		ensureIndexesFn: func(_ []mongo.IndexModel) ([]string, error) {
			t.Fatal("EnsureIndexes should not be called when WithSkipDBInit(true) is set")
			return nil, nil
		},
		transactionFn: func(_ storage.TxFunc) error {
			t.Fatal("Transaction should not be called when WithSkipDBInit(true) is set")
			return nil
		},
	}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	s, err := NewService(
		WithMongoClientURI("mongodb://example"),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NotNil(t, s)

	for _, op := range mc.recorded() {
		assert.NotEqual(t, "EnsureIndexes", op.name)
		assert.NotEqual(t, "Transaction", op.name)
	}
}

func TestNewService_SessionTTLAutoCleanupInterval(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	mc := &mockClient{}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	s, err := NewService(
		WithMongoClientURI("mongodb://example"),
		WithSkipDBInit(true),
		WithSessionTTL(time.Hour),
	)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, defaultCleanupIntervalSecond, s.opts.cleanupInterval)
	assert.NotNil(t, s.cleanupTicker)
	require.NoError(t, s.Close())
}

func TestNewService_CleanupTickerRequiresSessionTTL(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	mc := &mockClient{}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	s, err := NewService(
		WithMongoClientURI("mongodb://example"),
		WithSkipDBInit(true),
		WithCleanupInterval(time.Millisecond),
	)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Nil(t, s.cleanupTicker)
	require.NoError(t, s.Close())
}

func TestNewService_TransactionProbeFailureClosesClient(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	want := errors.New("transactions unsupported")
	closed := false
	mc := &mockClient{
		transactionFn: func(_ storage.TxFunc) error {
			return want
		},
		closeFn: func() error {
			closed = true
			return nil
		},
	}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	_, err := NewService(WithMongoClientURI("mongodb://example"))
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
	assert.Contains(t, err.Error(), "replica set or sharded cluster")
	assert.True(t, closed)
}

// -- Sanity: errSessionNotFound is referenced in service code paths. --------

func TestErrSessionNotFoundExposed(t *testing.T) {
	// Just keep the symbol exercised so a future refactor that drops it
	// surfaces here rather than deep inside an integration test.
	require.ErrorIs(t, errSessionNotFound, errSessionNotFound)
	assert.True(t, errors.Is(errSessionNotFound, errSessionNotFound))
}

// -- Error propagation -----------------------------------------------------

func TestCreateSession_PropagatesGenericInsertError(t *testing.T) {
	mc := &mockClient{
		insertOneFn: func(_ any) (*mongo.InsertOneResult, error) {
			return nil, errors.New("boom")
		},
	}
	s := newServiceForTest(t, mc)
	_, err := s.CreateSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "already exists")
}

func TestGetSession_FindOneErrorPropagates(t *testing.T) {
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			return mongo.NewSingleResultFromDocument(bson.D{}, errors.New("decode boom"), nil)
		},
	}
	s := newServiceForTest(t, mc)
	_, err := s.GetSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"})
	require.Error(t, err)
}

func TestListAppStates_RejectsBlankAppName(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	_, err := s.ListAppStates(context.Background(), "")
	require.ErrorIs(t, err, session.ErrAppNameRequired)
}

func TestDeleteAppState_RejectsBlankAppName(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	require.ErrorIs(t, s.DeleteAppState(context.Background(), "", "k"), session.ErrAppNameRequired)
}

func TestDeleteUserState_RejectsBlankKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	require.Error(t, s.DeleteUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"}, ""))
}

func TestDeleteUserState_SoftDelete(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc) // softDelete=true
	require.NoError(t, s.DeleteUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"}, "k"))
	assert.Equal(t, "UpdateOne", mc.recorded()[0].name)
}

func TestUpdateUserState_RejectsBadKey(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	err := s.UpdateUserState(context.Background(),
		session.UserKey{AppName: ""}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}

func TestUpdateUserState_TTLSetsExpiresAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.userStateTTL = 30 * time.Minute })
	require.NoError(t, s.UpdateUserState(context.Background(),
		session.UserKey{AppName: "app", UserID: "u"},
		session.StateMap{"k": []byte("v")}))
	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	exp, ok := set["expires_at"].(*time.Time)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(30*time.Minute), *exp, 5*time.Second)
}

func TestUpdateAppState_PropagatesError(t *testing.T) {
	mc := &mockClient{
		updateOneFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, errors.New("boom")
		},
	}
	s := newServiceForTest(t, mc)
	err := s.UpdateAppState(context.Background(), "app", session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}
