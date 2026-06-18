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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

// -- CreateSession ----------------------------------------------------------

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
	assert.Equal(t, "InsertOne", ops[0].name)
	assert.Equal(t, "session_states", ops[0].coll)

	doc, ok := ops[0].doc.(sessionStateDoc)
	require.True(t, ok, "InsertOne doc should be sessionStateDoc")
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

	doc := mc.recorded()[0].doc.(sessionStateDoc)
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

	doc := mc.recorded()[0].doc.(sessionStateDoc)
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
		findFn: func(_ any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1, 2: // app_states, user_states.
				return emptyCursor()
			case 3: // session_events.
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
		findFn: func(_ any) (*mongo.Cursor, error) {
			findCalls++
			switch findCalls {
			case 1, 2: // app_states, user_states.
				return emptyCursor()
			case 3: // paged events are read newest-first by query, then reversed.
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
	require.Len(t, ops, 4)
	assert.Equal(t, "Transaction", ops[0].name)
	assert.Equal(t, "UpdateOne", ops[1].name)
	assert.Equal(t, "session_states", ops[1].coll)
	assert.Equal(t, "UpdateMany", ops[2].name)
	assert.Equal(t, "session_events", ops[2].coll)
	assert.Equal(t, "UpdateMany", ops[3].name)
	assert.Equal(t, "session_summaries", ops[3].coll)

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
	require.Len(t, ops, 4)
	assert.Equal(t, "Transaction", ops[0].name)
	assert.Equal(t, "DeleteOne", ops[1].name)
	assert.Equal(t, "session_states", ops[1].coll)
	assert.Equal(t, "DeleteMany", ops[2].name)
	assert.Equal(t, "session_events", ops[2].coll)
	assert.Equal(t, "DeleteMany", ops[3].name)
	assert.Equal(t, "session_summaries", ops[3].coll)
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

	require.NoError(t, s.AppendEvent(context.Background(), sess, nonPartialResponseEvent(t)))

	// Expect: Transaction wrapper recorded plus the inner UpdateOne + InsertOne.
	var sawTransaction, sawUpdate, sawInsert bool
	for _, op := range mc.recorded() {
		switch op.name {
		case "Transaction":
			sawTransaction = true
		case "UpdateOne":
			if op.coll == "session_states" {
				sawUpdate = true
			}
		case "InsertOne":
			if op.coll == "session_events" {
				sawInsert = true
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
	called := false
	mc := &mockClient{
		closeFn: func() error {
			called = true
			return nil
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.Close())
	assert.True(t, called)
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
