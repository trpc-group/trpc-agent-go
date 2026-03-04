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

// TestVectorStore_SearchByHybridRRF tests RRF hybrid search with mock DB
func TestVectorStore_SearchByHybridRRF(t *testing.T) {
	t.Run("rrf_basic_vector_and_text", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
			WithRRFParams(&RRFParams{K: 60, CandidateRatio: 3}),
		)
		defer tc.Close()

		// RRF sub-queries run in parallel goroutines, so disable ordered matching.
		tc.mock.MatchExpectationsInOrder(false)

		// Mock vector rank sub-query: returns (id, rank)
		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1).
			AddRow("doc_2", 2).
			AddRow("doc_3", 3)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		// Mock text rank sub-query: returns (id, rank)
		textRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_2", 1).
			AddRow("doc_1", 2).
			AddRow("doc_4", 3)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ts_rank").
			WillReturnRows(textRankRows)

		// Mock fetch-by-IDs query
		fetchRows := sqlmock.NewRows([]string{
			"id", "name", "content", "embedding", "metadata",
			"created_at", "updated_at", "vector_score", "text_score", "score",
		}).
			AddRow("doc_1", "Doc 1", "content 1", "[0.1,0.2]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_2", "Doc 2", "content 2", "[0.3,0.4]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_3", "Doc 3", "content 3", "[0.5,0.6]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_4", "Doc 4", "content 4", "[0.7,0.8]", `{}`, 1000, 2000, 0.0, 0.0, 0.0)
		tc.mock.ExpectQuery("SELECT .+ FROM documents WHERE id IN").
			WillReturnRows(fetchRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "test query",
			Limit:      10,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)

		// doc_2 appears in both lists (rank 2 vector + rank 1 text) => highest RRF score
		// doc_1 appears in both lists (rank 1 vector + rank 2 text)
		// doc_2: 1/(60+2) + 1/(60+1) = 1/62 + 1/61
		// doc_1: 1/(60+1) + 1/(60+2) = 1/61 + 1/62
		// They have the same score, but order may vary; both should be present
		assert.GreaterOrEqual(t, len(result.Results), 3)

		// Verify all results have RRF scores set
		for _, doc := range result.Results {
			assert.Greater(t, doc.Score, 0.0)
			assert.Contains(t, doc.Document.Metadata, "trpc_agent_go_dense_score")
			assert.Contains(t, doc.Document.Metadata, "trpc_agent_go_sparse_score")
		}

		tc.AssertExpectations(t)
	})

	t.Run("rrf_vector_only_no_text_query", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		// Only vector rank sub-query expected (no text query)
		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1).
			AddRow("doc_2", 2)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		// Mock fetch-by-IDs query
		fetchRows := sqlmock.NewRows([]string{
			"id", "name", "content", "embedding", "metadata",
			"created_at", "updated_at", "vector_score", "text_score", "score",
		}).
			AddRow("doc_1", "Doc 1", "content 1", "[0.1,0.2]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_2", "Doc 2", "content 2", "[0.3,0.4]", `{}`, 1000, 2000, 0.0, 0.0, 0.0)
		tc.mock.ExpectQuery("SELECT .+ FROM documents WHERE id IN").
			WillReturnRows(fetchRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "", // no text query
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Results, 2)

		// doc_1 rank=1 => score = 1/(60+1) ≈ 0.01639
		// doc_2 rank=2 => score = 1/(60+2) ≈ 0.01613
		assert.Greater(t, result.Results[0].Score, result.Results[1].Score)
		// text_score should be 0 for all docs
		for _, doc := range result.Results {
			assert.Equal(t, 0.0, doc.Document.Metadata["trpc_agent_go_sparse_score"])
		}

		tc.AssertExpectations(t)
	})

	t.Run("rrf_empty_results", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		tc.mock.MatchExpectationsInOrder(false)

		// Both sub-queries return empty
		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"})
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		textRankRows := sqlmock.NewRows([]string{"id", "rank"})
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ts_rank").
			WillReturnRows(textRankRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "no match",
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Results)

		tc.AssertExpectations(t)
	})

	t.Run("rrf_vector_query_error", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		tc.mock.MatchExpectationsInOrder(false)

		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnError(errors.New("db connection lost"))

		textRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ts_rank").
			WillReturnRows(textRankRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "test",
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rrf vector search")
		assert.Nil(t, result)
	})

	t.Run("rrf_text_query_error", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		tc.mock.MatchExpectationsInOrder(false)

		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ts_rank").
			WillReturnError(errors.New("text search failed"))

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "test",
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rrf text search")
		assert.Nil(t, result)
	})

	t.Run("rrf_missing_vector_returns_error", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		query := &vectorstore.SearchQuery{
			Vector:     nil, // no vector
			Query:      "test",
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vector is required")
		assert.Nil(t, result)
	})

	t.Run("rrf_respects_limit", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
			WithRRFParams(&RRFParams{K: 60, CandidateRatio: 2}),
		)
		defer tc.Close()

		// Return many candidates
		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1).
			AddRow("doc_2", 2).
			AddRow("doc_3", 3).
			AddRow("doc_4", 4).
			AddRow("doc_5", 5)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		// Mock fetch-by-IDs (only top 2 should be fetched)
		fetchRows := sqlmock.NewRows([]string{
			"id", "name", "content", "embedding", "metadata",
			"created_at", "updated_at", "vector_score", "text_score", "score",
		}).
			AddRow("doc_1", "Doc 1", "c1", "[0.1,0.2]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_2", "Doc 2", "c2", "[0.3,0.4]", `{}`, 1000, 2000, 0.0, 0.0, 0.0)
		tc.mock.ExpectQuery("SELECT .+ FROM documents WHERE id IN").
			WillReturnRows(fetchRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "", // no text
			Limit:      2,  // only want 2
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Results, 2)

		tc.AssertExpectations(t)
	})

	t.Run("rrf_fetch_documents_error", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
		)
		defer tc.Close()

		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_1", 1)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		tc.mock.ExpectQuery("SELECT .+ FROM documents WHERE id IN").
			WillReturnError(errors.New("fetch failed"))

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.2},
			Query:      "", // no text
			Limit:      5,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rrf fetch documents")
		assert.Nil(t, result)
	})

	t.Run("rrf_score_calculation_correctness", func(t *testing.T) {
		vs, tc := newTestVectorStoreWithTSVector(t,
			WithHybridFusionMode(HybridFusionRRF),
			WithRRFParams(&RRFParams{K: 60, CandidateRatio: 3}),
		)
		defer tc.Close()

		tc.mock.MatchExpectationsInOrder(false)

		// doc_A: vector rank=1, text rank=3
		// doc_B: vector rank=2, text rank=1
		// doc_C: vector rank=3, no text match
		vectorRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_A", 1).
			AddRow("doc_B", 2).
			AddRow("doc_C", 3)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ORDER BY embedding").
			WillReturnRows(vectorRankRows)

		textRankRows := sqlmock.NewRows([]string{"id", "rank"}).
			AddRow("doc_B", 1).
			AddRow("doc_A", 3)
		tc.mock.ExpectQuery("SELECT id, ROW_NUMBER.+ts_rank").
			WillReturnRows(textRankRows)

		fetchRows := sqlmock.NewRows([]string{
			"id", "name", "content", "embedding", "metadata",
			"created_at", "updated_at", "vector_score", "text_score", "score",
		}).
			AddRow("doc_A", "A", "a", "[0.1]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_B", "B", "b", "[0.2]", `{}`, 1000, 2000, 0.0, 0.0, 0.0).
			AddRow("doc_C", "C", "c", "[0.3]", `{}`, 1000, 2000, 0.0, 0.0, 0.0)
		tc.mock.ExpectQuery("SELECT .+ FROM documents WHERE id IN").
			WillReturnRows(fetchRows)

		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1},
			Query:      "test",
			Limit:      10,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := vs.Search(context.Background(), query)
		require.NoError(t, err)
		require.Len(t, result.Results, 3)

		// Expected scores:
		// doc_B: 1/(60+2) + 1/(60+1) = 1/62 + 1/61 ≈ 0.03252
		// doc_A: 1/(60+1) + 1/(60+3) = 1/61 + 1/63 ≈ 0.03228
		// doc_C: 1/(60+3) + 0         = 1/63       ≈ 0.01587
		expectedScoreB := 1.0/62.0 + 1.0/61.0
		expectedScoreA := 1.0/61.0 + 1.0/63.0
		expectedScoreC := 1.0 / 63.0

		// doc_B should be ranked first (highest combined RRF score)
		assert.Equal(t, "doc_B", result.Results[0].Document.ID)
		assert.InDelta(t, expectedScoreB, result.Results[0].Score, 1e-10)

		assert.Equal(t, "doc_A", result.Results[1].Document.ID)
		assert.InDelta(t, expectedScoreA, result.Results[1].Score, 1e-10)

		assert.Equal(t, "doc_C", result.Results[2].Document.ID)
		assert.InDelta(t, expectedScoreC, result.Results[2].Score, 1e-10)

		// Verify dense/sparse score metadata
		assert.InDelta(t, 1.0/62.0, result.Results[0].Document.Metadata["trpc_agent_go_dense_score"], 1e-10)
		assert.InDelta(t, 1.0/61.0, result.Results[0].Document.Metadata["trpc_agent_go_sparse_score"], 1e-10)

		assert.InDelta(t, 1.0/61.0, result.Results[1].Document.Metadata["trpc_agent_go_dense_score"], 1e-10)
		assert.InDelta(t, 1.0/63.0, result.Results[1].Document.Metadata["trpc_agent_go_sparse_score"], 1e-10)

		assert.InDelta(t, 1.0/63.0, result.Results[2].Document.Metadata["trpc_agent_go_dense_score"], 1e-10)
		assert.Equal(t, 0.0, result.Results[2].Document.Metadata["trpc_agent_go_sparse_score"])

		tc.AssertExpectations(t)
	})
}
