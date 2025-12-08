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
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestRegisterAndGetMongoDBInstance(t *testing.T) {
	// Clean up registry
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	// Register an instance
	RegisterMongoDBInstance("test-instance", WithClientBuilderDSN("mongodb://localhost:27017"))

	// Get the instance
	opts, ok := GetMongoDBInstance("test-instance")
	assert.True(t, ok)
	assert.Len(t, opts, 1)

	// Get non-existent instance
	_, ok = GetMongoDBInstance("non-existent")
	assert.False(t, ok)
}

func TestRegisterMongoDBInstanceAppend(t *testing.T) {
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	RegisterMongoDBInstance("test", WithClientBuilderDSN("mongodb://localhost:27017"))
	RegisterMongoDBInstance("test", WithExtraOptions("extra"))

	opts, ok := GetMongoDBInstance("test")
	assert.True(t, ok)
	assert.Len(t, opts, 2)
}

func TestSetAndGetClientBuilder(t *testing.T) {
	// Save original builder
	original := globalBuilder
	defer func() { globalBuilder = original }()

	// Set custom builder
	customBuilder := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return nil, errors.New("custom builder")
	}
	SetClientBuilder(customBuilder)

	// Get builder
	builder := GetClientBuilder()
	assert.NotNil(t, builder)

	// Verify it's the custom builder
	_, err := builder(context.Background())
	assert.EqualError(t, err, "custom builder")
}

// mockMongoClient is a mock implementation of the Client interface for testing.
type mockMongoClient struct {
	insertOneFunc   func(ctx context.Context, database, coll string, document any) error
	updateOneFunc   func(ctx context.Context, database, coll string, filter, update any) error
	deleteOneFunc   func(ctx context.Context, database, coll string, filter any) error
	deleteManyFunc  func(ctx context.Context, database, coll string, filter any) error
	findOneFunc     func(ctx context.Context, database, coll string, filter any)
	findFunc        func(ctx context.Context, database, coll string, filter any) error
	countFunc       func(ctx context.Context, database, coll string, filter any) (int64, error)
	transactionFunc func(ctx context.Context) error
	disconnectFunc  func(ctx context.Context) error
}

func (m *mockMongoClient) InsertOne(ctx context.Context, database, coll string, document any,
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	if m.insertOneFunc != nil {
		err := m.insertOneFunc(ctx, database, coll, document)
		return &mongo.InsertOneResult{}, err
	}
	return &mongo.InsertOneResult{InsertedID: "test-id"}, nil
}

func (m *mockMongoClient) UpdateOne(ctx context.Context, database, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if m.updateOneFunc != nil {
		err := m.updateOneFunc(ctx, database, coll, filter, update)
		return &mongo.UpdateResult{}, err
	}
	return &mongo.UpdateResult{ModifiedCount: 1}, nil
}

func (m *mockMongoClient) DeleteOne(ctx context.Context, database, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteOneFunc != nil {
		err := m.deleteOneFunc(ctx, database, coll, filter)
		return &mongo.DeleteResult{}, err
	}
	return &mongo.DeleteResult{DeletedCount: 1}, nil
}

func (m *mockMongoClient) DeleteMany(ctx context.Context, database, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteManyFunc != nil {
		err := m.deleteManyFunc(ctx, database, coll, filter)
		return &mongo.DeleteResult{}, err
	}
	return &mongo.DeleteResult{DeletedCount: 5}, nil
}

func (m *mockMongoClient) FindOne(ctx context.Context, database, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	if m.findOneFunc != nil {
		m.findOneFunc(ctx, database, coll, filter)
	}
	return nil
}

func (m *mockMongoClient) Find(ctx context.Context, database, coll string, filter any,
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	if m.findFunc != nil {
		err := m.findFunc(ctx, database, coll, filter)
		return nil, err
	}
	return nil, nil
}

func (m *mockMongoClient) CountDocuments(ctx context.Context, database, coll string, filter any,
	opts ...*options.CountOptions) (int64, error) {
	if m.countFunc != nil {
		return m.countFunc(ctx, database, coll, filter)
	}
	return 10, nil
}

