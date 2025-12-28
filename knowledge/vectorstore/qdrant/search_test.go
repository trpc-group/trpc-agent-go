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
	"testing"

	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestSearch(t *testing.T) {
	t.Parallel()

	t.Run("nil query returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result, err := vs.Search(context.Background(), nil)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Nil(t, result)
	})

	t.Run("routes to correct search mode", func(t *testing.T) {
		tests := []struct {
			name       string
			mode       vectorstore.SearchMode
			setupMock  func(*mockClient)
			setupQuery func() *vectorstore.SearchQuery
		}{
			{
				name: "vector search",
				mode: vectorstore.SearchModeVector,
				setupMock: func(m *mockClient) {
					m.QueryFn = func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
						return []*qdrant.ScoredPoint{}, nil
					}
				},
				setupQuery: func() *vectorstore.SearchQuery {
					return &vectorstore.SearchQuery{
						SearchMode: vectorstore.SearchModeVector,
						Vector:     make([]float64, testDimension),
					}
				},
			},
			{
				name: "filter search",
				mode: vectorstore.SearchModeFilter,
				setupMock: func(m *mockClient) {
					m.ScrollFn = func(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
						return []*qdrant.RetrievedPoint{}, nil
					}
				},
				setupQuery: func() *vectorstore.SearchQuery {
					return &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeFilter}
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mock := newMockClient()
				tt.setupMock(mock)
				vs := newTestVectorStore(mock)

				result, err := vs.Search(context.Background(), tt.setupQuery())

				require.NoError(t, err)
				assert.NotNil(t, result)
			})
		}
	})
}

func TestSearchByVector(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1", Name: "Test"},
		}
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
			Limit:      10,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 1, mock.queryCalls)
	})

	t.Run("empty vector returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{},
		})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "vector is required")
		assert.Nil(t, result)
	})

	t.Run("nil vector returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     nil,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "vector is required")
		assert.Nil(t, result)
	})

	t.Run("wrong dimension returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{0.1, 0.2}, // Only 2 dimensions
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected 4 dimensions")
		assert.Nil(t, result)
	})

	t.Run("uses default limit when zero", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
			Limit:      0,
		})

		require.NoError(t, err)
	})

	t.Run("with min score", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
			MinScore:   0.5,
		})

		require.NoError(t, err)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"key": "value"}},
		})

		require.NoError(t, err)
	})

	t.Run("with ID filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
			Filter:     &vectorstore.SearchFilter{IDs: []string{"doc-1", "doc-2"}},
		})

		require.NoError(t, err)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.queryError = errors.New("query failed")
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "search in")
		assert.Nil(t, result)
	})

	t.Run("uses named vector when BM25 enabled", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.QueryPoints
		mock.QueryFn = func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
			capturedReq = req
			return []*qdrant.ScoredPoint{}, nil
		}
		vs := newTestVectorStoreWithBM25(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     make([]float64, testDimension),
		})

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		assert.Equal(t, vectorNameDense, *capturedReq.Using)
	})
}

func TestSearchByFilter(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1"},
		}
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Limit:      10,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 1, mock.scrollCalls)
		assert.Equal(t, 0, mock.queryCalls)
	})

	t.Run("results have zero score", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1"},
		}
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
		})

		require.NoError(t, err)
		for _, r := range result.Results {
			assert.Equal(t, float64(0), r.Score)
		}
	})

	t.Run("scroll error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.scrollError = errors.New("scroll failed")
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "filter search in")
		assert.Nil(t, result)
	})
}

