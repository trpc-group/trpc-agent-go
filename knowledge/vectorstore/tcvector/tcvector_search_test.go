package tcvector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TcVectorSearchTestSuite tests the new search methods
type TcVectorSearchTestSuite struct {
	suite.Suite
	vs       *VectorStore
	testDocs map[string]*document.Document // Store test documents for validation
}

func TestTcVectorSearchSuite(t *testing.T) {
	suite.Run(t, new(TcVectorSearchTestSuite))
}

// SetupSuite runs once before all tests
func (suite *TcVectorSearchTestSuite) SetupSuite() {
	if urlStr == "" || key == "" {
		suite.T().Skip("Skipping tcvector tests: VECTOR_STORE_URL and VECTOR_STORE_KEY must be set")
	}

	// Initialize vector store (skip if no configuration available)
	vs, err := New(
		WithURL(urlStr),
		WithUsername(user),
		WithPassword(key),
		WithDatabase(db),
		WithCollection(collection),
		WithLanguage("en"),
		WithIndexDimension(3), // Small dimension for testing
		WithEnableTSVector(true),
	)
	if err != nil {
		suite.T().Fatalf("Failed to initialize tcvector: %v", err)
	}

	// sleep 3 seconds to ensure the collection is ready
	time.Sleep(3 * time.Second)

	suite.vs = vs
	suite.testDocs = make(map[string]*document.Document)

	// Add test documents
	testData := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Name:    "Python Programming Guide",
				Content: "Python is a powerful programming language widely used for data science and machine learning",
				Metadata: map[string]interface{}{
					"category": "programming",
					"language": "python",
					"level":    "beginner",
					"tags":     []string{"python", "programming", "tutorial"},
					"score":    85,
				},
			},
			embedding: []float64{0.1, 0.2, 0.3},
		},
		{
			doc: &document.Document{
				ID:      "doc2",
				Name:    "Go Language Development",
				Content: "Go is a programming language developed by Google, known for its concurrency performance and simplicity",
				Metadata: map[string]interface{}{
					"category": "programming",
					"language": "go",
					"level":    "intermediate",
					"tags":     []string{"go", "programming", "backend"},
					"score":    90,
				},
			},
			embedding: []float64{0.2, 0.3, 0.4},
		},
		{
			doc: &document.Document{
				ID:      "doc3",
				Name:    "Data Science Tutorial",
				Content: "Data science combines statistics, machine learning and domain expertise to extract insights from data",
				Metadata: map[string]interface{}{
					"category": "data-science",
					"field":    "analytics",
					"level":    "advanced",
					"tags":     []string{"data", "science", "analytics"},
					"score":    88,
				},
			},
			embedding: []float64{0.3, 0.4, 0.5},
		},
	}

	ctx := context.Background()
	for _, td := range testData {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.Require().NoError(err)
		// Store for validation
		suite.testDocs[td.doc.ID] = td.doc
	}
	suite.T().Log("test docs loaded")
}

// TearDownSuite runs once after all tests
func (suite *TcVectorSearchTestSuite) TearDownSuite() {
	if suite.vs == nil {
		return
	}
	suite.vs.client.DropDatabase(context.Background(), db)
	suite.vs.Close()
}

// SetupTest runs before each test
func (suite *TcVectorSearchTestSuite) SetupTest() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}
}

// validateSearchResult validates a single search result
func (suite *TcVectorSearchTestSuite) validateSearchResult(result *vectorstore.ScoredDocument) {
	suite.NotNil(result.Document, "Document should not be nil")
	suite.GreaterOrEqual(result.Score, 0.0, "Score should be non-negative")
	suite.LessOrEqual(result.Score, 1.0, "Score should not exceed 1.0")

	originalDoc, exists := suite.testDocs[result.Document.ID]
	suite.Assert().True(exists, "Document should exist")
	suite.validateDocumentContent(originalDoc, result.Document)
}