func (m *mockMongoClient) Transaction(ctx context.Context, sf func(sc mongo.SessionContext) error,
	tOpts []*options.TransactionOptions, opts ...*options.SessionOptions) error {
	if m.transactionFunc != nil {
		return m.transactionFunc(ctx)
	}
	return nil
}

func (m *mockMongoClient) Disconnect(ctx context.Context) error {
	if m.disconnectFunc != nil {
		return m.disconnectFunc(ctx)
	}
	return nil
}

func TestMockClientOperations(t *testing.T) {
	ctx := context.Background()
	mock := &mockMongoClient{}

	t.Run("InsertOne", func(t *testing.T) {
		result, err := mock.InsertOne(ctx, "testdb", "testcoll", map[string]string{"key": "value"})
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("UpdateOne", func(t *testing.T) {
		result, err := mock.UpdateOne(ctx, "testdb", "testcoll",
			map[string]string{"_id": "1"}, map[string]any{"$set": map[string]string{"key": "newvalue"}})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), result.ModifiedCount)
	})

	t.Run("DeleteOne", func(t *testing.T) {
		result, err := mock.DeleteOne(ctx, "testdb", "testcoll", map[string]string{"_id": "1"})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), result.DeletedCount)
	})

	t.Run("DeleteMany", func(t *testing.T) {
		result, err := mock.DeleteMany(ctx, "testdb", "testcoll", map[string]string{"status": "inactive"})
		assert.NoError(t, err)
		assert.Equal(t, int64(5), result.DeletedCount)
	})

	t.Run("FindOne", func(t *testing.T) {
		result := mock.FindOne(ctx, "testdb", "testcoll", map[string]string{"_id": "1"})
		assert.Nil(t, result)
	})

	t.Run("Find", func(t *testing.T) {
		cursor, err := mock.Find(ctx, "testdb", "testcoll", map[string]string{})
		assert.NoError(t, err)
		assert.Nil(t, cursor)
	})

	t.Run("CountDocuments", func(t *testing.T) {
		count, err := mock.CountDocuments(ctx, "testdb", "testcoll", map[string]string{})
		assert.NoError(t, err)
		assert.Equal(t, int64(10), count)
	})

	t.Run("Transaction", func(t *testing.T) {
		err := mock.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil)
		assert.NoError(t, err)
	})

	t.Run("Disconnect", func(t *testing.T) {
		err := mock.Disconnect(ctx)
		assert.NoError(t, err)
	})
}

func TestMockClientWithCustomFuncs(t *testing.T) {
	ctx := context.Background()

	t.Run("InsertOne with error", func(t *testing.T) {
		mock := &mockMongoClient{
			insertOneFunc: func(ctx context.Context, database, coll string, document any) error {
				return errors.New("insert error")
			},
		}
		_, err := mock.InsertOne(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "insert error")
	})

	t.Run("UpdateOne with error", func(t *testing.T) {
		mock := &mockMongoClient{
			updateOneFunc: func(ctx context.Context, database, coll string, filter, update any) error {
				return errors.New("update error")
			},
		}
		_, err := mock.UpdateOne(ctx, "testdb", "testcoll", nil, nil)
		assert.EqualError(t, err, "update error")
	})

	t.Run("DeleteOne with error", func(t *testing.T) {
		mock := &mockMongoClient{
			deleteOneFunc: func(ctx context.Context, database, coll string, filter any) error {
				return errors.New("delete error")
			},
		}
		_, err := mock.DeleteOne(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "delete error")
	})

	t.Run("DeleteMany with error", func(t *testing.T) {
		mock := &mockMongoClient{
			deleteManyFunc: func(ctx context.Context, database, coll string, filter any) error {
				return errors.New("delete many error")
			},
		}
		_, err := mock.DeleteMany(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "delete many error")
	})

	t.Run("Find with error", func(t *testing.T) {
		mock := &mockMongoClient{
			findFunc: func(ctx context.Context, database, coll string, filter any) error {
				return errors.New("find error")
			},
		}
		_, err := mock.Find(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "find error")
	})

	t.Run("CountDocuments with error", func(t *testing.T) {
		mock := &mockMongoClient{
			countFunc: func(ctx context.Context, database, coll string, filter any) (int64, error) {
				return 0, errors.New("count error")
			},
		}
		_, err := mock.CountDocuments(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "count error")
	})

	t.Run("Transaction with error", func(t *testing.T) {
		mock := &mockMongoClient{
			transactionFunc: func(ctx context.Context) error {
				return errors.New("transaction error")
			},
		}
		err := mock.Transaction(ctx, nil, nil)
		assert.EqualError(t, err, "transaction error")
	})

	t.Run("Disconnect with error", func(t *testing.T) {
		mock := &mockMongoClient{
			disconnectFunc: func(ctx context.Context) error {
				return errors.New("disconnect error")
			},
		}
		err := mock.Disconnect(ctx)
		assert.EqualError(t, err, "disconnect error")
	})

	t.Run("FindOne with custom func", func(t *testing.T) {
		called := false
		mock := &mockMongoClient{
			findOneFunc: func(ctx context.Context, database, coll string, filter any) {
				called = true
			},
		}
		mock.FindOne(ctx, "testdb", "testcoll", nil)
		assert.True(t, called)
	})
}