func TestSearchByKeyword(t *testing.T) {
	t.Parallel()

	t.Run("returns error when BM25 disabled", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "test query",
		})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnsupportedSearchMode))
		assert.Contains(t, err.Error(), "requires WithBM25(true)")
		assert.Nil(t, result)
	})

	t.Run("success with BM25 enabled", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1", Content: "test content"},
		}
		vs := newTestVectorStoreWithBM25(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "test",
			Limit:      10,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 1, mock.queryCalls)
	})

	t.Run("requires query text", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "query text is required")
		assert.Nil(t, result)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStoreWithBM25(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "test",
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"key": "value"}},
		})

		require.NoError(t, err)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.queryError = errors.New("query failed")
		vs := newTestVectorStoreWithBM25(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "test",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "keyword search in")
		assert.Nil(t, result)
	})
}

func TestSearchByHybrid(t *testing.T) {
	t.Parallel()

	t.Run("falls back to vector search when BM25 disabled", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc:       &document.Document{ID: "doc-1"},
			embedding: make([]float64, testDimension),
		}
		vs := newTestVectorStore(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "test query",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1, mock.queryCalls)
	})

	t.Run("success with BM25 enabled", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1", Content: "machine learning"},
		}
		vs := newTestVectorStoreWithBM25(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "machine learning",
			Limit:      10,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("requires vector", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     []float64{},
			Query:      "test",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "vector is required")
		assert.Nil(t, result)
	})

	t.Run("requires query text", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "query text is required")
		assert.Nil(t, result)
	})

	t.Run("wrong dimension returns error", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     []float64{0.1, 0.2}, // Wrong dimension
			Query:      "test",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected 4 dimensions")
		assert.Nil(t, result)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStoreWithBM25(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "test",
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"key": "value"}},
		})

		require.NoError(t, err)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.queryError = errors.New("query failed")
		vs := newTestVectorStoreWithBM25(mock)

		result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "test",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "hybrid search in")
		assert.Nil(t, result)
	})

	t.Run("uses minimum prefetch limit for small limits", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.QueryPoints
		mock.QueryFn = func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
			capturedReq = req
			return []*qdrant.ScoredPoint{}, nil
		}
		vs := newTestVectorStoreWithBM25(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "test",
			Limit:      1,
		})

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		if len(capturedReq.Prefetch) > 0 {
			assert.GreaterOrEqual(t, *capturedReq.Prefetch[0].Limit, uint64(minPrefetchLimit))
		}
	})

	t.Run("caps prefetch limit for large limits", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.QueryPoints
		mock.QueryFn = func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
			capturedReq = req
			return []*qdrant.ScoredPoint{}, nil
		}
		vs := newTestVectorStoreWithBM25(mock)

		_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     make([]float64, testDimension),
			Query:      "test",
			Limit:      1000,
		})

		require.NoError(t, err)
		if len(capturedReq.Prefetch) > 0 {
			assert.LessOrEqual(t, *capturedReq.Prefetch[0].Limit, uint64(maxPrefetchLimit))
		}
	})
}

func TestBuildSearchFilter(t *testing.T) {
	t.Parallel()

	t.Run("nil filter returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(nil)

		require.NoError(t, err)
		assert.Nil(t, filter)
	})

	t.Run("empty filter returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(&vectorstore.SearchFilter{})

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
			Metadata: map[string]any{"category": "test"},
		})

		require.NoError(t, err)
		require.NotNil(t, filter)
	})

	t.Run("filter with both IDs and metadata", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		filter, err := vs.buildSearchFilter(&vectorstore.SearchFilter{
			IDs:      []string{"id1"},
			Metadata: map[string]any{"key": "value"},
		})

		require.NoError(t, err)
		require.NotNil(t, filter)
		// Should have both ID and metadata conditions
		assert.GreaterOrEqual(t, len(filter.Must), 1)
	})
}

func TestSearchWithResults(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	vs := newTestVectorStore(mock)

	// Add documents
	for i := 0; i < 3; i++ {
		doc := &document.Document{ID: "doc-" + string(rune('1'+i))}
		err := vs.Add(context.Background(), doc, make([]float64, testDimension))
		require.NoError(t, err)
	}

	// Search
	result, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     make([]float64, testDimension),
		Limit:      10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Results, 3)
}
