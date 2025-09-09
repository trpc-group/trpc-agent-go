//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/elastic/go-elasticsearch/v9/typedapi/core/search"
	"github.com/elastic/go-elasticsearch/v9/typedapi/core/update"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/densevectorsimilarity"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/dynamicmapping"

	istorage "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

var (
	// errDocumentCannotBeNil is the error when the document is nil.
	errDocumentCannotBeNil = errors.New("elasticsearch document cannot be nil")
	// errDocumentIDCannotBeEmpty is the error when the document ID is empty.
	errDocumentIDCannotBeEmpty = errors.New("elasticsearch document ID cannot be empty")
)

const (
	// defaultIndexName is the default index name for documents.
	defaultIndexName = "trpc_agent_documents"
	// defaultScoreThreshold is the default minimum similarity score.
	defaultScoreThreshold = 0.7
	// defaultVectorDimension is the default dimension for embedding vectors.
	defaultVectorDimension = 1536
	// defaultMaxResults is the default maximum number of search results.
	defaultMaxResults = 10
)

// Elasticsearch field name constants.
const (
	defaultFieldMetadata  = "metadata"
	defaultFieldCreatedAt = "created_at"
	defaultFieldUpdatedAt = "updated_at"
)

// esDocument represents a document in Elasticsearch format using composition.
type esDocument struct {
	*document.Document `json:",inline"`
	Embedding          []float64 `json:"embedding"`
}

// esUpdateDoc represents the typed partial update body for a document.
type esUpdateDoc struct {
	Name      string         `json:"name"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata"`
	UpdatedAt time.Time      `json:"updated_at"`
	Embedding []float64      `json:"embedding"`
}

// indexCreateBody is a lightweight helper used to marshal typed mappings and settings.
type indexCreateBody struct {
	Mappings *types.TypeMapping   `json:"mappings,omitempty"`
	Settings *types.IndexSettings `json:"settings,omitempty"`
}

// VectorStore implements vectorstore.VectorStore interface using Elasticsearch.
type VectorStore struct {
	client istorage.Client
	option options
}

// New creates a new Elasticsearch vector store with options.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}

	if option.indexName == "" {
		option.indexName = defaultIndexName
	}

	if option.vectorDimension == 0 {
		option.vectorDimension = defaultVectorDimension
	}

	// Create Elasticsearch client configuration.
	esClient, err := storage.GetClientBuilder()(
		storage.WithAddresses(option.addresses),
		storage.WithUsername(option.username),
		storage.WithPassword(option.password),
		storage.WithAPIKey(option.apiKey),
		storage.WithCertificateFingerprint(option.certificateFingerprint),
		storage.WithCompressRequestBody(option.compressRequestBody),
		storage.WithEnableMetrics(option.enableMetrics),
		storage.WithEnableDebugLogger(option.enableDebugLogger),
		storage.WithRetryOnStatus(option.retryOnStatus),
		storage.WithMaxRetries(option.maxRetries),
		storage.WithVersion(option.version),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch create client: %w", err)
	}

	// Wrap the generic Elasticsearch SDK client with our storage interface.
	// This creates a client that implements istorage.Client from the raw SDK client.
	client, err := storage.WrapSDKClient(esClient)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch new client: %w", err)
	}

	vs := &VectorStore{
		client: client,
		option: option,
	}

	// Ensure index exists with proper mapping.
	if err := vs.ensureIndex(); err != nil {
		return nil, fmt.Errorf("elasticsearch ensure index: %w", err)
	}

	return vs, nil
}

// ensureIndex ensures the Elasticsearch index exists with proper mapping.
func (vs *VectorStore) ensureIndex() error {
	ctx := context.Background()

	exists, err := vs.indexExists(ctx, vs.option.indexName)
	if err != nil {
		return fmt.Errorf("elasticsearch index exists: %w", err)
	}

	if exists {
		return nil
	}

	body := vs.buildIndexCreateBody()
	return vs.createIndex(ctx, vs.option.indexName, body)
}

