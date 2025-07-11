package pgvector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/vectorstore"
)

// PgVectorTestSuite contains the test suite for pgvector
type PgVectorTestSuite struct {
	suite.Suite
	vs    *VectorStore
	table string
}

// Run the test suite
func TestPgVectorSuite(t *testing.T) {
	suite.Run(t, new(PgVectorTestSuite))
}

// SetupSuite runs once before all tests
func (suite *PgVectorTestSuite) SetupSuite() {
	suite.table = "test_documents_" + time.Now().Format("20060102_150405")

	vs, err := New(
		WithHost("localhost"),
		WithPort(5432),
		WithUser("root"),
		WithPassword("123"),
		WithDatabase("vec"),
		WithTable(suite.table),
		WithIndexDimension(3),             // Small dimension for testing
		WithHybridSearchWeights(0.7, 0.3), // Test custom weights
	)
	suite.Require().NoError(err)
	suite.vs = vs
}

// TearDownSuite runs once after all tests
func (suite *PgVectorTestSuite) TearDownSuite() {
	if suite.vs != nil {
		// Clean up test table
		_, err := suite.vs.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+suite.table)
		suite.NoError(err)
		suite.vs.Close()
	}
}

// SetupTest runs before each test
func (suite *PgVectorTestSuite) SetupTest() {
	// Clean up table data before each test
	_, err := suite.vs.pool.Exec(context.Background(), "DELETE FROM "+suite.table)
	suite.NoError(err)
}

