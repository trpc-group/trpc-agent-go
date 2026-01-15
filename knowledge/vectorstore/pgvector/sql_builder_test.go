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
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestUpdateBuilder tests the update builder functionality without database
func TestUpdateBuilder(t *testing.T) {
	tests := []struct {
		name             string
		docID            string
		fields           map[string]any
		expectedContains []string
		expectedLen      int
		validateArg      func(*testing.T, []any)
	}{
		{
			name:   "basic_update",
			docID:  "doc1",
			fields: map[string]any{"name": "Updated Name", "content": "Updated Content"},
			expectedContains: []string{
				"UPDATE documents SET",
				"updated_at = $2",
				"WHERE id = $1",
				"name =",
				"content =",
			},
			expectedLen: 4,
			validateArg: func(t *testing.T, args []any) {
				assert.Equal(t, "doc1", args[0])
				// args[1] is timestamp
				// args[2] and args[3] are the two field values in unspecified order
				assert.Contains(t, args, "Updated Name")
				assert.Contains(t, args, "Updated Content")
			},
		},
		{
			name:   "single_field_update",
			docID:  "doc2",
			fields: map[string]any{"name": "New Name"},
			expectedContains: []string{
				"UPDATE documents SET",
				"updated_at = $2",
				"name = $3",
				"WHERE id = $1",
			},
			expectedLen: 3,
			validateArg: func(t *testing.T, args []any) {
				assert.Equal(t, "doc2", args[0])
				assert.Equal(t, "New Name", args[2])
			},
		},
		{
			name:   "no_additional_fields",
			docID:  "doc3",
			fields: map[string]any{},
			expectedContains: []string{
				"UPDATE documents SET",
				"updated_at = $2",
				"WHERE id = $1",
			},
			expectedLen: 2,
			validateArg: func(t *testing.T, args []any) {
				assert.Equal(t, "doc3", args[0])
			},
		},
		{
			name:   "vector_update",
			docID:  "doc4",
			fields: map[string]any{"embedding": pgvector.NewVector([]float32{0.1, 0.2, 0.3})},
			expectedContains: []string{
				"UPDATE documents SET",
				"updated_at = $2",
				"embedding = $3",
				"WHERE id = $1",
			},
			expectedLen: 3,
			validateArg: func(t *testing.T, args []any) {
				assert.Equal(t, "doc4", args[0])
			},
		},
		{
			name:   "metadata_update",
			docID:  "doc5",
			fields: map[string]any{"metadata": `{"key": "value"}`},
			expectedContains: []string{
				"UPDATE documents SET",
				"updated_at = $2",
				"metadata = $3",
				"WHERE id = $1",
			},
			expectedLen: 3,
			validateArg: func(t *testing.T, args []any) {
				assert.Equal(t, "doc5", args[0])
				assert.Equal(t, `{"key": "value"}`, args[2])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ub := newUpdateBuilder(defaultOptions, tt.docID)

			// Verify initial state
			assert.Equal(t, tt.docID, ub.args[0])

			// Add fields
			for field, value := range tt.fields {
				ub.addField(field, value)
			}

			sql, args := ub.build()

			// Verify SQL structure contains all expected parts
			for _, expected := range tt.expectedContains {
				assert.Contains(t, sql, expected, "SQL should contain: %s", expected)
			}
			assert.Len(t, args, tt.expectedLen)

			// Validate args
			if tt.validateArg != nil {
				tt.validateArg(t, args)
			}
		})
	}
}

