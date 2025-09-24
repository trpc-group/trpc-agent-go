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
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// SQLBuilderTestSuite contains the test suite for SQL builder.
type SQLBuilderTestSuite struct {
	suite.Suite
	pool *pgxpool.Pool
}

// Run the test suite.
func TestSQLBuilderSuite(t *testing.T) {
	suite.Run(t, new(SQLBuilderTestSuite))
}

// SetupSuite runs once before all tests.
func (suite *SQLBuilderTestSuite) SetupSuite() {
	// Use the same environment variables as pgvector_test.go
	host := getEnvOrDefault("PGVECTOR_HOST", "")
	if host == "" {
		suite.T().Skip("Skipping SQL Builder tests: PGVECTOR_HOST not set")
		return
	}

	port := getEnvIntOrDefault("PGVECTOR_PORT", 5432)
	user := getEnvOrDefault("PGVECTOR_USER", "root")
	password := getEnvOrDefault("PGVECTOR_PASSWORD", "")
	database := getEnvOrDefault("PGVECTOR_DATABASE", "trpc_agent_unit_test")

	// Build connection string.
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, database)

	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		suite.T().Skipf("Skipping SQL Builder tests: failed to connect to database: %v", err)
		return
	}

	// Test connection.
	err = pool.Ping(context.Background())
	if err != nil {
		suite.T().Skipf("Skipping SQL Builder tests: cannot ping PostgreSQL: %v", err)
		return
	}

	// Enable pgvector extension.
	_, err = pool.Exec(context.Background(), "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		suite.T().Skipf("Skipping SQL Builder tests: cannot enable vector extension: %v", err)
		return
	}

	suite.pool = pool
}

// TearDownSuite runs once after all tests.
func (suite *SQLBuilderTestSuite) TearDownSuite() {
	if suite.pool != nil {
		suite.pool.Close()
	}
}

// SetupTest runs before each test.
func (suite *SQLBuilderTestSuite) SetupTest() {
	if suite.pool == nil {
		suite.T().Skip("Database connection not available")
	}

	// Create test table.
	createTableSQL := fmt.Sprintf(sqlCreateTable, "test_documents", 3)
	_, err := suite.pool.Exec(context.Background(), createTableSQL)
	require.NoError(suite.T(), err)

	// Create vector index.
	indexSQL := fmt.Sprintf(sqlCreateIndex, "test_documents", "test_documents")
	_, err = suite.pool.Exec(context.Background(), indexSQL)
	require.NoError(suite.T(), err)

	// Create text index for full-text search.
	textIndexSQL := fmt.Sprintf(sqlCreateTextIndex, "test_documents", "test_documents", "english")
	_, err = suite.pool.Exec(context.Background(), textIndexSQL)
	require.NoError(suite.T(), err)

	// Insert test data.
	testData := []struct {
		id       string
		name     string
		content  string
		vector   []float32
		metadata string
	}{
		{"doc1", "Document 1", "This is a test document about machine learning", []float32{0.1, 0.2, 0.3}, `{"category": "AI", "score": 95}`},
		{"doc2", "Document 2", "Another document about artificial intelligence", []float32{0.2, 0.3, 0.4}, `{"category": "AI", "score": 87}`},
		{"doc3", "Document 3", "A document about database systems and indexing", []float32{0.3, 0.4, 0.5}, `{"category": "Database", "score": 92}`},
		{"doc4", "Document 4", "Testing vector similarity search functionality", []float32{0.4, 0.5, 0.6}, `{"category": "Testing", "score": 89}`},
	}

	for _, data := range testData {
		upsertSQL := fmt.Sprintf(sqlUpsertDocument, "test_documents")
		vector := pgvector.NewVector(data.vector)
		now := int64(1640995200) // Fixed timestamp for testing.
		_, err := suite.pool.Exec(context.Background(), upsertSQL,
			data.id, data.name, data.content, vector, data.metadata, now, now)
		require.NoError(suite.T(), err)
	}
}

