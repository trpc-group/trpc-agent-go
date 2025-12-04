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
	"time"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/tcvector"
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

	// Additional error simulation for specific scenarios
	existsCollectionError   error
	createCollectionError   error
	describeCollectionError error

	// Track the collection interface for parameter validation
	lastCollectionInterface *mockCollectionInterface
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

func (m *mockClient) Upsert(ctx context.Context, db, collection string, docs any, params ...*tcvectordb.UpsertDocumentParams) (*tcvectordb.UpsertDocumentResult, error) {
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

	return m.searchByVectors(vectors, params...), nil
}

// SearchByText simulates text-based search with remote embedding
// For testing purposes, we generate a simple vector from the text hash
func (m *mockClient) SearchByText(ctx context.Context, db, collection string, textMap map[string][]string, params ...*tcvectordb.SearchDocumentParams) (*tcvectordb.SearchDocumentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.searchCalls++

	if m.searchError != nil {
		return nil, m.searchError
	}

	// For testing, generate a simple vector from text
	// In real implementation, this would be done by the server
	var queryVectors [][]float32
	for _, texts := range textMap {
		for _, text := range texts {
			// Simple hash-based vector generation for testing
			vector := make([]float32, 3) // Assuming dimension 3 for tests
			for i, c := range text {
				vector[i%3] += float32(c) / 1000.0
			}
			queryVectors = append(queryVectors, vector)
		}
	}

	return m.searchByVectors(queryVectors, params...), nil
}

// searchByVectors is a helper method to perform vector search with similarity calculation
func (m *mockClient) searchByVectors(vectors [][]float32, params ...*tcvectordb.SearchDocumentParams) *tcvectordb.SearchDocumentResult {
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

	return &tcvectordb.SearchDocumentResult{Documents: results}
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
	// Return a mock database with our custom collection interface
	mockColInterface := &mockCollectionInterface{client: m}
	m.lastCollectionInterface = mockColInterface
	return &tcvectordb.Database{
		DatabaseName:        dbName,
		CollectionInterface: mockColInterface,
	}
}

// mockCollectionInterface implements tcvectordb.CollectionInterface for error injection.
type mockCollectionInterface struct {
	client *mockClient

	// Capture CreateCollectionParams for validation
	lastCreateParams *tcvectordb.CreateCollectionParams

	// Mock collection description for testing checkIndexes
	describeCollectionResult *tcvectordb.DescribeCollectionResult
}

func (m *mockCollectionInterface) ExistsCollection(ctx context.Context, collectionName string) (bool, error) {
	if m.client.existsCollectionError != nil {
		return false, m.client.existsCollectionError
	}

	m.client.mu.RLock()
	defer m.client.mu.RUnlock()

	// Check if collection exists in our tracking map
	if collections, ok := m.client.collections[""]; ok { // Using empty string as default db
		return collections[collectionName], nil
	}
	return false, nil
}

func (m *mockCollectionInterface) CreateCollectionIfNotExists(
	ctx context.Context,
	collectionName string,
	shardNum, replicaNum uint32,
	description string,
	index tcvectordb.Indexes,
	params ...*tcvectordb.CreateCollectionParams,
) (*tcvectordb.Collection, error) {
	if m.client.createCollectionError != nil {
		return nil, m.client.createCollectionError
	}

	m.client.mu.Lock()
	defer m.client.mu.Unlock()

	// Capture the params for validation
	if len(params) > 0 && params[0] != nil {
		m.lastCreateParams = params[0]
	}

	// Track the collection creation
	if m.client.collections[""] == nil {
		m.client.collections[""] = make(map[string]bool)
	}
	m.client.collections[""][collectionName] = true

	return &tcvectordb.Collection{}, nil
}

