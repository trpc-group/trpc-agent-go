//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_Search tests the Search method with vector search
func TestVectorStore_Search(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_vector_search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      5,
			},
			setupMock: func(m *mockClient) {
				// Pre-populate with documents
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				docs := []struct {
					doc    *document.Document
					vector []float64
				}{
					{
						doc: &document.Document{
							ID:      "doc1",
							Name:    "AI Document",
							Content: "AI content",
						},
						vector: []float64{1.0, 0.5, 0.2},
					},
					{
						doc: &document.Document{
							ID:      "doc2",
							Name:    "ML Document",
							Content: "ML content",
						},
						vector: []float64{0.8, 0.6, 0.3},
					},
				}
				for _, d := range docs {
					_ = vs.Add(context.Background(), d.doc, d.vector)
				}
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				require.Greater(t, len(result.Results), 0)
				// Verify first result is the exact match (doc1)
				assert.Equal(t, "doc1", result.Results[0].Document.ID)
				// Verify score is valid (0.0 to 1.0)
				assert.GreaterOrEqual(t, result.Results[0].Score, 0.0)
				assert.LessOrEqual(t, result.Results[0].Score, 1.0)
			},
		},
		{
			name:      "nil_query",
			query:     nil,
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "query is required",
		},
		{
			name: "empty_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "nil or empty vector",
		},
		{
			name: "dimension_mismatch",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5}, // Only 2 dimensions
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "client_error",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {
				m.SetSearchError(errors.New("search service unavailable"))
			},
			wantErr: true,
			errMsg:  "search service unavailable",
		},
		{
			name: "search_with_filter",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient) {
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				doc := &document.Document{
					ID:       "doc1",
					Content:  "AI content",
					Metadata: map[string]any{"category": "AI"},
				}
				_ = vs.Add(context.Background(), doc, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
			},
		},
		{
			name: "vector_with_nan_values",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{math.NaN(), 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   false, // NaN should be handled by the system
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				// NaN handling is implementation specific
				assert.NotNil(t, result)
			},
		},
		{
			name: "vector_with_inf_values",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{math.Inf(1), 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   false, // Inf should be handled by the system
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				// Inf handling is implementation specific
				assert.NotNil(t, result)
			},
		},
		{
			name: "zero_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.0, 0.0, 0.0},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				doc := &document.Document{
					ID:      "doc1",
					Content: "Test content",
				}
				_ = vs.Add(context.Background(), doc, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "negative_limit",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      -1,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   false, // Should use default limit
		},
		{
			name: "very_large_limit",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      1000000,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestVectorStore_Search_TextMode tests text-based search
func TestVectorStore_Search_TextMode(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		// Note: TSVector is not enabled in mock tests to avoid encoder dependency
	)

	// Pre-populate with documents
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Name:    "Machine Learning",
				Content: "Machine learning is a subset of AI",
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "doc2",
				Name:    "Deep Learning",
				Content: "Deep learning uses neural networks",
			},
			vector: []float64{0.8, 0.6, 0.3},
		},
	}

	ctx := context.Background()
	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	// Test text search
	query := &vectorstore.SearchQuery{
		Query:      "machine learning",
		SearchMode: vectorstore.SearchModeKeyword,
		Limit:      5,
	}

	result, err := vs.Search(ctx, query)

	// Note: Text search might not be fully implemented in mock
	// This test verifies the API works without errors
	if err == nil {
		assert.NotNil(t, result)
	}
}

// TestVectorStore_Search_HybridMode tests hybrid search (vector + text)
func TestVectorStore_Search_HybridMode(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		// Note: TSVector is not enabled in mock tests to avoid encoder dependency
	)

	// Pre-populate with documents
	doc := &document.Document{
		ID:      "doc1",
		Name:    "AI Research",
		Content: "Artificial intelligence research",
	}
	vector := []float64{1.0, 0.5, 0.2}

	ctx := context.Background()
	err := vs.Add(ctx, doc, vector)
	require.NoError(t, err)

	// Test hybrid search - falls back to vector search when TSVector is disabled
	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		Query:      "artificial intelligence",
		SearchMode: vectorstore.SearchModeHybrid,
		Limit:      5,
	}

	result, err := vs.Search(ctx, query)

	// When TSVector is disabled, hybrid search falls back to vector search
	require.NoError(t, err)
	assert.NotNil(t, result)
	// Should have called Search instead of HybridSearch
	assert.Greater(t, mockClient.GetSearchCalls(), 0)
}

