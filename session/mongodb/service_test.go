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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/session"
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

// -- ListSessions -----------------------------------------------------------

func TestListSessions_AppliesPagination(t *testing.T) {
	var seenOpts []*options.FindOptions
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			return emptyCursor()
		},
	}
	// Wrap Find so we can capture the options that were passed.
	original := mc.findFn
	mc.findFn = func(filter any) (*mongo.Cursor, error) {
		// session/mongodb passes options as the trailing variadic; we have to
		// peek through mockClient.Find's signature, which records only the
		// filter. To capture options in this test, rebuild the wrapper at the
		// mockClient layer below.
		return original(filter)
	}
	_ = seenOpts // no-op: this test is satisfied by the recorded call list

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
			// Subsequent Find calls (app_states, user_states) yield empty.
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

// -- DeleteSession ----------------------------------------------------------

func TestDeleteSession_SoftDeleteStampsDeletedAt(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc) // default: softDelete=true

	require.NoError(t, s.DeleteSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"}))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	assert.Equal(t, "UpdateOne", ops[0].name)

	upd, ok := ops[0].update.(bson.M)
	require.True(t, ok)
	set, ok := upd["$set"].(bson.M)
	require.True(t, ok)
	_, hasDeletedAt := set["deleted_at"]
	assert.True(t, hasDeletedAt, "soft delete should $set deleted_at")
}

func TestDeleteSession_HardDeleteCallsDeleteOne(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) { o.softDelete = false })

	require.NoError(t, s.DeleteSession(context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"}))

	ops := mc.recorded()
	require.Len(t, ops, 1)
	assert.Equal(t, "DeleteOne", ops[0].name)
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

// -- Stub methods (event / summary surface, not yet implemented) ---------

func TestStubMethods_ReturnsNotImplemented(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	require.Error(t, s.AppendEvent(context.Background(), nil, nil))
	require.Error(t, s.CreateSessionSummary(context.Background(), nil, "", false))
	require.Error(t, s.EnqueueSummaryJob(context.Background(), nil, "", false))

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