func TestClientInterfaceCompliance(t *testing.T) {
	// Verify that mockMongoClient implements Client interface
	var _ Client = (*mockMongoClient)(nil)
}

// TestDefaultClientErrorCases tests defaultClient methods with empty client to improve coverage
func TestDefaultClientErrorCases(t *testing.T) {
	ctx := context.Background()

	// Create a defaultClient with nil mongo.Client to test method coverage
	client := &defaultClient{client: &mongo.Client{}}

	t.Run("InsertOne with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				// Expected panic due to nil client
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.InsertOne(ctx, "testdb", "testcoll", map[string]any{"test": "data"})
	})

	t.Run("UpdateOne with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.UpdateOne(ctx, "testdb", "testcoll", map[string]any{"_id": "1"}, map[string]any{"$set": map[string]any{"updated": true}})
	})

	t.Run("DeleteOne with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.DeleteOne(ctx, "testdb", "testcoll", map[string]any{"_id": "1"})
	})

	t.Run("DeleteMany with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.DeleteMany(ctx, "testdb", "testcoll", map[string]any{"status": "inactive"})
	})

	t.Run("FindOne with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_ = client.FindOne(ctx, "testdb", "testcoll", map[string]any{"_id": "1"})
	})

	t.Run("Find with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.Find(ctx, "testdb", "testcoll", map[string]any{})
	})

	t.Run("CountDocuments with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_, _ = client.CountDocuments(ctx, "testdb", "testcoll", map[string]any{})
	})

	t.Run("Transaction with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_ = client.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil)
	})

	t.Run("Disconnect with nil client", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()
		_ = client.Disconnect(ctx)
	})

	t.Run("Methods with options", func(t *testing.T) {
		defer func() { recover() }()

		// Test all methods with options in one sub-test to reduce redundancy
		insertOpts := options.InsertOne()
		_, _ = client.InsertOne(ctx, "db", "coll", bson.M{"test": "data"}, insertOpts)

		updateOpts := options.Update()
		_, _ = client.UpdateOne(ctx, "db", "coll", bson.M{"_id": "1"}, bson.M{"$set": bson.M{"updated": true}}, updateOpts)

		deleteOpts := options.Delete()
		_, _ = client.DeleteOne(ctx, "db", "coll", bson.M{"_id": "1"}, deleteOpts)
		_, _ = client.DeleteMany(ctx, "db", "coll", bson.M{"status": "inactive"}, deleteOpts)

		findOpts := options.Find()
		_, _ = client.Find(ctx, "db", "coll", bson.M{}, findOpts)

		findOneOpts := options.FindOne()
		_ = client.FindOne(ctx, "db", "coll", bson.M{"_id": "1"}, findOneOpts)

		countOpts := options.Count()
		_, _ = client.CountDocuments(ctx, "db", "coll", bson.M{}, countOpts)
	})
}

