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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// resetRegistry resets the package-level registry before a test and restores
// it afterwards. This keeps registry tests isolated from each other.
func resetRegistry(t *testing.T) {
	t.Helper()
	old := mongodbRegistry
	mongodbRegistry = make(map[string][]ClientBuilderOpt)
	t.Cleanup(func() { mongodbRegistry = old })
}

// resetBuilder resets the package-level builder before a test and restores
// it afterwards.
func resetBuilder(t *testing.T) {
	t.Helper()
	old := globalBuilder
	t.Cleanup(func() { globalBuilder = old })
}

func TestRegisterAndGetMongoDBInstance(t *testing.T) {
	resetRegistry(t)

	const (
		name = "test-instance"
		uri  = "mongodb://localhost:27017"
	)
	RegisterMongoDBInstance(name, WithClientBuilderURI(uri))

	opts, ok := GetMongoDBInstance(name)
	require.True(t, ok)
	require.Len(t, opts, 1)

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	assert.Equal(t, uri, cfg.URI)
}

func TestGetMongoDBInstance_NotFound(t *testing.T) {
	resetRegistry(t)

	opts, ok := GetMongoDBInstance("missing")
	assert.False(t, ok)
	assert.Nil(t, opts)
}

func TestRegisterMongoDBInstance_Append(t *testing.T) {
	resetRegistry(t)

	const name = "appendable"
	RegisterMongoDBInstance(name, WithClientBuilderURI("mongodb://localhost:27017"))
	RegisterMongoDBInstance(name, WithExtraOptions("alpha"), WithExtraOptions("beta"))

	opts, ok := GetMongoDBInstance(name)
	require.True(t, ok)
	require.Len(t, opts, 3)

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	assert.Equal(t, []any{"alpha", "beta"}, cfg.ExtraOptions)
}

func TestSetAndGetClientBuilder(t *testing.T) {
	resetBuilder(t)

	invoked := false
	custom := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		invoked = true
		return nil, errors.New("custom builder")
	}
	SetClientBuilder(custom)

	b := GetClientBuilder()
	require.NotNil(t, b)

	_, err := b(context.Background(), WithClientBuilderURI("mongodb://localhost:27017"))
	assert.EqualError(t, err, "custom builder")
	assert.True(t, invoked)
}

func TestDefaultClientBuilder_EmptyURI(t *testing.T) {
	_, err := defaultClientBuilder(context.Background())
	require.Error(t, err)
	assert.Equal(t, "mongodb: uri is empty", err.Error())
}