// TestAdd tests adding documents with embeddings
func (suite *PgVectorTestSuite) TestAdd() {
	ctx := context.Background()

	testCases := []struct {
		name      string
		doc       *document.Document
		embedding []float64
		wantError bool
	}{
		{
			name: "valid document",
			doc: &document.Document{
				ID:      "doc1",
				Name:    "Test Document",
				Content: "This is a test document for vector search",
				Metadata: map[string]interface{}{
					"category": "test",
					"priority": 1,
					"active":   true,
				},
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantError: false,
		},
		{
			name: "empty ID",
			doc: &document.Document{
				Name:    "Test Document",
				Content: "Content",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantError: true,
		},
		{
			name: "wrong embedding dimension",
			doc: &document.Document{
				ID:      "doc2",
				Name:    "Test Document",
				Content: "Content",
			},
			embedding: []float64{0.1, 0.2}, // Wrong dimension
			wantError: true,
		},
		{
			name: "empty embedding",
			doc: &document.Document{
				ID:      "doc3",
				Name:    "Test Document",
				Content: "Content",
			},
			embedding: []float64{},
			wantError: true,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			err := suite.vs.Add(ctx, tc.doc, tc.embedding)
			if tc.wantError {
				suite.Error(err)
			} else {
				suite.NoError(err)
			}
		})
	}
}

// TestCRUDOperations tests basic CRUD operations
func (suite *PgVectorTestSuite) TestCRUDOperations() {
	ctx := context.Background()

	// Test document
	doc := &document.Document{
		ID:      "crud_test",
		Name:    "CRUD Test Document",
		Content: "This document tests CRUD operations",
		Metadata: map[string]interface{}{
			"type":    "test",
			"version": 1,
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}

	// Test Add
	err := suite.vs.Add(ctx, doc, embedding)
	suite.NoError(err)

	// Test Get
	retrievedDoc, retrievedEmbedding, err := suite.vs.Get(ctx, doc.ID)
	suite.NoError(err)
	suite.Equal(doc.ID, retrievedDoc.ID)
	suite.Equal(doc.Name, retrievedDoc.Name)
	suite.Equal(doc.Content, retrievedDoc.Content)
	// Use InDelta for float comparison due to precision loss in float64<->float32 conversion
	suite.Len(retrievedEmbedding, len(embedding))
	for i, expected := range embedding {
		suite.InDelta(expected, retrievedEmbedding[i], 0.0001)
	}

	// Test Update
	updatedDoc := &document.Document{
		ID:      doc.ID,
		Name:    "Updated Name",
		Content: "Updated content for testing",
		Metadata: map[string]interface{}{
			"type":    "test",
			"version": 2,
			"updated": true,
		},
	}
	updatedEmbedding := []float64{0.4, 0.5, 0.6}

	err = suite.vs.Update(ctx, updatedDoc, updatedEmbedding)
	suite.NoError(err)

	// Verify update
	retrievedDoc, retrievedEmbedding, err = suite.vs.Get(ctx, doc.ID)
	suite.NoError(err)
	suite.Equal(updatedDoc.Name, retrievedDoc.Name)
	suite.Equal(updatedDoc.Content, retrievedDoc.Content)
	// Use InDelta for float comparison due to precision loss in float64<->float32 conversion
	suite.Len(retrievedEmbedding, len(updatedEmbedding))
	for i, expected := range updatedEmbedding {
		suite.InDelta(expected, retrievedEmbedding[i], 0.0001)
	}

	// Test Delete
	err = suite.vs.Delete(ctx, doc.ID)
	suite.NoError(err)

	// Verify deletion
	_, _, err = suite.vs.Get(ctx, doc.ID)
	suite.Error(err)
}

// TestSearchModes tests different search modes
func (suite *PgVectorTestSuite) TestSearchModes() {
	ctx := context.Background()

	// Setup test data
	testDocs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Name:    "Python Programming",
				Content: "Python is a powerful programming language for data science and machine learning",
				Metadata: map[string]interface{}{
					"category": "programming",
					"language": "python",
					"level":    "beginner",
				},
			},
			embedding: []float64{0.1, 0.2, 0.3},
		},
		{
			doc: &document.Document{
				ID:      "doc2",
				Name:    "Go Development",
				Content: "Go is a fast and efficient language for system programming and web development",
				Metadata: map[string]interface{}{
					"category": "programming",
					"language": "go",
					"level":    "intermediate",
				},
			},
			embedding: []float64{0.2, 0.3, 0.4},
		},
		{
			doc: &document.Document{
				ID:      "doc3",
				Name:    "Data Science Tutorial",
				Content: "Learn data science fundamentals with Python and machine learning algorithms",
				Metadata: map[string]interface{}{
					"category": "tutorial",
					"language": "python",
					"level":    "advanced",
				},
			},
			embedding: []float64{0.15, 0.25, 0.35},
		},
	}

	// Add test documents
	for _, td := range testDocs {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.NoError(err)
	}

	testCases := []struct {
		name        string
		query       *vectorstore.SearchQuery
		expectError bool
		minResults  int
	}{
		{
			name: "vector search",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3}, // Similar to doc1
				Limit:  10,
			},
			expectError: false,
			minResults:  1,
		},
		{
			name: "keyword search",
			query: &vectorstore.SearchQuery{
				Query: "Python programming",
				Limit: 10,
			},
			expectError: false,
			minResults:  1,
		},
		{
			name: "hybrid search",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Query:  "machine learning",
				Limit:  10,
			},
			expectError: false,
			minResults:  1,
		},
		{
			name: "filter search",
			query: &vectorstore.SearchQuery{
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]interface{}{
						"category": "programming",
					},
				},
				Limit: 10,
			},
			expectError: false,
			minResults:  2, // doc1 and doc2 have category=programming
		},
		{
			name: "search with ID filter",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc3"},
				},
				Limit: 10,
			},
			expectError: false,
			minResults:  2,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			result, err := suite.vs.Search(ctx, tc.query)
			if tc.expectError {
				suite.Error(err)
			} else {
				suite.NoError(err)
				suite.GreaterOrEqual(len(result.Results), tc.minResults)

				// Verify scores are present and valid
				for _, scored := range result.Results {
					suite.NotNil(scored.Document)
					suite.GreaterOrEqual(scored.Score, 0.0)
				}
			}
		})
	}
}

