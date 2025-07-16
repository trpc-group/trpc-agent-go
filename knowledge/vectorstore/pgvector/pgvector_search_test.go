package pgvector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// PgVectorSearchTestSuite contains the search test suite for pgvector
type PgVectorSearchTestSuite struct {
	suite.Suite
	vs       *VectorStore
	testDocs map[string]*document.Document // Store test documents for validation
}

// Run the search test suite
func TestPgVectorSearchSuite(t *testing.T) {
	suite.Run(t, new(PgVectorSearchTestSuite))
}

// SetupSuite runs once before all tests
func (suite *PgVectorSearchTestSuite) SetupSuite() {
	if host == "" {
		suite.T().Skip("Skipping PgVector tests: PGVECTOR_HOST not set")
		return
	}
	fmt.Println("host", host)
	fmt.Println("port", port)
	fmt.Println("user", user)
	fmt.Println("password", password)
	fmt.Println("database", database)
	fmt.Println("table", table)

	vs, err := New(
		WithHost(host),
		WithPort(port),
		WithUser(user),
		WithPassword(password),
		WithDatabase(database),
		WithTable(table),
		WithIndexDimension(3),             // Small dimension for testing
		WithHybridSearchWeights(0.7, 0.3), // Test custom weights
	)
	if err != nil {
		suite.T().Skipf("Skipping PgVector tests: failed to connect to database: %v", err)
		return
	}
	suite.vs = vs
	suite.testDocs = make(map[string]*document.Document)
}

// TearDownSuite runs once after all search tests
func (suite *PgVectorSearchTestSuite) TearDownSuite() {
	if suite.vs != nil {
		// Clean up test table
		_, err := suite.vs.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
		suite.NoError(err)
		suite.vs.Close()
	}
}

// SetupTest runs before each search test
func (suite *PgVectorSearchTestSuite) SetupTest() {
	// Clean up table data before each test
	_, err := suite.vs.pool.Exec(context.Background(), "DELETE FROM "+table)
	suite.NoError(err)
}

// validateSearchResult validates a single search result
func (suite *PgVectorSearchTestSuite) validateSearchResult(result *vectorstore.ScoredDocument) {
	// Validate document structure
	suite.NotNil(result.Document, "Document should not be nil")
	suite.NotEmpty(result.Document.ID, "Document ID should not be empty")
	suite.NotEmpty(result.Document.Name, "Document Name should not be empty")
	suite.NotEmpty(result.Document.Content, "Document Content should not be empty")

	// Validate score
	suite.GreaterOrEqual(result.Score, 0.0, "Score should be non-negative")
	suite.LessOrEqual(result.Score, 1.0, "Score should not exceed 1.0")

	// Compare with original test document if available
	if originalDoc, exists := suite.testDocs[result.Document.ID]; exists {
		suite.validateDocumentContent(originalDoc, result.Document)
	}
}

// validateDocumentContent compares returned document with original
func (suite *PgVectorSearchTestSuite) validateDocumentContent(expected, actual *document.Document) {
	suite.Equal(expected.ID, actual.ID, "Document ID should match")
	suite.Equal(expected.Name, actual.Name, "Document Name should match")
	suite.Equal(expected.Content, actual.Content, "Document Content should match")

	// Validate metadata
	suite.NotNil(actual.Metadata, "Document metadata should not be nil")
	for key, expectedValue := range expected.Metadata {
		actualValue, exists := actual.Metadata[key]
		suite.True(exists, "Metadata key '%s' should exist", key)

		// Flexible type comparison for metadata
		suite.True(suite.compareMetadataValues(expectedValue, actualValue),
			"Metadata '%s' should match: expected %v (%T), got %v (%T)",
			key, expectedValue, expectedValue, actualValue, actualValue)
	}
}

// compareMetadataValues provides flexible comparison for metadata values
func (suite *PgVectorSearchTestSuite) compareMetadataValues(expected, actual interface{}) bool {
	// Direct equality check
	if expected == actual {
		return true
	}

	// Handle numeric type conversions (JSON unmarshaling often converts to float64)
	switch e := expected.(type) {
	case int:
		if a, ok := actual.(float64); ok {
			return float64(e) == a
		}
	case float64:
		if a, ok := actual.(int); ok {
			return e == float64(a)
		}
	case []string:
		if a, ok := actual.([]interface{}); ok {
			if len(e) != len(a) {
				return false
			}
			for i, v := range e {
				if str, ok := a[i].(string); !ok || str != v {
					return false
				}
			}
			return true
		}
	}
	return false
}