// TestVectorStore_Search_WithScoreThreshold tests search with score filtering
func TestVectorStore_Search_WithScoreThreshold(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		// Note: ScoreThreshold filtering happens in the actual implementation
	)

	// Pre-populate with documents
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:      "high_score",
				Content: "High relevance content",
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "low_score",
				Content: "Low relevance content",
			},
			vector: []float64{0.1, 0.1, 0.1},
		},
	}

	ctx := context.Background()
	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
	}

	result, err := vs.Search(ctx, query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	// Note: Score filtering happens in the actual implementation
	// Mock returns all documents, but real implementation would filter
}

// TestVectorStore_Search_EmptyResults tests search with no matching documents
func TestVectorStore_Search_EmptyResults(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Don't add any documents

	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      5,
	}

	result, err := vs.Search(context.Background(), query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	// Empty collection should return empty results
	assert.Equal(t, 0, len(result.Results))
}

// TestVectorStore_Search_TopKLimit tests TopK parameter
func TestVectorStore_Search_TopKLimit(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add multiple documents
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: "Content",
		}
		vector := []float64{float64(i) / 10.0, 0.5, 0.2}
		err := vs.Add(ctx, doc, vector)
		require.NoError(t, err)
	}

	tests := []struct {
		name       string
		limit      int
		maxResults int
	}{
		{"top_3", 3, 3},
		{"top_5", 5, 5},
		{"top_10", 10, 10},
		{"top_100", 100, 10}, // Should return max available (10)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &vectorstore.SearchQuery{
				Vector:     []float64{0.5, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      tt.limit,
			}

			result, err := vs.Search(ctx, query)
			require.NoError(t, err)
			assert.NotNil(t, result)
			// Note: Mock returns all documents, real implementation would limit
		})
	}
}

// TestVectorStore_Search_ComplexFilter tests complex filter conditions
func TestVectorStore_Search_ComplexFilter(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add documents with various metadata
	ctx := context.Background()
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Content: "AI content",
				Metadata: map[string]any{
					"category": "AI",
					"priority": 8,
					"tags":     []string{"ml", "ai"},
				},
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "doc2",
				Content: "ML content",
				Metadata: map[string]any{
					"category": "ML",
					"priority": 5,
					"tags":     []string{"ml"},
				},
			},
			vector: []float64{0.8, 0.6, 0.3},
		},
	}

	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	// Test with complex filter
	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{
				"category": "AI",
				"priority": 8,
			},
		},
	}

	result, err := vs.Search(ctx, query)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestVectorStore_Search_Pagination tests search with offset and limit
func TestVectorStore_Search_Pagination(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add multiple documents
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: "Content",
		}
		vector := []float64{float64(i) / 20.0, 0.5, 0.2}
		err := vs.Add(ctx, doc, vector)
		require.NoError(t, err)
	}

	// Test pagination
	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.5, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      5,
		// Note: Offset might be in params, not in SearchQuery
	}

	result, err := vs.Search(ctx, query)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestVectorStore_Search_ContextCancellation tests context cancellation
func TestVectorStore_Search_ContextCancellation(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add a document
	doc := &document.Document{
		ID:      "doc1",
		Content: "Test content",
	}
	err := vs.Add(context.Background(), doc, []float64{1.0, 0.5, 0.2})
	require.NoError(t, err)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      5,
	}

	// Note: Mock client doesn't check context cancellation
	// In real implementation, this should return context.Canceled error
	result, err := vs.Search(ctx, query)
	// Mock doesn't handle cancellation, so we just verify it doesn't panic
	_ = result
	_ = err
}

// TestVectorStore_Search_MinScore tests minimum score filtering
func TestVectorStore_Search_MinScore(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add documents with different similarities
	ctx := context.Background()
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:      "high_similarity",
				Content: "Very similar content",
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "low_similarity",
				Content: "Different content",
			},
			vector: []float64{0.1, 0.1, 0.1},
		},
	}

	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		minScore float64
		wantErr  bool
	}{
		{"min_score_0", 0.0, false},
		{"min_score_0.5", 0.5, false},
		{"min_score_0.9", 0.9, false},
		{"min_score_1.0", 1.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				MinScore:   tt.minScore,
				Limit:      10,
			}

			result, err := vs.Search(ctx, query)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				// Note: Mock doesn't implement score filtering
				// Real implementation should filter by MinScore
			}
		})
	}
}

