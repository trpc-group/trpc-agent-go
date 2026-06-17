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
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

// mockOp records a single Client method invocation for after-the-fact
// assertions. Only fields relevant to the operation are populated.
type mockOp struct {
	name     string
	database string
	coll     string
	filter   any
	doc      any   // InsertOne
	update   any   // UpdateOne
	docs     []any // InsertMany
}

// mockClient is a hand-rolled storage.Client mock that records calls and
// returns programmable results. Tests construct a mockClient, install it on
// the Service via newServiceForTest, exercise the method under test, then
// inspect the recorded ops.
//
// Returning concrete types like *mongo.SingleResult / *mongo.Cursor without a
// live deployment is impractical, so methods that return cursors / single
// results are routed through return-channel callbacks set by the test.
//
// This mirrors session/clickhouse/mock_test.go's hand-rolled mockClient — the
// MongoDB driver, like the ClickHouse driver, has no community sqlmock
// equivalent, so each backend writes its own.
type mockClient struct {
	mu  sync.Mutex
	ops []mockOp

	insertOneFn     func(doc any) (*mongo.InsertOneResult, error)
	updateOneFn     func(filter, update any, opts []*options.UpdateOptions) (*mongo.UpdateResult, error)
	deleteOneFn     func(filter any) (*mongo.DeleteResult, error)
	findOneFn       func(filter any) *mongo.SingleResult
	findFn          func(filter any) (*mongo.Cursor, error)
	ensureIndexesFn func(models []mongo.IndexModel) ([]string, error)
	transactionFn   func(fn storage.TxFunc) error
	closeFn         func() error
	deleteManyFn    func(filter any) (*mongo.DeleteResult, error)
}

func (m *mockClient) record(op mockOp) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, op)
}

func (m *mockClient) recorded() []mockOp {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockOp, len(m.ops))
	copy(out, m.ops)
	return out
}

func (m *mockClient) InsertOne(_ context.Context, db, coll string, document any,
	_ ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	m.record(mockOp{name: "InsertOne", database: db, coll: coll, doc: document})
	if m.insertOneFn != nil {
		return m.insertOneFn(document)
	}
	return &mongo.InsertOneResult{}, nil
}

func (m *mockClient) UpdateOne(_ context.Context, db, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	m.record(mockOp{name: "UpdateOne", database: db, coll: coll, filter: filter, update: update})
	if m.updateOneFn != nil {
		return m.updateOneFn(filter, update, opts)
	}
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
}

func (m *mockClient) DeleteOne(_ context.Context, db, coll string, filter any,
	_ ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	m.record(mockOp{name: "DeleteOne", database: db, coll: coll, filter: filter})
	if m.deleteOneFn != nil {
		return m.deleteOneFn(filter)
	}
	return &mongo.DeleteResult{DeletedCount: 1}, nil
}

func (m *mockClient) DeleteMany(_ context.Context, db, coll string, filter any,
	_ ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	m.record(mockOp{name: "DeleteMany", database: db, coll: coll, filter: filter})
	if m.deleteManyFn != nil {
		return m.deleteManyFn(filter)
	}
	return &mongo.DeleteResult{}, nil
}

func (m *mockClient) FindOne(_ context.Context, db, coll string, filter any,
	_ ...*options.FindOneOptions) *mongo.SingleResult {
	m.record(mockOp{name: "FindOne", database: db, coll: coll, filter: filter})
	if m.findOneFn != nil {
		return m.findOneFn(filter)
	}
	// Default: return a SingleResult that decodes ErrNoDocuments.
	return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
}

func (m *mockClient) Find(_ context.Context, db, coll string, filter any,
	_ ...*options.FindOptions) (*mongo.Cursor, error) {
	m.record(mockOp{name: "Find", database: db, coll: coll, filter: filter})
	if m.findFn != nil {
		return m.findFn(filter)
	}
	return emptyCursor()
}

func (m *mockClient) EnsureIndexes(_ context.Context, db, coll string, models []mongo.IndexModel,
	_ ...*options.CreateIndexesOptions) ([]string, error) {
	m.record(mockOp{name: "EnsureIndexes", database: db, coll: coll})
	if m.ensureIndexesFn != nil {
		return m.ensureIndexesFn(models)
	}
	return make([]string, len(models)), nil
}

func (m *mockClient) Transaction(_ context.Context, fn storage.TxFunc, _ ...storage.TxOption) error {
	m.record(mockOp{name: "Transaction"})
	if m.transactionFn != nil {
		return m.transactionFn(fn)
	}
	return errors.New("mockClient: Transaction was called without a programmed result")
}

func (m *mockClient) Close(_ context.Context) error {
	m.record(mockOp{name: "Close"})
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// emptyCursor returns a cursor that immediately yields no documents.
// `cursor.All` against this cursor decodes to an empty slice without error.
func emptyCursor() (*mongo.Cursor, error) {
	return mongo.NewCursorFromDocuments(nil, nil, nil)
}

// docsCursor returns a cursor that iterates the given BSON documents in order.
// Each element is encoded with bson.Marshal so callers can pass typed structs
// or bson.M values.
func docsCursor(docs []any) (*mongo.Cursor, error) {
	encoded := make([]any, 0, len(docs))
	for _, d := range docs {
		raw, err := bson.Marshal(d)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, raw)
	}
	return mongo.NewCursorFromDocuments(encoded, nil, nil)
}

// newServiceForTest builds a Service backed by the supplied mockClient with
// default options (soft-delete on, no TTL, no prefix).
func newServiceForTest(t interface{ Fatalf(string, ...any) }, mc *mockClient, mods ...func(*ServiceOpts)) *Service {
	opts := defaultOptions
	for _, m := range mods {
		m(&opts)
	}
	return &Service{
		opts:              opts,
		client:            mc,
		database:          defaultDatabase,
		collSessionStates: "session_states",
		collAppStates:     "app_states",
		collUserStates:    "user_states",
	}
}
