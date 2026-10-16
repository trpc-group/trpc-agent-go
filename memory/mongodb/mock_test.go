//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockOp records a single Client method invocation for after-the-fact
// assertions. Only fields relevant to the operation are populated.
type mockOp struct {
	name     string
	database string
	coll     string
	filter   any
	doc      any // InsertOne
	update   any // UpdateOne / UpdateMany
}

// mockClient is a hand-rolled storage.Client + storage.IndexViewer mock that
// records calls and returns programmable results, mirroring
// session/mongodb/mock_test.go. The MongoDB driver has no community sqlmock
// equivalent, so each backend writes its own. Methods that return cursors or
// single results are routed through callbacks because constructing
// *mongo.Cursor / *mongo.SingleResult values requires helpers.
type mockClient struct {
	mu  sync.Mutex
	ops []mockOp

	insertOneFn     func(doc any) (*mongo.InsertOneResult, error)
	updateOneFn     func(filter, update any, opts []*options.UpdateOptions) (*mongo.UpdateResult, error)
	updateManyFn    func(filter, update any, opts []*options.UpdateOptions) (*mongo.UpdateResult, error)
	deleteOneFn     func(filter any) (*mongo.DeleteResult, error)
	deleteManyFn    func(filter any) (*mongo.DeleteResult, error)
	findOneFn       func(filter any) *mongo.SingleResult
	findFn          func(filter any, opts []*options.FindOptions) (*mongo.Cursor, error)
	countFn         func(filter any) (int64, error)
	aggregateFn     func(pipeline any) (*mongo.Cursor, error)
	ensureIndexesFn func(models []mongo.IndexModel) ([]string, error)
	transactionFn   func(fn func(mongo.SessionContext) error) error
	disconnectFn    func() error
}

var _ mongoClient = (*mockClient)(nil)

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