// TearDownTest runs after each test.
func (suite *SQLBuilderTestSuite) TearDownTest() {
	if suite.pool == nil {
		return
	}
	// Clean up test table.
	_, _ = suite.pool.Exec(context.Background(), "DROP TABLE IF EXISTS test_documents")
}

// TestUpdateBuilder tests the update builder functionality.
func (suite *SQLBuilderTestSuite) TestUpdateBuilder() {
	tests := []struct {
		name        string
		table       string
		id          string
		fields      map[string]any
		expectedSQL string
		expectedLen int
	}{
		{
			name:        "basic update",
			table:       "test_documents",
			id:          "doc1",
			fields:      map[string]any{"name": "Updated Name", "content": "Updated Content"},
			expectedSQL: `UPDATE test_documents SET updated_at = $2, name = $3, content = $4 WHERE id = $1`,
			expectedLen: 4,
		},
		{
			name:        "single field update",
			table:       "test_documents",
			id:          "doc2",
			fields:      map[string]any{"name": "New Name"},
			expectedSQL: `UPDATE test_documents SET updated_at = $2, name = $3 WHERE id = $1`,
			expectedLen: 3,
		},
		{
			name:        "no additional fields",
			table:       "test_documents",
			id:          "doc3",
			fields:      map[string]any{},
			expectedSQL: `UPDATE test_documents SET updated_at = $2 WHERE id = $1`,
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			ub := newUpdateBuilder(tt.table, tt.id)

			// Test initial state.
			assert.Equal(suite.T(), tt.table, ub.table)
			assert.Equal(suite.T(), tt.id, ub.id)

			// Add fields.
			for field, value := range tt.fields {
				ub.addField(field, value)
			}

			sql, args := ub.build()

			// Verify SQL structure.
			assert.Equal(suite.T(), tt.expectedSQL, sql)
			assert.Len(suite.T(), args, tt.expectedLen)
			assert.Equal(suite.T(), tt.id, args[0])

			// Test executing the update.
			_, err := suite.pool.Exec(context.Background(), sql, args...)
			assert.NoError(suite.T(), err)
		})
	}
}

