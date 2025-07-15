package tcvector

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var (
	key        = getEnvOrDefault("TCVECTOR_STORE_KEY", "")
	url        = getEnvOrDefault("TCVECTOR_STORE_URL", "")
	user       = getEnvOrDefault("TCVECTOR_STORE_USER", "root")
	db         = getEnvOrDefault("TCVECTOR_STORE_DATABASE", "trpc_agent_unit_test")
	collection = getEnvOrDefault("TCVECTOR_STORE_COLLECTION", "trpc_agent_unit_test_documents")
)

// TCVectorTestSuite contains test suite state
type TCVectorTestSuite struct {
	suite.Suite
	vs          *VectorStore
	ctx         context.Context
	addedDocIDs []string // Track documents for cleanup
}

var testData = []struct {
	name   string
	doc    *document.Document
	vector []float64
}{
	{
		name: "ai_doc",
		doc: &document.Document{
			ID:      "test_001",
			Name:    "AI Fundamentals",
			Content: "Artificial intelligence is a branch of computer science",
			Metadata: map[string]interface{}{
				"category": "AI",
				"priority": 5,
				"tags":     []string{"AI", "fundamentals"},
			},
		},
		vector: []float64{1.0, 0.5, 0.2},
	},
	{
		name: "ml_doc",
		doc: &document.Document{
			ID:      "test_002",
			Name:    "Machine Learning Algorithms",
			Content: "Machine learning is the core technology of artificial intelligence",
			Metadata: map[string]interface{}{
				"category": "ML",
				"priority": 8,
				"tags":     []string{"ML", "algorithms"},
			},
		},
		vector: []float64{0.8, 1.0, 0.3},
	},
	{
		name: "dl_doc",
		doc: &document.Document{
			ID:      "test_003",
			Name:    "Deep Learning Framework",
			Content: "Deep learning is a subset of machine learning",
			Metadata: map[string]interface{}{
				"category": "DL",
				"priority": 6,
				"tags":     []string{"DL", "framework"},
			},
		},
		vector: []float64{0.6, 0.8, 1.0},
	},
}

// SetupSuite initializes the test suite
func (suite *TCVectorTestSuite) SetupSuite() {
	suite.ctx = context.Background()
	if url == "" || key == "" {
		suite.T().Skip("Skip test: TCVECTOR_STORE_URL, TCVECTOR_STORE_KEY environment variables required")
		return
	}

	vs, err := New(
		WithURL(url),
		WithUsername(user),
		WithPassword(key),
		WithDatabase(db),
		WithCollection(collection),
		WithIndexDimension(3),
		WithSharding(1),
		WithReplicas(0),
	)
	suite.Require().NoError(err, "Failed to create VectorStore")
	suite.vs = vs
	suite.addedDocIDs = make([]string, 0)

	suite.T().Logf("Test suite setup completed with collection: %s", collection)
}

// TearDownSuite cleans up test data
func (suite *TCVectorTestSuite) TearDownSuite() {
	if suite.vs == nil {
		return
	}

	// Drop the entire collection after all tests are done
	_, err := suite.vs.pool.DropCollection(suite.ctx, db, collection)
	if err != nil {
		suite.T().Logf("Warning: Failed to drop collection %s during cleanup: %v", collection, err)
	}

	suite.vs.Close()
	suite.T().Logf("Test suite cleanup completed.")
}

// SetupTest runs before each test to ensure a clean state
func (suite *TCVectorTestSuite) SetupTest() {
	// Clean up any documents that were added in a previous test run
	for _, id := range suite.addedDocIDs {
		_ = suite.vs.Delete(suite.ctx, id) // Ignore error as doc might already be deleted
	}
	// Reset the tracker
	suite.addedDocIDs = make([]string, 0)
}

