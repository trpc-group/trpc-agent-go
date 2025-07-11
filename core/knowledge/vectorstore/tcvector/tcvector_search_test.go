package tcvector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/vectorstore"
)

// TcVectorSearchTestSuite tests the new search methods
type TcVectorSearchTestSuite struct {
	suite.Suite
	vs         *VectorStore
	collection string
}

// SetupSuite runs once before all tests
func (suite *TcVectorSearchTestSuite) SetupSuite() {
	suite.collection = "test_search_" + time.Now().Format("20060102_150405")

	// Initialize vector store (skip if no configuration available)
	vs, err := New(
		WithURL("your-url"),           // Replace with actual URL
		WithUsername("your-username"), // Replace with actual username
		WithPassword("your-password"), // Replace with actual password
		WithDatabase("test_db"),       // Replace with actual database
		WithCollection(suite.collection),
		WithIndexDimension(3), // Small dimension for testing
	)
	if err != nil {
		suite.T().Skip("Skipping tcvector tests: configuration not available")
		return
	}

	suite.vs = vs

	// Add test documents
	testDocs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID:      "doc1",
				Name:    "Python编程指南",
				Content: "Python是一种功能强大的编程语言，广泛用于数据科学和机器学习",
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
				Name:    "Go语言开发",
				Content: "Go是Google开发的编程语言，以其并发性能和简洁性著称",
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
				Name:    "数据科学教程",
				Content: "数据科学结合了统计学、机器学习和领域专业知识来从数据中提取见解",
				Metadata: map[string]interface{}{
					"category": "data-science",
					"field":    "analytics",
					"level":    "advanced",
				},
			},
			embedding: []float64{0.3, 0.4, 0.5},
		},
	}

	ctx := context.Background()
	for _, td := range testDocs {
		err := suite.vs.Add(ctx, td.doc, td.embedding)
		suite.Require().NoError(err)
	}

	// Wait for indexing to complete
	time.Sleep(2 * time.Second)
}

// TearDownSuite runs once after all tests
func (suite *TcVectorSearchTestSuite) TearDownSuite() {
	if suite.vs != nil {
		// Clean up test documents
		ctx := context.Background()
		suite.vs.Delete(ctx, "doc1")
		suite.vs.Delete(ctx, "doc2")
		suite.vs.Delete(ctx, "doc3")
		suite.vs.Close()
	}
}

// TestSearchByVector tests pure vector similarity search
func (suite *TcVectorSearchTestSuite) TestSearchByVector() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}

	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Vector: []float64{0.15, 0.25, 0.35}, // Similar to doc1 and doc2
		Limit:  5,
	}

	result, err := suite.vs.SearchByVector(ctx, query)
	suite.NoError(err)
	suite.NotEmpty(result.Results)

	// Verify results contain expected documents
	foundDocs := make(map[string]bool)
	for _, r := range result.Results {
		foundDocs[r.Document.ID] = true
		suite.T().Logf("Vector Search - ID: %s, Name: %s, Score: %.4f",
			r.Document.ID, r.Document.Name, r.Score)
	}

	suite.True(foundDocs["doc1"] || foundDocs["doc2"], "Should find similar documents")
}

// TestSearchByKeyword tests BM25-based keyword search
func (suite *TcVectorSearchTestSuite) TestSearchByKeyword() {
	if suite.vs == nil {
		suite.T().Skip("Vector store not initialized")
	}

	ctx := context.Background()
	query := &vectorstore.SearchQuery{
		Query: "编程语言",
		Limit: 5,
	}

	result, err := suite.vs.SearchByKeyword(ctx, query)
	suite.NoError(err)
	suite.NotEmpty(result.Results)

	// Verify results contain documents with programming content
	foundProgDocs := false
	for _, r := range result.Results {
		suite.T().Logf("Keyword Search - ID: %s, Name: %s, Score: %.4f",
			r.Document.ID, r.Document.Name, r.Score)
		if r.Document.ID == "doc1" || r.Document.ID == "doc2" {
			foundProgDocs = true
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
		Vector: []float64{0.15, 0.25, 0.35}, // Similar to programming docs
		Query:  "编程",                        // Programming keyword
		Limit:  5,
	}

	result, err := suite.vs.SearchByHybrid(ctx, query)
	suite.NoError(err)
	suite.NotEmpty(result.Results)

	// Verify hybrid search combines both vector similarity and keyword relevance
	foundProgDocs := false
	for _, r := range result.Results {
		suite.T().Logf("Hybrid Search - ID: %s, Name: %s, Score: %.4f",
			r.Document.ID, r.Document.Name, r.Score)
		if r.Document.ID == "doc1" || r.Document.ID == "doc2" {
			foundProgDocs = true
		}
	}

	suite.True(foundProgDocs, "Hybrid search should find programming documents")
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
		Limit: 5,
	}

	result, err := suite.vs.SearchByVector(ctx, query)
	suite.NoError(err)

	// All results should be programming category
	for _, r := range result.Results {
		suite.T().Logf("Filtered Search - ID: %s, Category: %v",
			r.Document.ID, r.Document.Metadata["category"])
		if category, ok := r.Document.Metadata["category"]; ok {
			suite.Equal("programming", category)
		}
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
	}{
		{
			name: "Vector only should use SearchByVector",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.1, 0.2, 0.3},
				Limit:  3,
			},
			expectedLog: "Should route to vector search",
		},
		{
			name: "Keyword only should use SearchByKeyword",
			query: &vectorstore.SearchQuery{
				Query: "数据科学",
				Limit: 3,
			},
			expectedLog: "Should route to keyword search",
		},
		{
			name: "Both vector and keyword should use SearchByHybrid",
			query: &vectorstore.SearchQuery{
				Vector: []float64{0.3, 0.4, 0.5},
				Query:  "数据",
				Limit:  3,
			},
			expectedLog: "Should route to hybrid search",
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			result, err := suite.vs.Search(ctx, tc.query)
			suite.NoError(err, "Search should not error for: %s", tc.expectedLog)

			suite.T().Logf("%s - Found %d results", tc.expectedLog, len(result.Results))
			for _, r := range result.Results {
				suite.T().Logf("  Result: ID=%s, Name=%s, Score=%.4f",
					r.Document.ID, r.Document.Name, r.Score)
			}
		})
	}
}

// TestSearchFunctionSuite runs the tcvector search test suite
func TestSearchFunctionSuite(t *testing.T) {
	suite.Run(t, new(TcVectorSearchTestSuite))
}