// validateDocumentContent compares returned document with original
func (suite *TcVectorSearchTestSuite) validateDocumentContent(expected, actual *document.Document) {
	suite.Equal(expected.ID, actual.ID, "Document ID should match")
	suite.Equal(expected.Name, actual.Name, "Document Name should match")
	suite.Equal(expected.Content, actual.Content, "Document Content should match")
	// validate metadata
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
func (suite *TcVectorSearchTestSuite) compareMetadataValues(expected, actual interface{}) bool {
	// Direct equality check
	if expected == actual {
		return true
	}

	// Handle numeric type conversions (JSON unmarshaling often converts to float64)
	switch a := actual.(type) {
	case json.Number:
		eStr := fmt.Sprintf("%v", expected)
		return eStr == a.String()
	case int:
		if e, ok := expected.(int); ok {
			return e == a
		}
	case float64:
		fmt.Printf("float64: expected %v, actual %v\n", expected, actual)
		e, ok := expected.(float64)
		fmt.Printf("float64: e %v, ok %v\n", e, ok)
		if e, ok := expected.(float64); ok {
			return e == a
		}
	case []string:
		if e, ok := expected.([]string); ok {
			if len(e) != len(a) {
				return false
			}
			for i, v := range a {
				if v != e[i] {
					return false
				}
			}
		}
		return true
	case []interface{}:
		if e, ok := expected.([]interface{}); ok {
			if len(e) != len(a) {
				return false
			}
			for i, v := range a {
				if !suite.compareMetadataValues(e[i], v) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// validateSearchResults validates the complete search result set
func (suite *TcVectorSearchTestSuite) validateSearchResults(results []*vectorstore.ScoredDocument, expectedMinCount int) {
	suite.GreaterOrEqual(len(results), expectedMinCount, "Should return at least %d results", expectedMinCount)

	// Validate individual results
	for i, result := range results {
		suite.validateSearchResult(result)
		suite.T().Logf("Result %d: ID=%s, Name=%s, Score=%.4f", i+1, result.Document.ID, result.Document.Name, result.Score)
	}

	// Validate score ordering (descending)
	for i := 1; i < len(results); i++ {
		suite.GreaterOrEqual(results[i-1].Score, results[i].Score,
			"Results should be ordered by score (descending): result[%d].Score=%.4f >= result[%d].Score=%.4f",
			i-1, results[i-1].Score, i, results[i].Score)
	}
}

// validateKeywordRelevance checks if results are relevant to the keyword query
func (suite *TcVectorSearchTestSuite) validateKeywordRelevance(results []*vectorstore.ScoredDocument, keywords []string) {
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

// TestSearchByVector tests pure vector similarity search
func (suite *TcVectorSearchTestSuite) TestSearchByVector() {
	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.15, 0.25, 0.35}, // Similar to doc1 and doc2
		Limit:      5,
		SearchMode: vectorstore.SearchModeVector,
	}

	result, err := suite.vs.Search(ctx, query)
	suite.NoError(err, "SearchByVector should not error")
	suite.NotNil(result, "Search result should not be nil")

	// Validate search results
	suite.validateSearchResults(result.Results, 1)

	// Verify results contain expected documents (doc1 and doc2 should be similar)
	foundDocs := make(map[string]bool)
	for _, r := range result.Results {
		foundDocs[r.Document.ID] = true
	}

	suite.True(foundDocs["doc1"] || foundDocs["doc2"], "Should find documents similar to query vector")

	topResult := result.Results[0]
	suite.Greater(topResult.Score, 0.0, "Top result should have positive similarity score")
}

// TestSearchByKeyword tests BM25-based keyword search
func (suite *TcVectorSearchTestSuite) TestSearchByKeyword() {
	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Query:      "programming language",
		Limit:      5,
		SearchMode: vectorstore.SearchModeKeyword,
	}

	result, err := suite.vs.Search(ctx, query)
	suite.NoError(err, "SearchByKeyword should not error")
	suite.NotNil(result, "Search result should not be nil")

	// Validate search results
	suite.validateSearchResults(result.Results, 1)

	// Validate keyword relevance
	suite.validateKeywordRelevance(result.Results, []string{"programming", "language"})

	// Verify results contain documents with programming content
	foundProgDocs := false
	for _, r := range result.Results {
		if r.Document.ID == "doc1" || r.Document.ID == "doc2" {
			foundProgDocs = true
			// Validate that programming documents have relevant metadata
			if category, ok := r.Document.Metadata["category"]; ok {
				suite.Equal("programming", category, "Programming documents should have correct category")
			}
		}
	}

	suite.True(foundProgDocs, "Should find documents about programming languages")
}

// TestSearchByHybrid tests hybrid search combining vector and keyword matching
func (suite *TcVectorSearchTestSuite) TestSearchByHybrid() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}

	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.15, 0.25, 0.35}, // Similar to programming docs
		Query:      "programming",               // Programming keyword
		Limit:      5,
		SearchMode: vectorstore.SearchModeHybrid,
	}

	result, err := suite.vs.Search(ctx, query)
	suite.NoError(err, "SearchByHybrid should not error")
	suite.NotNil(result, "Search result should not be nil")

	// Validate search results
	suite.validateSearchResults(result.Results, 1)

	// Validate keyword relevance
	suite.validateKeywordRelevance(result.Results, []string{"programming"})

	// Verify hybrid search finds programming documents
	foundProgDocs := false
	maxScore := 0.0
	for _, r := range result.Results {
		if r.Score > maxScore {
			maxScore = r.Score
		}
		if r.Document.ID == "doc1" || r.Document.ID == "doc2" {
			foundProgDocs = true
			// Hybrid search should potentially have higher scores than pure vector/keyword
			suite.T().Logf("Hybrid result - ID: %s, Score: %.4f", r.Document.ID, r.Score)
		}
	}

	suite.True(foundProgDocs, "Hybrid search should find programming documents")
	suite.Greater(maxScore, 0.0, "Hybrid search should return meaningful scores")
}