// TestQueryBuilders tests all query builder types with a unified approach
func TestQueryBuilders(t *testing.T) {
	tests := []struct {
		name             string
		builderType      vectorstore.SearchMode
		vectorWeight     float64
		textWeight       float64
		setupFunc        func(*queryBuilder)
		expectedContains []string
		expectedOrder    string
		minArgs          int
	}{
		// Vector Query Tests
		{
			name:        "vector_basic",
			builderType: vectorstore.SearchModeVector,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
			},
			expectedContains: []string{"SELECT", "vector_score as score", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          1,
		},
		{
			name:        "vector_with_id_filter",
			builderType: vectorstore.SearchModeVector,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
				qb.addIDFilter([]string{"doc1", "doc2"})
			},
			expectedContains: []string{"WHERE", "id IN", "$2, $3", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          3,
		},
		{
			name:        "vector_with_score_filter",
			builderType: vectorstore.SearchModeVector,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
				qb.addScoreFilter(0.5)
			},
			expectedContains: []string{"WHERE", "(1.0 - (embedding <=> $1) / 2.0) >= 0.500000", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          1,
		},
		{
			name:        "vector_with_metadata_filter",
			builderType: vectorstore.SearchModeVector,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
				qb.addMetadataFilter(map[string]any{"category": "AI"})
			},
			expectedContains: []string{"WHERE", "metadata @>", "::jsonb", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          2,
		},

		// Keyword Query Tests
		{
			name:        "keyword_basic",
			builderType: vectorstore.SearchModeKeyword,
			setupFunc: func(qb *queryBuilder) {
				qb.addKeywordSearchConditions("machine learning", 0.0)
			},
			expectedContains: []string{"SELECT", "to_tsvector", "ts_rank", "text_score as score", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          1,
		},
		{
			name:        "keyword_with_min_score",
			builderType: vectorstore.SearchModeKeyword,
			setupFunc: func(qb *queryBuilder) {
				qb.addKeywordSearchConditions("machine learning", 0.1)
			},
			expectedContains: []string{"WHERE", "ts_rank", ">= $", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          2,
		},
		{
			name:        "keyword_with_id_filter",
			builderType: vectorstore.SearchModeKeyword,
			setupFunc: func(qb *queryBuilder) {
				qb.addKeywordSearchConditions("test", 0.0)
				qb.addIDFilter([]string{"doc1", "doc3"})
			},
			expectedContains: []string{"WHERE", "id IN", "FROM (", ") subq"},
			expectedOrder:    "",
			minArgs:          3,
		},

		// Hybrid Query Tests
		{
			name:         "hybrid_with_text",
			builderType:  vectorstore.SearchModeHybrid,
			vectorWeight: 0.7,
			textWeight:   0.3,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
				qb.addHybridFtsCondition("machine learning")
			},
			expectedContains: []string{"0.700", "0.300", "ts_rank", "/ (COALESCE(ts_rank", "ORDER BY", "DESC"},
			expectedOrder:    "",
			minArgs:          2,
		},
		{
			name:         "hybrid_vector_only",
			builderType:  vectorstore.SearchModeHybrid,
			vectorWeight: 1.0,
			textWeight:   0.0,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
			},
			expectedContains: []string{"1.000", "as score", "ORDER BY", "DESC"},
			expectedOrder:    "",
			minArgs:          1,
		},
		{
			name:         "hybrid_with_score_filter",
			builderType:  vectorstore.SearchModeHybrid,
			vectorWeight: 0.6,
			textWeight:   0.4,
			setupFunc: func(qb *queryBuilder) {
				qb.addVectorArg(pgvector.NewVector([]float32{0.1, 0.2, 0.3}))
				qb.addHybridFtsCondition("test")
				qb.addScoreFilter(0.5)
			},
			expectedContains: []string{"WHERE", ">= 0.500", "ORDER BY", "DESC"},
			expectedOrder:    "",
			minArgs:          2,
		},

		// Filter Query Tests
		{
			name:        "filter_with_ids",
			builderType: vectorstore.SearchModeFilter,
			setupFunc: func(qb *queryBuilder) {
				qb.addIDFilter([]string{"doc1", "doc2", "doc3"})
			},
			expectedContains: []string{"SELECT", "1.0 as score", "WHERE", "id IN"},
			expectedOrder:    "ORDER BY created_at DESC",
			minArgs:          3,
		},
		{
			name:        "filter_with_metadata",
			builderType: vectorstore.SearchModeFilter,
			setupFunc: func(qb *queryBuilder) {
				qb.addMetadataFilter(map[string]any{"category": "AI"})
			},
			expectedContains: []string{"1.0 as score", "metadata @>", "::jsonb"},
			expectedOrder:    "ORDER BY created_at DESC",
			minArgs:          1,
		},
		{
			name:        "filter_with_both",
			builderType: vectorstore.SearchModeFilter,
			setupFunc: func(qb *queryBuilder) {
				qb.addIDFilter([]string{"doc1", "doc2"})
				qb.addMetadataFilter(map[string]any{"category": "AI"})
			},
			expectedContains: []string{"id IN", "metadata @>"},
			expectedOrder:    "ORDER BY created_at DESC",
			minArgs:          3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create appropriate builder based on type
			var qb *queryBuilder
			switch tt.builderType {
			case vectorstore.SearchModeVector:
				qb = newVectorQueryBuilder(defaultOptions)
			case vectorstore.SearchModeKeyword:
				qb = newKeywordQueryBuilder(defaultOptions)
			case vectorstore.SearchModeHybrid:
				qb = newHybridQueryBuilder(defaultOptions, tt.vectorWeight, tt.textWeight)
				assert.Equal(t, tt.vectorWeight, qb.vectorWeight)
				assert.Equal(t, tt.textWeight, qb.textWeight)
			case vectorstore.SearchModeFilter:
				qb = newFilterQueryBuilder(defaultOptions)
			}

			// Verify builder configuration
			assert.Equal(t, tt.builderType, qb.searchMode)
			if tt.expectedOrder != "" {
				assert.Equal(t, tt.expectedOrder, qb.orderClause)
			}

			// Setup and build query
			tt.setupFunc(qb)
			sql, args := qb.build(10)

			// Verify SQL structure
			for _, expected := range tt.expectedContains {
				assert.Contains(t, sql, expected, "SQL should contain: %s", expected)
			}

			// Verify arguments
			assert.GreaterOrEqual(t, len(args), tt.minArgs)
		})
	}
}