// TestQueryBuilders tests all query builder types.
func (suite *SQLBuilderTestSuite) TestQueryBuilders() {
	tests := []struct {
		name          string
		mode          vectorstore.SearchMode
		vectorWeight  float64
		textWeight    float64
		setupFunc     func(*queryBuilder)
		expectedSQL   []string
		expectedOrder string
	}{
		{
			name: "vector query builder",
			mode: vectorstore.SearchModeVector,
			setupFunc: func(qb *queryBuilder) {
				vector := pgvector.NewVector([]float32{0.1, 0.2, 0.3})
				qb.addVectorArg(vector)
				qb.addIDFilter([]string{"doc1", "doc2"})
				qb.addScoreFilter(0.5)
			},
			expectedSQL:   []string{"SELECT", "FROM test_documents", "WHERE", "LIMIT", "1 - (embedding <=> $1) as score"},
			expectedOrder: "ORDER BY embedding <=> $1",
		},
		{
			name: "keyword query builder",
			mode: vectorstore.SearchModeKeyword,
			setupFunc: func(qb *queryBuilder) {
				qb.addKeywordSearchConditions("machine learning", 0.1)
				qb.addIDFilter([]string{"doc1", "doc3"})
			},
			expectedSQL:   []string{"SELECT", "FROM test_documents", "WHERE", "LIMIT", "to_tsvector", "ts_rank_cd"},
			expectedOrder: "ORDER BY score DESC, created_at DESC",
		},
		{
			name:         "hybrid query builder",
			mode:         vectorstore.SearchModeHybrid,
			vectorWeight: 0.7,
			textWeight:   0.3,
			setupFunc: func(qb *queryBuilder) {
				vector := pgvector.NewVector([]float32{0.1, 0.2, 0.3})
				qb.addVectorArg(vector)
				qb.addHybridFtsCondition("machine learning")
			},
			expectedSQL:   []string{"SELECT", "FROM test_documents", "WHERE", "LIMIT", "0.700", "0.300", "ts_rank_cd"},
			expectedOrder: "ORDER BY score DESC",
		},
		{
			name: "filter query builder",
			mode: vectorstore.SearchModeFilter,
			setupFunc: func(qb *queryBuilder) {
				qb.addIDFilter([]string{"doc1", "doc2", "doc3"})
				qb.addMetadataFilter(map[string]any{"category": "AI"})
			},
			expectedSQL:   []string{"SELECT", "FROM test_documents", "WHERE", "LIMIT", "1.0 as score", "id IN", "metadata @>"},
			expectedOrder: "ORDER BY created_at DESC",
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			var qb *queryBuilder
			switch tt.mode {
			case vectorstore.SearchModeVector:
				qb = newVectorQueryBuilder("test_documents", "english")
			case vectorstore.SearchModeKeyword:
				qb = newKeywordQueryBuilder("test_documents", "english")
			case vectorstore.SearchModeHybrid:
				qb = newHybridQueryBuilder("test_documents", "english", tt.vectorWeight, tt.textWeight)
			case vectorstore.SearchModeFilter:
				qb = newFilterQueryBuilder("test_documents", "english")
			}

			// Test initial state.
			assert.Equal(suite.T(), tt.mode, qb.searchMode)
			assert.Equal(suite.T(), tt.expectedOrder, qb.orderClause)

			// Setup query.
			tt.setupFunc(qb)

			sql, args := qb.build(10)

			// Verify SQL structure.
			for _, expected := range tt.expectedSQL {
				assert.Contains(suite.T(), sql, expected)
			}

			// Verify arguments.
			assert.Greater(suite.T(), len(args), 0)

			// Test executing the query.
			rows, err := suite.pool.Query(context.Background(), sql, args...)
			assert.NoError(suite.T(), err)
			defer rows.Close()

			// Should return results (basic smoke test).
			count := 0
			for rows.Next() {
				count++
			}
			// For some queries we might get 0 results, that's okay.
			assert.GreaterOrEqual(suite.T(), count, 0)
		})
	}
}

// TestBuildSelectClause tests the dynamic SELECT clause building.
func (suite *SQLBuilderTestSuite) TestBuildSelectClause() {
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
			name:             "vector mode",
			mode:             vectorstore.SearchModeVector,
			expectedContains: []string{"1 - (embedding <=> $1) as score"},
		},
		{
			name:             "keyword mode with text",
			mode:             vectorstore.SearchModeKeyword,
			textQueryPos:     1,
			expectedContains: []string{"ts_rank_cd", "as score"},
		},
		{
			name:             "keyword mode without text",
			mode:             vectorstore.SearchModeKeyword,
			textQueryPos:     0,
			expectedContains: []string{"0.0 as score"},
		},
		{
			name:             "hybrid mode with text",
			mode:             vectorstore.SearchModeHybrid,
			vectorWeight:     0.6,
			textWeight:       0.4,
			textQueryPos:     2,
			expectedContains: []string{"0.600", "0.400", "as score", "ts_rank_cd"},
		},
		{
			name:                "hybrid mode without text (pure vector)",
			mode:                vectorstore.SearchModeHybrid,
			vectorWeight:        0.8,
			textWeight:          0.2,
			textQueryPos:        0,
			expectedContains:    []string{"0.800", "as score"},
			expectedNotContains: []string{"ts_rank_cd"},
		},
		{
			name:             "filter mode",
			mode:             vectorstore.SearchModeFilter,
			expectedContains: []string{"1.0 as score"},
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			var qb *queryBuilder
			switch tt.mode {
			case vectorstore.SearchModeVector:
				qb = newVectorQueryBuilder("test_documents", "english")
			case vectorstore.SearchModeKeyword:
				qb = newKeywordQueryBuilder("test_documents", "english")
			case vectorstore.SearchModeHybrid:
				qb = newHybridQueryBuilder("test_documents", "english", tt.vectorWeight, tt.textWeight)
			case vectorstore.SearchModeFilter:
				qb = newFilterQueryBuilder("test_documents", "english")
			}

			qb.textQueryPos = tt.textQueryPos
			selectClause := qb.buildSelectClause()

			// Check expected contains.
			for _, expected := range tt.expectedContains {
				assert.Contains(suite.T(), selectClause, expected)
			}

			// Check expected not contains.
			for _, notExpected := range tt.expectedNotContains {
				assert.NotContains(suite.T(), selectClause, notExpected)
			}
		})
	}
}

