//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"testing"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// mockClient is a mock implementation of storage.ClientInterface for testing.
// It provides an in-memory storage and allows simulating various scenarios.
type mockClient struct {
	// Embed interfaces to satisfy the interface requirements
	tcvectordb.DatabaseInterface
	tcvectordb.FlatInterface

	// In-memory storage
	documents map[string]tcvectordb.Document
	mu        sync.RWMutex

	// Call tracking for verification
	upsertCalls  int
	queryCalls   int
	searchCalls  int
	deleteCalls  int
	updateCalls  int
	hybridCalls  int
	batchCalls   int
	rebuildCalls int

	// Error simulation
	upsertError  error
	queryError   error
	searchError  error
	deleteError  error
	updateError  error
	hybridError  error
	batchError   error
	rebuildError error

	// Database and collection tracking
	databases   map[string]bool
	collections map[string]map[string]bool // db -> collection -> exists
}

// newMockClient creates a new mock client for testing.
func newMockClient() *mockClient {
	return &mockClient{
		documents:   make(map[string]tcvectordb.Document),
		databases:   make(map[string]bool),
		collections: make(map[string]map[string]bool),
	}
}

// Reset clears all state and counters.
func (m *mockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.documents = make(map[string]tcvectordb.Document)
	m.databases = make(map[string]bool)
	m.collections = make(map[string]map[string]bool)

	m.upsertCalls = 0
	m.queryCalls = 0
	m.searchCalls = 0
	m.deleteCalls = 0
	m.updateCalls = 0
	m.hybridCalls = 0
	m.batchCalls = 0
	m.rebuildCalls = 0

	m.upsertError = nil
	m.queryError = nil
	m.searchError = nil
	m.deleteError = nil
	m.updateError = nil
	m.hybridError = nil
	m.batchError = nil
	m.rebuildError = nil
}

// Error setters for simulating failures

func (m *mockClient) SetUpsertError(err error)  { m.upsertError = err }
func (m *mockClient) SetQueryError(err error)   { m.queryError = err }
func (m *mockClient) SetSearchError(err error)  { m.searchError = err }
func (m *mockClient) SetDeleteError(err error)  { m.deleteError = err }
func (m *mockClient) SetUpdateError(err error)  { m.updateError = err }
func (m *mockClient) SetHybridError(err error)  { m.hybridError = err }
func (m *mockClient) SetBatchError(err error)   { m.batchError = err }
func (m *mockClient) SetRebuildError(err error) { m.rebuildError = err }

// Getters for verification

func (m *mockClient) GetDocument(id string) (tcvectordb.Document, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	doc, ok := m.documents[id]
	return doc, ok
}

func (m *mockClient) GetDocumentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.documents)
}

func (m *mockClient) GetUpsertCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upsertCalls
}

func (m *mockClient) GetQueryCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queryCalls
}

func (m *mockClient) GetSearchCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.searchCalls
}

func (m *mockClient) GetDeleteCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deleteCalls
}

func (m *mockClient) GetUpdateCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.updateCalls
}

func (m *mockClient) GetHybridCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hybridCalls
}

func (m *mockClient) GetBatchCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.batchCalls
}

func (m *mockClient) GetRebuildCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rebuildCalls
}

// Implementation of storage.ClientInterface methods

func (m *mockClient) Upsert(ctx context.Context, db, collection string, docs interface{}, params ...*tcvectordb.UpsertDocumentParams) (*tcvectordb.UpsertDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.upsertCalls++

	if m.upsertError != nil {
		return nil, m.upsertError
	}

	// Convert docs to []tcvectordb.Document
	docSlice, ok := docs.([]tcvectordb.Document)
	if !ok {
		return nil, errors.New("invalid document type")
	}

	for _, doc := range docSlice {
		m.documents[doc.Id] = doc
	}

	return &tcvectordb.UpsertDocumentResult{AffectedCount: len(docSlice)}, nil
}

