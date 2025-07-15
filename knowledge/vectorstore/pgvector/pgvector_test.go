package pgvector

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

var (
	host     = getEnvOrDefault("PGVECTOR_HOST", "")
	port     = getEnvIntOrDefault("PGVECTOR_PORT", 5432)
	user     = getEnvOrDefault("PGVECTOR_USER", "root")
	password = getEnvOrDefault("PGVECTOR_PASSWORD", "")
	database = getEnvOrDefault("PGVECTOR_DATABASE", "trpc_agent_unit_test")
	table    = getEnvOrDefault("PGVECTOR_TABLE", "trpc_agent_unit_test_documents")
)

// PgVectorTestSuite contains the test suite for pgvector basic operations
type PgVectorTestSuite struct {
	suite.Suite
	vs *VectorStore
}

// Run the test suite
func TestPgVectorSuite(t *testing.T) {
	suite.Run(t, new(PgVectorTestSuite))
}

// SetupSuite runs once before all tests
func (suite *PgVectorTestSuite) SetupSuite() {
	if host == "" {
		suite.T().Skip("Skipping PgVector tests: PGVECTOR_HOST not set")
		return
	}

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
}

// TearDownSuite runs once after all tests
func (suite *PgVectorTestSuite) TearDownSuite() {
	if suite.vs != nil {
		// Clean up test table
		_, err := suite.vs.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
		suite.NoError(err)
		suite.vs.Close()
	}
}

// SetupTest runs before each test
func (suite *PgVectorTestSuite) SetupTest() {
	// Clean up table data before each test
	_, err := suite.vs.pool.Exec(context.Background(), "DELETE FROM "+table)
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

// TestErrorHandling tests various non-search error conditions
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
			name: "add document with nil metadata",
			operation: func() error {
				doc := &document.Document{
					ID:       "nil_meta_test",
					Name:     "Test",
					Content:  "Test",
					Metadata: nil,
				}
				return suite.vs.Add(ctx, doc, []float64{0.1, 0.2, 0.3})
			},
			wantError: false, // Should handle nil metadata gracefully
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

// Helper functions for environment variable parsing used in tests
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