// TestBuildSelectClause tests the dynamic SELECT clause building
func TestBuildSelectClause(t *testing.T) {
	tests := []struct {
		name                string
		mode                vectorstore.SearchMode
		vectorWeight        float64
		textWeight          float64
		textQueryPos        int
		expectedContains    []string
		expectedNotContains []string
	}{
		{
			name:             "vector_mode",
			mode:             vectorstore.SearchModeVector,
			expectedContains: []string{"(1.0 - (embedding <=> $1) / 2.0) as score"},
		},
		{
			name:             "keyword_mode_with_text",
			mode:             vectorstore.SearchModeKeyword,
			textQueryPos:     1,
			expectedContains: []string{"ts_rank", "as score"},
		},
		{
			name:             "keyword_mode_without_text",
			mode:             vectorstore.SearchModeKeyword,
			textQueryPos:     0,
			expectedContains: []string{"0.0 as score"},
		},
		{
			name:             "hybrid_mode_with_text",
			mode:             vectorstore.SearchModeHybrid,
			vectorWeight:     0.6,
			textWeight:       0.4,
			textQueryPos:     2,
			expectedContains: []string{"0.600", "0.400", "as score", "ts_rank", "/ (COALESCE(ts_rank"},
		},
		{
			name:                "hybrid_mode_without_text",
			mode:                vectorstore.SearchModeHybrid,
			vectorWeight:        0.8,
			textWeight:          0.2,
			textQueryPos:        0,
			expectedContains:    []string{"0.800", "as score"},
			expectedNotContains: []string{"ts_rank"},
		},
		{
			name:             "filter_mode",
			mode:             vectorstore.SearchModeFilter,
			expectedContains: []string{"1.0 as score"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var qb *queryBuilder
			switch tt.mode {
			case vectorstore.SearchModeVector:
				qb = newVectorQueryBuilder(defaultOptions)
			case vectorstore.SearchModeKeyword:
				qb = newKeywordQueryBuilder(defaultOptions)
			case vectorstore.SearchModeHybrid:
				qb = newHybridQueryBuilder(defaultOptions, tt.vectorWeight, tt.textWeight)
			case vectorstore.SearchModeFilter:
				qb = newFilterQueryBuilder(defaultOptions)
			}

			qb.textQueryPos = tt.textQueryPos
			selectClause := qb.buildSelectClause()

			// Check expected contains
			for _, expected := range tt.expectedContains {
				assert.Contains(t, selectClause, expected)
			}

			// Check expected not contains
			for _, notExpected := range tt.expectedNotContains {
				assert.NotContains(t, selectClause, notExpected)
			}
		})
	}
}