func (m *mockClient) opsByName(name string) []mockOp {
	var out []mockOp
	for _, op := range m.recorded() {
		if op.name == name {
			out = append(out, op)
		}
	}
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

func (m *mockClient) UpdateMany(_ context.Context, db, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	m.record(mockOp{name: "UpdateMany", database: db, coll: coll, filter: filter, update: update})
	if m.updateManyFn != nil {
		return m.updateManyFn(filter, update, opts)
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
	return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
}

func (m *mockClient) Find(_ context.Context, db, coll string, filter any,
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	m.record(mockOp{name: "Find", database: db, coll: coll, filter: filter})
	if m.findFn != nil {
		return m.findFn(filter, opts)
	}
	return emptyCursor()
}

func (m *mockClient) CountDocuments(_ context.Context, db, coll string, filter any,
	_ ...*options.CountOptions) (int64, error) {
	m.record(mockOp{name: "CountDocuments", database: db, coll: coll, filter: filter})
	if m.countFn != nil {
		return m.countFn(filter)
	}
	return 0, nil
}

func (m *mockClient) Aggregate(_ context.Context, db, coll string, pipeline any,
	_ ...*options.AggregateOptions) (*mongo.Cursor, error) {
	m.record(mockOp{name: "Aggregate", database: db, coll: coll, filter: pipeline})
	if m.aggregateFn != nil {
		return m.aggregateFn(pipeline)
	}
	return emptyCursor()
}

func (m *mockClient) CreateMany(_ context.Context, db, coll string, models []mongo.IndexModel,
	_ ...*options.CreateIndexesOptions) ([]string, error) {
	m.record(mockOp{name: "CreateMany", database: db, coll: coll})
	if m.ensureIndexesFn != nil {
		return m.ensureIndexesFn(models)
	}
	return make([]string, len(models)), nil
}

func (m *mockClient) CreateOne(_ context.Context, db, coll string, model mongo.IndexModel,
	_ ...*options.CreateIndexesOptions) (string, error) {
	m.record(mockOp{name: "CreateOne", database: db, coll: coll, doc: model})
	return "", nil
}

func (m *mockClient) DropOne(_ context.Context, db, coll string, name string,
	_ ...*options.DropIndexesOptions) (bson.Raw, error) {
	m.record(mockOp{name: "DropOne", database: db, coll: coll, filter: name})
	return nil, nil
}

func (m *mockClient) DropAll(_ context.Context, db, coll string,
	_ ...*options.DropIndexesOptions) (bson.Raw, error) {
	m.record(mockOp{name: "DropAll", database: db, coll: coll})
	return nil, nil
}

func (m *mockClient) Transaction(
	_ context.Context,
	fn func(mongo.SessionContext) error,
	_ []*options.TransactionOptions,
	_ ...*options.SessionOptions,
) error {
	m.record(mockOp{name: "Transaction"})
	if m.transactionFn != nil {
		return m.transactionFn(fn)
	}
	return errors.New("mockClient: Transaction was called without a programmed result")
}

func (m *mockClient) Disconnect(_ context.Context) error {
	m.record(mockOp{name: "Disconnect"})
	if m.disconnectFn != nil {
		return m.disconnectFn()
	}
	return nil
}

func (m *mockClient) Indexes(_ context.Context, _ string, _ string) (mongo.IndexView, error) {
	return mongo.IndexView{}, nil
}

// emptyCursor returns a cursor that immediately yields no documents.
func emptyCursor() (*mongo.Cursor, error) {
	return mongo.NewCursorFromDocuments(nil, nil, nil)
}

// docsCursor returns a cursor that iterates the given documents in order.
// Each element must be marshalable by bson.Marshal (typed structs or bson.M).
func docsCursor(docs []any) (*mongo.Cursor, error) {
	return mongo.NewCursorFromDocuments(docs, nil, nil)
}

// fakeDoc is one in-memory stored document.
type fakeDoc struct {
	appName    string
	userID     string
	memoryID   string
	memoryData string
	createdAt  time.Time
	updatedAt  time.Time
	deletedAt  *time.Time
}

func (d *fakeDoc) toDoc() memoryDoc {
	return memoryDoc{
		AppName:    d.appName,
		UserID:     d.userID,
		MemoryID:   d.memoryID,
		MemoryData: d.memoryData,
		CreatedAt:  d.createdAt,
		UpdatedAt:  d.updatedAt,
		DeletedAt:  d.deletedAt,
	}
}

// fakeStore is a stateful in-memory document store used to back the
// mockClient callbacks in semantic tests. It evaluates the bson.M filters
// the service issues, applies $set/$unset/$setOnInsert updates, honors
// upserts, and emulates the partial unique index on active documents.
type fakeStore struct {
	mu   sync.Mutex
	docs []*fakeDoc
}

// newFakeClient returns a mockClient whose callbacks are backed by a fresh
// fakeStore, together with the store for direct assertions.
func newFakeClient() (*mockClient, *fakeStore) {
	store := &fakeStore{}
	mc := &mockClient{}
	mc.insertOneFn = store.insertOne
	mc.updateOneFn = store.updateOne
	mc.updateManyFn = store.updateMany
	mc.deleteOneFn = store.deleteOne
	mc.deleteManyFn = store.deleteMany
	mc.findOneFn = store.findOne
	mc.findFn = store.find
	mc.countFn = store.count
	return mc, store
}

// matchFilter reports whether doc matches the bson.M filter. Unknown filter
// keys fail the match so tests catch unexpected query shapes early.
func matchFilter(doc *fakeDoc, filter any) bool {
	f, ok := filter.(bson.M)
	if !ok {
		return false
	}
	for key, value := range f {
		switch key {
		case "app_name":
			if value != doc.appName {
				return false
			}
		case "user_id":
			if value != doc.userID {
				return false
			}
		case "memory_id":
			if value != doc.memoryID {
				return false
			}
		case "deleted_at":
			switch v := value.(type) {
			case nil:
				if doc.deletedAt != nil {
					return false
				}
			case bson.M:
				ne, ok := v["$ne"]
				if !ok || ne != nil {
					return false
				}
				if doc.deletedAt == nil {
					return false
				}
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func setFakeField(doc *fakeDoc, key string, value any) {
	switch key {
	case "app_name":
		doc.appName, _ = value.(string)
	case "user_id":
		doc.userID, _ = value.(string)
	case "memory_id":
		doc.memoryID, _ = value.(string)
	case "memory_data":
		doc.memoryData, _ = value.(string)
	case "created_at":
		doc.createdAt, _ = value.(time.Time)
	case "updated_at":
		doc.updatedAt, _ = value.(time.Time)
	case "deleted_at":
		if value == nil {
			doc.deletedAt = nil
			return
		}
		if ts, ok := value.(time.Time); ok {
			doc.deletedAt = &ts
		}
	}
}

// applyUpdate applies a bson.M update document ($set, $unset, $setOnInsert)
// to doc. $setOnInsert applies only when inserting via upsert.
func applyUpdate(doc *fakeDoc, update any, isInsert bool) {
	u, ok := update.(bson.M)
	if !ok {
		return
	}
	if set, ok := u["$set"].(bson.M); ok {
		for key, value := range set {
			setFakeField(doc, key, value)
		}
	}
	if unset, ok := u["$unset"].(bson.M); ok {
		for key := range unset {
			if key == "deleted_at" {
				doc.deletedAt = nil
			}
		}
	}
	if isInsert {
		if onInsert, ok := u["$setOnInsert"].(bson.M); ok {
			for key, value := range onInsert {
				setFakeField(doc, key, value)
			}
		}
	}
}

func upsertEnabled(opts []*options.UpdateOptions) bool {
	for _, o := range opts {
		if o != nil && o.Upsert != nil && *o.Upsert {
			return true
		}
	}
	return false
}

// conflictsUnique emulates the partial unique index on
// (app_name, user_id, memory_id) over active documents.
func (s *fakeStore) conflictsUnique(doc *fakeDoc) bool {
	if doc.deletedAt != nil {
		return false
	}
	for _, existing := range s.docs {
		if existing.deletedAt == nil &&
			existing.appName == doc.appName &&
			existing.userID == doc.userID &&
			existing.memoryID == doc.memoryID {
			return true
		}
	}
	return false
}

func (s *fakeStore) insertOne(document any) (*mongo.InsertOneResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	md, ok := document.(memoryDoc)
	if !ok {
		return nil, fmt.Errorf("fakeStore: unexpected insert document type %T", document)
	}
	doc := &fakeDoc{
		appName:    md.AppName,
		userID:     md.UserID,
		memoryID:   md.MemoryID,
		memoryData: md.MemoryData,
		createdAt:  md.CreatedAt,
		updatedAt:  md.UpdatedAt,
		deletedAt:  md.DeletedAt,
	}
	if s.conflictsUnique(doc) {
		return nil, fmt.Errorf("fakeStore: duplicate key error on (app_name, user_id, memory_id)")
	}
	s.docs = append(s.docs, doc)
	return &mongo.InsertOneResult{}, nil
}

func (s *fakeStore) updateOne(filter, update any,
	opts []*options.UpdateOptions) (*mongo.UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			applyUpdate(doc, update, false)
			return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	if upsertEnabled(opts) {
		doc := &fakeDoc{}
		applyUpdate(doc, update, true)
		if s.conflictsUnique(doc) {
			return nil, fmt.Errorf("fakeStore: duplicate key error on upsert")
		}
		s.docs = append(s.docs, doc)
		return &mongo.UpdateResult{UpsertedCount: 1}, nil
	}
	return &mongo.UpdateResult{}, nil
}

func (s *fakeStore) updateMany(filter, update any,
	_ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched int64
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			applyUpdate(doc, update, false)
			matched++
		}
	}
	return &mongo.UpdateResult{MatchedCount: matched, ModifiedCount: matched}, nil
}

func (s *fakeStore) deleteOne(filter any) (*mongo.DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, doc := range s.docs {
		if matchFilter(doc, filter) {
			s.docs = append(s.docs[:i], s.docs[i+1:]...)
			return &mongo.DeleteResult{DeletedCount: 1}, nil
		}
	}
	return &mongo.DeleteResult{}, nil
}

func (s *fakeStore) deleteMany(filter any) (*mongo.DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []*fakeDoc
	var deleted int64
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			deleted++
			continue
		}
		kept = append(kept, doc)
	}
	s.docs = kept
	return &mongo.DeleteResult{DeletedCount: deleted}, nil
}

func (s *fakeStore) findOne(filter any) *mongo.SingleResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			return mongo.NewSingleResultFromDocument(doc.toDoc(), nil, nil)
		}
	}
	return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
}

