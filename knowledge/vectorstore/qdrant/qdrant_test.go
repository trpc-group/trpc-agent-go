//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// mockClient is a mock implementation of Client for testing.
type mockClient struct {
	mu sync.RWMutex

	// Storage
	documents map[string]*mockDoc

	// Function overrides for custom behavior
	CollectionExistsFn func(ctx context.Context, name string) (bool, error)
	CreateCollectionFn func(ctx context.Context, req *qdrant.CreateCollection) error
	UpsertFn           func(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	GetFn              func(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error)
	DeleteFn           func(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	QueryFn            func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	CountFn            func(ctx context.Context, req *qdrant.CountPoints) (uint64, error)
	ScrollFn           func(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error)
	CloseFn            func() error

	// Call counters
	upsertCalls int
	getCalls    int
	deleteCalls int
	queryCalls  int
	countCalls  int
	scrollCalls int
	closeCalls  int

	// Error injection
	upsertError error
	getError    error
	deleteError error
	queryError  error
	countError  error
	scrollError error
}

type mockDoc struct {
	doc       *document.Document
	embedding []float64
}

func newMockClient() *mockClient {
	return &mockClient{
		documents: make(map[string]*mockDoc),
	}
}

func (m *mockClient) CollectionExists(ctx context.Context, name string) (bool, error) {
	if m.CollectionExistsFn != nil {
		return m.CollectionExistsFn(ctx, name)
	}
	return true, nil
}

func (m *mockClient) CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error {
	if m.CreateCollectionFn != nil {
		return m.CreateCollectionFn(ctx, req)
	}
	return nil
}

func (m *mockClient) Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.upsertCalls++

	if m.upsertError != nil {
		return nil, m.upsertError
	}

	if m.UpsertFn != nil {
		return m.UpsertFn(ctx, req)
	}

	// Store documents
	for _, pt := range req.Points {
		id := pointIDToStr(pt.Id)
		m.documents[id] = &mockDoc{
			doc: &document.Document{
				ID:      getPayloadString(pt.Payload, fieldID),
				Name:    getPayloadString(pt.Payload, fieldName),
				Content: getPayloadString(pt.Payload, fieldContent),
			},
			embedding: extractVector(pt.Vectors),
		}
	}

	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) Get(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.getCalls++

	if m.getError != nil {
		return nil, m.getError
	}

	if m.GetFn != nil {
		return m.GetFn(ctx, req)
	}

	var results []*qdrant.RetrievedPoint
	for _, id := range req.Ids {
		idStr := pointIDToStr(id)
		if doc, ok := m.documents[idStr]; ok {
			results = append(results, &qdrant.RetrievedPoint{
				Id: id,
				Payload: map[string]*qdrant.Value{
					fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
					fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
					fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
				},
				Vectors: &qdrant.Vectors{
					VectorsOptions: &qdrant.Vectors_Vector{
						Vector: &qdrant.Vector{Data: toFloat32Slice(doc.embedding)},
					},
				},
			})
		}
	}

	return results, nil
}

func (m *mockClient) Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCalls++

	if m.deleteError != nil {
		return nil, m.deleteError
	}

	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, req)
	}

	// Handle point selector deletion
	if selector := req.Points.GetPoints(); selector != nil {
		for _, id := range selector.Ids {
			delete(m.documents, pointIDToStr(id))
		}
	}

	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queryCalls++

	if m.queryError != nil {
		return nil, m.queryError
	}

	if m.QueryFn != nil {
		return m.QueryFn(ctx, req)
	}

	var results []*qdrant.ScoredPoint
	for id, doc := range m.documents {
		results = append(results, &qdrant.ScoredPoint{
			Id: qdrant.NewID(id),
			Payload: map[string]*qdrant.Value{
				fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
				fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
				fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
			},
			Score: 0.95,
		})
		if req.Limit != nil && len(results) >= int(*req.Limit) {
			break
		}
	}

	return results, nil
}

func (m *mockClient) Count(ctx context.Context, req *qdrant.CountPoints) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.countCalls++

	if m.countError != nil {
		return 0, m.countError
	}

	if m.CountFn != nil {
		return m.CountFn(ctx, req)
	}

	return uint64(len(m.documents)), nil
}

func (m *mockClient) Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.scrollCalls++

	if m.scrollError != nil {
		return nil, m.scrollError
	}

	if m.ScrollFn != nil {
		return m.ScrollFn(ctx, req)
	}

	var results []*qdrant.RetrievedPoint
	for id, doc := range m.documents {
		results = append(results, &qdrant.RetrievedPoint{
			Id: qdrant.NewID(id),
			Payload: map[string]*qdrant.Value{
				fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
				fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
				fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
			},
		})
		if req.Limit != nil && len(results) >= int(*req.Limit) {
			break
		}
	}

	return results, nil
}

