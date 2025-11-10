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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_RemoteEmbedding_VectorSearch tests vector search with remote embedding
func TestVectorStore_RemoteEmbedding_VectorSearch(t *testing.T) {
	tests := []struct {
		name     string
		query    *vectorstore.SearchQuery
		wantErr  bool
		errMsg   string
		validate func(t *testing.T, result *vectorstore.SearchResult)
	}{
		{
			name: "search_with_text_only",
			query: &vectorstore.SearchQuery{
				Query:      "artificial intelligence",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "search_with_empty_text",
			query: &vectorstore.SearchQuery{
				Query:      "",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
			},
			wantErr: true,
			errMsg:  "searching with a nil or empty vector is not supported",
		},
		{
			name: "search_with_text_and_filter",
			query: &vectorstore.SearchQuery{
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
				},
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
				WithRemoteEmbeddingModel("bge-base-zh"),
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

// TestVectorStore_RemoteEmbedding_HybridSearch tests hybrid search with remote embedding
func TestVectorStore_RemoteEmbedding_HybridSearch(t *testing.T) {
	tests := []struct {
		name     string
		query    *vectorstore.SearchQuery
		wantErr  bool
		errMsg   string
		validate func(t *testing.T, result *vectorstore.SearchResult)
	}{
		{
			name: "hybrid_search_with_text_only",
			query: &vectorstore.SearchQuery{
				Query:      "deep learning",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "hybrid_search_with_empty_text",
			query: &vectorstore.SearchQuery{
				Query:      "",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
			},
			wantErr: true,
			errMsg:  "vector is required for hybrid search",
		},
		{
			name: "hybrid_search_with_text_and_filter",
			query: &vectorstore.SearchQuery{
				Query:      "neural networks",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"topic": "ML",
					},
				},
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
				WithRemoteEmbeddingModel("bge-base-zh"),
			)
			// Inject mock sparse encoder
			vs.sparseEncoder = newMockSparseEncoder()
			// Enable TSVector option
			vs.option.enableTSVector = true

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

// TestVectorStore_RemoteEmbedding_FallbackToLocal tests fallback to local embedding
func TestVectorStore_RemoteEmbedding_FallbackToLocal(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	// When vector is provided, should use local mode even with remote embedding enabled
	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.5, 0.2},
		Query:      "artificial intelligence", // Text is ignored when vector is provided
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
	}

	result, err := vs.Search(context.Background(), query)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that Search API was called (not SearchByText)
	assert.Greater(t, mockClient.GetSearchCalls(), 0)
}

// TestVectorStore_RemoteEmbedding_Options tests remote embedding options
func TestVectorStore_RemoteEmbedding_Options(t *testing.T) {
	tests := []struct {
		name            string
		opts            []Option
		expectedEnabled bool
		expectedModel   string
	}{
		{
			name: "default_options",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
			},
			expectedEnabled: false,
			expectedModel:   "",
		},
		{
			name: "enable_remote_embedding",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithRemoteEmbeddingModel("bge-base-zh"),
			},
			expectedEnabled: true,
			expectedModel:   "bge-base-zh",
		},
		{
			name: "custom_embedding_model",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithRemoteEmbeddingModel("text-embedding-ada-002"),
			},
			expectedEnabled: true,
			expectedModel:   "text-embedding-ada-002",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient, tt.opts...)

			assert.Equal(t, tt.expectedEnabled, vs.isRemoteEmbeddingEnabled())
			assert.Equal(t, tt.expectedModel, vs.option.embeddingModel)
		})
	}
}