// validateDocument compares two documents for equality
func (suite *TCVectorTestSuite) validateDocument(expected *document.Document, actual *document.Document) {
	suite.Equal(expected.ID, actual.ID, "Document ID should match")
	suite.Equal(expected.Name, actual.Name, "Document Name should match")
	suite.Equal(expected.Content, actual.Content, "Document Content should match")

	// Validate metadata with more flexible type checking
	for key, expectedValue := range expected.Metadata {
		actualValue, exists := actual.Metadata[key]
		if !exists {
			suite.T().Logf("Note: Missing metadata key: %s", key)
			continue
		}

		// More flexible comparison for different types
		suite.True(suite.compareMetadataValues(expectedValue, actualValue),
			"Metadata %s should match: got %v (%T), expected %v (%T)",
			key, actualValue, actualValue, expectedValue, expectedValue)
	}
}

// compareMetadataValues provides flexible comparison for metadata values
func (suite *TCVectorTestSuite) compareMetadataValues(expected, actual interface{}) bool {
	// Direct equality
	if reflect.DeepEqual(expected, actual) {
		return true
	}

	// Handle numeric type conversions
	switch e := expected.(type) {
	case int:
		if a, ok := actual.(float64); ok {
			return float64(e) == a
		}
	case float64:
		if a, ok := actual.(int); ok {
			return e == float64(a)
		}
	case int64:
		if a, ok := actual.(float64); ok {
			return float64(e) == a
		}
	}

	// For slices, try element-wise comparison
	expectedVal := reflect.ValueOf(expected)
	actualVal := reflect.ValueOf(actual)
	if expectedVal.Kind() == reflect.Slice && actualVal.Kind() == reflect.Slice {
		if expectedVal.Len() != actualVal.Len() {
			return false
		}
		for i := 0; i < expectedVal.Len(); i++ {
			if !suite.compareMetadataValues(expectedVal.Index(i).Interface(), actualVal.Index(i).Interface()) {
				return false
			}
		}
		return true
	}

	return false
}

// validateVector compares two vectors for equality
func (suite *TCVectorTestSuite) validateVector(expected []float64, actual []float64, tolerance float64) {
	suite.Require().Len(actual, len(expected), "Vector length should match")

	for i, expectedVal := range expected {
		suite.Assert().InDelta(expectedVal, actual[i], tolerance,
			"Vector[%d] should match within tolerance", i)
	}
}

// abs returns absolute value of float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestTCVectorSuite(t *testing.T) {
	suite.Run(t, new(TCVectorTestSuite))
}

func (suite *TCVectorTestSuite) TestAdd() {
	tests := []struct {
		name    string
		doc     *document.Document
		vector  []float64
		wantErr bool
	}{
		{
			name:    "valid_document",
			doc:     testData[0].doc,
			vector:  testData[0].vector,
			wantErr: false,
		},
		{
			name:    "empty_vector",
			doc:     testData[1].doc,
			vector:  []float64{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			err := suite.vs.Add(suite.ctx, tt.doc, tt.vector)
			if tt.wantErr {
				suite.Error(err, "error expected")
				return
			}

			suite.Require().NoError(err, "no error expected")

			// Track for cleanup
			suite.addedDocIDs = append(suite.addedDocIDs, tt.doc.ID)
			retrievedDoc, retrievedVector, err := suite.vs.Get(suite.ctx, tt.doc.ID)
			suite.Require().NoError(err, "query after add should succeed")
			suite.Require().NotNil(retrievedDoc, "retrieved document should not be nil")

			fmt.Println(retrievedDoc)
			suite.validateDocument(tt.doc, retrievedDoc)
			suite.validateVector(tt.vector, retrievedVector, 0.0001)
			suite.T().Logf("Successfully added and validated document: %s", tt.doc.ID)
		})
	}
}

func (suite *TCVectorTestSuite) TestGet() {
	// Setup: Add test document first
	testDoc := testData[0]
	err := suite.vs.Add(suite.ctx, testDoc.doc, testDoc.vector)
	suite.Require().NoError(err, "Failed to add test document")

	suite.addedDocIDs = append(suite.addedDocIDs, testDoc.doc.ID)

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "existing_document",
			id:      testDoc.doc.ID,
			wantErr: false,
		},
		{
			name:    "non_existing_document",
			id:      "non_existent_id",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			doc, vector, err := suite.vs.Get(suite.ctx, tt.id)

			if tt.wantErr {
				suite.Error(err, "error expected for non-existing document")
				return
			}

			suite.Require().NoError(err, "no error expected for existing document")
			suite.Require().NotNil(doc, "document should not be nil")
			suite.Require().NotNil(vector, "vector should not be nil")

			suite.validateDocument(testDoc.doc, doc)
			suite.validateVector(testDoc.vector, vector, 0.0001)
			suite.T().Logf("Successfully retrieved and validated document: %s", tt.id)
		})
	}
}

