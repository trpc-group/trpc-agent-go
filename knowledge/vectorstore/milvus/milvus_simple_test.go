//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package milvus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/milvus-io/milvus/client/v2/column"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

type mockClient struct {
	HasCollectionFn    func(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (has bool, err error)
	CreateCollectionFn func(ctx context.Context, option client.CreateCollectionOption, callOptions ...grpc.CallOption) error
	LoadCollectionFn   func(ctx context.Context, option client.LoadCollectionOption, callOptions ...grpc.CallOption) (client.LoadTask, error)
	InsertFn           func(ctx context.Context, option client.InsertOption, callOptions ...grpc.CallOption) (client.InsertResult, error)
	UpsertFn           func(ctx context.Context, option client.UpsertOption, callOptions ...grpc.CallOption) (client.UpsertResult, error)
	QueryFn            func(ctx context.Context, option client.QueryOption, callOptions ...grpc.CallOption) (client.ResultSet, error)
	DeleteFn           func(ctx context.Context, option client.DeleteOption, callOptions ...grpc.CallOption) (client.DeleteResult, error)
	SearchFn           func(ctx context.Context, option client.SearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error)
	HybridSearchFn     func(ctx context.Context, option client.HybridSearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error)
	CloseFn            func(ctx context.Context) error

	// In-memory storage
	documents map[string]*mockDocument
	mu        sync.RWMutex

	// Call tracking
	insertCalls       int
	upsertCalls       int
	queryCalls        int
	deleteCalls       int
	searchCalls       int
	hybridSearchCalls int
	closeCalls        int

	// Error simulation
	insertError       error
	upsertError       error
	queryError        error
	deleteError       error
	searchError       error
	hybridSearchError error
}

type mockDocument struct {
	doc    *document.Document
	vector []float64
}

// newMockClient creates a new mock client for testing
func newMockClient() *mockClient {
	return &mockClient{
		documents: make(map[string]*mockDocument),
	}
}

// AddDocument adds a document to the mock storage
func (m *mockClient) AddDocument(id string, doc *document.Document, vector []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[id] = &mockDocument{
		doc:    doc,
		vector: vector,
	}
}

// GetDocumentCount returns the number of documents in storage
func (m *mockClient) GetDocumentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.documents)
}

// GetInsertCalls returns the number of insert calls
func (m *mockClient) GetInsertCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.insertCalls
}

// GetUpsertCalls returns the number of upsert calls
func (m *mockClient) GetUpsertCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upsertCalls
}

// GetQueryCalls returns the number of query calls
func (m *mockClient) GetQueryCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queryCalls
}

// GetDeleteCalls returns the number of delete calls
func (m *mockClient) GetDeleteCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deleteCalls
}

// GetSearchCalls returns the number of search calls
func (m *mockClient) GetSearchCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.searchCalls
}

// GetCloseCalls returns the number of close calls
func (m *mockClient) GetCloseCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closeCalls
}

// SetInsertError sets the error to return for insert operations
func (m *mockClient) SetInsertError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertError = err
}

// SetUpsertError sets the error to return for upsert operations
func (m *mockClient) SetUpsertError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertError = err
}

// SetQueryError sets the error to return for query operations
func (m *mockClient) SetQueryError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryError = err
}

// SetHybridSearchError sets the error to return for hybrid search operations
func (m *mockClient) SetHybridSearchError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hybridSearchError = err
}

// SetDeleteError sets the error to return for delete operations
func (m *mockClient) SetDeleteError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteError = err
}

// SetSearchError sets the error to return for search operations
func (m *mockClient) SetSearchError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchError = err
}