func (m *mockClient) Query(ctx context.Context, db, collection string, ids []string, params ...*tcvectordb.QueryDocumentParams) (*tcvectordb.QueryDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queryCalls++

	if m.queryError != nil {
		return nil, m.queryError
	}

	var docs []tcvectordb.Document
	if len(ids) > 0 {
		// Query by specific IDs
		for _, id := range ids {
			if doc, ok := m.documents[id]; ok {
				docs = append(docs, doc)
			}
		}
	} else {
		// Query all documents (for GetMetadata)
		for _, doc := range m.documents {
			docs = append(docs, doc)
		}
	}

	// Apply offset and limit
	offset := 0
	limit := len(docs)
	if len(params) > 0 && params[0] != nil {
		if params[0].Offset > 0 {
			offset = int(params[0].Offset)
		}
		if params[0].Limit > 0 {
			limit = int(params[0].Limit)
		}
	}

	// Apply pagination
	if offset >= len(docs) {
		return &tcvectordb.QueryDocumentResult{
			Documents:     []tcvectordb.Document{},
			AffectedCount: 0,
		}, nil
	}

	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}

	docs = docs[offset:end]

	return &tcvectordb.QueryDocumentResult{
		Documents:     docs,
		AffectedCount: len(docs),
	}, nil
}

func (m *mockClient) Search(ctx context.Context, db, collection string, vectors [][]float32, params ...*tcvectordb.SearchDocumentParams) (*tcvectordb.SearchDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.searchCalls++

	if m.searchError != nil {
		return nil, m.searchError
	}

	// Calculate cosine similarity and sort by score
	var results [][]tcvectordb.Document
	for _, queryVector := range vectors {
		var batch []tcvectordb.Document

		// Calculate similarity scores
		type docWithScore struct {
			doc   tcvectordb.Document
			score float32
		}
		var scored []docWithScore

		for _, doc := range m.documents {
			score := cosineSimilarity(queryVector, doc.Vector)
			docCopy := doc
			docCopy.Score = score
			scored = append(scored, docWithScore{doc: docCopy, score: score})
		}

		// Sort by score descending
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})

		// Apply limit if specified
		limit := len(scored)
		if len(params) > 0 && params[0] != nil && params[0].Limit > 0 {
			limit = int(params[0].Limit)
			if limit > len(scored) {
				limit = len(scored)
			}
		}

		// Apply score threshold if specified
		minScore := float32(0.0)
		if len(params) > 0 && params[0] != nil && params[0].Radius != nil {
			minScore = *params[0].Radius
		}

		for i := 0; i < limit; i++ {
			if scored[i].score >= minScore {
				batch = append(batch, scored[i].doc)
			}
		}

		results = append(results, batch)
	}

	return &tcvectordb.SearchDocumentResult{Documents: results}, nil
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func (m *mockClient) Delete(ctx context.Context, db, collection string, params tcvectordb.DeleteDocumentParams) (*tcvectordb.DeleteDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCalls++

	if m.deleteError != nil {
		return nil, m.deleteError
	}

	count := 0
	for _, id := range params.DocumentIds {
		if _, ok := m.documents[id]; ok {
			delete(m.documents, id)
			count++
		}
	}

	return &tcvectordb.DeleteDocumentResult{AffectedCount: count}, nil
}

func (m *mockClient) Update(ctx context.Context, db, collection string, params tcvectordb.UpdateDocumentParams) (*tcvectordb.UpdateDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateCalls++

	if m.updateError != nil {
		return nil, m.updateError
	}

	// Update existing documents
	// For mock purposes, we just track that update was called
	// In real implementation, this would update the document fields
	affectedCount := 0
	for _, id := range params.QueryIds {
		if _, ok := m.documents[id]; ok {
			affectedCount++
		}
	}

	return &tcvectordb.UpdateDocumentResult{AffectedCount: affectedCount}, nil
}