func TestDefaultClientBuilder_InvalidURI(t *testing.T) {
	// A syntactically invalid URI is rejected by the driver during
	// options.Client().ApplyURI parsing inside mongo.Connect, before any
	// network I/O. The error must be wrapped under "mongodb: connect".
	_, err := defaultClientBuilder(context.Background(),
		WithClientBuilderURI("not-a-mongo-uri"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mongodb: connect")
}

func TestDefaultClientBuilder_PingError(t *testing.T) {
	// Use a connection string that resolves quickly but cannot be reached, so
	// Ping (called inside defaultClientBuilder) fails.
	const uri = "mongodb://127.0.0.1:1/?serverSelectionTimeoutMS=50&connectTimeoutMS=50"

	_, err := defaultClientBuilder(context.Background(), WithClientBuilderURI(uri))
	require.Error(t, err)
	// Either the connect or ping path may fire first depending on driver
	// internals, but the error must be wrapped under our "mongodb:" prefix.
	assert.Contains(t, err.Error(), "mongodb:")
}

// mockClient is a manual mock of Client used for table-driven tests.
type mockClient struct {
	insertOneFn     func(ctx context.Context, db, coll string, doc any) (*mongo.InsertOneResult, error)
	updateOneFn     func(ctx context.Context, db, coll string, filter, update any) (*mongo.UpdateResult, error)
	deleteOneFn     func(ctx context.Context, db, coll string, filter any) (*mongo.DeleteResult, error)
	deleteManyFn    func(ctx context.Context, db, coll string, filter any) (*mongo.DeleteResult, error)
	findOneFn       func(ctx context.Context, db, coll string, filter any) *mongo.SingleResult
	findFn          func(ctx context.Context, db, coll string, filter any) (*mongo.Cursor, error)
	ensureIndexesFn func(ctx context.Context, db, coll string, models []mongo.IndexModel) ([]string, error)
	transactionFn   func(ctx context.Context, fn TxFunc, opts ...TxOption) error
	closeFn         func(ctx context.Context) error
}

func (m *mockClient) InsertOne(ctx context.Context, db, coll string, doc any,
	_ ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	if m.insertOneFn != nil {
		return m.insertOneFn(ctx, db, coll, doc)
	}
	return &mongo.InsertOneResult{}, nil
}

func (m *mockClient) UpdateOne(ctx context.Context, db, coll string, filter, update any,
	_ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if m.updateOneFn != nil {
		return m.updateOneFn(ctx, db, coll, filter, update)
	}
	return &mongo.UpdateResult{}, nil
}

func (m *mockClient) DeleteOne(ctx context.Context, db, coll string, filter any,
	_ ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteOneFn != nil {
		return m.deleteOneFn(ctx, db, coll, filter)
	}
	return &mongo.DeleteResult{}, nil
}

func (m *mockClient) DeleteMany(ctx context.Context, db, coll string, filter any,
	_ ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteManyFn != nil {
		return m.deleteManyFn(ctx, db, coll, filter)
	}
	return &mongo.DeleteResult{}, nil
}

func (m *mockClient) FindOne(ctx context.Context, db, coll string, filter any,
	_ ...*options.FindOneOptions) *mongo.SingleResult {
	if m.findOneFn != nil {
		return m.findOneFn(ctx, db, coll, filter)
	}
	return nil
}

func (m *mockClient) Find(ctx context.Context, db, coll string, filter any,
	_ ...*options.FindOptions) (*mongo.Cursor, error) {
	if m.findFn != nil {
		return m.findFn(ctx, db, coll, filter)
	}
	return nil, nil
}

func (m *mockClient) EnsureIndexes(ctx context.Context, db, coll string, models []mongo.IndexModel,
	_ ...*options.CreateIndexesOptions) ([]string, error) {
	if m.ensureIndexesFn != nil {
		return m.ensureIndexesFn(ctx, db, coll, models)
	}
	return nil, nil
}

func (m *mockClient) Transaction(ctx context.Context, fn TxFunc, opts ...TxOption) error {
	if m.transactionFn != nil {
		return m.transactionFn(ctx, fn, opts...)
	}
	return nil
}

func (m *mockClient) Close(ctx context.Context) error {
	if m.closeFn != nil {
		return m.closeFn(ctx)
	}
	return nil
}

// TestMockClientInterfaceCompliance verifies mockClient implements Client.
func TestMockClientInterfaceCompliance(t *testing.T) {
	var _ Client = (*mockClient)(nil)
}

// TestDefaultClientInterfaceCompliance verifies defaultClient implements Client.
func TestDefaultClientInterfaceCompliance(t *testing.T) {
	var _ Client = (*defaultClient)(nil)
}

func TestMockClientDispatch(t *testing.T) {
	ctx := context.Background()

	t.Run("InsertOne dispatches and propagates error", func(t *testing.T) {
		want := errors.New("insert err")
		mc := &mockClient{
			insertOneFn: func(_ context.Context, _, _ string, _ any) (*mongo.InsertOneResult, error) {
				return nil, want
			},
		}
		_, err := mc.InsertOne(ctx, "db", "c", bson.M{"k": "v"})
		assert.ErrorIs(t, err, want)
	})

	t.Run("UpdateOne dispatches", func(t *testing.T) {
		called := false
		mc := &mockClient{
			updateOneFn: func(_ context.Context, _, _ string, _, _ any) (*mongo.UpdateResult, error) {
				called = true
				return &mongo.UpdateResult{ModifiedCount: 1}, nil
			},
		}
		res, err := mc.UpdateOne(ctx, "db", "c", bson.M{}, bson.M{"$set": bson.M{"k": "v"}})
		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, int64(1), res.ModifiedCount)
	})

	t.Run("DeleteOne / DeleteMany default to empty results", func(t *testing.T) {
		res1, err := (&mockClient{}).DeleteOne(ctx, "db", "c", bson.M{})
		require.NoError(t, err)
		assert.NotNil(t, res1)

		res2, err := (&mockClient{}).DeleteMany(ctx, "db", "c", bson.M{})
		require.NoError(t, err)
		assert.NotNil(t, res2)
	})

	t.Run("FindOne / Find dispatch", func(t *testing.T) {
		var calls int
		mc := &mockClient{
			findOneFn: func(_ context.Context, _, _ string, _ any) *mongo.SingleResult {
				calls++
				return nil
			},
			findFn: func(_ context.Context, _, _ string, _ any) (*mongo.Cursor, error) {
				calls++
				return nil, nil
			},
		}
		mc.FindOne(ctx, "db", "c", bson.M{})
		_, _ = mc.Find(ctx, "db", "c", bson.M{})
		assert.Equal(t, 2, calls)
	})

	t.Run("EnsureIndexes dispatches", func(t *testing.T) {
		mc := &mockClient{
			ensureIndexesFn: func(_ context.Context, _, _ string, models []mongo.IndexModel) ([]string, error) {
				names := make([]string, len(models))
				for i := range models {
					names[i] = "idx"
				}
				return names, nil
			},
		}
		got, err := mc.EnsureIndexes(ctx, "db", "c",
			[]mongo.IndexModel{{Keys: bson.D{{Key: "k", Value: 1}}}, {Keys: bson.D{{Key: "k2", Value: 1}}}})
		require.NoError(t, err)
		assert.Equal(t, []string{"idx", "idx"}, got)
	})

	t.Run("Transaction dispatches and forwards options", func(t *testing.T) {
		var receivedOpts int
		mc := &mockClient{
			transactionFn: func(_ context.Context, fn TxFunc, opts ...TxOption) error {
				receivedOpts = len(opts)
				return fn(nil)
			},
		}
		called := false
		err := mc.Transaction(ctx, func(_ mongo.SessionContext) error {
			called = true
			return nil
		}, WithTransactionOptions(options.Transaction()))
		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, 1, receivedOpts)
	})

	t.Run("Close dispatches", func(t *testing.T) {
		var called bool
		mc := &mockClient{
			closeFn: func(_ context.Context) error {
				called = true
				return nil
			},
		}
		require.NoError(t, mc.Close(ctx))
		assert.True(t, called)
	})
}