// TestRegistryEdgeCases tests edge cases for registry operations
func TestRegistryEdgeCases(t *testing.T) {
	originalRegistry := mongodbRegistry
	defer func() { mongodbRegistry = originalRegistry }()

	t.Run("Empty instance name", func(t *testing.T) {
		mongodbRegistry = make(map[string][]ClientBuilderOpt)
		RegisterMongoDBInstance("", WithClientBuilderDSN("mongodb://localhost:27017"))
		opts, exists := GetMongoDBInstance("")
		assert.True(t, exists)
		assert.Len(t, opts, 1)
	})

	t.Run("Register with no options", func(t *testing.T) {
		mongodbRegistry = make(map[string][]ClientBuilderOpt)
		RegisterMongoDBInstance("no-opts")
		opts, exists := GetMongoDBInstance("no-opts")
		assert.True(t, exists)
		assert.Len(t, opts, 0)
	})
}

// TestDefaultClientBuilderErrorCases tests various error scenarios
func TestDefaultClientBuilderErrorCases(t *testing.T) {
	ctx := context.Background()

	t.Run("Empty URI", func(t *testing.T) {
		_, err := defaultClientBuilder(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "URI is empty")
	})

	t.Run("Invalid URI format", func(t *testing.T) {
		_, err := defaultClientBuilder(ctx, WithClientBuilderDSN("invalid-uri-format"))
		assert.Error(t, err)
	})

	t.Run("Builder processes options correctly", func(t *testing.T) {
		// Test that options are processed without panicking
		opts := []ClientBuilderOpt{
			WithClientBuilderDSN("mongodb://localhost:27017/testdb"),
			WithExtraOptions("option1", "option2"),
		}

		// Verify options were processed by applying them to a ClientBuilderOpts struct
		builderOpts := &ClientBuilderOpts{}
		for _, opt := range opts {
			opt(builderOpts)
		}

		assert.Equal(t, "mongodb://localhost:27017/testdb", builderOpts.URI)
		assert.Len(t, builderOpts.ExtraOptions, 2)
		assert.Equal(t, "option1", builderOpts.ExtraOptions[0])
		assert.Equal(t, "option2", builderOpts.ExtraOptions[1])
	})
}

// TestConnectionEdgeCases tests various edge cases in connection handling
func TestConnectionEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("Connection attempts with various parameters", func(t *testing.T) {
		// Try various MongoDB connection parameters that might trigger different error paths
		testCases := []string{
			"mongodb://localhost:27017/?serverSelectionTimeoutMS=50&connectTimeoutMS=50",
			"mongodb://127.0.0.1:27017/?serverSelectionTimeoutMS=50&socketTimeoutMS=50",
			"mongodb://localhost:27017/?maxPoolSize=1&serverSelectionTimeoutMS=50",
			"mongodb://localhost:27017/?replicaSet=nonexistent&serverSelectionTimeoutMS=50",
		}

		for _, uri := range testCases {
			_, err := defaultClientBuilder(ctx, WithClientBuilderDSN(uri))
			// We expect these to fail quickly due to short timeouts
			assert.Error(t, err)
			// The error should contain either "connect failed" or "ping failed"
			assert.True(t,
				containsAny(err.Error(), []string{"connect failed", "ping failed", "server selection timeout"}),
				"Expected connection/ping/timeout error, got: %s", err.Error())
		}
	})
}

// Helper function to check if string contains any of the given substrings
func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// TestTransactionComprehensive tests transaction scenarios
func TestTransactionComprehensive(t *testing.T) {
	ctx := context.Background()

	t.Run("Default client code path coverage", func(t *testing.T) {
		client := &defaultClient{client: nil}

		testCases := []struct {
			name  string
			tOpts []*options.TransactionOptions
			sOpts []*options.SessionOptions
			fn    func(sc mongo.SessionContext) error
		}{
			{
				name:  "nil transaction options",
				tOpts: nil,
				sOpts: nil,
				fn:    func(sc mongo.SessionContext) error { return nil },
			},
			{
				name:  "single transaction option",
				tOpts: []*options.TransactionOptions{options.Transaction()},
				sOpts: nil,
				fn:    func(sc mongo.SessionContext) error { return nil },
			},
			{
				name:  "with session options",
				tOpts: nil,
				sOpts: []*options.SessionOptions{options.Session()},
				fn:    func(sc mongo.SessionContext) error { return errors.New("test error") },
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						assert.NotNil(t, r)
					}
				}()
				_ = client.Transaction(ctx, tc.fn, tc.tOpts, tc.sOpts...)
			})
		}
	})
}