// TestQueryBuilderEdgeCases tests edge cases and empty filter handling
func TestQueryBuilderEdgeCases(t *testing.T) {
	tests := []struct {
		name                string
		idFilter            []string
		metadataFilter      map[string]any
		expectedContains    []string
		expectedNotContains []string
	}{
		{
			name:                "empty_filters",
			idFilter:            []string{},
			metadataFilter:      map[string]any{},
			expectedNotContains: []string{"id IN", "metadata @>"},
		},
		{
			name:                "empty_id_filter_only",
			idFilter:            []string{},
			metadataFilter:      map[string]any{"test": "value"},
			expectedContains:    []string{"metadata @>"},
			expectedNotContains: []string{"id IN"},
		},
		{
			name:                "empty_metadata_filter_only",
			idFilter:            []string{"doc1"},
			metadataFilter:      map[string]any{},
			expectedContains:    []string{"id IN"},
			expectedNotContains: []string{"metadata @>"},
		},
		{
			name:             "both_filters_present",
			idFilter:         []string{"doc1", "doc2"},
			metadataFilter:   map[string]any{"test": "value", "score": 95},
			expectedContains: []string{"id IN", "metadata @>"},
		},
		{
			name:                "nil_metadata_filter",
			idFilter:            []string{"doc1"},
			metadataFilter:      nil,
			expectedContains:    []string{"id IN"},
			expectedNotContains: []string{"metadata @>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := newVectorQueryBuilder(defaultOptions)

			// Add filters
			qb.addIDFilter(tt.idFilter)
			qb.addMetadataFilter(tt.metadataFilter)

			sql, _ := qb.build(10)

			// Check expected contains
			for _, expected := range tt.expectedContains {
				assert.Contains(t, sql, expected)
			}

			// Check expected not contains
			for _, notExpected := range tt.expectedNotContains {
				assert.NotContains(t, sql, notExpected)
			}
		})
	}
}

// TestMetadataQueryBuilder tests metadata query builder without database
func TestMetadataQueryBuilder(t *testing.T) {
	tests := []struct {
		name             string
		setupFunc        func(*metadataQueryBuilder)
		limit            int
		offset           int
		expectedContains []string
		expectedArgs     int
	}{
		{
			name:      "basic_query",
			setupFunc: func(mqb *metadataQueryBuilder) {},
			limit:     10,
			offset:    0,
			expectedContains: []string{
				"SELECT *",
				"0.0 as vector_score",
				"0.0 as text_score",
				"0.0 as score",
				"FROM documents",
				"WHERE 1=1",
				"ORDER BY created_at",
				"LIMIT $1 OFFSET $2",
			},
			expectedArgs: 2,
		},
		{
			name: "with_id_filter",
			setupFunc: func(mqb *metadataQueryBuilder) {
				mqb.addIDFilter([]string{"id1", "id2", "id3"})
			},
			limit:  10,
			offset: 0,
			expectedContains: []string{
				"id IN ($1, $2, $3)",
			},
			expectedArgs: 5, // id1, id2, id3, limit, offset
		},
		{
			name: "with_metadata_filter",
			setupFunc: func(mqb *metadataQueryBuilder) {
				mqb.addMetadataFilter(map[string]any{"category": "test", "status": "active"})
			},
			limit:  10,
			offset: 0,
			expectedContains: []string{
				"metadata @> $1::jsonb",
			},
			expectedArgs: 3, // metadata, limit, offset
		},
		{
			name: "with_both_filters",
			setupFunc: func(mqb *metadataQueryBuilder) {
				mqb.addIDFilter([]string{"id1", "id2"})
				mqb.addMetadataFilter(map[string]any{"category": "test"})
			},
			limit:  5,
			offset: 10,
			expectedContains: []string{
				"id IN ($1, $2)",
				"metadata @> $3::jsonb",
			},
			expectedArgs: 5, // id1, id2, metadata, limit, offset
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mqb := newMetadataQueryBuilder(defaultOptions)
			tt.setupFunc(mqb)

			sql, args := mqb.buildWithPagination(tt.limit, tt.offset)

			// Verify SQL structure
			for _, expected := range tt.expectedContains {
				assert.Contains(t, sql, expected)
			}

			// Verify arguments
			assert.Len(t, args, tt.expectedArgs)
			assert.Equal(t, tt.limit, args[len(args)-2])
			assert.Equal(t, tt.offset, args[len(args)-1])
		})
	}
}