// TestQueryBuilderEdgeCases tests edge cases and error conditions.
func (suite *SQLBuilderTestSuite) TestQueryBuilderEdgeCases() {
	tests := []struct {
		name                string
		idFilter            []string
		metadataFilter      map[string]any
		expectedContains    []string
		expectedNotContains []string
		expectArgs          bool
	}{
		{
			name:                "empty filters",
			idFilter:            []string{},
			metadataFilter:      map[string]any{},
			expectedNotContains: []string{"id IN", "metadata @>"},
			expectArgs:          false,
		},
		{
			name:                "empty ID filter only",
			idFilter:            []string{},
			metadataFilter:      map[string]any{"test": "value"},
			expectedContains:    []string{"metadata @>"},
			expectedNotContains: []string{"id IN"},
			expectArgs:          true,
		},
		{
			name:                "empty metadata filter only",
			idFilter:            []string{"doc1"},
			metadataFilter:      map[string]any{},
			expectedContains:    []string{"id IN"},
			expectedNotContains: []string{"metadata @>"},
			expectArgs:          true,
		},
		{
			name:             "both filters present",
			idFilter:         []string{"doc1", "doc2"},
			metadataFilter:   map[string]any{"test": "value", "score": 95},
			expectedContains: []string{"id IN", "metadata @>"},
			expectArgs:       true,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			qb := newVectorQueryBuilder("test_documents", "english")

			// Add filters.
			qb.addIDFilter(tt.idFilter)
			qb.addMetadataFilter(tt.metadataFilter)

			sql, args := qb.build(10)

			// Check expected contains.
			for _, expected := range tt.expectedContains {
				assert.Contains(suite.T(), sql, expected)
			}

			// Check expected not contains.
			for _, notExpected := range tt.expectedNotContains {
				assert.NotContains(suite.T(), sql, notExpected)
			}

			// Check args expectation.
			if tt.expectArgs {
				assert.Greater(suite.T(), len(args), 0)
			}
		})
	}
}

func TestMetadataQueryBuilder_Basic(t *testing.T) {
	mqb := newMetadataQueryBuilder("test_table")

	sql, args := mqb.buildWithPagination(10, 0)

	assert.Contains(t, sql, "SELECT id, metadata")
	assert.Contains(t, sql, "FROM test_table")
	assert.Contains(t, sql, "WHERE 1=1")
	assert.Contains(t, sql, "ORDER BY created_at")
	assert.Contains(t, sql, "LIMIT $1 OFFSET $2")
	assert.Equal(t, []interface{}{10, 0}, args)
}

func TestMetadataQueryBuilder_WithIDFilter(t *testing.T) {
	mqb := newMetadataQueryBuilder("test_table")
	mqb.addIDFilter([]string{"id1", "id2", "id3"})

	sql, args := mqb.buildWithPagination(10, 0)

	assert.Contains(t, sql, "id IN ($1, $2, $3)")
	assert.Equal(t, []interface{}{"id1", "id2", "id3", 10, 0}, args)
}