func (m *mockClient) HasCollection(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (has bool, err error) {
	if m.HasCollectionFn != nil {
		return m.HasCollectionFn(ctx, option, callOptions...)
	}
	return true, nil
}

func (m *mockClient) CreateCollection(ctx context.Context, option client.CreateCollectionOption, callOptions ...grpc.CallOption) error {
	if m.CreateCollectionFn != nil {
		return m.CreateCollectionFn(ctx, option, callOptions...)
	}
	return nil
}

func (m *mockClient) LoadCollection(ctx context.Context, option client.LoadCollectionOption, callOptions ...grpc.CallOption) (client.LoadTask, error) {
	if m.LoadCollectionFn != nil {
		return m.LoadCollectionFn(ctx, option, callOptions...)
	}
	return client.LoadTask{}, nil
}

func (m *mockClient) Insert(ctx context.Context, option client.InsertOption, callOptions ...grpc.CallOption) (client.InsertResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.insertCalls++

	if m.insertError != nil {
		return client.InsertResult{}, m.insertError
	}

	if m.InsertFn != nil {
		return m.InsertFn(ctx, option, callOptions...)
	}

	return client.InsertResult{}, nil
}

func (m *mockClient) Upsert(ctx context.Context, option client.UpsertOption, callOptions ...grpc.CallOption) (client.UpsertResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.upsertCalls++

	if m.upsertError != nil {
		return client.UpsertResult{}, m.upsertError
	}

	if m.UpsertFn != nil {
		return m.UpsertFn(ctx, option, callOptions...)
	}

	return client.UpsertResult{UpsertCount: 1}, nil
}

func (m *mockClient) Query(ctx context.Context, option client.QueryOption, callOptions ...grpc.CallOption) (client.ResultSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queryCalls++

	if m.queryError != nil {
		return client.ResultSet{}, m.queryError
	}

	if m.QueryFn != nil {
		return m.QueryFn(ctx, option, callOptions...)
	}

	// Return documents from storage
	var docs []*mockDocument
	for _, doc := range m.documents {
		docs = append(docs, doc)
	}

	if len(docs) == 0 {
		return client.ResultSet{}, nil
	}

	return m.buildResultSet(docs), nil
}

func (m *mockClient) Delete(ctx context.Context, option client.DeleteOption, callOptions ...grpc.CallOption) (client.DeleteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCalls++

	if m.deleteError != nil {
		return client.DeleteResult{}, m.deleteError
	}

	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, option, callOptions...)
	}

	return client.DeleteResult{}, nil
}

func (m *mockClient) Search(ctx context.Context, option client.SearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.searchCalls++

	if m.searchError != nil {
		return nil, m.searchError
	}

	if m.SearchFn != nil {
		return m.SearchFn(ctx, option, callOptions...)
	}

	// Perform similarity search
	var docs []*mockDocument
	for _, doc := range m.documents {
		docs = append(docs, doc)
	}

	// Sort by similarity (mock implementation)
	sort.Slice(docs, func(i, j int) bool {
		return true // Keep original order for simplicity
	})

	if len(docs) == 0 {
		return []client.ResultSet{{}}, nil
	}

	return []client.ResultSet{m.buildResultSet(docs)}, nil
}

func (m *mockClient) HybridSearch(ctx context.Context, option client.HybridSearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.hybridSearchCalls++

	if m.hybridSearchError != nil {
		return nil, m.hybridSearchError
	}

	if m.HybridSearchFn != nil {
		return m.HybridSearchFn(ctx, option, callOptions...)
	}

	// Return all documents for hybrid search
	var docs []*mockDocument
	for _, doc := range m.documents {
		docs = append(docs, doc)
	}

	if len(docs) == 0 {
		return []client.ResultSet{{}}, nil
	}

	return []client.ResultSet{m.buildResultSet(docs)}, nil
}

func (m *mockClient) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closeCalls++

	if m.CloseFn != nil {
		return m.CloseFn(ctx)
	}
	return nil
}

// buildResultSet builds a ResultSet from mock documents
func (m *mockClient) buildResultSet(docs []*mockDocument) client.ResultSet {
	if len(docs) == 0 {
		return client.ResultSet{}
	}

	ids := make([]string, len(docs))
	names := make([]string, len(docs))
	contents := make([]string, len(docs))
	metadataBytes := make([][]byte, len(docs))
	vectors := make([][]float64, len(docs))
	createdAts := make([]int64, len(docs))
	updatedAts := make([]int64, len(docs))
	scores := make([]float32, len(docs))

	for i, doc := range docs {
		ids[i] = doc.doc.ID
		names[i] = doc.doc.Name
		contents[i] = doc.doc.Content
		if doc.doc.Metadata != nil {
			metadataBytes[i], _ = json.Marshal(doc.doc.Metadata)
		}
		createdAts[i] = doc.doc.CreatedAt.Unix()
		updatedAts[i] = doc.doc.UpdatedAt.Unix()
		scores[i] = 0.95 // Mock score
		vectors[i] = doc.vector
	}

	return client.ResultSet{
		ResultCount: len(docs),
		Scores:      scores,
		Fields: []column.Column{
			column.NewColumnVarChar("id", ids),
			column.NewColumnVarChar("name", names),
			column.NewColumnVarChar("content", contents),
			column.NewColumnDoubleArray("vector", vectors),
			column.NewColumnJSONBytes("metadata", metadataBytes),
			column.NewColumnInt64("created_at", createdAts),
			column.NewColumnInt64("updated_at", updatedAts),
		},
	}
}