func TestEnsureIndexesEmpty(t *testing.T) {
	dc := &defaultClient{}
	names, err := dc.EnsureIndexes(context.Background(), "db", "c", nil)
	require.NoError(t, err)
	assert.Nil(t, names)
}

func TestTxOptionConstructors(t *testing.T) {
	t.Run("WithTransactionOptions sets field", func(t *testing.T) {
		o := &TxOptions{}
		got := options.Transaction()
		WithTransactionOptions(got)(o)
		assert.Same(t, got, o.Transaction)
	})

	t.Run("WithSessionOptions sets field", func(t *testing.T) {
		o := &TxOptions{}
		got := options.Session()
		WithSessionOptions(got)(o)
		assert.Same(t, got, o.Session)
	})
}

// -- ClientBuilderOpt tests ---------------------------------------------------

func TestWithClientBuilderURI(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithClientBuilderURI("mongodb://localhost:27017")(opts)
	assert.Equal(t, "mongodb://localhost:27017", opts.URI)

	// Test overwrite
	WithClientBuilderURI("mongodb://otherhost:27017")(opts)
	assert.Equal(t, "mongodb://otherhost:27017", opts.URI)
}

func TestWithExtraOptions(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithExtraOptions("opt1", "opt2")(opts)
	assert.Len(t, opts.ExtraOptions, 2)
	assert.Equal(t, "opt1", opts.ExtraOptions[0])
	assert.Equal(t, "opt2", opts.ExtraOptions[1])
}