// TestHybridSearchWeights tests hybrid search weight configuration
func (suite *PgVectorTestSuite) TestHybridSearchWeights() {
	ctx := context.Background()

	// Add test document
	doc := &document.Document{
		ID:      "weight_test",
		Name:    "Weight Test Document",
		Content: "This document tests hybrid search weight configuration with machine learning",
		Metadata: map[string]interface{}{
			"category": "test",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}
	err := suite.vs.Add(ctx, doc, embedding)
	suite.NoError(err)

	testCases := []struct {
		name         string
		vectorWeight float64
		textWeight   float64
		expectNorm   bool // Whether weights should be normalized
	}{
		{
			name:         "default weights",
			vectorWeight: 0.7,
			textWeight:   0.3,
			expectNorm:   false,
		},
		{
			name:         "equal weights",
			vectorWeight: 0.5,
			textWeight:   0.5,
			expectNorm:   false,
		},
		{
			name:         "unnormalized weights",
			vectorWeight: 3.0,
			textWeight:   1.0,
			expectNorm:   true, // Should be normalized to 0.75 and 0.25
		},
		{
			name:         "vector priority",
			vectorWeight: 0.8,
			textWeight:   0.2,
			expectNorm:   false,
		},
		{
			name:         "text priority",
			vectorWeight: 0.2,
			textWeight:   0.8,
			expectNorm:   false,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			// Create a new vector store with specific weights
			table := "test_weights_" + time.Now().Format("150405")
			vs, err := New(
				WithHost("localhost"),
				WithPort(5432),
				WithUser("root"),
				WithPassword("123"),
				WithDatabase("vec"),
				WithTable(table),
				WithIndexDimension(3),
				WithHybridSearchWeights(tc.vectorWeight, tc.textWeight),
			)
			suite.NoError(err)
			defer func() {
				vs.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
				vs.Close()
			}()

			// Add the same test document
			err = vs.Add(ctx, doc, embedding)
			suite.NoError(err)

			// Perform hybrid search
			query := &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Query:  "machine learning",
				Limit:  1,
			}

			result, err := vs.Search(ctx, query)
			suite.NoError(err)
			suite.Len(result.Results, 1)

			// Verify weights are correctly applied
			if tc.expectNorm {
				// For normalized weights, just ensure search works
				suite.Greater(result.Results[0].Score, 0.0)
			} else {
				// Verify the actual weights are used
				expectedVector := tc.vectorWeight / (tc.vectorWeight + tc.textWeight)
				expectedText := tc.textWeight / (tc.vectorWeight + tc.textWeight)
				suite.InDelta(expectedVector, vs.option.vectorWeight, 0.001)
				suite.InDelta(expectedText, vs.option.textWeight, 0.001)
			}
		})
	}
}

// TestMetadataFiltering tests different metadata filtering approaches
func (suite *PgVectorTestSuite) TestMetadataFiltering() {
	ctx := context.Background()

	// Setup test data with various metadata types
	testDocs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "meta1",
				Name:    "Document 1",
				Content: "Content with integer metadata",
				Metadata: map[string]interface{}{
					"priority": 1,
					"active":   true,
					"score":    95.5,
					"category": "urgent",
				},
			},
			embedding: []float64{0.1, 0.2, 0.3},
		},
		{
			doc: &document.Document{
				ID:      "meta2",
				Name:    "Document 2",
				Content: "Content with different metadata",
				Metadata: map[string]interface{}{
					"priority": 2,
					"active":   false,
					"score":    87.2,
					"category": "normal",
				},
			},
			embedding: []float64{0.2, 0.3, 0.4},
		},
		{
			doc: &document.Document{
				ID:      "meta3",
				Name:    "Document 3",
				Content: "Content with mixed metadata",
				Metadata: map[string]interface{}{
					"priority": 1,
					"active":   true,
					"score":    92.8,
					"category": "urgent",
				},
			},
			embedding: []float64{0.15, 0.25, 0.35},
		},
	}

	// Add test documents
	for _, td := range testDocs {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.NoError(err)
	}

	testCases := []struct {
		name          string
		filter        map[string]interface{}
		expectedCount int
		expectedIDs   []string
	}{
		{
			name: "integer filter",
			filter: map[string]interface{}{
				"priority": 1,
			},
			expectedCount: 2,
			expectedIDs:   []string{"meta1", "meta3"},
		},
		{
			name: "boolean filter",
			filter: map[string]interface{}{
				"active": true,
			},
			expectedCount: 2,
			expectedIDs:   []string{"meta1", "meta3"},
		},
		{
			name: "string filter",
			filter: map[string]interface{}{
				"category": "urgent",
			},
			expectedCount: 2,
			expectedIDs:   []string{"meta1", "meta3"},
		},
		{
			name: "float filter",
			filter: map[string]interface{}{
				"score": 95.5,
			},
			expectedCount: 1,
			expectedIDs:   []string{"meta1"},
		},
		{
			name: "multiple filters",
			filter: map[string]interface{}{
				"priority": 1,
				"active":   true,
			},
			expectedCount: 2,
			expectedIDs:   []string{"meta1", "meta3"},
		},
		{
			name: "no match filter",
			filter: map[string]interface{}{
				"priority": 999,
			},
			expectedCount: 0,
			expectedIDs:   []string{},
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			query := &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Filter: &vectorstore.SearchFilter{
					Metadata: tc.filter,
				},
				Limit: 10,
			}

			result, err := suite.vs.Search(ctx, query)
			suite.NoError(err)
			suite.Len(result.Results, tc.expectedCount)

			// Verify the correct documents are returned
			actualIDs := make([]string, len(result.Results))
			for i, scored := range result.Results {
				actualIDs[i] = scored.Document.ID
			}

			suite.ElementsMatch(tc.expectedIDs, actualIDs)
		})
	}
}