func (m *mockClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closeCalls++

	if m.CloseFn != nil {
		return m.CloseFn()
	}

	return nil
}

// testDimension is the dimension used in tests for easier testing
const testDimension = 4

// testOptions returns options configured for testing with a small dimension
var testOptions = options{
	host:            defaultHost,
	port:            defaultPort,
	collectionName:  defaultCollectionName,
	dimension:       testDimension,
	distance:        DistanceCosine,
	hnswM:           defaultHNSWM,
	hnswEfConstruct: defaultHNSWEfConstruct,
	maxResults:      defaultMaxResults,
	maxRetries:      defaultMaxRetries,
	baseRetryDelay:  1 * time.Millisecond,
	maxRetryDelay:   10 * time.Millisecond,
}

// Helper to create a VectorStore with a mock client
func newTestVectorStore(mock *mockClient) *VectorStore {
	return &VectorStore{
		client:          mock,
		opts:            testOptions,
		filterConverter: newFilterConverter(),
		retryCfg: retryConfig{
			maxRetries:     testOptions.maxRetries,
			baseRetryDelay: testOptions.baseRetryDelay,
			maxRetryDelay:  testOptions.maxRetryDelay,
		},
	}
}

// ============================================================================
// VectorStore Tests
// ============================================================================

func TestVectorStore_Add(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Test Document",
			Content: "Test content",
		}
		embedding := []float64{0.1, 0.2, 0.3, 0.4} // testDimension = 4

		err := vs.Add(context.Background(), doc, embedding)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
		assert.Len(t, mock.documents, 1)
	})

	t.Run("nil document returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.Add(context.Background(), nil, []float64{0.1, 0.2, 0.3, 0.4})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Equal(t, 0, mock.upsertCalls)
	})

	t.Run("empty document ID returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "",
			Name:    "Test",
			Content: "Content",
		}

		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3, 0.4})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Equal(t, 0, mock.upsertCalls)
	})

	t.Run("upsert error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.upsertError = errors.New("upsert failed")
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Test",
			Content: "Content",
		}

		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3, 0.4})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "add document")
		assert.Contains(t, err.Error(), "upsert failed")
	})

	t.Run("with metadata", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Test",
			Content: "Content",
			Metadata: map[string]any{
				"category": "test",
				"version":  1,
			},
		}

		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3, 0.4})

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
	})

	t.Run("with timestamps", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		now := time.Now()
		doc := &document.Document{
			ID:        "doc-1",
			Name:      "Test",
			Content:   "Content",
			CreatedAt: now,
			UpdatedAt: now,
		}

		err := vs.Add(context.Background(), doc, make([]float64, testDimension))

		require.NoError(t, err)
	})

	t.Run("nil embedding returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Test",
			Content: "Content",
		}

		err := vs.Add(context.Background(), doc, nil)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "embedding is required")
		assert.Equal(t, 0, mock.upsertCalls)
	})

	t.Run("wrong dimension embedding returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Test",
			Content: "Content",
		}

		// testDimension is 4, we pass only 2
		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "expected 4 dimensions")
		assert.Contains(t, err.Error(), "got 2")
		assert.Equal(t, 0, mock.upsertCalls)
	})
}

func TestVectorStore_Get(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		// Pre-populate mock storage
		docID := "doc-1"
		mock.documents[idToUUID(docID)] = &mockDoc{
			doc: &document.Document{
				ID:      docID,
				Name:    "Test Document",
				Content: "Test content",
			},
			embedding: []float64{0.1, 0.2, 0.3, 0.4},
		}

		doc, vec, err := vs.Get(context.Background(), docID)

		require.NoError(t, err)
		require.NotNil(t, doc)
		assert.Equal(t, docID, doc.ID)
		assert.Equal(t, "Test Document", doc.Name)
		assert.Equal(t, "Test content", doc.Content)
		require.Len(t, vec, testDimension)
		assert.Equal(t, 1, mock.getCalls)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc, vec, err := vs.Get(context.Background(), "")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Nil(t, doc)
		assert.Nil(t, vec)
		assert.Equal(t, 0, mock.getCalls)
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc, vec, err := vs.Get(context.Background(), "non-existent")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
		assert.Nil(t, doc)
		assert.Nil(t, vec)
	})

	t.Run("get error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.getError = errors.New("get failed")
		vs := newTestVectorStore(mock)

		doc, vec, err := vs.Get(context.Background(), "doc-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get document")
		assert.Contains(t, err.Error(), "get failed")
		assert.Nil(t, doc)
		assert.Nil(t, vec)
	})
}

func TestVectorStore_Update(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		doc := &document.Document{
			ID:      "doc-1",
			Name:    "Updated Document",
			Content: "Updated content",
		}
		embedding := []float64{0.4, 0.5, 0.6, 0.7}

		err := vs.Update(context.Background(), doc, embedding)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
	})

	t.Run("nil document returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.Update(context.Background(), nil, []float64{0.1, 0.2, 0.3, 0.4})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})
}

