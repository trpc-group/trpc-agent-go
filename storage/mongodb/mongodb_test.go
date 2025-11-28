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

func TestDefaultClientBuilderEmptyURI(t *testing.T) {
	_, err := defaultClientBuilder(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "URI is empty")
}

// mockMongoClient is a mock implementation of the Client interface for testing.
type mockMongoClient struct {
	insertOneFunc   func(ctx context.Context, database, coll string, document interface{}) error
	updateOneFunc   func(ctx context.Context, database, coll string, filter, update interface{}) error
	deleteOneFunc   func(ctx context.Context, database, coll string, filter interface{}) error
	deleteManyFunc  func(ctx context.Context, database, coll string, filter interface{}) error
	findOneFunc     func(ctx context.Context, database, coll string, filter interface{})
	findFunc        func(ctx context.Context, database, coll string, filter interface{}) error
	countFunc       func(ctx context.Context, database, coll string, filter interface{}) (int64, error)
	transactionFunc func(ctx context.Context) error
	disconnectFunc  func(ctx context.Context) error
}

func (m *mockMongoClient) InsertOne(ctx context.Context, database, coll string, document interface{},
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	if m.insertOneFunc != nil {
		err := m.insertOneFunc(ctx, database, coll, document)
		return &mongo.InsertOneResult{}, err
	}
	return &mongo.InsertOneResult{InsertedID: "test-id"}, nil
}

func (m *mockMongoClient) UpdateOne(ctx context.Context, database, coll string, filter, update interface{},
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if m.updateOneFunc != nil {
		err := m.updateOneFunc(ctx, database, coll, filter, update)
		return &mongo.UpdateResult{}, err
	}
	return &mongo.UpdateResult{ModifiedCount: 1}, nil
}

func (m *mockMongoClient) DeleteOne(ctx context.Context, database, coll string, filter interface{},
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteOneFunc != nil {
		err := m.deleteOneFunc(ctx, database, coll, filter)
		return &mongo.DeleteResult{}, err
	}
	return &mongo.DeleteResult{DeletedCount: 1}, nil
}

func (m *mockMongoClient) DeleteMany(ctx context.Context, database, coll string, filter interface{},
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if m.deleteManyFunc != nil {
		err := m.deleteManyFunc(ctx, database, coll, filter)
		return &mongo.DeleteResult{}, err
	}
	return &mongo.DeleteResult{DeletedCount: 5}, nil
}

func (m *mockMongoClient) FindOne(ctx context.Context, database, coll string, filter interface{},
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	if m.findOneFunc != nil {
		m.findOneFunc(ctx, database, coll, filter)
	}
	return nil
}

func (m *mockMongoClient) Find(ctx context.Context, database, coll string, filter interface{},
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	if m.findFunc != nil {
		err := m.findFunc(ctx, database, coll, filter)
		return nil, err
	}
	return nil, nil
}

func (m *mockMongoClient) CountDocuments(ctx context.Context, database, coll string, filter interface{},
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
			map[string]string{"_id": "1"}, map[string]interface{}{"$set": map[string]string{"key": "newvalue"}})
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
			insertOneFunc: func(ctx context.Context, database, coll string, document interface{}) error {
				return errors.New("insert error")
			},
		}
		_, err := mock.InsertOne(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "insert error")
	})

	t.Run("UpdateOne with error", func(t *testing.T) {
		mock := &mockMongoClient{
			updateOneFunc: func(ctx context.Context, database, coll string, filter, update interface{}) error {
				return errors.New("update error")
			},
		}
		_, err := mock.UpdateOne(ctx, "testdb", "testcoll", nil, nil)
		assert.EqualError(t, err, "update error")
	})

	t.Run("DeleteOne with error", func(t *testing.T) {
		mock := &mockMongoClient{
			deleteOneFunc: func(ctx context.Context, database, coll string, filter interface{}) error {
				return errors.New("delete error")
			},
		}
		_, err := mock.DeleteOne(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "delete error")
	})

	t.Run("DeleteMany with error", func(t *testing.T) {
		mock := &mockMongoClient{
			deleteManyFunc: func(ctx context.Context, database, coll string, filter interface{}) error {
				return errors.New("delete many error")
			},
		}
		_, err := mock.DeleteMany(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "delete many error")
	})

	t.Run("Find with error", func(t *testing.T) {
		mock := &mockMongoClient{
			findFunc: func(ctx context.Context, database, coll string, filter interface{}) error {
				return errors.New("find error")
			},
		}
		_, err := mock.Find(ctx, "testdb", "testcoll", nil)
		assert.EqualError(t, err, "find error")
	})

	t.Run("CountDocuments with error", func(t *testing.T) {
		mock := &mockMongoClient{
			countFunc: func(ctx context.Context, database, coll string, filter interface{}) (int64, error) {
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
			findOneFunc: func(ctx context.Context, database, coll string, filter interface{}) {
				called = true
			},
		}
		mock.FindOne(ctx, "testdb", "testcoll", nil)
		assert.True(t, called)
	})
}

func TestDefaultClientBuilderInvalidURI(t *testing.T) {
	// Test with invalid URI format that will fail during connect
	_, err := defaultClientBuilder(context.Background(), WithClientBuilderDSN("invalid-uri"))
	assert.Error(t, err)
}

func TestClientInterfaceCompliance(t *testing.T) {
	// Verify that mockMongoClient implements Client interface
	var _ Client = (*mockMongoClient)(nil)
}