func (m *mockCollectionInterface) DescribeCollection(ctx context.Context, collectionName string) (*tcvectordb.DescribeCollectionResult, error) {
	if m.client.describeCollectionError != nil {
		return nil, m.client.describeCollectionError
	}
	// Return the configured collection description, or a basic one
	if m.describeCollectionResult != nil {
		return m.describeCollectionResult, nil
	}
	// Return a basic collection description with vector index
	return createMockCollectionDesc(true, false, []string{"id", "created_at", "metadata"}), nil
}

// Implement other required methods with no-op or basic implementations
func (m *mockCollectionInterface) CreateCollection(ctx context.Context, name string, shardNum, replicaNum uint32, description string, index tcvectordb.Indexes, params ...*tcvectordb.CreateCollectionParams) (*tcvectordb.Collection, error) {
	return m.CreateCollectionIfNotExists(ctx, name, shardNum, replicaNum, description, index, params...)
}

func (m *mockCollectionInterface) ListCollection(ctx context.Context) (*tcvectordb.ListCollectionResult, error) {
	return &tcvectordb.ListCollectionResult{}, nil
}

func (m *mockCollectionInterface) DropCollection(ctx context.Context, collectionName string) (*tcvectordb.DropCollectionResult, error) {
	return &tcvectordb.DropCollectionResult{}, nil
}

func (m *mockCollectionInterface) TruncateCollection(ctx context.Context, collectionName string) (*tcvectordb.TruncateCollectionResult, error) {
	return &tcvectordb.TruncateCollectionResult{}, nil
}

func (m *mockCollectionInterface) Collection(name string) *tcvectordb.Collection {
	return &tcvectordb.Collection{
		CollectionName: name,
		IndexInterface: &mockIndexInterface{},
	}
}

// mockIndexInterface implements tcvectordb.IndexInterface for testing.
type mockIndexInterface struct{}

func (m *mockIndexInterface) AddIndex(ctx context.Context, params ...*tcvectordb.AddIndexParams) error {
	// No-op for testing
	return nil
}

func (m *mockIndexInterface) DropIndex(ctx context.Context, params tcvectordb.DropIndexParams) error {
	return nil
}

func (m *mockIndexInterface) ModifyVectorIndex(ctx context.Context, param tcvectordb.ModifyVectorIndexParam) error {
	return nil
}

func (m *mockIndexInterface) RebuildIndex(ctx context.Context, params ...*tcvectordb.RebuildIndexParams) (*tcvectordb.RebuildIndexResult, error) {
	return &tcvectordb.RebuildIndexResult{}, nil
}

func (m *mockIndexInterface) Debug(v bool) {}

func (m *mockIndexInterface) WithTimeout(t time.Duration) {}

func (m *mockIndexInterface) Close() {}

func (m *mockIndexInterface) Options() tcvectordb.ClientOption {
	return tcvectordb.ClientOption{}
}

func (m *mockIndexInterface) Request(ctx context.Context, req, res any) error {
	return nil
}

func (m *mockCollectionInterface) Debug(v bool) {}

func (m *mockCollectionInterface) WithTimeout(t time.Duration) {}

func (m *mockCollectionInterface) Close() {}

func (m *mockCollectionInterface) Options() tcvectordb.ClientOption {
	return tcvectordb.ClientOption{}
}