// validateSearchResults validates the complete search result set
func (suite *PgVectorSearchTestSuite) validateSearchResults(results []*vectorstore.ScoredDocument, expectedMinCount int) {
	suite.GreaterOrEqual(len(results), expectedMinCount,
		"Should return at least %d results", expectedMinCount)

	// Validate individual results
	for i, result := range results {
		suite.validateSearchResult(result)
		suite.T().Logf("Result %d: ID=%s, Name=%s, Score=%.4f",
			i+1, result.Document.ID, result.Document.Name, result.Score)
	}

	// Validate score ordering (descending)
	for i := 1; i < len(results); i++ {
		suite.GreaterOrEqual(results[i-1].Score, results[i].Score,
			"Results should be ordered by score (descending): result[%d].Score=%.4f >= result[%d].Score=%.4f",
			i-1, results[i-1].Score, i, results[i].Score)
	}
}

// validateKeywordRelevance checks if results are relevant to the keyword query
func (suite *PgVectorSearchTestSuite) validateKeywordRelevance(results []*vectorstore.ScoredDocument, keywords []string) {
	for _, result := range results {
		hasRelevantContent := false
		content := strings.ToLower(result.Document.Name + " " + result.Document.Content)

		for _, keyword := range keywords {
			if strings.Contains(content, strings.ToLower(keyword)) {
				hasRelevantContent = true
				break
			}
		}

		// Log for debugging - keyword relevance might not be strict
		if !hasRelevantContent {
			suite.T().Logf("Note: Document %s may not contain keywords %v",
				result.Document.ID, keywords)
		}
	}
}

// TestSearchModes tests different search modes
func (suite *PgVectorSearchTestSuite) TestSearchModes() {
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
		// Store for validation
		suite.testDocs[td.doc.ID] = td.doc
	}

	testCases := []struct {
		name        string
		query       *vectorstore.SearchQuery
		expectError bool
		minResults  int
		validator   func(results []*vectorstore.ScoredDocument)
	}{
		{
			name: "vector search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3}, // Similar to doc1
				Limit:      10,
				SearchMode: vectorstore.SearchModeVector,
			},
			expectError: false,
			minResults:  1,
			validator: func(results []*vectorstore.ScoredDocument) {
				// Vector search should prioritize similarity
				if len(results) > 0 {
					suite.Greater(results[0].Score, 0.0, "Vector search should return similarity scores")
				}
			},
		},
		{
			name: "keyword search",
			query: &vectorstore.SearchQuery{
				Query:      "Python programming",
				Limit:      10,
				SearchMode: vectorstore.SearchModeKeyword,
			},
			expectError: false,
			minResults:  1,
			validator: func(results []*vectorstore.ScoredDocument) {
				// Validate keyword relevance
				suite.validateKeywordRelevance(results, []string{"Python", "programming"})
			},
		},
		{
			name: "hybrid search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				Query:      "machine learning",
				Limit:      10,
				SearchMode: vectorstore.SearchModeHybrid,
			},
			expectError: false,
			minResults:  1,
			validator: func(results []*vectorstore.ScoredDocument) {
				// Hybrid search should combine both signals
				suite.validateKeywordRelevance(results, []string{"machine", "learning"})
				if len(results) > 0 {
					suite.Greater(results[0].Score, 0.0, "Hybrid search should return meaningful scores")
				}
			},
		},
		{
			name: "filter search",
			query: &vectorstore.SearchQuery{
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]interface{}{
						"category": "programming",
					},
				},
				Limit:      10,
				SearchMode: vectorstore.SearchModeFilter,
			},
			expectError: false,
			minResults:  2, // doc1 and doc2 have category=programming
			validator: func(results []*vectorstore.ScoredDocument) {
				// All results should match the filter
				for _, r := range results {
					if category, ok := r.Document.Metadata["category"]; ok {
						suite.Equal("programming", category,
							"All filtered results should have category='programming'")
					} else {
						suite.Fail("Result should have category metadata")
					}
				}
			},
		},
		{
			name: "search with ID filter",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc3"},
				},
				Limit:      10,
				SearchMode: vectorstore.SearchModeVector,
			},
			expectError: false,
			minResults:  2,
			validator: func(results []*vectorstore.ScoredDocument) {
				// All results should match the ID filter
				allowedIDs := map[string]bool{"doc1": true, "doc3": true}
				for _, r := range results {
					suite.True(allowedIDs[r.Document.ID],
						"Result ID %s should be in allowed list", r.Document.ID)
				}
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			result, err := suite.vs.Search(ctx, tc.query)
			if tc.expectError {
				suite.Error(err)
				return
			}

			suite.NoError(err, "Search should not error")
			suite.NotNil(result, "Search result should not be nil")

			// Validate search results
			suite.validateSearchResults(result.Results, tc.minResults)

			// Run custom validator
			if tc.validator != nil {
				tc.validator(result.Results)
			}
		})
	}
}