// buildIndexCreateBody constructs the typed mappings and settings for index creation.
func (vs *VectorStore) buildIndexCreateBody() *indexCreateBody {
	// Create index with mapping for vector search using official typed types.
	tm := types.NewTypeMapping()
	tm.Properties = make(map[string]types.Property)

	// id: keyword
	tm.Properties[vs.option.idFieldName] = types.NewKeywordProperty()
	// name/content: text
	tm.Properties[vs.option.nameFieldName] = types.NewTextProperty()
	contentField := vs.option.contentFieldName
	tm.Properties[contentField] = types.NewTextProperty()
	// metadata: object with dynamic true
	metaObj := types.NewObjectProperty()
	dm := dynamicmapping.True
	metaObj.Dynamic = &dm
	tm.Properties[defaultFieldMetadata] = metaObj
	// created_at / updated_at: date
	tm.Properties[defaultFieldCreatedAt] = types.NewDateProperty()
	tm.Properties[defaultFieldUpdatedAt] = types.NewDateProperty()
	// embedding: dense_vector with dims, index, similarity
	dv := types.NewDenseVectorProperty()
	dims := vs.option.vectorDimension
	dv.Dims = &dims
	indexed := true
	dv.Index = &indexed
	sim := densevectorsimilarity.Cosine
	dv.Similarity = &sim
	embeddingField := vs.option.embeddingFieldName
	tm.Properties[embeddingField] = dv

	// Settings: shards/replicas are strings in IndexSettings
	is := types.NewIndexSettings()
	shards := "1"
	replicas := "0"
	is.NumberOfShards = &shards
	is.NumberOfReplicas = &replicas

	return &indexCreateBody{
		Mappings: tm,
		Settings: is,
	}
}

// indexExists checks if an index exists.
func (vs *VectorStore) indexExists(ctx context.Context, indexName string) (bool, error) {
	ok, err := vs.client.IndexExists(ctx, indexName)
	if err != nil {
		return false, fmt.Errorf("elasticsearch index exists: %w", err)
	}
	return ok, nil
}

// createIndex creates an index with mapping.
func (vs *VectorStore) createIndex(ctx context.Context, indexName string, body *indexCreateBody) error {
	mappingBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal index create body: %w", err)
	}
	if err := vs.client.CreateIndex(ctx, indexName, mappingBytes); err != nil {
		return fmt.Errorf("elasticsearch create index: %w", err)
	}
	return nil
}

// newESDocument creates an Elasticsearch document from document.Document and embedding.
func newESDocument(doc *document.Document, embedding []float64) *esDocument {
	return &esDocument{
		Document:  doc,
		Embedding: embedding,
	}
}

// Add stores a document with its embedding vector.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentCannotBeNil
	}

	if len(embedding) == 0 {
		return fmt.Errorf("elasticsearch embedding vector cannot be empty for %s", doc.ID)
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d",
			len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for indexing using helper function.
	esDoc := newESDocument(doc, embedding)

	return vs.indexDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// indexDocument indexes a document.
func (vs *VectorStore) indexDocument(ctx context.Context, indexName, id string, document *esDocument) error {
	documentBytes, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal index document: %w", err)
	}
	if err := vs.client.IndexDoc(ctx, indexName, id, documentBytes); err != nil {
		return fmt.Errorf("elasticsearch index document: %w", err)
	}
	return nil
}

// Get retrieves a document by ID along with its embedding.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocumentIDCannotBeEmpty
	}

	data, err := vs.getDocument(ctx, vs.option.indexName, id)
	if err != nil {
		return nil, nil, fmt.Errorf("elasticsearch get document: %w", err)
	}

	// Use official GetResult struct for better type safety.
	var response types.GetResult
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, fmt.Errorf("elasticsearch unmarshal get document: %w", err)
	}

	if !response.Found {
		return nil, nil, fmt.Errorf("elasticsearch document not found: %s", id)
	}

	// Parse the _source field using our unified esDocument struct.
	var source esDocument
	if err := json.Unmarshal(response.Source_, &source); err != nil {
		return nil, nil, fmt.Errorf("elasticsearch invalid document source: %w", err)
	}

	// Extract document fields.
	doc := &document.Document{
		ID:        source.ID,
		Name:      source.Name,
		Content:   source.Content,
		CreatedAt: source.CreatedAt,
		UpdatedAt: source.UpdatedAt,
	}

	// Extract metadata.
	if source.Metadata != nil {
		doc.Metadata = source.Metadata
	}

	// Extract embedding vector.
	if len(source.Embedding) == 0 {
		return nil, nil, fmt.Errorf("elasticsearch embedding vector not found: %s", id)
	}

	return doc, source.Embedding, nil
}

// getDocument retrieves a document by ID.
func (vs *VectorStore) getDocument(ctx context.Context, indexName, id string) ([]byte, error) {
	body, err := vs.client.GetDoc(ctx, indexName, id)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch get document: %w", err)
	}
	return body, nil
}