func TestVectorStore_Delete(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		// Pre-populate
		docID := "doc-1"
		mock.documents[idToUUID(docID)] = &mockDoc{
			doc: &document.Document{ID: docID},
		}

		err := vs.Delete(context.Background(), docID)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.Delete(context.Background(), "")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("delete error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.Delete(context.Background(), "doc-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete document")
		assert.Contains(t, err.Error(), "delete failed")
	})
}

func TestVectorStore_Search(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		// Pre-populate
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{
				ID:      "doc-1",
				Name:    "Document 1",
				Content: "Content 1",
			},
		}
		mock.documents[idToUUID("doc-2")] = &mockDoc{
			doc: &document.Document{
				ID:      "doc-2",
				Name:    "Document 2",
				Content: "Content 2",
			},
		}

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
			Limit:  10,
		}

		result, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Results, 2)
		assert.Equal(t, 1, mock.queryCalls)
	})

	t.Run("nil query returns error", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), nil)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Nil(t, result)
		assert.Equal(t, 0, mock.queryCalls)
	})

	t.Run("with limit", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		for i := 0; i < 5; i++ {
			id := idToUUID("doc-" + string(rune('1'+i)))
			mock.documents[id] = &mockDoc{
				doc: &document.Document{ID: "doc-" + string(rune('1'+i))},
			}
		}

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
			Limit:  2,
		}

		result, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
		assert.LessOrEqual(t, len(result.Results), 2)
	})

	t.Run("with min score", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		query := &vectorstore.SearchQuery{
			Vector:   []float64{0.1, 0.2, 0.3, 0.4},
			MinScore: 0.5,
		}

		_, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]any{
					"category": "docs",
				},
			},
		}

		_, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
	})

	t.Run("with ID filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
			Filter: &vectorstore.SearchFilter{
				IDs: []string{"doc-1", "doc-2"},
			},
		}

		_, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.queryError = errors.New("query failed")
		vs := newTestVectorStore(mock)

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
		}

		result, err := vs.Search(context.Background(), query)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search in")
		assert.Contains(t, err.Error(), "query failed")
		assert.Nil(t, result)
	})

	t.Run("uses default max results when limit is 0", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		query := &vectorstore.SearchQuery{
			Vector: []float64{0.1, 0.2, 0.3, 0.4},
			Limit:  0, // Should use default
		}

		_, err := vs.Search(context.Background(), query)

		require.NoError(t, err)
	})
}

func TestVectorStore_DeleteByFilter(t *testing.T) {
	t.Parallel()
	t.Run("delete by IDs", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteDocumentIDs([]string{"doc-1", "doc-2"}),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("delete by filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("delete all", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteAll(true),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("no options does nothing", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("delete by IDs error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteDocumentIDs([]string{"doc-1"}),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete by IDs")
	})

	t.Run("delete by filter error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteFilter(map[string]any{"key": "value"}),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete by filter")
	})

	t.Run("delete all error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteAll(true),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete all")
	})
}

func TestVectorStore_Count(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		mock.documents[idToUUID("doc-1")] = &mockDoc{doc: &document.Document{ID: "doc-1"}}
		mock.documents[idToUUID("doc-2")] = &mockDoc{doc: &document.Document{ID: "doc-2"}}

		count, err := vs.Count(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 2, count)
		assert.Equal(t, 1, mock.countCalls)
	})

	t.Run("with filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		count, err := vs.Count(context.Background(),
			vectorstore.WithCountFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 0)
	})

	t.Run("count error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.countError = errors.New("count failed")
		vs := newTestVectorStore(mock)

		count, err := vs.Count(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "count documents")
		assert.Contains(t, err.Error(), "count failed")
		assert.Equal(t, 0, count)
	})
}

func TestVectorStore_GetMetadata(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1", Name: "Doc 1"},
		}

		metadata, err := vs.GetMetadata(context.Background())

		require.NoError(t, err)
		assert.NotNil(t, metadata)
		assert.Equal(t, 1, mock.scrollCalls)
	})

	t.Run("with IDs filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataIDs([]string{"doc-1", "doc-2"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, metadata)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, metadata)
	})

	t.Run("with limit", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataLimit(10),
		)

		require.NoError(t, err)
		assert.NotNil(t, metadata)
	})

	t.Run("scroll error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.scrollError = errors.New("scroll failed")
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get metadata")
		assert.Contains(t, err.Error(), "scroll failed")
		assert.Nil(t, metadata)
	})
}