func TestNew(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cli, err := New(ctx, WithAddress("invalid"),
		WithUsername("invalid"),
		WithPassword("invalid"),
		WithDBName("invalid"),
		WithAPIKey("invalid"))
	require.Error(t, err)
	require.Nil(t, cli)

	cli, err = New(ctx, WithAddress(""))
	require.Error(t, err)
	require.Nil(t, cli)
}

func Test_initCollection(t *testing.T) {
	mc := &mockClient{
		documents: map[string]*mockDocument{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if err := recover(); err != nil {
			t.Logf("wait await err: %v", err)
		}
	}()
	vs := newVectorStoreWithMockClient(mc)
	err := vs.initCollection(ctx)
	require.Error(t, err)
}

func Test_initCollection_create(t *testing.T) {
	mc := &mockClient{
		documents: map[string]*mockDocument{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if err := recover(); err != nil {
			t.Logf("wait await err: %v", err)
		}
	}()
	vs := newVectorStoreWithMockClient(mc)

	mc.HasCollectionFn = func(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (bool, error) {
		return false, nil
	}
	mc.CreateCollectionFn = func(ctx context.Context, option client.CreateCollectionOption, callOptions ...grpc.CallOption) error {
		return nil
	}
	err := vs.initCollection(ctx)
	require.Error(t, err)
}

func Test_initCollection_createFailed(t *testing.T) {
	mc := &mockClient{
		documents: map[string]*mockDocument{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if err := recover(); err != nil {
			t.Logf("wait await err: %v", err)
		}
	}()
	vs := newVectorStoreWithMockClient(mc)

	mc.HasCollectionFn = func(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (bool, error) {
		return false, fmt.Errorf("failed to check collection existence")
	}
	mc.CreateCollectionFn = func(ctx context.Context, option client.CreateCollectionOption, callOptions ...grpc.CallOption) error {
		return fmt.Errorf("create collection failed")
	}
	err := vs.initCollection(ctx)
	require.Error(t, err)

	mc.HasCollectionFn = func(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (bool, error) {
		return false, nil
	}
	err = vs.initCollection(ctx)
	require.Error(t, err)
}

func customDocBuilder(columns []column.Column) (*document.Document, []float64, error) {
	doc := &document.Document{}
	embedding := make([]float64, 0)
	for _, col := range columns {
		if col == nil {
			continue
		}
		switch col.Name() {
		case "id":
			id, err := col.GetAsString(0)
			if err != nil {
				return nil, nil, err
			}
			doc.ID = id
		case "name":
			name, err := col.GetAsString(0)
			if err != nil {
				return nil, nil, err
			}
			doc.Name = name
		case "content":
			content, err := col.GetAsString(0)
			if err != nil {
				return nil, nil, err
			}
			doc.Content = content
		case "vector":
			vectorColumn, ok := col.(*column.ColumnDoubleArray)
			if !ok {
				return nil, nil, fmt.Errorf("vector column is not a double array")
			}
			for i := 0; i < vectorColumn.Len(); i++ {
				val, err := vectorColumn.Value(i)
				if err != nil {
					return nil, nil, fmt.Errorf("get vector failed: %w", err)
				}
				embedding = append(embedding, val...)
			}
		case "metadata":
			val, err := col.Get(0)
			if err != nil {
				return nil, nil, fmt.Errorf("get metadata failed: %w", err)
			}
			if metadataBytes, ok := val.([]byte); ok {
				var metadata map[string]any
				if err := json.Unmarshal(metadataBytes, &metadata); err == nil {
					doc.Metadata = metadata
				}
			}
		case "created_at":
			createdAt, err := col.GetAsInt64(0)
			if err != nil {
				return nil, nil, fmt.Errorf("get created at failed: %w", err)
			}
			doc.CreatedAt = time.Unix(createdAt, 0)
		case "updated_at":
			updatedAt, err := col.GetAsInt64(0)
			if err != nil {
				return nil, nil, fmt.Errorf("get updated at failed: %w", err)
			}
			doc.UpdatedAt = time.Unix(updatedAt, 0)
		}
	}
	return doc, embedding, nil
}