// TestSearchWithFilters tests search with metadata filters
func (suite *TcVectorSearchTestSuite) TestSearchWithFilters() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}

	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Vector: []float64{0.2, 0.3, 0.4},
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]interface{}{
				"category": "programming",
			},
		},
		Limit:      5,
		SearchMode: vectorstore.SearchModeFilter,
	}

	result, err := suite.vs.Search(ctx, query)
	suite.NoError(err, "Filtered search should not error")
	suite.NotNil(result, "Search result should not be nil")

	// Validate search results
	if len(result.Results) > 0 {
		suite.validateSearchResults(result.Results, 1)

		// All results should match the filter
		for _, r := range result.Results {
			suite.T().Logf("Filtered Search - ID: %s, Category: %v, Level: %v",
				r.Document.ID, r.Document.Metadata["category"], r.Document.Metadata["level"])

			if category, ok := r.Document.Metadata["category"]; ok {
				suite.Equal("programming", category,
					"All filtered results should have category='programming'")
			} else {
				suite.Fail("Result should have category metadata")
			}
		}
	} else {
		suite.T().Log("No results returned for filtered search - this may be expected")
	}
}

// TestSearchModeSelection tests automatic search mode selection
func (suite *TcVectorSearchTestSuite) TestSearchModeSelection() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}

	ctx := context.Background()

	testCases := []struct {
		name        string
		query       *vectorstore.SearchQuery
		expectedLog string
		validator   func(results []*vectorstore.ScoredDocument)
	}{
		{
			name: "Vector only should use SearchByVector",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeVector,
				Vector:     []float64{0.1, 0.2, 0.3},
				Limit:      3,
			},
			expectedLog: "Should route to vector search",
			validator: func(results []*vectorstore.ScoredDocument) {
				// Vector search should prioritize similarity
				if len(results) > 0 {
					suite.Greater(results[0].Score, 0.0, "Vector search should return similarity scores")
				}
			},
		},
		{
			name: "Keyword only should use SearchByKeyword",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeKeyword,
				Query:      "data science",
				Limit:      3,
			},
			expectedLog: "Should route to keyword search",
			validator: func(results []*vectorstore.ScoredDocument) {
				// Keyword search should find relevant documents
				suite.validateKeywordRelevance(results, []string{"data", "science"})
			},
		},
		{
			name: "Both vector and keyword should use SearchByHybrid",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeHybrid,
				Vector:     []float64{0.3, 0.4, 0.5},
				Query:      "data",
				Limit:      3,
			},
			expectedLog: "Should route to hybrid search",
			validator: func(results []*vectorstore.ScoredDocument) {
				// Hybrid search should combine both signals
				suite.validateKeywordRelevance(results, []string{"data"})
				if len(results) > 0 {
					suite.Greater(results[0].Score, 0.0, "Hybrid search should return meaningful scores")
				}
			},
		},
		{
			name: "Filter only should use SearchByFilter",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc3"},
				},
				Limit: 3,
			},
			expectedLog: "Should route to filter search",
			validator: func(results []*vectorstore.ScoredDocument) {
				// Filter search should return exact matches
				suite.Len(results, 2, "Filter search should return exactly 2 results")
				foundDocs := make(map[string]bool)
				for _, r := range results {
					foundDocs[r.Document.ID] = true
					suite.Equal(1.0, r.Score, "Filter search results should have score 1.0")
				}
				suite.True(foundDocs["doc1"], "Should find doc1")
				suite.True(foundDocs["doc3"], "Should find doc3")
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			result, err := suite.vs.Search(ctx, tc.query)
			suite.NoError(err, "Search should not error for: %s", tc.expectedLog)
			suite.NotNil(result, "Search result should not be nil")

			// Validate basic result structure
			if len(result.Results) > 0 {
				suite.validateSearchResults(result.Results, 0)
			}

			// Run specific validator
			if tc.validator != nil {
				tc.validator(result.Results)
			}

			suite.T().Logf("%s - Found %d results", tc.expectedLog, len(result.Results))
			for i, r := range result.Results {
				suite.T().Logf("  Result %d: ID=%s, Name=%s, Score=%.4f",
					i+1, r.Document.ID, r.Document.Name, r.Score)
			}
		})
	}
}