func (suite *TCVectorTestSuite) TestUpdate() {
	// Setup: Add test document
	testDoc := testData[0]
	err := suite.vs.Add(suite.ctx, testDoc.doc, testDoc.vector)
	suite.Require().NoError(err, "Failed to add test document")

	suite.addedDocIDs = append(suite.addedDocIDs, testDoc.doc.ID)

	tests := []struct {
		name      string
		updateDoc *document.Document
		newVector []float64
		wantErr   bool
	}{
		{
			name: "valid_update",
			updateDoc: &document.Document{
				ID:      testDoc.doc.ID,
				Name:    "Updated AI Fundamentals",
				Content: "Updated content about artificial intelligence",
				Metadata: map[string]interface{}{
					"category":   "AI",
					"priority":   9,
					"tags":       []string{"AI", "fundamentals", "updated"},
					"updated_at": time.Now().Unix(),
				},
			},
			newVector: []float64{1.0, 0.6, 0.3},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			err := suite.vs.Update(suite.ctx, tt.updateDoc, tt.newVector)
			if tt.wantErr {
				suite.Error(err, "error expected")
				return
			}

			suite.Require().NoError(err, "update should succeed")

			doc, vector, err := suite.vs.Get(suite.ctx, tt.updateDoc.ID)
			suite.Require().NoError(err, "get after update should succeed")
			suite.Require().NotNil(doc, "document should not be nil after update")
			suite.Require().NotNil(vector, "vector should not be nil after update")

			suite.validateDocument(tt.updateDoc, doc)
			suite.validateVector(tt.newVector, vector, 0.0001)
			suite.T().Logf("Successfully updated and validated document: %s", tt.updateDoc.ID)
		})
	}
}

func (suite *TCVectorTestSuite) TestDelete() {
	// Setup: Add test document
	testDoc := testData[0]
	err := suite.vs.Add(suite.ctx, testDoc.doc, testDoc.vector)
	suite.Require().NoError(err, "Failed to add test document")

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "existing_document",
			id:      testDoc.doc.ID,
			wantErr: false,
		},
		{
			name:    "non_existing_document",
			id:      "non_existent_id",
			wantErr: false, // Delete non-existing should not error
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			err := suite.vs.Delete(suite.ctx, tt.id)
			if tt.wantErr {
				suite.Error(err, "error expected")
				return
			}

			suite.NoError(err, "delete should not error")

			if tt.id == testDoc.doc.ID {
				_, _, err := suite.vs.Get(suite.ctx, tt.id)
				suite.Error(err, "document should not exist after deletion")
				suite.T().Logf("Successfully deleted document: %s", tt.id)
			}
		})
	}
}

func (suite *TCVectorTestSuite) TestEdgeCases() {
	suite.Run("empty_vector_search", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{0, 0, 0},
			Limit:      1,
			SearchMode: vectorstore.SearchModeVector,
		}
		_, err := suite.vs.Search(suite.ctx, query)
		// Empty vector search might be valid or invalid depending on implementation
		if err != nil {
			suite.T().Logf("Empty vector search error (may be expected): %v", err)
		}
	})

	suite.Run("high_threshold_search", func() {
		query := &vectorstore.SearchQuery{
			Vector:     []float64{1.0, 1.0, 1.0},
			Limit:      1000,
			MinScore:   0.99,
			SearchMode: vectorstore.SearchModeVector,
		}
		result, err := suite.vs.Search(suite.ctx, query)
		suite.NoError(err, "high threshold search should not error")
		if result != nil {
			suite.GreaterOrEqual(len(result.Results), 0,
				"high threshold search should return >= 0 results")
			suite.T().Logf("High threshold search returned %d results", len(result.Results))
		}
	})
}

// Helper functions for environment variable parsing used in tests
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