// TestHybridSearchWeights tests hybrid search weight configuration
func (suite *PgVectorSearchTestSuite) TestHybridSearchWeights() {
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
	// Store for validation
	suite.testDocs[doc.ID] = doc

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
				WithHost(host),
				WithPort(port),
				WithUser(user),
				WithPassword(password),
				WithDatabase(database),
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
				Vector:     []float64{0.1, 0.2, 0.3},
				Query:      "machine learning",
				Limit:      1,
				SearchMode: vectorstore.SearchModeHybrid,
			}

			result, err := vs.Search(ctx, query)
			suite.NoError(err, "Hybrid search should not error")
			suite.NotNil(result, "Search result should not be nil")
			suite.Len(result.Results, 1, "Should return exactly 1 result")

			// Validate search results content
			if len(result.Results) > 0 {
				resultDoc := result.Results[0]

				// Validate document structure
				suite.NotNil(resultDoc.Document, "Document should not be nil")
				suite.Equal(doc.ID, resultDoc.Document.ID, "Document ID should match")
				suite.Equal(doc.Name, resultDoc.Document.Name, "Document name should match")
				suite.Equal(doc.Content, resultDoc.Document.Content, "Document content should match")

				// Validate score
				suite.Greater(resultDoc.Score, 0.0, "Score should be positive")
				suite.LessOrEqual(resultDoc.Score, 1.0, "Score should not exceed 1.0")

				// Validate keyword relevance
				content := strings.ToLower(resultDoc.Document.Name + " " + resultDoc.Document.Content)
				suite.True(strings.Contains(content, "machine") || strings.Contains(content, "learning"),
					"Result should be relevant to search keywords")
			}

			// Verify weights are correctly applied
			if tc.expectNorm {
				// For normalized weights, just ensure search works
				suite.Greater(result.Results[0].Score, 0.0, "Normalized weights should produce valid scores")
			} else {
				// Verify the actual weights are used
				expectedVector := tc.vectorWeight / (tc.vectorWeight + tc.textWeight)
				expectedText := tc.textWeight / (tc.vectorWeight + tc.textWeight)
				suite.InDelta(expectedVector, vs.option.vectorWeight, 0.001,
					"Vector weight should be normalized correctly")
				suite.InDelta(expectedText, vs.option.textWeight, 0.001,
					"Text weight should be normalized correctly")
			}
		})
	}
}

// TestMetadataFiltering tests different metadata filtering approaches
func (suite *PgVectorSearchTestSuite) TestMetadataFiltering() {
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
		// Store for validation
		suite.testDocs[td.doc.ID] = td.doc
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
				Limit:      10,
				SearchMode: vectorstore.SearchModeVector,
			}

			result, err := suite.vs.Search(ctx, query)
			suite.NoError(err, "Metadata filtering search should not error")
			suite.NotNil(result, "Search result should not be nil")
			suite.Len(result.Results, tc.expectedCount,
				"Should return exactly %d results", tc.expectedCount)

			// Validate search results
			if len(result.Results) > 0 {
				suite.validateSearchResults(result.Results, tc.expectedCount)
			}

			// Verify the correct documents are returned
			actualIDs := make([]string, len(result.Results))
			for i, scored := range result.Results {
				actualIDs[i] = scored.Document.ID
			}
			suite.ElementsMatch(tc.expectedIDs, actualIDs,
				"Should return expected document IDs")

			// Validate that all results match the filter criteria
			for _, result := range result.Results {
				for filterKey, filterValue := range tc.filter {
					actualValue, exists := result.Document.Metadata[filterKey]
					suite.True(exists, "Filter key '%s' should exist in result", filterKey)
					suite.True(suite.compareMetadataValues(filterValue, actualValue),
						"Filter value should match: expected %v, got %v", filterValue, actualValue)
				}
			}
		})
	}
}

