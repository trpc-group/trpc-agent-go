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
					[]float64{0.9, 0.5, 0.2}, map[string]any{"rank": 1}, 0.975)
				rows.AddRow("doc_2", "Second Doc", "Second content", "[0.8,0.4,0.3]",
					mapToJSON(map[string]any{"rank": 2}), 1000000, 2000000, 0.85, 0.05, 0.925)
				rows.AddRow("doc_3", "Third Doc", "Third content", "[0.7,0.6,0.1]",
					mapToJSON(map[string]any{"rank": 3}), 1000000, 2000000, 0.70, 0.05, 0.80)

				// Match any SELECT query with LIMIT
				mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").
					WillReturnRows(rows)
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.Len(t, result.Results, 3)
				assert.Equal(t, "doc_1", result.Results[0].Document.ID)
				assert.Equal(t, 0.975, result.Results[0].Score)
				assert.Equal(t, "doc_2", result.Results[1].Document.ID)
				assert.Equal(t, 0.925, result.Results[1].Score)
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
			name: "no_results",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score"})
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
		{
			// This test documents that dimension validation is enforced at the database level.
			// When a query vector with wrong dimensions is used, PostgreSQL/pgvector returns
			// an error like "expected 3 dimensions, not 2".
			name: "dimension_mismatch_from_database",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5}, // 2 dimensions, but table expects 3
				Limit:      5,
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				// Simulate PostgreSQL/pgvector dimension mismatch error
				mock.ExpectQuery("SELECT .+ FROM documents").
					WillReturnError(errors.New("ERROR: expected 3 dimensions, not 2 (SQLSTATE 22000)"))
			},
			wantErr: true,
			errMsg:  "expected 3 dimensions, not 2",
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

// TestVectorStore_SearchWithMinScore tests MinScore filtering
func TestVectorStore_SearchWithMinScore(t *testing.T) {
	t.Run("vector_search_with_min_score", func(t *testing.T) {
		vs, tc := newTestVectorStore(t, WithIndexDimension(3))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 0.5, 0.2},
			Limit:      5,
			MinScore:   0.8, // MinScore > 0 should add score filter
			SearchMode: vectorstore.SearchModeVector,
		}

		// Mock result with high score
		rows := mockSearchResultRow("doc_1", "High Score Doc", "Highly relevant content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"quality": "high"}, 0.95)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").
			WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		assert.Equal(t, "doc_1", result.Results[0].Document.ID)
		assert.GreaterOrEqual(t, result.Results[0].Score, 0.8)
		tc.AssertExpectations(t)
	})

	t.Run("hybrid_search_with_min_score", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 0.5, 0.2},
			Query:      "test",
			Limit:      5,
			MinScore:   0.75, // MinScore > 0 should add score filter
			SearchMode: vectorstore.SearchModeHybrid,
		}

		rows := mockSearchResultRow("doc_1", "Relevant Doc", "Test content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{}, 0.85)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").
			WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		assert.GreaterOrEqual(t, result.Results[0].Score, 0.75)
		tc.AssertExpectations(t)
	})
}

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
			emptyRows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score"})
			tc.mock.ExpectQuery("SELECT .+ FROM documents").
				WillReturnRows(emptyRows)

			result, err := vs.Search(context.Background(), tt.query)

			require.NoError(t, err)
			assert.Empty(t, result.Results)

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_SearchByKeyword tests keyword search
func TestVectorStore_SearchByKeyword(t *testing.T) {
	t.Run("keyword_search_without_tsvector", func(t *testing.T) {
		vs, tc := newTestVectorStore(t, WithEnableTSVector(false))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Query:      "test query",
			SearchMode: vectorstore.SearchModeKeyword,
			Limit:      10,
		}

		// Should fall back to filter search
		rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		tc.AssertExpectations(t)
	})

	t.Run("keyword_search_with_tsvector", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Query:      "artificial intelligence",
			SearchMode: vectorstore.SearchModeKeyword,
			Limit:      5,
		}

		rows := mockSearchResultRow("doc_1", "AI Doc", "Artificial intelligence content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"category": "AI"}, 0.95)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		assert.Equal(t, "doc_1", result.Results[0].Document.ID)
		tc.AssertExpectations(t)
	})

	t.Run("keyword_search_empty_query", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Query:      "",
			SearchMode: vectorstore.SearchModeKeyword,
			Limit:      5,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keyword is required")
		require.Nil(t, result)
		tc.AssertExpectations(t)
	})
}

