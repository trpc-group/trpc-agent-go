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

// setupRemoteEmbeddingVectorStore creates a VectorStore with remote embedding enabled for testing.
func setupRemoteEmbeddingVectorStore(t *testing.T, extraOpts ...Option) (*VectorStore, *mockClient) {
	t.Helper()
	mockClient := newMockClient()
	defaultOpts := []Option{
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
		WithRemoteEmbeddingModel("bge-base-zh"),
	}
	opts := append(defaultOpts, extraOpts...)
	vs := newVectorStoreWithMockClient(mockClient, opts...)
	return vs, mockClient
}

// setupHybridSearchVectorStore creates a VectorStore with hybrid search enabled for testing.
func setupHybridSearchVectorStore(t *testing.T, extraOpts ...Option) (*VectorStore, *mockClient) {
	t.Helper()
	vs, mockClient := setupRemoteEmbeddingVectorStore(t, extraOpts...)
	vs.sparseEncoder = newMockSparseEncoder()
	vs.option.enableTSVector = true
	return vs, mockClient
}

// TestVectorStore_RemoteEmbedding_VectorSearch tests vector search with remote embedding
func TestVectorStore_RemoteEmbedding_VectorSearch(t *testing.T) {
	tests := []struct {
		name     string
		query    *vectorstore.SearchQuery
		wantErr  bool
		errMsg   string
		validate func(t *testing.T, result *vectorstore.SearchResult, client *mockClient)
	}{
		{
			name: "search_with_text_only",
			query: &vectorstore.SearchQuery{
				Query:      "artificial intelligence",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult, client *mockClient) {
				assert.NotNil(t, result)
				assert.NotNil(t, result.Results)
				assert.Greater(t, client.GetSearchCalls(), 0, "Search should be called")
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
			validate: func(t *testing.T, result *vectorstore.SearchResult, client *mockClient) {
				assert.NotNil(t, result)
				assert.NotNil(t, result.Results)
			},
		},
		{
			name: "search_with_min_score",
			query: &vectorstore.SearchQuery{
				Query:      "test query with score threshold",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				MinScore:   0.8,
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult, client *mockClient) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "search_with_complex_filter",
			query: &vectorstore.SearchQuery{
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
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult, client *mockClient) {
				assert.NotNil(t, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, mockClient := setupRemoteEmbeddingVectorStore(t)
			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result, mockClient)
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
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
				assert.NotNil(t, result.Results)
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
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, _ := setupHybridSearchVectorStore(t)
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
	vs, mockClient := setupRemoteEmbeddingVectorStore(t)

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

// TestVectorStore_RemoteEmbedding_Options tests remote embedding configuration options
func TestVectorStore_RemoteEmbedding_Options(t *testing.T) {
	tests := []struct {
		name            string
		opts            []Option
		expectedEnabled bool
		expectedModel   string
		expectedFilter  bool
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
		{
			name: "empty_model_disables_remote_embedding",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithRemoteEmbeddingModel(""),
			},
			expectedEnabled: false,
			expectedModel:   "",
		},
		{
			name: "with_filter_all_enabled",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithFilterAll(true),
			},
			expectedEnabled: false,
			expectedFilter:  true,
		},
		{
			name: "with_filter_all_disabled",
			opts: []Option{
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithFilterAll(false),
			},
			expectedEnabled: false,
			expectedFilter:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient, tt.opts...)

			assert.Equal(t, tt.expectedEnabled, vs.isRemoteEmbeddingEnabled())
			assert.Equal(t, tt.expectedModel, vs.option.embeddingModel)
			if tt.name == "with_filter_all_enabled" || tt.name == "with_filter_all_disabled" {
				assert.Equal(t, tt.expectedFilter, vs.option.filterAll)
			}
		})
	}
}

// TestVectorStore_RemoteEmbedding_SearchModes tests different search modes with remote embedding
func TestVectorStore_RemoteEmbedding_SearchModes(t *testing.T) {
	tests := []struct {
		name       string
		searchMode vectorstore.SearchMode
		query      string
		setupVS    func(t *testing.T) *VectorStore
	}{
		{
			name:       "keyword_search_mode",
			searchMode: vectorstore.SearchModeKeyword,
			query:      "keyword search test",
			setupVS: func(t *testing.T) *VectorStore {
				vs, _ := setupRemoteEmbeddingVectorStore(t)
				return vs
			},
		},
		{
			name:       "vector_search_mode",
			searchMode: vectorstore.SearchModeVector,
			query:      "vector search test",
			setupVS: func(t *testing.T) *VectorStore {
				vs, _ := setupRemoteEmbeddingVectorStore(t)
				return vs
			},
		},
		{
			name:       "hybrid_search_mode",
			searchMode: vectorstore.SearchModeHybrid,
			query:      "hybrid search test",
			setupVS: func(t *testing.T) *VectorStore {
				vs, _ := setupHybridSearchVectorStore(t)
				return vs
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := tt.setupVS(t)
			query := &vectorstore.SearchQuery{
				Query:      tt.query,
				SearchMode: tt.searchMode,
				Limit:      10,
			}

			result, err := vs.Search(context.Background(), query)
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

// TestVectorStore_RemoteEmbedding_ErrorHandling tests error handling with remote embedding
func TestVectorStore_RemoteEmbedding_ErrorHandling(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockClient)
		query     *vectorstore.SearchQuery
		setupVS   func(t *testing.T, mockClient *mockClient) *VectorStore
		wantErr   bool
		errMsg    string
	}{
		{
			name: "vector_search_error",
			setupMock: func(mc *mockClient) {
				mc.SetSearchError(assert.AnError)
			},
			query: &vectorstore.SearchQuery{
				Query:      "search error test",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
			},
			setupVS: func(t *testing.T, mc *mockClient) *VectorStore {
				return newVectorStoreWithMockClient(mc,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
					WithRemoteEmbeddingModel("bge-base-zh"),
				)
			},
			wantErr: true,
			errMsg:  "tcvectordb",
		},
		{
			name: "hybrid_search_error",
			setupMock: func(mc *mockClient) {
				mc.SetHybridError(assert.AnError)
			},
			query: &vectorstore.SearchQuery{
				Query:      "hybrid search error test",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
			},
			setupVS: func(t *testing.T, mc *mockClient) *VectorStore {
				vs := newVectorStoreWithMockClient(mc,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
					WithRemoteEmbeddingModel("bge-base-zh"),
				)
				vs.sparseEncoder = newMockSparseEncoder()
				vs.option.enableTSVector = true
				return vs
			},
			wantErr: true,
			errMsg:  "tcvectordb",
		},
		{
			name:      "search_without_remote_embedding",
			setupMock: func(mc *mockClient) {},
			query: &vectorstore.SearchQuery{
				Query:      "test without remote embedding",
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
			},
			setupVS: func(t *testing.T, mc *mockClient) *VectorStore {
				return newVectorStoreWithMockClient(mc,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
					WithRemoteEmbeddingModel(""), // Disable remote embedding
				)
			},
			wantErr: true,
			errMsg:  "searching with a nil or empty vector is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)
			vs := tt.setupVS(t, mockClient)

			_, err := vs.Search(context.Background(), tt.query)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
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