// TestSearchErrorHandling tests search-related error conditions
func (suite *PgVectorSearchTestSuite) TestSearchErrorHandling() {
	ctx := context.Background()

	testCases := []struct {
		name      string
		operation func() error
		wantError bool
	}{
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
					Query:      "test",
					SearchMode: vectorstore.SearchModeHybrid,
				}
				_, err := suite.vs.searchByHybrid(ctx, query)
				return err
			},
			wantError: true,
		},
		{
			name: "hybrid search without keyword",
			operation: func() error {
				query := &vectorstore.SearchQuery{
					Vector:     []float64{0.1, 0.2, 0.3},
					SearchMode: vectorstore.SearchModeHybrid,
				}
				_, err := suite.vs.searchByHybrid(ctx, query)
				return err
			},
			wantError: true,
		},
		{
			name: "search with invalid vector dimension",
			operation: func() error {
				query := &vectorstore.SearchQuery{
					Vector:     []float64{0.1, 0.2}, // Wrong dimension (should be 3)
					Limit:      10,
					SearchMode: vectorstore.SearchModeVector,
				}
				_, err := suite.vs.Search(ctx, query)
				return err
			},
			wantError: true,
		},
		{
			name: "search with empty vector",
			operation: func() error {
				query := &vectorstore.SearchQuery{
					Vector:     []float64{},
					Limit:      10,
					SearchMode: vectorstore.SearchModeVector,
				}
				_, err := suite.vs.Search(ctx, query)
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

// TestAdvancedSearchScenarios tests more complex search scenarios
func (suite *PgVectorSearchTestSuite) TestAdvancedSearchScenarios() {
	ctx := context.Background()

	// Setup test data for advanced scenarios
	testDocs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "advanced1",
				Name:    "Machine Learning Fundamentals",
				Content: "Introduction to machine learning algorithms and neural networks",
				Metadata: map[string]interface{}{
					"category":   "education",
					"difficulty": "beginner",
					"rating":     4.5,
					"topics":     []string{"ml", "ai", "algorithms"},
				},
			},
			embedding: []float64{0.1, 0.8, 0.2},
		},
		{
			doc: &document.Document{
				ID:      "advanced2",
				Name:    "Deep Learning Applications",
				Content: "Advanced neural network architectures and their applications",
				Metadata: map[string]interface{}{
					"category":   "education",
					"difficulty": "advanced",
					"rating":     4.8,
					"topics":     []string{"deep learning", "neural networks", "applications"},
				},
			},
			embedding: []float64{0.2, 0.9, 0.1},
		},
		{
			doc: &document.Document{
				ID:      "advanced3",
				Name:    "Python for Data Science",
				Content: "Using Python libraries for data analysis and machine learning",
				Metadata: map[string]interface{}{
					"category":   "programming",
					"difficulty": "intermediate",
					"rating":     4.2,
					"topics":     []string{"python", "data science", "pandas"},
				},
			},
			embedding: []float64{0.3, 0.1, 0.9},
		},
	}

	// Add test documents
	for _, td := range testDocs {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.NoError(err)
		// Store for validation
		suite.testDocs[td.doc.ID] = td.doc
	}

	suite.Run("complex metadata filtering", func() {
		query := &vectorstore.SearchQuery{
			Vector: []float64{0.2, 0.5, 0.3},
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]interface{}{
					"category":   "education",
					"difficulty": "beginner",
				},
			},
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Complex metadata filtering should not error")
		suite.NotNil(result, "Search result should not be nil")
		suite.Len(result.Results, 1, "Should return exactly 1 result")

		if len(result.Results) > 0 {
			suite.validateSearchResults(result.Results, 1)
			suite.Equal("advanced1", result.Results[0].Document.ID,
				"Should return the beginner education document")
		}
	})

	suite.Run("high similarity threshold", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.8, 0.2}, // Very similar to advanced1
			MinScore:   0.95,                     // High threshold
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "High similarity threshold search should not error")
		suite.NotNil(result, "Search result should not be nil")

		// Should find at least the exact match
		suite.GreaterOrEqual(len(result.Results), 1,
			"Should find at least 1 high similarity result")

		if len(result.Results) > 0 {
			suite.validateSearchResults(result.Results, 1)
			suite.GreaterOrEqual(result.Results[0].Score, 0.95,
				"Top result should meet high similarity threshold")
		}
	})

	suite.Run("hybrid search with complex query", func() {
		query := &vectorstore.SearchQuery{
			Vector: []float64{0.2, 0.7, 0.3},
			Query:  "machine learning neural networks",
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]interface{}{
					"category": "education",
				},
			},
			Limit:      10,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Hybrid search with complex query should not error")
		suite.NotNil(result, "Search result should not be nil")
		suite.GreaterOrEqual(len(result.Results), 1,
			"Should find at least 1 result")

		if len(result.Results) > 0 {
			suite.validateSearchResults(result.Results, 1)

			// Validate keyword relevance
			suite.validateKeywordRelevance(result.Results,
				[]string{"machine", "learning", "neural", "networks"})

			// Verify all results match the filter
			for _, scored := range result.Results {
				category, exists := scored.Document.Metadata["category"]
				suite.True(exists, "Category metadata should exist")
				suite.Equal("education", category,
					"All results should have category='education'")
			}
		}
	})

}