func TestMetadataQueryBuilder_WithMetadataFilter(t *testing.T) {
	mqb := newMetadataQueryBuilder("test_table")
	filter := map[string]interface{}{
		"category": "test",
		"status":   "active",
	}
	mqb.addMetadataFilter(filter)

	sql, args := mqb.buildWithPagination(10, 0)

	assert.Contains(t, sql, "metadata @> $1::jsonb")
	assert.Len(t, args, 3) // metadata JSON, limit, offset
	assert.Equal(t, 10, args[1])
	assert.Equal(t, 0, args[2])
}

func TestMetadataQueryBuilder_WithBothFilters(t *testing.T) {
	mqb := newMetadataQueryBuilder("test_table")
	mqb.addIDFilter([]string{"id1", "id2"})
	filter := map[string]interface{}{
		"category": "test",
	}
	mqb.addMetadataFilter(filter)

	sql, args := mqb.buildWithPagination(5, 10)

	assert.Contains(t, sql, "id IN ($1, $2)")
	assert.Contains(t, sql, "metadata @> $3::jsonb")
	assert.Contains(t, sql, "WHERE 1=1 AND id IN ($1, $2) AND metadata @> $3::jsonb")
	assert.Len(t, args, 5) // id1, id2, metadata JSON, limit, offset
	assert.Equal(t, "id1", args[0])
	assert.Equal(t, "id2", args[1])
	assert.Equal(t, 5, args[3])  // limit
	assert.Equal(t, 10, args[4]) // offset
}

func TestMetadataQueryBuilder_EmptyFilters(t *testing.T) {
	mqb := newMetadataQueryBuilder("test_table")

	// Test with empty ID filter
	mqb.addIDFilter([]string{})
	mqb.addMetadataFilter(map[string]interface{}{})

	sql, args := mqb.buildWithPagination(10, 0)

	// Should only have the basic WHERE 1=1 condition
	assert.Contains(t, sql, "WHERE 1=1")
	assert.NotContains(t, sql, "id IN")
	assert.NotContains(t, sql, "metadata @>")
	assert.Equal(t, []interface{}{10, 0}, args)
}

// TestCountQueryBuilder_Basic tests basic count query building
func TestCountQueryBuilder_Basic(t *testing.T) {
	cqb := newCountQueryBuilder("test_table")

	sql, args := cqb.build()

	assert.Equal(t, "SELECT COUNT(*) FROM test_table WHERE 1=1", sql)
	assert.Empty(t, args)
}

// TestCountQueryBuilder_WithMetadataFilter tests count query with metadata filter
func TestCountQueryBuilder_WithMetadataFilter(t *testing.T) {
	cqb := newCountQueryBuilder("test_table")

	filter := map[string]interface{}{
		"category": "science",
		"status":   "published",
	}
	cqb.addMetadataFilter(filter)

	sql, args := cqb.build()

	assert.Equal(t, "SELECT COUNT(*) FROM test_table WHERE 1=1 AND metadata @> $1::jsonb", sql)
	assert.Len(t, args, 1)

	// Verify the JSON argument contains the filter
	jsonArg, ok := args[0].(string)
	assert.True(t, ok)
	assert.Contains(t, jsonArg, "category")
	assert.Contains(t, jsonArg, "science")
	assert.Contains(t, jsonArg, "status")
	assert.Contains(t, jsonArg, "published")
}

// TestCountQueryBuilder_EmptyFilter tests count query with empty filter
func TestCountQueryBuilder_EmptyFilter(t *testing.T) {
	cqb := newCountQueryBuilder("test_table")

	// Add empty filter (should be ignored)
	cqb.addMetadataFilter(map[string]interface{}{})

	sql, args := cqb.build()

	assert.Equal(t, "SELECT COUNT(*) FROM test_table WHERE 1=1", sql)
	assert.Empty(t, args)
}

// TestDeleteSQLBuilder_Basic tests basic delete query building
func TestDeleteSQLBuilder_Basic(t *testing.T) {
	dsb := newDeleteSQLBuilder("test_table")

	sql, args := dsb.build()

	assert.Equal(t, "DELETE FROM test_table WHERE 1=1", sql)
	assert.Empty(t, args)
}