// TestVectorStore_SearchByHybrid tests hybrid search
func TestVectorStore_SearchByHybrid(t *testing.T) {
	t.Run("hybrid_search_with_vector_and_text", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 0.5, 0.2},
			Query:      "artificial intelligence",
			SearchMode: vectorstore.SearchModeHybrid,
			Limit:      5,
		}

		rows := mockSearchResultRow("doc_1", "AI Doc", "Artificial intelligence content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"category": "AI"}, 0.92)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		assert.Equal(t, "doc_1", result.Results[0].Document.ID)
		tc.AssertExpectations(t)
	})

	t.Run("hybrid_search_without_tsvector", func(t *testing.T) {
		vs, tc := newTestVectorStore(t, WithEnableTSVector(false), WithIndexDimension(3))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 0.5, 0.2},
			Query:      "test",
			SearchMode: vectorstore.SearchModeHybrid,
			Limit:      5,
		}

		// Should fall back to vector search
		rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		tc.AssertExpectations(t)
	})

	t.Run("hybrid_search_empty_vector", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{},
			Query:      "test",
			SearchMode: vectorstore.SearchModeHybrid,
			Limit:      5,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vector is required")
		require.Nil(t, result)
		tc.AssertExpectations(t)
	})

	t.Run("hybrid_search_vector_only_no_text", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 0.5, 0.2},
			Query:      "", // No text query
			SearchMode: vectorstore.SearchModeHybrid,
			Limit:      5,
		}

		rows := mockSearchResultRow("doc_1", "Test Doc", "Test content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{}, 0.98)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		tc.AssertExpectations(t)
	})
}

// TestVectorStore_SearchByFilter tests filter-only search
func TestVectorStore_SearchByFilter(t *testing.T) {
	t.Run("filter_search_success", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				IDs: []string{"doc_1", "doc_2"},
			},
			Limit: 10,
		}

		rows := mockSearchResultRow("doc_1", "Test Doc", "Test content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"category": "test"}, 1.0)
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		assert.Equal(t, "doc_1", result.Results[0].Document.ID)
		tc.AssertExpectations(t)
	})

	t.Run("filter_search_with_metadata", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]any{"category": "AI"},
			},
			Limit: 5,
		}

		rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT").WillReturnRows(rows)

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		tc.AssertExpectations(t)
	})
}

// TestVectorStore_DeleteByFilter tests delete by filter
func TestVectorStore_DeleteByFilter(t *testing.T) {
	t.Run("delete_all_documents", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		tc.mock.ExpectExec("TRUNCATE TABLE documents").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := vs.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true))
		require.NoError(t, err)
		tc.AssertExpectations(t)
	})

	t.Run("delete_by_ids", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		tc.mock.ExpectExec("DELETE FROM documents WHERE .+ IN").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteDocumentIDs([]string{"doc_1", "doc_2"}))
		require.NoError(t, err)
		tc.AssertExpectations(t)
	})

	t.Run("delete_by_metadata_filter", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		tc.mock.ExpectExec("DELETE FROM documents WHERE").
			WillReturnResult(sqlmock.NewResult(0, 3))

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteFilter(map[string]any{"category": "deprecated"}))
		require.NoError(t, err)
		tc.AssertExpectations(t)
	})

	t.Run("delete_all_with_conflicting_params", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteAll(true),
			vectorstore.WithDeleteDocumentIDs([]string{"doc_1"}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete all documents, but document ids")
		tc.AssertExpectations(t)
	})

	t.Run("delete_without_conditions", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		err := vs.DeleteByFilter(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no filter conditions")
		tc.AssertExpectations(t)
	})
}

// TestVectorStore_Search_InvalidMode tests invalid search mode
func TestVectorStore_Search_InvalidMode(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	query := &vectorstore.SearchQuery{
		SearchMode: 999, // Invalid mode
		Limit:      5,
	}

	result, err := vs.Search(context.Background(), query)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid search mode")
	require.Nil(t, result)
	tc.AssertExpectations(t)
}