func (m *mockClient) HybridSearch(ctx context.Context, db, collection string, params tcvectordb.HybridSearchDocumentParams) (*tcvectordb.SearchDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.hybridCalls++

	if m.hybridError != nil {
		return nil, m.hybridError
	}

	// Simple implementation: return all documents
	var batch []tcvectordb.Document
	for _, doc := range m.documents {
		batch = append(batch, doc)
	}

	return &tcvectordb.SearchDocumentResult{Documents: [][]tcvectordb.Document{batch}}, nil
}

func (m *mockClient) FullTextSearch(ctx context.Context, db, collection string, params tcvectordb.FullTextSearchParams) (*tcvectordb.SearchDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return all documents for keyword search in mock
	var batch []tcvectordb.Document
	for _, doc := range m.documents {
		batch = append(batch, doc)
	}

	return &tcvectordb.SearchDocumentResult{Documents: [][]tcvectordb.Document{batch}}, nil
}

func (m *mockClient) Count(ctx context.Context, db, collection string, params ...tcvectordb.CountDocumentParams) (*tcvectordb.CountDocumentResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := len(m.documents)
	return &tcvectordb.CountDocumentResult{Count: uint64(count)}, nil
}

func (m *mockClient) SearchByContents(ctx context.Context, db, collection string, contents []string, params ...*tcvectordb.SearchDocumentParams) (*tcvectordb.SearchDocumentResult, error) {
	// Delegate to Search for simplicity
	return m.Search(ctx, db, collection, nil, params...)
}

func (m *mockClient) RebuildIndex(ctx context.Context, db, collection string, params *tcvectordb.RebuildIndexParams) (*tcvectordb.RebuildIndexResult, error) {
	m.rebuildCalls++

	if m.rebuildError != nil {
		return nil, m.rebuildError
	}

	return &tcvectordb.RebuildIndexResult{}, nil
}

// Close closes the connection to the vector store.
func (m *mockClient) Close() {
	// No-op for mock
}

// Database operations (minimal implementation for testing)

func (m *mockClient) CreateDatabaseIfNotExists(ctx context.Context, dbName string) (*tcvectordb.CreateDatabaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.databases[dbName] = true
	return &tcvectordb.CreateDatabaseResult{}, nil
}

func (m *mockClient) DropDatabase(ctx context.Context, dbName string) (*tcvectordb.DropDatabaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.databases, dbName)
	return &tcvectordb.DropDatabaseResult{}, nil
}

func (m *mockClient) ListDatabases(ctx context.Context) ([]*tcvectordb.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dbs []*tcvectordb.Database
	for name := range m.databases {
		dbs = append(dbs, &tcvectordb.Database{DatabaseName: name})
	}
	return dbs, nil
}

func (m *mockClient) Database(dbName string) *tcvectordb.Database {
	// Return a mock database that has the required methods
	return &tcvectordb.Database{DatabaseName: dbName}
}

// TruncateCollection truncates a collection (for DeleteAll support).
func (m *mockClient) TruncateCollection(ctx context.Context, dbName, collectionName string) (*tcvectordb.TruncateCollectionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all documents
	m.documents = make(map[string]tcvectordb.Document)
	return &tcvectordb.TruncateCollectionResult{}, nil
}

// defaultMockDocBuilder creates a default document builder for mock testing
func defaultMockDocBuilder(opt options) DocBuilderFunc {
	return func(tcDoc tcvectordb.Document) (*document.Document, []float64, error) {
		doc := &document.Document{
			ID: tcDoc.Id,
		}

		// Extract fields
		if nameField, ok := tcDoc.Fields[opt.nameFieldName]; ok {
			if name, ok := nameField.Val.(string); ok {
				doc.Name = name
			}
		}
		if contentField, ok := tcDoc.Fields[opt.contentFieldName]; ok {
			if content, ok := contentField.Val.(string); ok {
				doc.Content = content
			}
		}
		if metadataField, ok := tcDoc.Fields[opt.metadataFieldName]; ok {
			if metadata, ok := metadataField.Val.(map[string]any); ok {
				doc.Metadata = metadata
			}
		}

		// Convert vector from float32 to float64
		embedding := make([]float64, len(tcDoc.Vector))
		for i, v := range tcDoc.Vector {
			embedding[i] = float64(v)
		}

		return doc, embedding, nil
	}
}