// TestCountQueryBuilder tests count query builder without database
func TestCountQueryBuilder(t *testing.T) {
	tests := []struct {
		name         string
		setupFunc    func(*countQueryBuilder)
		expectedSQL  string
		expectedArgs int
	}{
		{
			name:         "basic_count",
			setupFunc:    func(cqb *countQueryBuilder) {},
			expectedSQL:  "SELECT COUNT(*) FROM documents WHERE 1=1",
			expectedArgs: 0,
		},
		{
			name: "count_with_metadata_filter",
			setupFunc: func(cqb *countQueryBuilder) {
				cqb.addMetadataFilter(map[string]any{"category": "science", "status": "published"})
			},
			expectedSQL:  "SELECT COUNT(*) FROM documents WHERE 1=1 AND metadata @> $1::jsonb",
			expectedArgs: 1,
		},
		{
			name: "count_with_empty_filter",
			setupFunc: func(cqb *countQueryBuilder) {
				cqb.addMetadataFilter(map[string]any{})
			},
			expectedSQL:  "SELECT COUNT(*) FROM documents WHERE 1=1",
			expectedArgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cqb := newCountQueryBuilder(defaultOptions)
			tt.setupFunc(cqb)

			sql, args := cqb.build()

			assert.Equal(t, tt.expectedSQL, sql)
			assert.Len(t, args, tt.expectedArgs)
		})
	}
}

// TestDeleteSQLBuilder tests delete SQL builder without database
func TestDeleteSQLBuilder(t *testing.T) {
	tests := []struct {
		name         string
		setupFunc    func(*deleteSQLBuilder)
		expectedSQL  string
		expectedArgs int
		validateArgs func(*testing.T, []any)
	}{
		{
			name:         "basic_delete",
			setupFunc:    func(dsb *deleteSQLBuilder) {},
			expectedSQL:  "DELETE FROM documents WHERE 1=1",
			expectedArgs: 0,
		},
		{
			name: "delete_with_id_filter",
			setupFunc: func(dsb *deleteSQLBuilder) {
				dsb.addIDFilter([]string{"doc1", "doc2", "doc3"})
			},
			expectedSQL:  "DELETE FROM documents WHERE 1=1 AND id IN ($1, $2, $3)",
			expectedArgs: 3,
			validateArgs: func(t *testing.T, args []any) {
				assert.Equal(t, []any{"doc1", "doc2", "doc3"}, args)
			},
		},
		{
			name: "delete_with_metadata_filter",
			setupFunc: func(dsb *deleteSQLBuilder) {
				dsb.addMetadataFilter(map[string]any{"category": "test", "status": "deleted"})
			},
			expectedSQL:  "DELETE FROM documents WHERE 1=1 AND metadata @> $1::jsonb",
			expectedArgs: 1,
		},
		{
			name: "delete_with_both_filters",
			setupFunc: func(dsb *deleteSQLBuilder) {
				dsb.addIDFilter([]string{"doc1", "doc2"})
				dsb.addMetadataFilter(map[string]any{"category": "test"})
			},
			expectedSQL:  "DELETE FROM documents WHERE 1=1 AND id IN ($1, $2) AND metadata @> $3::jsonb",
			expectedArgs: 3,
			validateArgs: func(t *testing.T, args []any) {
				assert.Equal(t, "doc1", args[0])
				assert.Equal(t, "doc2", args[1])
				assert.Contains(t, args[2], "category")
			},
		},
		{
			name: "delete_with_empty_filters",
			setupFunc: func(dsb *deleteSQLBuilder) {
				dsb.addIDFilter([]string{})
				dsb.addMetadataFilter(map[string]any{})
			},
			expectedSQL:  "DELETE FROM documents WHERE 1=1",
			expectedArgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsb := newDeleteSQLBuilder(defaultOptions)
			tt.setupFunc(dsb)

			sql, args := dsb.build()

			assert.Equal(t, tt.expectedSQL, sql)
			assert.Len(t, args, tt.expectedArgs)

			if tt.validateArgs != nil {
				tt.validateArgs(t, args)
			}
		})
	}
}