// TestDeleteSQLBuilder_WithIDFilter tests delete query with ID filter
func TestDeleteSQLBuilder_WithIDFilter(t *testing.T) {
	dsb := newDeleteSQLBuilder("test_table")
	dsb.addIDFilter([]string{"doc1", "doc2", "doc3"})

	sql, args := dsb.build()

	assert.Equal(t, "DELETE FROM test_table WHERE 1=1 AND id IN ($1, $2, $3)", sql)
	assert.Equal(t, []interface{}{"doc1", "doc2", "doc3"}, args)
}

// TestDeleteSQLBuilder_WithMetadataFilter tests delete query with metadata filter
func TestDeleteSQLBuilder_WithMetadataFilter(t *testing.T) {
	dsb := newDeleteSQLBuilder("test_table")

	filter := map[string]interface{}{
		"category": "test",
		"status":   "deleted",
	}
	dsb.addMetadataFilter(filter)

	sql, args := dsb.build()

	assert.Equal(t, "DELETE FROM test_table WHERE 1=1 AND metadata @> $1::jsonb", sql)
	assert.Len(t, args, 1)

	// Verify the JSON argument contains the filter
	jsonArg, ok := args[0].(string)
	assert.True(t, ok)
	assert.Contains(t, jsonArg, "category")
	assert.Contains(t, jsonArg, "test")
	assert.Contains(t, jsonArg, "status")
	assert.Contains(t, jsonArg, "deleted")
}

// TestDeleteSQLBuilder_WithBothFilters tests delete query with both ID and metadata filters
func TestDeleteSQLBuilder_WithBothFilters(t *testing.T) {
	dsb := newDeleteSQLBuilder("test_table")
	dsb.addIDFilter([]string{"doc1", "doc2"})

	filter := map[string]interface{}{
		"category": "test",
	}
	dsb.addMetadataFilter(filter)

	sql, args := dsb.build()

	assert.Equal(t, "DELETE FROM test_table WHERE 1=1 AND id IN ($1, $2) AND metadata @> $3::jsonb", sql)
	assert.Equal(t, []interface{}{"doc1", "doc2", "{\"category\":\"test\"}"}, args)
}

// TestDeleteSQLBuilder_EmptyFilters tests delete query with empty filters
func TestDeleteSQLBuilder_EmptyFilters(t *testing.T) {
	dsb := newDeleteSQLBuilder("test_table")

	// Test with empty ID filter
	dsb.addIDFilter([]string{})
	dsb.addMetadataFilter(map[string]interface{}{})

	sql, args := dsb.build()

	// Should only have the basic WHERE 1=1 condition
	assert.Equal(t, "DELETE FROM test_table WHERE 1=1", sql)
	assert.Empty(t, args)
}

// TestDeleteSQLBuilder_Integration tests delete query execution integration
func (suite *SQLBuilderTestSuite) TestDeleteSQLBuilder_Integration() {
	// First verify document exists
	countSQL := "SELECT COUNT(*) FROM test_documents WHERE id IN ('doc1', 'doc2')"
	var initialCount int
	err := suite.pool.QueryRow(context.Background(), countSQL).Scan(&initialCount)
	require.NoError(suite.T(), err)
	assert.Greater(suite.T(), initialCount, 0)

	// Build delete query
	dsb := newDeleteSQLBuilder("test_documents")
	dsb.addIDFilter([]string{"doc1", "doc2"})

	sql, args := dsb.build()

	// Execute delete
	_, err = suite.pool.Exec(context.Background(), sql, args...)
	assert.NoError(suite.T(), err)

	// Verify documents were deleted
	var finalCount int
	err = suite.pool.QueryRow(context.Background(), countSQL).Scan(&finalCount)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), 0, finalCount)
}