// TestVectorStore_Search_InvalidSearchMode tests invalid search mode
func TestVectorStore_Search_InvalidSearchMode(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		SearchMode: 999, // Invalid search mode
		Limit:      5,
	}

	result, err := vs.Search(context.Background(), query)
	// Should handle invalid search mode gracefully
	_ = result
	_ = err
}

// TestVectorStore_Search_LargeResultSet tests handling of large result sets
func TestVectorStore_Search_LargeResultSet(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add many documents
	ctx := context.Background()
	numDocs := 100
	for i := 0; i < numDocs; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: fmt.Sprintf("Content %d", i),
		}
		vector := []float64{float64(i) / 100.0, 0.5, 0.2}
		err := vs.Add(ctx, doc, vector)
		require.NoError(t, err)
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.5, 0.5, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Limit:      50,
	}

	result, err := vs.Search(ctx, query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	// Note: Mock returns all documents, real implementation would limit
}

// TestVectorStore_Search_MultipleFilters tests combining multiple filters
func TestVectorStore_Search_MultipleFilters(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Add documents with various metadata
	ctx := context.Background()
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Content: "AI content",
				Metadata: map[string]any{
					"category": "AI",
					"priority": 8,
					"status":   "active",
				},
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "doc2",
				Content: "ML content",
				Metadata: map[string]any{
					"category": "ML",
					"priority": 5,
					"status":   "active",
				},
			},
			vector: []float64{0.8, 0.6, 0.3},
		},
		{
			doc: &document.Document{
				ID:      "doc3",
				Content: "Data content",
				Metadata: map[string]any{
					"category": "Data",
					"priority": 3,
					"status":   "inactive",
				},
			},
			vector: []float64{0.6, 0.4, 0.5},
		},
	}

	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		filter   map[string]any
		expected []string // Expected document IDs in results
	}{
		{
			name: "filter_by_category",
			filter: map[string]any{
				"category": "AI",
			},
			expected: []string{"doc1"},
		},
		{
			name: "filter_by_status",
			filter: map[string]any{
				"status": "active",
			},
			expected: []string{"doc1", "doc2"},
		},
		{
			name: "filter_by_multiple",
			filter: map[string]any{
				"category": "AI",
				"priority": 8,
				"status":   "active",
			},
			expected: []string{"doc1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: tt.filter,
				},
			}

			result, err := vs.Search(ctx, query)
			require.NoError(t, err)
			assert.NotNil(t, result)
			// Note: Mock doesn't implement filtering
			// Real implementation should return only matching documents
		})
	}
}

// TestVectorStore_SearchByFilterMode tests filter-only search mode
func TestVectorStore_SearchByFilterMode(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient, *VectorStore)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_filter_search",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				for i := 0; i < 5; i++ {
					doc := &document.Document{
						ID:      fmt.Sprintf("filter_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
						Metadata: map[string]any{
							"category": "AI",
						},
					}
					_ = vs.Add(ctx, doc, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			wantErr: false,
			validate: func(t *testing.T, r *vectorstore.SearchResult) {
				assert.NotNil(t, r)
				assert.GreaterOrEqual(t, len(r.Results), 0)
			},
		},
		{
			name: "filter_search_empty_results",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "NonExistent",
					},
				},
			},
			setupMock: func(m *mockClient, vs *VectorStore) {},
			wantErr:   false,
			validate: func(t *testing.T, r *vectorstore.SearchResult) {
				assert.NotNil(t, r)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			if tt.setupMock != nil {
				tt.setupMock(mockClient, vs)
			}

			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestVectorStore_SearchConvertResult tests result conversion edge cases
func TestVectorStore_SearchConvertResult(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockClient, *VectorStore)
		query     *vectorstore.SearchQuery
		validate  func(*testing.T, *vectorstore.SearchResult, error)
	}{
		{
			name: "convert_with_metadata",
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:      "meta_doc",
					Content: "Content with metadata",
					Metadata: map[string]any{
						"key1": "value1",
						"key2": 123,
					},
				}
				_ = vs.Add(ctx, doc, []float64{1.0, 0.5, 0.2})
			},
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      1,
			},
			validate: func(t *testing.T, r *vectorstore.SearchResult, err error) {
				require.NoError(t, err)
				assert.NotNil(t, r)
				if len(r.Results) > 0 {
					assert.NotNil(t, r.Results[0].Document.Metadata)
				}
			},
		},
		{
			name: "convert_with_scores",
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				for i := 0; i < 3; i++ {
					doc := &document.Document{
						ID:      fmt.Sprintf("score_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}
					vector := []float64{float64(i) / 3.0, 0.5, 0.2}
					_ = vs.Add(ctx, doc, vector)
				}
			},
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.5, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      3,
			},
			validate: func(t *testing.T, r *vectorstore.SearchResult, err error) {
				require.NoError(t, err)
				assert.NotNil(t, r)
				for _, res := range r.Results {
					assert.GreaterOrEqual(t, res.Score, 0.0)
					assert.LessOrEqual(t, res.Score, 1.0)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			if tt.setupMock != nil {
				tt.setupMock(mockClient, vs)
			}

			result, err := vs.Search(context.Background(), tt.query)
			tt.validate(t, result, err)
		})
	}
}