// Helper function to create a VectorStore with mock client for testing
func newVectorStoreWithMockClient(mockClient *mockClient, opts ...Option) *VectorStore {
	option := defaultOptions
	// Disable TSVector by default in mock tests to avoid encoder dependency
	option.enableTSVector = false

	for _, opt := range opts {
		opt(&option)
	}

	// Set default docBuilder if not provided
	if option.docBuilder == nil {
		option.docBuilder = defaultMockDocBuilder(option)
	}

	vs := &VectorStore{
		client:          mockClient,
		option:          option,
		filterConverter: &tcVectorConverter{},
		sparseEncoder:   nil, // Will be initialized if enableTSVector is true
	}

	// Initialize sparse encoder if needed
	if option.enableTSVector {
		// For testing, we can skip the sparse encoder initialization
		// or use a mock encoder if needed
		// sparseEncoder, _ := encoder.NewBM25Encoder(&encoder.BM25EncoderParams{Bm25Language: option.language})
		// vs.sparseEncoder = sparseEncoder
	}

	return vs
}

// mockSparseEncoder is a mock implementation of sparseEncoder interface for testing.
type mockSparseEncoder struct{}

// newMockSparseEncoder creates a new mock sparse encoder.
func newMockSparseEncoder() *mockSparseEncoder {
	return &mockSparseEncoder{}
}

// EncodeText encodes a single text into sparse vector format.
func (m *mockSparseEncoder) EncodeText(text string) ([]encoder.SparseVecItem, error) {
	// Return a simple mock sparse vector
	return []encoder.SparseVecItem{
		{TermId: 1, Score: 0.8},
		{TermId: 2, Score: 0.6},
	}, nil
}

// EncodeQuery encodes a single query into sparse vector format.
func (m *mockSparseEncoder) EncodeQuery(query string) ([]encoder.SparseVecItem, error) {
	return m.EncodeText(query)
}

// EncodeQueries encodes multiple queries into sparse vector format.
func (m *mockSparseEncoder) EncodeQueries(queries []string) ([][]encoder.SparseVecItem, error) {
	result := make([][]encoder.SparseVecItem, len(queries))
	for i, query := range queries {
		sparseVec, err := m.EncodeText(query)
		if err != nil {
			return nil, err
		}
		result[i] = sparseVec
	}
	return result, nil
}

// TestVectorStore_GetFilterFieldName tests the getFilterFieldName method.
func TestVectorStore_GetFilterFieldName(t *testing.T) {
	tests := []struct {
		name         string
		filterFields []string
		inputField   string
		expected     string
	}{
		{
			name:         "field_in_filterFields",
			filterFields: []string{"category", "content_type", "topic"},
			inputField:   "content_type",
			expected:     "content_type",
		},
		{
			name:         "field_not_in_filterFields",
			filterFields: []string{"category", "topic"},
			inputField:   "content_type",
			expected:     "metadata.content_type",
		},
		{
			name:         "empty_filterFields",
			filterFields: []string{},
			inputField:   "category",
			expected:     "metadata.category",
		},
		{
			name:         "field_in_default_filterFields",
			filterFields: []string{"uri", "source_name"},
			inputField:   "uri",
			expected:     "uri",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
				WithFilterIndexFields(tt.filterFields),
			)

			result := vs.getFilterFieldName(tt.inputField)
			if result != tt.expected {
				t.Errorf("getFilterFieldName() = %v, want %v", result, tt.expected)
			}
		})
	}
}