func (m *mockCollectionInterface) Request(ctx context.Context, req, res any) error {
	return nil
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

// Test_initVectorDB tests the initVectorDB function error handling.
func Test_initVectorDB(t *testing.T) {
	baseOptions := options{
		database:              "test_db",
		collection:            "test_collection",
		indexDimension:        128,
		sharding:              1,
		replicas:              0,
		idFieldName:           "id",
		contentFieldName:      "content",
		embeddingFieldName:    "vector",
		metadataFieldName:     "metadata",
		createdAtFieldName:    "created_at",
		sparseVectorFieldName: "sparse_vector",
		filterIndexes:         []tcvectordb.FilterIndex{},
	}

	tests := []struct {
		name        string
		setupClient func() storage.ClientInterface
		options     options
		wantErr     bool
		errContains string
	}{
		{
			name: "create_database_error",
			setupClient: func() storage.ClientInterface {
				return &mockClientWithDBError{
					mockClient:          newMockClient(),
					createDatabaseError: errors.New("database creation failed"),
				}
			},
			options:     baseOptions,
			wantErr:     true,
			errContains: "database creation failed",
		},
		{
			name: "database_not_found",
			setupClient: func() storage.ClientInterface {
				return &mockClientWithDBError{
					mockClient:  newMockClient(),
					returnNilDB: true,
				}
			},
			options:     baseOptions,
			wantErr:     true,
			errContains: "not found",
		},
		{
			name: "check_collection_exists_error",
			setupClient: func() storage.ClientInterface {
				return &mockClientWithCollectionError{
					mockClient:            newMockClient(),
					existsCollectionError: errors.New("check exists failed"),
				}
			},
			options:     baseOptions,
			wantErr:     true,
			errContains: "check collection exists",
		},
		{
			name: "collection_already_exists",
			setupClient: func() storage.ClientInterface {
				client := newMockClient()
				client.databases["test_db"] = true
				if client.collections["test_db"] == nil {
					client.collections["test_db"] = make(map[string]bool)
				}
				client.collections["test_db"]["test_collection"] = true
				return client
			},
			options: baseOptions,
			wantErr: false,
		},
		{
			name: "create_collection_with_enableTSVector",
			setupClient: func() storage.ClientInterface {
				return newMockClient()
			},
			options: options{
				database:              "test_db",
				collection:            "test_collection",
				indexDimension:        128,
				sharding:              1,
				replicas:              0,
				idFieldName:           "id",
				contentFieldName:      "content",
				embeddingFieldName:    "vector",
				metadataFieldName:     "metadata",
				createdAtFieldName:    "created_at",
				sparseVectorFieldName: "sparse_vector",
				filterIndexes:         []tcvectordb.FilterIndex{},
				enableTSVector:        true,
			},
			wantErr: false,
		},
		{
			name: "create_collection_error",
			setupClient: func() storage.ClientInterface {
				return &mockClientWithCollectionError{
					mockClient:            newMockClient(),
					createCollectionError: errors.New("create collection failed"),
				}
			},
			options:     baseOptions,
			wantErr:     true,
			errContains: "create collection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.setupClient()
			err := initVectorDB(client, tt.options)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error = %v", err)
				}
			}
		})
	}
}

// mockClientWithDBError extends mockClient to simulate database-level errors.
type mockClientWithDBError struct {
	*mockClient
	createDatabaseError error
	returnNilDB         bool
}

// CreateDatabaseIfNotExists simulates database creation with potential errors.
func (m *mockClientWithDBError) CreateDatabaseIfNotExists(ctx context.Context, dbName string) (*tcvectordb.CreateDatabaseResult, error) {
	if m.createDatabaseError != nil {
		return nil, m.createDatabaseError
	}
	return m.mockClient.CreateDatabaseIfNotExists(ctx, dbName)
}

// Database returns nil when configured to simulate database not found.
func (m *mockClientWithDBError) Database(dbName string) *tcvectordb.Database {
	if m.returnNilDB {
		return nil
	}
	return m.mockClient.Database(dbName)
}

// mockClientWithCollectionError extends mockClient to simulate collection-level errors.
type mockClientWithCollectionError struct {
	*mockClient
	existsCollectionError error
	createCollectionError error
}

// CreateDatabaseIfNotExists for mockClientWithCollectionError.
func (m *mockClientWithCollectionError) CreateDatabaseIfNotExists(ctx context.Context, dbName string) (*tcvectordb.CreateDatabaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.databases[dbName] = true
	return &tcvectordb.CreateDatabaseResult{}, nil
}

