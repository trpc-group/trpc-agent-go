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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_Search tests Search method with vector search
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
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      5,
			},
			setupMock: func(mc *mockClient) {
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
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				require.GreaterOrEqual(t, len(result.Results), 1)
				assert.Equal(t, "High Match", result.Results[0].Document.Name)
				assert.Equal(t, 0.95, result.Results[0].Score)
			},
		},
		{
			name:      "nil_query",
			query:     nil,
			setupMock: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "query cannot be nil",
		},
		{
			name: "empty_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mc *mockClient) {},
			wantErr:   true,
		},
		{
			name: "wrong_dimension",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2}, // Only 2 dimensions, expected 3
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "dimension",
		},
		{
			name: "empty_results",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mc *mockClient) {
				mc.SetSearchHits([]map[string]any{}) // Empty results
			},
			wantErr: false,
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
			setupMock: func(mc *mockClient) {
				mc.SetSearchError(errors.New("search service unavailable"))
			},
			wantErr: true,
			errMsg:  "search service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupMock(mc)
			vs := newTestVectorStore(t, mc, WithScoreThreshold(0.5), WithVectorDimension(3))

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
