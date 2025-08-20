//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var (
	esHost = getEnvOrDefault("ELASTICSEARCH_HOST", "")
	esPort = getEnvOrDefault("ELASTICSEARCH_PORT", "9200")
)

// getEnvOrDefault gets environment variable value or returns default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// MockElasticsearchClient is a mock implementation for testing.
type MockElasticsearchClient struct {
	documents map[string]map[string]any
	indices   map[string]bool
}

// NewMockElasticsearchClient creates a new mock client.
func NewMockElasticsearchClient() *MockElasticsearchClient {
	return &MockElasticsearchClient{
		documents: make(map[string]map[string]any),
		indices:   make(map[string]bool),
	}
}

// Ping always succeeds in mock.
func (m *MockElasticsearchClient) Ping(ctx context.Context) error {
	return nil
}

// CreateIndex creates an index in mock.
func (m *MockElasticsearchClient) CreateIndex(ctx context.Context, indexName string, mapping map[string]any) error {
	m.indices[indexName] = true
	return nil
}

// DeleteIndex deletes an index in mock.
func (m *MockElasticsearchClient) DeleteIndex(ctx context.Context, indexName string) error {
	delete(m.indices, indexName)
	return nil
}

// IndexExists checks if index exists in mock.
func (m *MockElasticsearchClient) IndexExists(ctx context.Context, indexName string) (bool, error) {
	return m.indices[indexName], nil
}

// IndexDocument indexes a document in mock.
func (m *MockElasticsearchClient) IndexDocument(ctx context.Context, indexName, id string, document any) error {
	if doc, ok := document.(map[string]any); ok {
		m.documents[id] = doc
	}
	return nil
}