// Update modifies an existing document and its embedding.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentCannotBeNil
	}

	if len(embedding) == 0 {
		return fmt.Errorf("elasticsearch embedding vector cannot be empty for %s", doc.ID)
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d",
			len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for updating using helper function.
	esDoc := newESDocument(doc, embedding)

	return vs.updateDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// updateDocument updates a document.
func (vs *VectorStore) updateDocument(ctx context.Context, indexName, id string, document *esDocument) error {
	updateDoc := esUpdateDoc{
		Name:      document.Name,
		Content:   document.Content,
		Metadata:  document.Metadata,
		UpdatedAt: document.UpdatedAt,
		Embedding: document.Embedding,
	}

	// Marshal the update document to JSON.
	docBytes, err := json.Marshal(updateDoc)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal update document: %w", err)
	}

	// Use official update.Request type.
	updateReq := update.NewRequest()
	updateReq.Doc = docBytes

	// Marshal the complete update request.
	updateBytes, err := json.Marshal(updateReq)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal update request: %w", err)
	}

	if err := vs.client.UpdateDoc(ctx, indexName, id, updateBytes); err != nil {
		return fmt.Errorf("elasticsearch update document: %w", err)
	}
	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocumentIDCannotBeEmpty
	}

	return vs.deleteDocument(ctx, vs.option.indexName, id)
}

// deleteDocument deletes a document.
func (vs *VectorStore) deleteDocument(ctx context.Context, indexName, id string) error {
	if err := vs.client.DeleteDoc(ctx, indexName, id); err != nil {
		return fmt.Errorf("elasticsearch delete document: %w", err)
	}
	return nil
}

// Search performs similarity search and returns the most similar documents.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errors.New("elasticsearch search query cannot be nil")
	}

	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("elasticsearch query vector cannot be empty for %s", query.Query)
	}

	if len(query.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("elasticsearch query vector dimension %d does not match expected dimension %d", len(query.Vector), vs.option.vectorDimension)
	}

	// Build search query based on search mode.
	var searchQuery *types.SearchRequestBody
	var err error

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		searchQuery, err = vs.buildVectorSearchQuery(query)
	case vectorstore.SearchModeKeyword:
		if !vs.option.enableTSVector {
			log.Infof("elasticsearch: keyword search is not supported when enableTSVector is disabled, use vector search instead")
			searchQuery, err = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery, err = vs.buildKeywordSearchQuery(query)
		}
	case vectorstore.SearchModeHybrid:
		if !vs.option.enableTSVector {
			log.Infof("elasticsearch: hybrid search is not supported when enableTSVector is disabled, use vector search instead")
			searchQuery, err = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery, err = vs.buildHybridSearchQuery(query)
		}
	default:
		searchQuery, err = vs.buildVectorSearchQuery(query)
	}

	if err != nil {
		return nil, fmt.Errorf("elasticsearch build search query: %w", err)
	}

	// Execute search.
	data, err := vs.search(ctx, vs.option.indexName, searchQuery)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}

	// Parse search results.
	return vs.parseSearchResults(data)
}

// search performs a search query.
func (vs *VectorStore) search(ctx context.Context, indexName string, query *types.SearchRequestBody) ([]byte, error) {
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch marshal search query: %w", err)
	}
	body, err := vs.client.Search(ctx, indexName, queryBytes)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}
	return body, nil
}

// parseSearchResults parses Elasticsearch search response.
func (vs *VectorStore) parseSearchResults(data []byte) (*vectorstore.SearchResult, error) {
	// Use official SearchResponse struct for better type safety.
	var response search.Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("elasticsearch unmarshal search response: %w", err)
	}

	results := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0),
	}

	// Guard against empty hits (e.g., minimal/mocked responses).
	if len(response.Hits.Hits) == 0 {
		return results, nil
	}

	for _, hit := range response.Hits.Hits {
		// Skip hits without score.
		if hit.Score_ == nil {
			continue
		}

		// Skip hits without _source payload.
		if len(hit.Source_) == 0 {
			continue
		}

		score := float64(*hit.Score_)

		// Check score threshold.
		if score < vs.option.scoreThreshold {
			continue
		}

		// Parse the _source field using our unified esDocument struct.
		var source esDocument
		if err := json.Unmarshal(hit.Source_, &source); err != nil {
			continue // Skip invalid documents
		}

		// Create document.
		doc := &document.Document{
			ID:        source.ID,
			Name:      source.Name,
			Content:   source.Content,
			CreatedAt: source.CreatedAt,
			UpdatedAt: source.UpdatedAt,
		}

		// Extract metadata.
		if source.Metadata != nil {
			doc.Metadata = source.Metadata
		}

		scoredDoc := &vectorstore.ScoredDocument{
			Document: doc,
			Score:    score,
		}

		results.Results = append(results.Results, scoredDoc)
	}

	return results, nil
}

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	// Elasticsearch client doesn't need explicit close.
	return nil
}