// Database returns a mock database - we set errors on the mockClient level.
func (m *mockClientWithCollectionError) Database(dbName string) *tcvectordb.Database {
	// Set the error flags on the embedded mockClient so that collection operations will fail
	m.mockClient.existsCollectionError = m.existsCollectionError
	m.mockClient.createCollectionError = m.createCollectionError
	return m.mockClient.Database(dbName)
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test_initVectorDB_CreateCollectionParams tests CreateCollectionParams configuration.
func Test_initVectorDB_CreateCollectionParams(t *testing.T) {
	tests := []struct {
		name               string
		embeddingModel     string
		filterAll          bool
		expectEmbedding    bool
		expectFilterConfig bool
		expectedModelName  string
	}{
		{
			name:               "no_remote_embedding_no_filterAll",
			embeddingModel:     "",
			filterAll:          false,
			expectEmbedding:    false,
			expectFilterConfig: false,
		},
		{
			name:               "with_remote_embedding_bge-base-zh",
			embeddingModel:     "bge-base-zh",
			filterAll:          false,
			expectEmbedding:    true,
			expectFilterConfig: false,
			expectedModelName:  "bge-base-zh",
		},
		{
			name:               "with_remote_embedding_m3e-base",
			embeddingModel:     "m3e-base",
			filterAll:          false,
			expectEmbedding:    true,
			expectFilterConfig: false,
			expectedModelName:  "m3e-base",
		},
		{
			name:               "with_filterAll_only",
			embeddingModel:     "",
			filterAll:          true,
			expectEmbedding:    false,
			expectFilterConfig: true,
		},
		{
			name:               "with_both_remote_embedding_and_filterAll",
			embeddingModel:     "text-embedding-ada-002",
			filterAll:          true,
			expectEmbedding:    true,
			expectFilterConfig: true,
			expectedModelName:  "text-embedding-ada-002",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := newMockClient()

			// Prepare options
			opts := options{
				database:           "test_db",
				collection:         "test_collection",
				indexDimension:     128,
				embeddingModel:     tt.embeddingModel,
				filterAll:          tt.filterAll,
				contentFieldName:   "content",
				embeddingFieldName: "vector",
				metadataFieldName:  "metadata",
				idFieldName:        "id",
				createdAtFieldName: "created_at",
				sharding:           1,
				replicas:           0,
				filterIndexes:      []tcvectordb.FilterIndex{},
			}

			// Call initVectorDB
			err := initVectorDB(mockClient, opts)
			if err != nil {
				t.Fatalf("initVectorDB failed: %v", err)
			}

			// Get the captured CreateCollectionParams
			if mockClient.lastCollectionInterface == nil {
				t.Fatal("lastCollectionInterface is nil")
			}

			createParams := mockClient.lastCollectionInterface.lastCreateParams

			// Verify embedding configuration
			if tt.expectEmbedding {
				if createParams == nil || createParams.Embedding == nil {
					t.Error("Expected Embedding config but got nil")
				} else {
					if createParams.Embedding.ModelName != tt.expectedModelName {
						t.Errorf("ModelName = %v, want %v", createParams.Embedding.ModelName, tt.expectedModelName)
					}
					if createParams.Embedding.Field != opts.contentFieldName {
						t.Errorf("Embedding.Field = %v, want %v", createParams.Embedding.Field, opts.contentFieldName)
					}
					if createParams.Embedding.VectorField != opts.embeddingFieldName {
						t.Errorf("Embedding.VectorField = %v, want %v", createParams.Embedding.VectorField, opts.embeddingFieldName)
					}
				}
			} else {
				if createParams != nil && createParams.Embedding != nil {
					t.Error("Expected no Embedding config but got one")
				}
			}

			// Verify filterAll configuration
			if tt.expectFilterConfig {
				if createParams == nil || createParams.FilterIndexConfig == nil {
					t.Error("Expected FilterIndexConfig but got nil")
				} else if !createParams.FilterIndexConfig.FilterAll {
					t.Error("Expected FilterAll to be true")
				}
			} else {
				if createParams != nil && createParams.FilterIndexConfig != nil && createParams.FilterIndexConfig.FilterAll {
					t.Error("Expected no FilterIndexConfig or FilterAll=false")
				}
			}
		})
	}
}