func TestVectorStore_Close(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.Close()

		require.NoError(t, err)
		assert.Equal(t, 1, mock.closeCalls)
	})

	t.Run("nil client", func(t *testing.T) {
		vs := &VectorStore{
			client: nil,
		}

		err := vs.Close()

		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		mock := newMockClient()
		mock.CloseFn = func() error {
			return errors.New("close failed")
		}
		vs := newTestVectorStore(mock)

		err := vs.Close()

		require.Error(t, err)
		assert.Equal(t, "close failed", err.Error())
	})
}

func TestVectorStore_EnsureCollection(t *testing.T) {
	t.Parallel()
	t.Run("collection exists", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return true, nil
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err)
	})

	t.Run("collection does not exist - creates it", func(t *testing.T) {
		mock := newMockClient()
		createCalled := false
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			createCalled = true
			assert.Equal(t, defaultCollectionName, req.CollectionName)
			return nil
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err)
		assert.True(t, createCalled)
	})

	t.Run("check exists error", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, errors.New("connection error")
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "check collection")
	})

	t.Run("create collection error", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			return errors.New("create failed")
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "create collection")
	})
}

func TestVectorStore_BuildSearchFilter(t *testing.T) {
	t.Parallel()
	t.Run("nil filter returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(nil)

		require.NoError(t, err)
		assert.Nil(t, filter)
	})

	t.Run("filter with IDs", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(&vectorstore.SearchFilter{
			IDs: []string{"id1", "id2"},
		})

		require.NoError(t, err)
		require.NotNil(t, filter)
		require.Len(t, filter.Must, 1)
	})

	t.Run("filter with metadata", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(&vectorstore.SearchFilter{
			Metadata: map[string]any{
				"category": "test",
			},
		})

		require.NoError(t, err)
		require.NotNil(t, filter)
	})

	t.Run("empty filter returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(&vectorstore.SearchFilter{})

		require.NoError(t, err)
		assert.Nil(t, filter)
	})
}

func TestVectorStore_BuildMetadataFilter(t *testing.T) {
	t.Parallel()
	t.Run("with IDs", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		config := &vectorstore.GetMetadataConfig{
			IDs: []string{"id1", "id2"},
		}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		require.NotNil(t, filter)
		require.Len(t, filter.Must, 1)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		config := &vectorstore.GetMetadataConfig{
			Filter: map[string]any{"category": "test"},
		}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		require.NotNil(t, filter)
	})

	t.Run("empty config returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		config := &vectorstore.GetMetadataConfig{}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		assert.Nil(t, filter)
	})
}

// ============================================================================
// Integration-style tests with mock
// ============================================================================

func TestVectorStore_AddGetDeleteFlow(t *testing.T) {
	t.Parallel()
	mock := newMockClient()
	vs := newTestVectorStore(mock)

	// Add a document
	doc := &document.Document{
		ID:      "test-doc",
		Name:    "Test Document",
		Content: "This is test content",
		Metadata: map[string]any{
			"category": "test",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3, 0.4}

	err := vs.Add(context.Background(), doc, embedding)
	require.NoError(t, err)

	// Get the document
	retrieved, vec, err := vs.Get(context.Background(), "test-doc")
	require.NoError(t, err)
	assert.Equal(t, doc.ID, retrieved.ID)
	assert.Equal(t, doc.Name, retrieved.Name)
	assert.Equal(t, doc.Content, retrieved.Content)
	assert.Len(t, vec, testDimension)

	// Delete the document
	err = vs.Delete(context.Background(), "test-doc")
	require.NoError(t, err)

	// Verify counts
	assert.Equal(t, 1, mock.upsertCalls)
	assert.Equal(t, 1, mock.getCalls)
	assert.Equal(t, 1, mock.deleteCalls)
}

func TestVectorStore_SearchWithResults(t *testing.T) {
	t.Parallel()
	mock := newMockClient()
	vs := newTestVectorStore(mock)

	// Add multiple documents
	docs := []*document.Document{
		{ID: "doc-1", Name: "First", Content: "First content"},
		{ID: "doc-2", Name: "Second", Content: "Second content"},
		{ID: "doc-3", Name: "Third", Content: "Third content"},
	}

	for _, doc := range docs {
		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3, 0.4})
		require.NoError(t, err)
	}

	// Search
	result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		Vector: []float64{0.1, 0.2, 0.3, 0.4},
		Limit:  10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Results, 3)

	// All results should have scores
	for _, r := range result.Results {
		assert.Greater(t, r.Score, 0.0)
	}
}

func TestVectorStore_CountAfterOperations(t *testing.T) {
	t.Parallel()
	mock := newMockClient()
	vs := newTestVectorStore(mock)

	// Initially empty
	count, err := vs.Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add documents
	for i := 0; i < 5; i++ {
		doc := &document.Document{
			ID:      "doc-" + string(rune('1'+i)),
			Name:    "Document",
			Content: "Content",
		}
		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3, 0.4})
		require.NoError(t, err)
	}

	// Count after adding
	count, err = vs.Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}
