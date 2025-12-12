//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_Search tests Search method with various scenarios
func TestVectorStore_Search(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient, *VectorStore) // Added *VectorStore for setup that needs Add
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_vector_search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      5,
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				mc.SetSearchHits([]map[string]any{
					{
						"_score": 0.95,
						"_source": map[string]any{
							"id":      "doc1",
							"name":    "High Match",
							"content": "Very relevant content",
						},
					},
				})
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				require.GreaterOrEqual(t, len(result.Results), 1)
				assert.Equal(t, "High Match", result.Results[0].Document.Name)
				assert.Equal(t, 0.95, result.Results[0].Score)
			},
		},
		{
			name:    "nil_query",
			query:   nil,
			wantErr: true,
			errMsg:  "query cannot be nil",
		},
		{
			name: "empty_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				SearchMode: vectorstore.SearchModeVector,
			},
			wantErr: true,
		},
		{
			name: "wrong_dimension",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2}, // Only 2 dimensions, expected 3
				SearchMode: vectorstore.SearchModeVector,
			},
			wantErr: true,
			errMsg:  "dimension",
		},
		{
			name: "empty_results",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				mc.SetSearchHits([]map[string]any{}) // Empty results
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				assert.Equal(t, 0, len(result.Results))
			},
		},
		{
			name: "client_error",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				mc.SetSearchError(errors.New("search service unavailable"))
			},
			wantErr: true,
			errMsg:  "search service unavailable",
		},
		{
			name: "with_min_score_filter",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				MinScore:   0.8,
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				ctx := context.Background()
				// Add documents with varying similarity scores
				for i := 0; i < 5; i++ {
					doc := &document.Document{
						ID:      fmt.Sprintf("score_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}
					_ = vs.Add(ctx, doc, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				// All results should have score >= minScore
				for _, res := range result.Results {
					assert.GreaterOrEqual(t, res.Score, 0.8)
				}
			},
		},
		{
			name: "with_metadata_filter",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "test",
					},
				},
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				ctx := context.Background()
				doc := &document.Document{
					ID:      "filtered_doc",
					Content: "Test content",
					Metadata: map[string]any{
						"category": "test",
					},
				}
				_ = vs.Add(ctx, doc, []float64{1.0, 0.5, 0.2})
			},
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				// Could add more specific assertions if needed
			},
		},
		{
			name: "keyword_mode_with_tsvector_disabled",
			query: &vectorstore.SearchQuery{
				Query:      "hello",
				SearchMode: vectorstore.SearchModeKeyword,
				Limit:      1,
			},
			setupMock: func(mc *mockClient, vs *VectorStore) {
				mc.SetSearchHits([]map[string]any{
					{
						"_score": 0.9,
						"_source": map[string]any{
							"id":      "doc1",
							"name":    "Doc 1",
							"content": "Hello world",
						},
					},
				})
			},
			wantErr: true,
			errMsg:  "query vector cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			vs := newTestVectorStore(
				t,
				mc,
				WithScoreThreshold(0.5),
				WithVectorDimension(3),
				WithEnableTSVector(false),
			)

			if tt.setupMock != nil {
				tt.setupMock(mc, vs)
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
