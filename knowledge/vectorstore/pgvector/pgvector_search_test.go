//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_Search tests the Search method with various scenarios
func TestVectorStore_Search(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_simple_search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := mockSearchResultRow("doc_1", "First Doc", "First content",
					[]float64{0.9, 0.5, 0.2}, map[string]any{"rank": 1}, 0.95)
				rows.AddRow("doc_2", "Second Doc", "Second content", "[0.8,0.4,0.3]",
					mapToJSON(map[string]any{"rank": 2}), 1000000, 2000000, 0.85)
				rows.AddRow("doc_3", "Third Doc", "Third content", "[0.7,0.6,0.1]",
					mapToJSON(map[string]any{"rank": 3}), 1000000, 2000000, 0.75)

				// Match any SELECT query with LIMIT
				mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").
					WillReturnRows(rows)
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.Len(t, result.Results, 3)
				assert.Equal(t, "doc_1", result.Results[0].Document.ID)
				assert.Equal(t, 0.95, result.Results[0].Score)
				assert.Equal(t, "doc_2", result.Results[1].Document.ID)
				assert.Equal(t, 0.85, result.Results[1].Score)
			},
		},
		{
			name:      "nil_query",
			query:     nil,
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "query is required",
		},
		{
			name: "empty_query_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "vector is not supported",
		},
		{
			name: "dimension_mismatch",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5}, // Only 2 dimensions
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "no_results",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"})
				mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").
					WillReturnRows(rows)
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.Len(t, result.Results, 0)
			},
		},
		{
			name: "database_error",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT .+ FROM documents").
					WillReturnError(errors.New("connection timeout"))
			},
			wantErr: true,
			errMsg:  "connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

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

			tc.AssertExpectations(t)
		})
	}
}

// Note: Complex search tests with filters, score thresholds, and different search modes
// use very specific SQL patterns that are difficult to mock precisely with sqlmock.
// The basic search functionality is tested above. For comprehensive testing of:
// - Metadata filters
// - Document ID filters
// - Score thresholds
// - Keyword search mode
// - Hybrid search mode
// - Multiple combined filters
// Please refer to pgvector_test.go which uses a real PostgreSQL instance for integration tests.

// TestVectorStore_SearchEmptyResults tests handling of queries that return no results
func TestVectorStore_SearchEmptyResults(t *testing.T) {
	tests := []struct {
		name  string
		query *vectorstore.SearchQuery
	}{
		{
			name: "no_matching_documents",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
		},
		{
			name: "high_score_threshold",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				MinScore:   0.99,
				SearchMode: vectorstore.SearchModeVector,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			// Mock empty result set
			emptyRows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"})
			tc.mock.ExpectQuery("SELECT .+ FROM documents").
				WillReturnRows(emptyRows)

			result, err := vs.Search(context.Background(), tt.query)

			require.NoError(t, err)
			assert.Empty(t, result.Results)

			tc.AssertExpectations(t)
		})
	}
}