// TestBuildUpsertSQL tests the upsert SQL generation
func TestBuildUpsertSQL(t *testing.T) {
	o := defaultOptions
	o.table = "test_table"

	expected := `
		INSERT INTO test_table (id, name, content, embedding, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			metadata = EXCLUDED.metadata,
			updated_at = EXCLUDED.updated_at`

	upsertSQL := buildUpsertSQL(o)
	assert.Equal(t, expected, upsertSQL)
}

// TestQueryBuilderFilterCondition tests filter condition handling
func TestQueryBuilderFilterCondition(t *testing.T) {
	tests := []struct {
		name             string
		condition        *condConvertResult
		expectedContains []string
	}{
		{
			name: "simple_condition",
			condition: &condConvertResult{
				cond: "age > 18",
				args: []any{},
			},
			expectedContains: []string{"WHERE", "age > 18"},
		},
		{
			name: "condition_with_args",
			condition: &condConvertResult{
				cond: "name = $1 AND age > $2",
				args: []any{"John", 18},
			},
			expectedContains: []string{"WHERE", "name = $1", "age > $2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := newVectorQueryBuilder(defaultOptions)
			qb.addFilterCondition(tt.condition)

			sql, _ := qb.build(10)

			for _, expected := range tt.expectedContains {
				assert.Contains(t, sql, expected)
			}
		})
	}
}

// TestQueryBuilderMultipleFilters tests combining multiple filters
func TestQueryBuilderMultipleFilters(t *testing.T) {
	qb := newVectorQueryBuilder(defaultOptions)

	// Add vector
	vector := pgvector.NewVector([]float32{0.1, 0.2, 0.3})
	qb.addVectorArg(vector)

	// Add ID filter
	qb.addIDFilter([]string{"doc1", "doc2"})

	// Add metadata filter
	qb.addMetadataFilter(map[string]any{"category": "AI", "score": 95})

	// Add score filter
	qb.addScoreFilter(0.8)

	// Add filter condition
	qb.addFilterCondition(&condConvertResult{
		cond: "created_at > $5",
		args: []any{},
	})

	sql, args := qb.build(10)

	// Verify all filters are present
	require.Contains(t, sql, "id IN")
	require.Contains(t, sql, "metadata @>")
	require.Contains(t, sql, ">= 0.800")
	require.Contains(t, sql, "created_at > $")

	// Verify we have multiple arguments
	assert.GreaterOrEqual(t, len(args), 4, "Should have at least vector + 2 ids + metadata + limit")
}

