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
	"testing"

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
	updateManyFn    func(ctx context.Context, db, coll string, filter, update any) (*mongo.UpdateResult, error)
	deleteOneFn     func(ctx context.Context, db, coll string, filter any) (*mongo.DeleteResult, error)
	deleteManyFn    func(ctx context.Context, db, coll string, filter any) (*mongo.DeleteResult, error)
	findOneFn       func(ctx context.Context, db, coll string, filter any) *mongo.SingleResult
	findFn          func(ctx context.Context, db, coll string, filter any) (*mongo.Cursor, error)
	aggregateFn     func(ctx context.Context, db, coll string, pipeline any) (*mongo.Cursor, error)
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

func (m *mockClient) UpdateMany(ctx context.Context, db, coll string, filter, update any,
	_ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if m.updateManyFn != nil {
		return m.updateManyFn(ctx, db, coll, filter, update)
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

func (m *mockClient) Aggregate(ctx context.Context, db, coll string, pipeline any,
	_ ...*options.AggregateOptions) (*mongo.Cursor, error) {
	if m.aggregateFn != nil {
		return m.aggregateFn(ctx, db, coll, pipeline)
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

	t.Run("UpdateMany dispatches", func(t *testing.T) {
		called := false
		mc := &mockClient{
			updateManyFn: func(_ context.Context, _, _ string, _, _ any) (*mongo.UpdateResult, error) {
				called = true
				return &mongo.UpdateResult{MatchedCount: 2, ModifiedCount: 2}, nil
			},
		}
		res, err := mc.UpdateMany(ctx, "db", "c", bson.M{"kind": "old"}, bson.M{"$set": bson.M{"deleted_at": true}})
		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, int64(2), res.ModifiedCount)
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

	t.Run("Aggregate dispatches", func(t *testing.T) {
		called := false
		mc := &mockClient{
			aggregateFn: func(_ context.Context, _, _ string, pipeline any) (*mongo.Cursor, error) {
				called = true
				assert.Equal(t, bson.A{bson.M{"$match": bson.M{"k": "v"}}}, pipeline)
				return mongo.NewCursorFromDocuments(nil, nil, nil)
			},
		}
		cursor, err := mc.Aggregate(ctx, "db", "c", bson.A{bson.M{"$match": bson.M{"k": "v"}}})
		require.NoError(t, err)
		require.NotNil(t, cursor)
		assert.True(t, called)
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
			transactionFn: func(ctx context.Context, fn TxFunc, opts ...TxOption) error {
				receivedOpts = len(opts)
				return fn(mongo.NewSessionContext(ctx, nil))
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

func TestTransactionRejectsNilCallback(t *testing.T) {
	dc := &defaultClient{}
	err := dc.Transaction(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TxFunc must not be nil")
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