// TestVectorStore_RemoteEmbedding_KeywordSearch tests keyword search with remote embedding
func TestVectorStore_RemoteEmbedding_KeywordSearch(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	// Test keyword search mode
	query := &vectorstore.SearchQuery{
		Query:      "keyword search test",
		SearchMode: vectorstore.SearchModeKeyword,
		Limit:      10,
	}

	result, err := vs.Search(context.Background(), query)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestVectorStore_RemoteEmbedding_WithMinScore tests remote embedding search with minimum score
func TestVectorStore_RemoteEmbedding_WithMinScore(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	query := &vectorstore.SearchQuery{
		Query:      "test query with score threshold",
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
		MinScore:   0.8,
	}

	result, err := vs.Search(context.Background(), query)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestVectorStore_RemoteEmbedding_DisableRemoteEmbedding tests disabling remote embedding
func TestVectorStore_RemoteEmbedding_DisableRemoteEmbedding(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel(""), // Disable remote embedding
	)

	assert.False(t, vs.isRemoteEmbeddingEnabled())

	// Should fail without local vector
	query := &vectorstore.SearchQuery{
		Query:      "test without remote embedding",
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
	}

	_, err := vs.Search(context.Background(), query)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "searching with a nil or empty vector is not supported")
}

// TestVectorStore_RemoteEmbedding_HybridSearchError tests hybrid search error handling
func TestVectorStore_RemoteEmbedding_HybridSearchError(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)
	vs.sparseEncoder = newMockSparseEncoder()
	vs.option.enableTSVector = true

	// Simulate hybrid search error
	mockClient.SetHybridError(assert.AnError)

	query := &vectorstore.SearchQuery{
		Query:      "hybrid search error test",
		SearchMode: vectorstore.SearchModeHybrid,
		Limit:      10,
	}

	_, err := vs.Search(context.Background(), query)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tcvectordb")
}

// TestVectorStore_RemoteEmbedding_VectorSearchError tests vector search error handling
func TestVectorStore_RemoteEmbedding_VectorSearchError(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	// Simulate search error
	mockClient.SetSearchError(assert.AnError)

	query := &vectorstore.SearchQuery{
		Query:      "search error test",
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
	}

	_, err := vs.Search(context.Background(), query)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tcvectordb")
}

// TestVectorStore_RemoteEmbedding_ComplexFilter tests remote embedding with complex filters
func TestVectorStore_RemoteEmbedding_ComplexFilter(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	query := &vectorstore.SearchQuery{
		Query:      "complex filter test",
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
		Filter: &vectorstore.SearchFilter{
			IDs: []string{"doc1", "doc2"},
			Metadata: map[string]any{
				"category": "test",
				"priority": 1,
			},
		},
	}

	result, err := vs.Search(context.Background(), query)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestVectorStore_RemoteEmbedding_MultipleQueries tests remote embedding with multiple queries
func TestVectorStore_RemoteEmbedding_MultipleQueries(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	)

	queries := []string{
		"first query",
		"second query",
		"third query",
	}

	for _, queryText := range queries {
		query := &vectorstore.SearchQuery{
			Query:      queryText,
			SearchMode: vectorstore.SearchModeVector,
			Limit:      5,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err, "failed for query: %s", queryText)
		assert.NotNil(t, result)
	}
}

// TestVectorStore_RemoteEmbedding_EmptyModelName tests behavior with empty model name
func TestVectorStore_RemoteEmbedding_EmptyModelName(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel(""),
	)

	assert.False(t, vs.isRemoteEmbeddingEnabled())
	assert.Equal(t, "", vs.option.embeddingModel)
}

// TestVectorStore_RemoteEmbedding_ModelNameValidation tests model name validation
func TestVectorStore_RemoteEmbedding_ModelNameValidation(t *testing.T) {
	testModels := []string{
		"bge-base-zh",
		"bge-large-zh",
		"m3e-base",
		"text2vec-large-chinese",
		"text-embedding-ada-002",
		"custom-model-v1",
	}

	for _, model := range testModels {
		t.Run(model, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
				WithRemoteEmbeddingModel(model),
			)

			assert.True(t, vs.isRemoteEmbeddingEnabled())
			assert.Equal(t, model, vs.option.embeddingModel)
		})
	}
}