// TestUpdateByFilterBuilder tests the updateByFilterBuilder functionality
func TestUpdateByFilterBuilder(t *testing.T) {
	t.Run("basic_field_update", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addField("name", "new_name")
		ub.addIDFilter([]string{"doc1", "doc2"})

		sql, args := ub.build()

		assert.Contains(t, sql, "UPDATE documents SET")
		assert.Contains(t, sql, "updated_at = $1")
		assert.Contains(t, sql, "name = $2")
		assert.Contains(t, sql, "WHERE")
		assert.Contains(t, sql, "id IN")
		assert.Len(t, args, 4) // timestamp + name + 2 ids
	})

	t.Run("multiple_fields_update", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addField("name", "new_name")
		ub.addField("content", "new_content")
		ub.addIDFilter([]string{"doc1"})

		sql, args := ub.build()

		assert.Contains(t, sql, "name = $2")
		assert.Contains(t, sql, "content = $3")
		assert.Len(t, args, 4) // timestamp + name + content + 1 id
	})

	t.Run("metadata_field_update", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addMetadataField("status", "archived")
		ub.addIDFilter([]string{"doc1"})

		sql, args := ub.build()

		assert.Contains(t, sql, "jsonb_set")
		assert.Contains(t, sql, "ARRAY[$2]::text[]") // SQL ARRAY constructor
		assert.Contains(t, sql, "$3::jsonb")         // parameterized value
		assert.Len(t, args, 4)                       // timestamp + field + value + 1 id
		assert.Equal(t, "status", args[1])           // plain string field name
		assert.Equal(t, `"archived"`, args[2])       // JSON encoded string
	})

	t.Run("metadata_field_with_number", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addMetadataField("score", 95)
		ub.addIDFilter([]string{"doc1"})

		sql, args := ub.build()

		assert.Contains(t, sql, "jsonb_set")
		assert.Contains(t, sql, "ARRAY[$2]::text[]")
		assert.Equal(t, "score", args[1]) // plain string field name
		assert.Equal(t, "95", args[2])    // JSON encoded number
	})

	t.Run("metadata_field_with_bool", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addMetadataField("active", true)
		ub.addIDFilter([]string{"doc1"})

		sql, args := ub.build()

		assert.Contains(t, sql, "jsonb_set")
		assert.Contains(t, sql, "ARRAY[$2]::text[]")
		assert.Equal(t, "active", args[1]) // plain string field name
		assert.Equal(t, "true", args[2])   // JSON encoded bool
	})

	t.Run("embedding_field_update", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addEmbeddingField([]float64{0.1, 0.2, 0.3})
		ub.addIDFilter([]string{"doc1"})

		sql, args := ub.build()

		assert.Contains(t, sql, "embedding = $2")
		assert.Len(t, args, 3) // timestamp + embedding + 1 id
	})

	t.Run("with_metadata_filter", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addField("name", "new_name")
		ub.addMetadataFilter(map[string]any{"category": "test"})

		sql, args := ub.build()

		assert.Contains(t, sql, "metadata @>")
		assert.Contains(t, sql, "::jsonb")
		assert.Len(t, args, 3) // timestamp + name + metadata filter
	})

	t.Run("combined_filters_and_updates", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addField("name", "new_name")
		ub.addMetadataField("status", "updated")
		ub.addIDFilter([]string{"doc1", "doc2"})
		ub.addMetadataFilter(map[string]any{"category": "test"})

		sql, args := ub.build()

		assert.Contains(t, sql, "UPDATE documents SET")
		assert.Contains(t, sql, "name =")
		assert.Contains(t, sql, "jsonb_set")
		assert.Contains(t, sql, "id IN")
		assert.Contains(t, sql, "metadata @>")
		// timestamp + name + path + value + 2 ids + metadata filter
		assert.Len(t, args, 7)
	})

	t.Run("with_filter_condition", func(t *testing.T) {
		ub := newUpdateByFilterBuilder(defaultOptions)
		ub.addField("name", "new_name")
		ub.addFilterCondition(&condConvertResult{
			cond: "created_at > $%d",
			args: []any{1000},
		})

		sql, args := ub.build()

		assert.Contains(t, sql, "created_at >")
		assert.Len(t, args, 3) // timestamp + name + created_at value
	})

	t.Run("metadata_field_with_special_chars", func(t *testing.T) {
		// With ARRAY[$n] constructor, special characters are safe
		testCases := []struct {
			field string
			desc  string
		}{
			{field: "test}", desc: "closing brace"},
			{field: "test{", desc: "opening brace"},
			{field: `test"`, desc: "double quote"},
			{field: "test'", desc: "single quote"},
			{field: `test\`, desc: "backslash"},
		}

		for _, tc := range testCases {
			t.Run(tc.desc, func(t *testing.T) {
				ub := newUpdateByFilterBuilder(defaultOptions)
				err := ub.addMetadataField(tc.field, "value")
				assert.NoError(t, err) // parameterized field handles special chars safely

				sql, args := ub.build()
				assert.Contains(t, sql, "ARRAY[$2]::text[]")
				assert.Equal(t, tc.field, args[1]) // field preserved as plain string
			})
		}
	})
}