// Test_checkIndexes_FilterAll tests the filterAll skip logic in checkIndexes.
func Test_checkIndexes_FilterAll(t *testing.T) {
	tests := []struct {
		name            string
		filterAll       bool
		enableTSVector  bool
		existingIndexes *tcvectordb.DescribeCollectionResult
		expectError     bool
	}{
		{
			name:            "filterAll_enabled_skips_filter_index_validation",
			filterAll:       true,
			enableTSVector:  false,
			existingIndexes: createMockCollectionDesc(true, false, []string{}),
			expectError:     false,
		},
		{
			name:            "filterAll_disabled_validates_filter_indexes",
			filterAll:       false,
			enableTSVector:  false,
			existingIndexes: createMockCollectionDesc(true, false, []string{"id", "created_at", "metadata"}),
			expectError:     false,
		},
		{
			name:            "filterAll_with_tsvector_enabled",
			filterAll:       true,
			enableTSVector:  true,
			existingIndexes: createMockCollectionDesc(true, true, []string{}),
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := newMockClient()

			// Get database and set up the collection description
			db := mockClient.Database("test_db")
			if mockColInterface, ok := db.CollectionInterface.(*mockCollectionInterface); ok {
				mockColInterface.describeCollectionResult = tt.existingIndexes
			}

			// Mark collection as existing
			mockClient.collections[""] = make(map[string]bool)
			mockClient.collections[""]["test_collection"] = true

			// Prepare options
			opts := options{
				database:              "test_db",
				collection:            "test_collection",
				filterAll:             tt.filterAll,
				enableTSVector:        tt.enableTSVector,
				embeddingFieldName:    "vector",
				sparseVectorFieldName: "sparse_vector",
				idFieldName:           "id",
				createdAtFieldName:    "created_at",
				metadataFieldName:     "metadata",
				filterIndexes: []tcvectordb.FilterIndex{
					{FieldName: "new_field", FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
				},
			}

			// Call checkIndexes
			err := checkIndexes(db, opts)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// createMockCollectionDesc creates a mock DescribeCollectionResult for testing.
func createMockCollectionDesc(hasVectorIndex, hasSparseIndex bool, filterFieldNames []string) *tcvectordb.DescribeCollectionResult {
	indexes := tcvectordb.Indexes{
		VectorIndex:       []tcvectordb.VectorIndex{},
		FilterIndex:       []tcvectordb.FilterIndex{},
		SparseVectorIndex: []tcvectordb.SparseVectorIndex{},
	}

	if hasVectorIndex {
		indexes.VectorIndex = append(indexes.VectorIndex, tcvectordb.VectorIndex{
			FilterIndex: tcvectordb.FilterIndex{
				FieldName: "vector",
				FieldType: tcvectordb.Vector,
				IndexType: tcvectordb.HNSW,
			},
			MetricType: tcvectordb.COSINE,
			Dimension:  128,
			Params: &tcvectordb.HNSWParam{
				M:              32,
				EfConstruction: 400,
			},
		})
	}

	if hasSparseIndex {
		indexes.SparseVectorIndex = append(indexes.SparseVectorIndex, tcvectordb.SparseVectorIndex{
			FieldName:  "sparse_vector",
			FieldType:  tcvectordb.SparseVector,
			IndexType:  tcvectordb.SPARSE_INVERTED,
			MetricType: tcvectordb.IP,
		})
	}

	for _, fieldName := range filterFieldNames {
		indexes.FilterIndex = append(indexes.FilterIndex, tcvectordb.FilterIndex{
			FieldName: fieldName,
			FieldType: tcvectordb.String,
			IndexType: tcvectordb.FILTER,
		})
	}

	result := &tcvectordb.DescribeCollectionResult{}
	result.Indexes = indexes
	result.IndexInterface = &mockIndexInterface{}
	return result
}