func (s *fakeStore) find(filter any, opts []*options.FindOptions) (*mongo.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]*fakeDoc, 0, len(s.docs))
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			matched = append(matched, doc)
		}
	}
	var limit int64
	for _, o := range opts {
		if o == nil {
			continue
		}
		if o.Limit != nil {
			limit = *o.Limit
		}
		if sortSpec, ok := o.Sort.(bson.D); ok {
			sort.SliceStable(matched, func(i, j int) bool {
				return fakeLess(matched[i], matched[j], sortSpec)
			})
		}
	}
	docs := make([]any, 0, len(matched))
	for i, doc := range matched {
		if limit > 0 && int64(i) >= limit {
			break
		}
		docs = append(docs, doc.toDoc())
	}
	return docsCursor(docs)
}

// fakeLess compares two docs against a bson.D sort specification.
func fakeLess(a, b *fakeDoc, spec bson.D) bool {
	for _, elem := range spec {
		var av, bv time.Time
		switch elem.Key {
		case "updated_at":
			av, bv = a.updatedAt, b.updatedAt
		case "created_at":
			av, bv = a.createdAt, b.createdAt
		default:
			continue
		}
		if av.Equal(bv) {
			continue
		}
		if dir, ok := elem.Value.(int); ok && dir < 0 {
			return av.After(bv)
		}
		return av.Before(bv)
	}
	return false
}