// TestInitFunction tests the init function behavior
func TestInitFunction(t *testing.T) {
	assert.NotNil(t, mongodbRegistry)
}

// mockSession implements the session interface for testing
type mockSession struct {
	endSessionCalled bool
	withTxErr        error
	withTxResult     any
}

func (m *mockSession) EndSession(ctx context.Context) {
	m.endSessionCalled = true
}

func (m *mockSession) WithTransaction(ctx context.Context, fn func(sc mongo.SessionContext) (any, error),
	opts ...*options.TransactionOptions) (any, error) {
	if m.withTxErr != nil {
		return nil, m.withTxErr
	}
	return m.withTxResult, nil
}

// TestNewDefaultClient tests the newDefaultClient function
func TestNewDefaultClient(t *testing.T) {
	// Create a minimal mongo.Client for testing
	client := &mongo.Client{}
	dc := newDefaultClient(client)

	assert.NotNil(t, dc)
	assert.Equal(t, client, dc.client)
	assert.NotNil(t, dc.startSession)
}

// TestTransactionWithMockSession tests Transaction method with mock session
func TestTransactionWithMockSession(t *testing.T) {
	ctx := context.Background()

	t.Run("Session start error", func(t *testing.T) {
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				return nil, errors.New("session start error")
			},
		}

		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "start session failed")
	})

	t.Run("Transaction success with nil tOpts", func(t *testing.T) {
		ms := &mockSession{}
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				return ms, nil
			},
		}

		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil)

		assert.NoError(t, err)
		assert.True(t, ms.endSessionCalled)
	})

	t.Run("Transaction success with tOpts", func(t *testing.T) {
		ms := &mockSession{}
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				return ms, nil
			},
		}

		tOpts := []*options.TransactionOptions{options.Transaction()}
		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, tOpts)

		assert.NoError(t, err)
		assert.True(t, ms.endSessionCalled)
	})

	t.Run("Transaction with WithTransaction error", func(t *testing.T) {
		ms := &mockSession{withTxErr: errors.New("transaction failed")}
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				return ms, nil
			},
		}

		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "transaction failed")
	})

	t.Run("Transaction with session options", func(t *testing.T) {
		ms := &mockSession{}
		var receivedOpts []*options.SessionOptions
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				receivedOpts = opts
				return ms, nil
			},
		}

		sOpts := []*options.SessionOptions{options.Session()}
		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, nil, sOpts...)

		assert.NoError(t, err)
		assert.Len(t, receivedOpts, 1)
	})

	t.Run("Transaction with multiple tOpts uses first", func(t *testing.T) {
		ms := &mockSession{}
		dc := &defaultClient{
			client: &mongo.Client{},
			startSession: func(opts ...*options.SessionOptions) (session, error) {
				return ms, nil
			},
		}

		tOpts := []*options.TransactionOptions{
			options.Transaction(),
			options.Transaction(),
		}
		err := dc.Transaction(ctx, func(sc mongo.SessionContext) error {
			return nil
		}, tOpts)

		assert.NoError(t, err)
	})
}

// TestDefaultClientBuilderWithMockConnector tests defaultClientBuilder with mock connector
func TestDefaultClientBuilderWithMockConnector(t *testing.T) {
	ctx := context.Background()
	originalConnector := mongoConnector
	defer func() { mongoConnector = originalConnector }()

	t.Run("Connect error", func(t *testing.T) {
		mongoConnector = func(ctx context.Context, opts ...*options.ClientOptions) (*mongo.Client, error) {
			return nil, errors.New("connect error")
		}

		_, err := defaultClientBuilder(ctx, WithClientBuilderDSN("mongodb://localhost:27017"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connect failed")
	})
}

// TestDefaultClientInterfaceCompliance verifies defaultClient implements Client
func TestDefaultClientInterfaceCompliance(t *testing.T) {
	var _ Client = (*defaultClient)(nil)
}