func TestWithExtraOptions_Append(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithExtraOptions("opt1")(opts)
	WithExtraOptions("opt2", "opt3")(opts)
	assert.Equal(t, []any{"opt1", "opt2", "opt3"}, opts.ExtraOptions)
}

func TestClientBuilderOptsDefaults(t *testing.T) {
	opts := &ClientBuilderOpts{}
	assert.Empty(t, opts.URI)
	assert.Nil(t, opts.ExtraOptions)
}

func TestWithExtraOptions_Empty(t *testing.T) {
	opts := &ClientBuilderOpts{}
	WithExtraOptions()(opts)
	assert.Empty(t, opts.ExtraOptions)
}

func TestWithExtraOptions_MixedTypes(t *testing.T) {
	opts := &ClientBuilderOpts{}

	type custom struct{ Name string }
	WithExtraOptions("string", 123, true, custom{"test"})(opts)
	require.Len(t, opts.ExtraOptions, 4)
	assert.Equal(t, "string", opts.ExtraOptions[0])
	assert.Equal(t, 123, opts.ExtraOptions[1])
	assert.Equal(t, true, opts.ExtraOptions[2])
	assert.Equal(t, custom{"test"}, opts.ExtraOptions[3])
}

// -- Integration tests --------------------------------------------------------
//
// The tests below exercise defaultClient against a real MongoDB deployment
// when MONGO_URI is set; they skip otherwise. Tests that exercise multi-doc
// transactions skip cleanly on standalone deployments that do not support
// them.

// integrationURI returns the MongoDB URI to use for integration tests, or
// skips the calling test when the environment variable is unset.
func integrationURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set; skipping integration test")
	}
	return uri
}

// integrationDB returns a unique database name for the current test run, so
// concurrent runs and leftover state from previous runs do not collide.
func integrationDB(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("trpc_agent_go_storage_mongodb_test_%d", time.Now().UnixNano())
}

// newIntegrationClient connects to the MongoDB deployment described by
// MONGO_URI and registers cleanup for both the client and the test database.
func newIntegrationClient(t *testing.T) (Client, string) {
	t.Helper()

	uri := integrationURI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := defaultClientBuilder(ctx, WithClientBuilderURI(uri))
	require.NoError(t, err, "connect to MongoDB at %s", uri)

	dbName := integrationDB(t)
	t.Cleanup(func() {
		// Best-effort cleanup; ignore errors so tests still report the
		// underlying assertion failure rather than a teardown failure.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if dc, ok := client.(*defaultClient); ok {
			_ = dc.client.Database(dbName).Drop(dropCtx)
		}
		_ = client.Close(dropCtx)
	})
	return client, dbName
}