func (s *fakeStore) count(filter any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, doc := range s.docs {
		if matchFilter(doc, filter) {
			n++
		}
	}
	return n, nil
}

// activeDocs returns the number of active (non-tombstoned) documents.
func (s *fakeStore) activeDocs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, doc := range s.docs {
		if doc.deletedAt == nil {
			n++
		}
	}
	return n
}

// newServiceForTest builds a Service backed by the supplied mockClient with
// default options. mods can mutate the resolved ServiceOpts before the
// service is assembled.
func newServiceForTest(mc *mockClient, mods ...func(*ServiceOpts)) *Service {
	opts := defaultOptions.clone()
	for _, m := range mods {
		m(&opts)
	}
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}
	s := &Service{
		opts:        opts,
		client:      mc,
		database:    opts.database,
		collection:  opts.collectionName,
		cachedTools: make(map[string]tool.Tool),
	}
	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)
	return s
}

// installMockBuilder routes storage.GetClientBuilder() to the supplied
// mockClient for the duration of the test so NewService can be exercised
// end to end. The captured builder options are returned through capture
// when non-nil.
func installMockBuilder(t interface {
	Helper()
	Cleanup(func())
}, mc *mockClient, capture func([]storage.ClientBuilderOpt)) {
	t.Helper()
	original := storage.GetClientBuilder()
	storage.SetClientBuilder(func(_ context.Context,
		builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		if capture != nil {
			capture(builderOpts)
		}
		return mc, nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(original) })
}