// GetDocument retrieves a document from mock.
func (m *MockElasticsearchClient) GetDocument(ctx context.Context, indexName, id string) ([]byte, error) {
	doc, exists := m.documents[id]
	if !exists {
		return nil, fmt.Errorf("document not found")
	}

	response := map[string]any{
		"_source": doc,
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	return responseBytes, nil
}

// UpdateDocument updates a document in mock.
func (m *MockElasticsearchClient) UpdateDocument(ctx context.Context, indexName, id string, document any) error {
	if doc, ok := document.(map[string]any); ok {
		if existing, exists := m.documents[id]; exists {
			// Merge updates.
			for k, v := range doc {
				existing[k] = v
			}
		}
	}
	return nil
}

// DeleteDocument deletes a document from mock.
func (m *MockElasticsearchClient) DeleteDocument(ctx context.Context, indexName, id string) error {
	delete(m.documents, id)
	return nil
}

// Search performs search in mock.
func (m *MockElasticsearchClient) Search(ctx context.Context, indexName string, query map[string]any) ([]byte, error) {
	// Simple mock search that returns all documents.
	var hits []map[string]any
	for _, doc := range m.documents {
		hit := map[string]any{
			"_source": doc,
			"_score":  0.9, // Mock score.
		}
		hits = append(hits, hit)
	}

	response := map[string]any{
		"hits": map[string]any{
			"hits": hits,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	return responseBytes, nil
}

// BulkIndex performs bulk indexing in mock.
func (m *MockElasticsearchClient) BulkIndex(ctx context.Context, indexName string, documents []BulkDocument) error {
	for _, doc := range documents {
		switch doc.Action {
		case "index":
			m.documents[doc.ID] = doc.Document.(map[string]any)
		case "delete":
			delete(m.documents, doc.ID)
		}
	}
	return nil
}

// Close does nothing in mock.
func (m *MockElasticsearchClient) Close() error { return nil }

// GetRawClient returns nil in mock.
func (m *MockElasticsearchClient) GetRawClient() any { return nil }

// BulkDocument represents a document for bulk operations.
type BulkDocument struct {
	ID       string
	Document any
	Action   string
}

func TestNewVectorStore(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}

	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithIndexName("test_index"),
		WithScoreThreshold(0.5),
		WithMaxResults(20),
		WithVectorDimension(5),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	if vs == nil {
		t.Fatal("Vector store should not be nil")
	}
	if vs.option.indexName != "test_index" {
		t.Errorf("Expected index name 'test_index', got '%s'", vs.option.indexName)
	}
}

func TestVectorStore_Add(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}
	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithVectorDimension(5),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	// Add
	doc := &document.Document{ID: "test_doc_1", Name: "Test Document", Content: "This is a test document content.", Metadata: map[string]any{"type": "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	embedding := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	if err := vs.Add(context.Background(), doc, embedding); err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}
}

func TestVectorStore_Get(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}
	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithVectorDimension(5),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	// Add
	doc := &document.Document{ID: "test_doc_2", Name: "Test Document 2", Content: "This is another test document.", Metadata: map[string]any{"category": "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	embedding := []float64{0.5, 0.4, 0.3, 0.2, 0.1}
	if err := vs.Add(context.Background(), doc, embedding); err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}
	// Get
	retrievedDoc, retrievedEmbedding, err := vs.Get(context.Background(), "test_doc_2")
	if err != nil {
		t.Fatalf("Failed to get document: %v", err)
	}
	if retrievedDoc == nil {
		t.Fatal("Retrieved document should not be nil")
	}
	if retrievedDoc.Name != "Test Document 2" {
		t.Errorf("Expected name 'Test Document 2', got '%s'", retrievedDoc.Name)
	}
	if len(retrievedEmbedding) != 5 {
		t.Errorf("Expected embedding length 5, got %d", len(retrievedEmbedding))
	}
}

func TestVectorStore_Search(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}
	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithVectorDimension(5),
		WithIndexName("test_index_search"),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	// Add docs
	docs := []*document.Document{
		{ID: "doc1", Name: "Document 1", Content: "First test document", Metadata: map[string]any{"type": "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "doc2", Name: "Document 2", Content: "Second test document", Metadata: map[string]any{"type": "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	for _, d := range docs {
		if err := vs.Add(context.Background(), d, []float64{0.1, 0.2, 0.3, 0.4, 0.5}); err != nil {
			t.Fatalf("Failed to add document: %v", err)
		}
	}
	// Search
	query := &vectorstore.SearchQuery{Query: "test document", Vector: []float64{0.1, 0.2, 0.3, 0.4, 0.5}, Limit: 10, MinScore: 0.5, SearchMode: vectorstore.SearchModeHybrid}
	results, err := vs.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}
	if results == nil {
		t.Fatal("Search results should not be nil")
	}
	if len(results.Results) == 0 {
		t.Fatal("Search should return some results")
	}
}

func TestVectorStore_Update(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}
	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithVectorDimension(3),
		WithIndexName("test_index_update"),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	// Add initial
	doc := &document.Document{ID: "update_test_doc", Name: "Original Name", Content: "Original content", Metadata: map[string]any{"version": "1"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	embedding := []float64{0.1, 0.2, 0.3}
	if err := vs.Add(context.Background(), doc, embedding); err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}
	// Update
	doc.Name = "Updated Name"
	doc.Content = "Updated content"
	doc.Metadata["version"] = "2"
	if err := vs.Update(context.Background(), doc, []float64{0.4, 0.5, 0.6}); err != nil {
		t.Fatalf("Failed to update document: %v", err)
	}
	// Verify
	retrievedDoc, _, err := vs.Get(context.Background(), "update_test_doc")
	if err != nil {
		t.Fatalf("Failed to get updated document: %v", err)
	}
	if retrievedDoc.Name != "Updated Name" {
		t.Errorf("Expected updated name 'Updated Name', got '%s'", retrievedDoc.Name)
	}
}

func TestVectorStore_Delete(t *testing.T) {
	if esHost == "" {
		t.Skip("Skipping Elasticsearch tests: ELASTICSEARCH_HOST not set")
	}
	vs, err := New(
		WithAddresses([]string{fmt.Sprintf("http://%s:%s", esHost, esPort)}),
		WithVectorDimension(3),
		WithIndexName("test_index_delete"),
	)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	// Add
	doc := &document.Document{ID: "delete_test_doc", Name: "Document to Delete", Content: "This document will be deleted", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3}); err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}
	// Delete
	if err := vs.Delete(context.Background(), "delete_test_doc"); err != nil {
		t.Fatalf("Failed to delete document: %v", err)
	}
	// Verify
	if _, _, err := vs.Get(context.Background(), "delete_test_doc"); err == nil {
		t.Fatal("Document should not exist after deletion")
	}
}

func TestDefaultOptions(t *testing.T) {
	opt := defaultOptions()
	if opt.indexName != DefaultIndexName {
		t.Errorf("Expected default index name '%s', got '%s'", DefaultIndexName, opt.indexName)
	}
	if opt.vectorField != DefaultVectorField {
		t.Errorf("Expected default vector field '%s', got '%s'", DefaultVectorField, opt.vectorField)
	}
	if opt.scoreThreshold != DefaultScoreThreshold {
		t.Errorf("Expected default score threshold %f, got %f", DefaultScoreThreshold, opt.scoreThreshold)
	}
	if opt.maxResults != 10 {
		t.Errorf("Expected default max results 10, got %d", opt.maxResults)
	}
	if opt.vectorDimension != DefaultVectorDimension {
		t.Errorf("Expected default vector dimension %d, got %d", DefaultVectorDimension, opt.vectorDimension)
	}
}