func TestIntegration_CRUD(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "items"

	// Seed three documents via InsertOne (the only insert primitive on
	// Client).
	for _, doc := range []bson.M{
		{"_id": "a", "v": 1},
		{"_id": "b", "v": 2},
		{"_id": "c", "v": 3},
	} {
		_, err := client.InsertOne(ctx, db, coll, doc)
		require.NoError(t, err)
	}

	// FindOne round-trip.
	var doc struct {
		ID string `bson:"_id"`
		V  int    `bson:"v"`
	}
	require.NoError(t, client.FindOne(ctx, db, coll, bson.M{"_id": "a"}).Decode(&doc))
	assert.Equal(t, "a", doc.ID)
	assert.Equal(t, 1, doc.V)

	// UpdateOne.
	updRes, err := client.UpdateOne(ctx, db, coll,
		bson.M{"_id": "a"}, bson.M{"$set": bson.M{"v": 10}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), updRes.ModifiedCount)

	// Find + cursor iteration returns all three documents in id order.
	cursor, err := client.Find(ctx, db, coll, bson.M{},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	require.NoError(t, err)
	defer cursor.Close(ctx)

	var all []bson.M
	require.NoError(t, cursor.All(ctx, &all))
	require.Len(t, all, 3)
	assert.Equal(t, "a", all[0]["_id"])
	assert.EqualValues(t, 10, all[0]["v"])

	// DeleteOne removes a single matching document.
	delRes, err := client.DeleteOne(ctx, db, coll, bson.M{"_id": "a"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), delRes.DeletedCount)

	// DeleteMany removes the rest.
	delMany, err := client.DeleteMany(ctx, db, coll, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), delMany.DeletedCount)
}

func TestIntegration_EnsureIndexes(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "indexed"

	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "app_name", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().SetName("by_app_user"),
		},
		{
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetName("ttl_expires_at").SetExpireAfterSeconds(0),
		},
	}

	// First call creates the indexes.
	names, err := client.EnsureIndexes(ctx, db, coll, models)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"by_app_user", "ttl_expires_at"}, names)

	// Second call is idempotent: same models returned, no error.
	namesAgain, err := client.EnsureIndexes(ctx, db, coll, models)
	require.NoError(t, err)
	assert.ElementsMatch(t, names, namesAgain)
}

func TestIntegration_Transaction_CommitAndAbort(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "tx"

	t.Run("commit on success", func(t *testing.T) {
		err := client.Transaction(ctx, func(sc mongo.SessionContext) error {
			_, err := client.InsertOne(sc, db, coll, bson.M{"_id": "ok", "v": 1})
			return err
		})
		// Transactions require a replica set / sharded cluster. If the
		// configured deployment is standalone, the driver returns a clear
		// error and we skip the rest of this subtest.
		if err != nil && isUnsupportedTransactionError(err) {
			t.Skipf("MongoDB deployment does not support transactions: %v", err)
		}
		require.NoError(t, err)

		var got bson.M
		require.NoError(t, client.FindOne(ctx, db, coll, bson.M{"_id": "ok"}).Decode(&got))
	})

	t.Run("rollback on error", func(t *testing.T) {
		boom := errors.New("rollback please")
		err := client.Transaction(ctx, func(sc mongo.SessionContext) error {
			if _, err := client.InsertOne(sc, db, coll, bson.M{"_id": "abort", "v": 1}); err != nil {
				return err
			}
			return boom
		})
		if err != nil && isUnsupportedTransactionError(err) {
			t.Skipf("MongoDB deployment does not support transactions: %v", err)
		}
		require.ErrorIs(t, err, boom)

		// The aborted insert must not be visible.
		err = client.FindOne(ctx, db, coll, bson.M{"_id": "abort"}).Err()
		assert.ErrorIs(t, err, mongo.ErrNoDocuments)
	})
}

// isUnsupportedTransactionError reports whether err looks like
// "transactions are not supported by this deployment". The MongoDB driver
// does not export a sentinel for this, so we match on the well-known
// message fragments.
func isUnsupportedTransactionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, frag := range []string{
		"Transaction numbers are only allowed on a replica set",
		"Transactions are not supported",
		"transactions are not supported",
	} {
		if containsFold(msg, frag) {
			return true
		}
	}
	return false
}

// containsFold is a tiny case-insensitive substring check; we avoid pulling
// in strings.EqualFold-based helpers for one site.
func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFoldASCII(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func TestIntegration_Close(t *testing.T) {
	uri := integrationURI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := defaultClientBuilder(ctx, WithClientBuilderURI(uri))
	require.NoError(t, err)

	require.NoError(t, client.Close(ctx))
}