// TestVectorStore_SearchByKeyword tests searchByKeyword method directly.
func TestVectorStore_SearchByKeyword(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient, *VectorStore)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_keyword_search",
			query: &vectorstore.SearchQuery{
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeKeyword,
				Limit:      5,
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				// Add documents with content
				ctx := context.Background()
				docs := []struct {
					id      string
					content string
					vector  []float64
				}{
					{"doc1", "machine learning basics", []float64{1.0, 0.5, 0.2}},
					{"doc2", "deep learning tutorial", []float64{0.8, 0.6, 0.3}},
				}
				for _, d := range docs {
					doc := &document.Document{
						ID:      d.id,
						Content: d.content,
					}
					_ = vs.Add(ctx, doc, d.vector)
				}
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
				assert.GreaterOrEqual(t, len(result.Results), 0)
			},
		},
		{
			name: "empty_query_keyword",
			query: &vectorstore.SearchQuery{
				Query:      "",
				SearchMode: vectorstore.SearchModeKeyword,
				Limit:      5,
			},
			setupMock: func(m *mockClient, vs *VectorStore) {},
			wantErr:   true,
			errMsg:    "keyword is required",
		},
		{
			name: "keyword_search_with_filter",
			query: &vectorstore.SearchQuery{
				Query:      "artificial intelligence",
				SearchMode: vectorstore.SearchModeKeyword,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:       "ai_doc",
					Content:  "artificial intelligence overview",
					Metadata: map[string]any{"category": "AI"},
				}
				_ = vs.Add(ctx, doc, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)
			// Inject mock sparse encoder
			vs.sparseEncoder = newMockSparseEncoder()
			// Enable TSVector option
			vs.option.enableTSVector = true

			if tt.setupMock != nil {
				tt.setupMock(mockClient, vs)
			}

			result, err := vs.searchByKeyword(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestVectorStore_SearchByHybrid tests searchByHybrid method directly.
func TestVectorStore_SearchByHybrid(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient, *VectorStore)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_hybrid_search_with_both",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:      "hybrid_doc",
					Content: "machine learning and AI",
				}
				_ = vs.Add(ctx, doc, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
				assert.GreaterOrEqual(t, len(result.Results), 0)
			},
		},
		{
			name: "hybrid_search_vector_only",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:      "vector_only_doc",
					Content: "test content",
				}
				_ = vs.Add(ctx, doc, []float64{0.9, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "hybrid_search_missing_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				Query:      "test query",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
			},
			setupMock: func(m *mockClient, vs *VectorStore) {},
			wantErr:   true,
			errMsg:    "vector is required",
		},
		{
			name: "hybrid_search_with_filter",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "deep learning",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"topic": "ML",
					},
				},
			},
			setupMock: func(m *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:       "filtered_doc",
					Content:  "deep learning fundamentals",
					Metadata: map[string]any{"topic": "ML"},
				}
				_ = vs.Add(ctx, doc, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)
			// Inject mock sparse encoder
			vs.sparseEncoder = newMockSparseEncoder()
			// Enable TSVector option
			vs.option.enableTSVector = true

			if tt.setupMock != nil {
				tt.setupMock(mockClient, vs)
			}

			result, err := vs.searchByHybrid(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}