func (suite *PgVectorSearchTestSuite) TestMinScoreFiltering() {
	ctx := context.Background()

	// Setup test data specifically for MinScore testing
	testDocs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "minscore1",
				Name:    "Artificial Intelligence Guide",
				Content: "Comprehensive guide to artificial intelligence and machine learning concepts",
				Metadata: map[string]interface{}{
					"category": "education",
					"type":     "guide",
					"rating":   4.8,
				},
			},
			embedding: []float64{0.1, 0.9, 0.1}, // High similarity target
		},
		{
			doc: &document.Document{
				ID:      "minscore2",
				Name:    "Programming Best Practices",
				Content: "Essential programming practices for software development and code quality",
				Metadata: map[string]interface{}{
					"category": "programming",
					"type":     "tutorial",
					"rating":   4.5,
				},
			},
			embedding: []float64{0.5, 0.3, 0.2}, // Medium similarity
		},
		{
			doc: &document.Document{
				ID:      "minscore3",
				Name:    "Database Design Principles",
				Content: "Database design fundamentals and normalization techniques for efficient data storage",
				Metadata: map[string]interface{}{
					"category": "database",
					"type":     "reference",
					"rating":   4.2,
				},
			},
			embedding: []float64{0.8, 0.1, 0.1}, // Low similarity to AI content
		},
	}

	// Add test documents
	for _, td := range testDocs {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.NoError(err)
		// Store for validation
		suite.testDocs[td.doc.ID] = td.doc
	}

	suite.Run("vector search with medium MinScore", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.9, 0.1}, // Very similar to minscore1
			MinScore:   0.6,                      // Medium threshold
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Vector search with MinScore should not error")
		suite.NotNil(result, "Search result should not be nil")

		// All results should meet the minimum score requirement
		for _, scored := range result.Results {
			suite.GreaterOrEqual(scored.Score, 0.6,
				"All results should meet MinScore threshold of 0.6, got %.4f", scored.Score)
		}

		// Should find at least the highly similar document
		suite.GreaterOrEqual(len(result.Results), 1,
			"Should find at least 1 result with score >= 0.6")

		suite.T().Logf("Vector search with MinScore 0.6 returned %d results", len(result.Results))
	})

	suite.Run("keyword search with MinScore", func() {
		query := &vectorstore.SearchQuery{
			Query:      "artificial intelligence machine learning",
			MinScore:   0.01, // Low threshold for keyword search (ts_rank scores are typically low)
			Limit:      10,
			SearchMode: vectorstore.SearchModeKeyword,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Keyword search with MinScore should not error")
		suite.NotNil(result, "Search result should not be nil")

		// All results should meet the minimum score requirement
		for _, scored := range result.Results {
			suite.GreaterOrEqual(scored.Score, 0.01,
				"All results should meet MinScore threshold of 0.01, got %.4f", scored.Score)
		}

		// Should find relevant documents
		if len(result.Results) > 0 {
			suite.validateKeywordRelevance(result.Results,
				[]string{"artificial", "intelligence", "machine", "learning"})
		}

		suite.T().Logf("Keyword search with MinScore 0.01 returned %d results", len(result.Results))
	})

	suite.Run("hybrid search with MinScore", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{0.2, 0.8, 0.2},
			Query:      "artificial intelligence programming",
			MinScore:   0.3, // Medium threshold for hybrid search
			Limit:      10,
			SearchMode: vectorstore.SearchModeHybrid,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Hybrid search with MinScore should not error")
		suite.NotNil(result, "Search result should not be nil")

		// All results should meet the minimum score requirement
		for _, scored := range result.Results {
			suite.GreaterOrEqual(scored.Score, 0.3,
				"All results should meet MinScore threshold of 0.3, got %.4f", scored.Score)
		}

		// Should combine semantic and keyword relevance
		if len(result.Results) > 0 {
			suite.validateKeywordRelevance(result.Results,
				[]string{"artificial", "intelligence", "programming"})
		}

		suite.T().Logf("Hybrid search with MinScore 0.3 returned %d results", len(result.Results))
	})

	suite.Run("very high MinScore returns no results", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{-1.0, -1.0, -1.0}, // Vector very dissimilar to all test data
			MinScore:   0.95,                        // High threshold that should filter out all results
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Search with very high MinScore should not error")
		suite.NotNil(result, "Search result should not be nil")

		// Should return no results due to high threshold
		suite.Equal(0, len(result.Results),
			"Search with very high MinScore should return no results")

		suite.T().Logf("Search with MinScore 0.95 correctly returned 0 results")
	})

	suite.Run("MinScore combined with metadata filtering", func() {
		query := &vectorstore.SearchQuery{
			Vector: []float64{0.2, 0.8, 0.2},
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]interface{}{
					"category": "education",
				},
			},
			MinScore:   0.5,
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result, err := suite.vs.Search(ctx, query)
		suite.NoError(err, "Search with MinScore and metadata filter should not error")
		suite.NotNil(result, "Search result should not be nil")

		// All results should meet both criteria
		for _, scored := range result.Results {
			suite.GreaterOrEqual(scored.Score, 0.5,
				"All results should meet MinScore threshold of 0.5, got %.4f", scored.Score)

			category, exists := scored.Document.Metadata["category"]
			suite.True(exists, "Category metadata should exist")
			suite.Equal("education", category,
				"All results should have category='education'")
		}

		suite.T().Logf("Search with MinScore 0.5 and metadata filter returned %d results", len(result.Results))
	})

	suite.Run("MinScore edge cases", func() {
		// Test with MinScore = 0 (should return all results)
		query1 := &vectorstore.SearchQuery{
			Vector:     []float64{0.5, 0.5, 0.5},
			MinScore:   0.0,
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result1, err := suite.vs.Search(ctx, query1)
		suite.NoError(err, "Search with MinScore 0.0 should not error")
		suite.NotNil(result1, "Search result should not be nil")

		// Should return documents (scores can be 0 or higher)
		for _, scored := range result1.Results {
			suite.GreaterOrEqual(scored.Score, 0.0,
				"All results should have score >= 0.0, got %.4f", scored.Score)
		}

		// Test with MinScore = 1.0 (perfect match only)
		query2 := &vectorstore.SearchQuery{
			Vector:     []float64{0.1, 0.9, 0.1}, // Exact match to minscore1
			MinScore:   1.0,
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
		}

		result2, err := suite.vs.Search(ctx, query2)
		suite.NoError(err, "Search with MinScore 1.0 should not error")
		suite.NotNil(result2, "Search result should not be nil")

		// Should only return perfect or near-perfect matches
		for _, scored := range result2.Results {
			suite.GreaterOrEqual(scored.Score, 1.0,
				"All results should meet MinScore threshold of 1.0, got %.4f", scored.Score)
		}

		suite.T().Logf("MinScore edge cases: 0.0 returned %d results, 1.0 returned %d results",
			len(result1.Results), len(result2.Results))
	})
}