// TestErrorHandling tests various error conditions
func (suite *PgVectorTestSuite) TestErrorHandling() {
	ctx := context.Background()

	testCases := []struct {
		name      string
		operation func() error
		wantError bool
	}{
		{
			name: "get non-existent document",
			operation: func() error {
				_, _, err := suite.vs.Get(ctx, "non_existent")
				return err
			},
			wantError: true,
		},
		{
			name: "update non-existent document",
			operation: func() error {
				doc := &document.Document{
					ID:      "non_existent",
					Name:    "Test",
					Content: "Test",
				}
				return suite.vs.Update(ctx, doc, []float64{0.1, 0.2, 0.3})
			},
			wantError: true,
		},
		{
			name: "delete non-existent document",
			operation: func() error {
				return suite.vs.Delete(ctx, "non_existent")
			},
			wantError: true,
		},
		{
			name: "search with nil query",
			operation: func() error {
				_, err := suite.vs.Search(ctx, nil)
				return err
			},
			wantError: true,
		},
		{
			name: "hybrid search without vector",
			operation: func() error {
				query := &vectorstore.SearchQuery{
					Query: "test",
				}
				_, err := suite.vs.hybridSearch(ctx, query)
				return err
			},
			wantError: true,
		},
		{
			name: "hybrid search without keyword",
			operation: func() error {
				query := &vectorstore.SearchQuery{
					Vector: []float64{0.1, 0.2, 0.3},
				}
				_, err := suite.vs.hybridSearch(ctx, query)
				return err
			},
			wantError: true,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			err := tc.operation()
			if tc.wantError {
				suite.Error(err)
			} else {
				suite.NoError(err)
			}
		})
	}
}

// TestWeightNormalization tests the weight normalization functionality
func (suite *PgVectorTestSuite) TestWeightNormalization() {
	testCases := []struct {
		name                 string
		inputVectorWeight    float64
		inputTextWeight      float64
		expectedVectorWeight float64
		expectedTextWeight   float64
	}{
		{
			name:                 "already normalized",
			inputVectorWeight:    0.7,
			inputTextWeight:      0.3,
			expectedVectorWeight: 0.7,
			expectedTextWeight:   0.3,
		},
		{
			name:                 "need normalization",
			inputVectorWeight:    3.0,
			inputTextWeight:      1.0,
			expectedVectorWeight: 0.75,
			expectedTextWeight:   0.25,
		},
		{
			name:                 "equal weights",
			inputVectorWeight:    2.0,
			inputTextWeight:      2.0,
			expectedVectorWeight: 0.5,
			expectedTextWeight:   0.5,
		},
		{
			name:                 "zero weights fallback",
			inputVectorWeight:    0.0,
			inputTextWeight:      0.0,
			expectedVectorWeight: 0.7, // Default fallback
			expectedTextWeight:   0.3, // Default fallback
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			table := "test_norm_" + time.Now().Format("150405")
			vs, err := New(
				WithHost("localhost"),
				WithPort(5432),
				WithUser("root"),
				WithPassword("123"),
				WithDatabase("vec"),
				WithTable(table),
				WithIndexDimension(3),
				WithHybridSearchWeights(tc.inputVectorWeight, tc.inputTextWeight),
			)
			suite.NoError(err)
			defer func() {
				vs.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
				vs.Close()
			}()

			suite.InDelta(tc.expectedVectorWeight, vs.option.vectorWeight, 0.001)
			suite.InDelta(tc.expectedTextWeight, vs.option.textWeight, 0.001)

			// Also test runtime normalization in queryBuilder
			qb := newHybridQueryBuilder(table, tc.inputVectorWeight, tc.inputTextWeight)
			suite.NotNil(qb)
			// The selectClause should contain the normalized weights
			suite.Contains(qb.selectClause, "* 0.") // Should contain decimal weights
		})
	}
}
