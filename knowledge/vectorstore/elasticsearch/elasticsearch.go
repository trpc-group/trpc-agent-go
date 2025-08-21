//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/typedapi/core/search"
	"github.com/elastic/go-elasticsearch/v9/typedapi/esdsl"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/textquerytype"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Error constants for common error messages.
var (
	errDocumentCannotBeNil          = errors.New("elasticsearch document cannot be nil")
	errDocumentIDCannotBeEmpty      = errors.New("elasticsearch document ID cannot be empty")
	errDocumentNotFound             = errors.New("elasticsearch document not found")
	errEmbeddingVectorCannotBeEmpty = errors.New("elasticsearch embedding vector cannot be empty")
	errEmbeddingNotFound            = errors.New("elasticsearch embedding not found")
	errInvalidDocumentSource        = errors.New("elasticsearch invalid document source")
	errQueryVectorCannotBeEmpty     = errors.New("elasticsearch query vector cannot be empty")
	errSearchQueryCannotBeNil       = errors.New("elasticsearch search query cannot be nil")
)

const (
	// defaultIndexName is the default index name for documents.
	defaultIndexName = "trpc_agent_documents"
	// defaultVectorField is the default field name for embedding vectors.
	defaultVectorField = "embedding"
	// defaultContentField is the default field name for document content.
	defaultContentField = "content"
	// defaultMetadataField is the default field name for document metadata.
	defaultMetadataField = "metadata"
	// defaultScoreThreshold is the default minimum similarity score.
	defaultScoreThreshold = 0.7
	// defaultVectorDimension is the default dimension for embedding vectors.
	defaultVectorDimension = 1536
	// defaultMaxResults is the default maximum number of search results.
	defaultMaxResults = 10
)

// esDocument represents a document in Elasticsearch format using composition.
type esDocument struct {
	*document.Document `json:",inline"`
	Embedding          []float64 `json:"embedding"`
}

// indexMapping defines the Elasticsearch index mapping structure.
type indexMapping struct {
	Mappings indexMappings `json:"mappings"`
	Settings indexSettings `json:"settings"`
}

// indexMappings defines the mappings section of the index.
type indexMappings struct {
	Properties map[string]fieldMapping `json:"properties"`
}

// indexSettings defines the settings section of the index.
type indexSettings struct {
	NumberOfShards   int `json:"number_of_shards"`
	NumberOfReplicas int `json:"number_of_replicas"`
}

// fieldMapping defines a field mapping in Elasticsearch.
type fieldMapping struct {
	Type       string                  `json:"type,omitempty"`
	Dims       int                     `json:"dims,omitempty"`
	Index      bool                    `json:"index,omitempty"`
	Similarity string                  `json:"similarity,omitempty"`
	Dynamic    bool                    `json:"dynamic,omitempty"`
	Fields     map[string]fieldMapping `json:"fields,omitempty"`
}

// VectorStore implements vectorstore.VectorStore interface using Elasticsearch.
type VectorStore struct {
	client *elasticsearch.Client
	option options
}

// New creates a new Elasticsearch vector store with options.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions()
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
	esConfig := elasticsearch.Config{
		Addresses:              option.addresses,
		Username:               option.username,
		Password:               option.password,
		APIKey:                 option.apiKey,
		CertificateFingerprint: option.certificateFingerprint,
		CompressRequestBody:    option.compressRequestBody,
		EnableMetrics:          option.enableMetrics,
		EnableDebugLogger:      option.enableDebugLogger,
		RetryOnStatus:          option.retryOnStatus,
		MaxRetries:             option.maxRetries,
	}

	esClient, err := elasticsearch.NewClient(esConfig)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch create client: %w", err)
	}

	vs := &VectorStore{
		client: esClient,
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
		return err
	}

	if exists {
		return nil
	}

	// Create index with mapping for vector search.
	mapping := &indexMapping{
		Mappings: indexMappings{
			Properties: map[string]fieldMapping{
				"id": {
					Type: "keyword",
				},
				"name": {
					Type: "text",
				},
				"content": {
					Type: "text",
				},
				"metadata": {
					Type:    "object",
					Dynamic: true,
				},
				"created_at": {
					Type: "date",
				},
				"updated_at": {
					Type: "date",
				},
				"embedding": {
					Type:       "dense_vector",
					Dims:       vs.option.vectorDimension,
					Index:      true,
					Similarity: "cosine",
				},
			},
		},
		Settings: indexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
	}

	return vs.createIndex(ctx, vs.option.indexName, mapping)
}

// indexExists checks if an index exists.
func (vs *VectorStore) indexExists(ctx context.Context, indexName string) (bool, error) {
	res, err := vs.client.Indices.Exists(
		[]string{indexName},
		vs.client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()

	return res.StatusCode == 200, nil
}

// createIndex creates an index with mapping.
func (vs *VectorStore) createIndex(ctx context.Context, indexName string, mapping *indexMapping) error {
	mappingBytes, err := json.Marshal(mapping)
	if err != nil {
		return err
	}

	res, err := vs.client.Indices.Create(
		indexName,
		vs.client.Indices.Create.WithContext(ctx),
		vs.client.Indices.Create.WithBody(bytes.NewReader(mappingBytes)),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch failed to create index: %s", res.Status())
	}
	return nil
}

// buildESDocument creates an Elasticsearch document from document.Document and embedding.
func buildESDocument(doc *document.Document, embedding []float64) *esDocument {
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
		return errEmbeddingVectorCannotBeEmpty
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d", len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for indexing using helper function.
	esDoc := buildESDocument(doc, embedding)

	return vs.indexDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// indexDocument indexes a document.
func (vs *VectorStore) indexDocument(ctx context.Context, indexName, id string, document *esDocument) error {
	documentBytes, err := json.Marshal(document)
	if err != nil {
		return err
	}

	res, err := vs.client.Index(
		indexName,
		bytes.NewReader(documentBytes),
		vs.client.Index.WithContext(ctx),
		vs.client.Index.WithDocumentID(id),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch failed to index document: %s", res.Status())
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
		return nil, nil, err
	}

	// Use official GetResult struct for better type safety.
	var response types.GetResult
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, err
	}

	if !response.Found {
		return nil, nil, errDocumentNotFound
	}

	// Parse the _source field using our unified esDocument struct.
	var source esDocument
	if err := json.Unmarshal(response.Source_, &source); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errInvalidDocumentSource, err)
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
		return nil, nil, errEmbeddingNotFound
	}

	return doc, source.Embedding, nil
}

// getDocument retrieves a document by ID.
func (vs *VectorStore) getDocument(ctx context.Context, indexName, id string) ([]byte, error) {
	res, err := vs.client.Get(
		indexName,
		id,
		vs.client.Get.WithContext(ctx),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch failed to get document: %s", res.Status())
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Update modifies an existing document and its embedding.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentCannotBeNil
	}

	if len(embedding) == 0 {
		return errEmbeddingVectorCannotBeEmpty
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d", len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for updating using helper function.
	esDoc := buildESDocument(doc, embedding)

	return vs.updateDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// updateDocument updates a document.
func (vs *VectorStore) updateDocument(ctx context.Context, indexName, id string, document *esDocument) error {
	// For updates, we only want to update specific fields, not the entire document
	updateBody := &esUpdateRequest{
		Doc: map[string]any{
			"name":       document.Name,
			"content":    document.Content,
			"metadata":   document.Metadata,
			"updated_at": document.UpdatedAt,
			"embedding":  document.Embedding,
		},
	}

	updateBytes, err := json.Marshal(updateBody)
	if err != nil {
		return err
	}

	res, err := vs.client.Update(
		indexName,
		id,
		bytes.NewReader(updateBytes),
		vs.client.Update.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch failed to update document: %s", res.Status())
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
	res, err := vs.client.Delete(
		indexName,
		id,
		vs.client.Delete.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch failed to delete document: %s", res.Status())
	}
	return nil
}

// Search performs similarity search and returns the most similar documents.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errSearchQueryCannotBeNil
	}

	if len(query.Vector) == 0 {
		return nil, errQueryVectorCannotBeEmpty
	}

	if len(query.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("elasticsearch query vector dimension %d does not match expected dimension %d", len(query.Vector), vs.option.vectorDimension)
	}

	// Build search query based on search mode.
	var searchQuery *types.SearchRequestBody

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		searchQuery = vs.buildVectorSearchQuery(query)
	case vectorstore.SearchModeKeyword:
		if !vs.option.enableTSVector {
			log.Infof("elasticsearch: keyword search is not supported when enableTSVector is disabled, use vector search instead")
			searchQuery = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery = vs.buildKeywordSearchQuery(query)
		}
	case vectorstore.SearchModeHybrid:
		if !vs.option.enableTSVector {
			log.Infof("elasticsearch: hybrid search is not supported when enableTSVector is disabled, use vector search instead")
			searchQuery = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery = vs.buildHybridSearchQuery(query)
		}
	default:
		searchQuery = vs.buildVectorSearchQuery(query)
	}

	// Execute search.
	data, err := vs.search(ctx, vs.option.indexName, searchQuery)
	if err != nil {
		return nil, err
	}

	// Parse search results.
	return vs.parseSearchResults(data)
}

// search performs a search query.
func (vs *VectorStore) search(ctx context.Context, indexName string, query *types.SearchRequestBody) ([]byte, error) {
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	res, err := vs.client.Search(
		vs.client.Search.WithContext(ctx),
		vs.client.Search.WithIndex(indexName),
		vs.client.Search.WithBody(bytes.NewReader(queryBytes)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	if res.IsError() {
		return nil, fmt.Errorf(
			"elasticsearch search failed: %s: %s",
			res.Status(), string(body),
		)
	}
	return body, nil
}

// esUpdateRequest represents an Elasticsearch update request.
type esUpdateRequest struct {
	Doc map[string]any `json:"doc"`
}

// buildVectorSearchQuery builds a vector similarity search query.
func (vs *VectorStore) buildVectorSearchQuery(query *vectorstore.SearchQuery) *types.SearchRequestBody {
	// Create script for cosine similarity using esdsl.
	script := esdsl.NewScript().
		Source(esdsl.NewScriptSource().String("if (doc['embedding'].size() > 0) { cosineSimilarity(params.query_vector, 'embedding') + 1.0 } else { 0.0 }")).
		AddParam("query_vector", json.RawMessage(fmt.Sprintf("%.6f", query.Vector)))

	// Create match_all query using esdsl.
	matchAllQuery := esdsl.NewMatchAllQuery()

	// Create script_score query using esdsl.
	scriptScoreQuery := esdsl.NewScriptScoreQuery(matchAllQuery, script)

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(scriptScoreQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster()
}

// buildKeywordSearchQuery builds a keyword-based search query.
func (vs *VectorStore) buildKeywordSearchQuery(query *vectorstore.SearchQuery) *types.SearchRequestBody {
	// Create multi_match query using esdsl.
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields("content^2", "name^1.5").
		Type(textquerytype.Bestfields)

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(multiMatchQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster()
}

// buildHybridSearchQuery builds a hybrid search query combining vector and keyword search.
func (vs *VectorStore) buildHybridSearchQuery(query *vectorstore.SearchQuery) *types.SearchRequestBody {
	// Create script for vector similarity.
	script := esdsl.NewScript().
		Source(esdsl.NewScriptSource().String("if (doc['embedding'].size() > 0) { cosineSimilarity(params.query_vector, 'embedding') + 1.0 } else { 0.0 }")).
		AddParam("query_vector", json.RawMessage(fmt.Sprintf("%.6f", query.Vector)))

	// Create match_all query for script_score.
	matchAllQuery := esdsl.NewMatchAllQuery()

	// Create script_score query.
	scriptScoreQuery := esdsl.NewScriptScoreQuery(matchAllQuery, script)

	// Create multi_match query.
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields("content^2", "name^1.5").
		Type(textquerytype.Bestfields)

	// Combine queries using bool query.
	boolQuery := esdsl.NewBoolQuery().
		Should(scriptScoreQuery, multiMatchQuery).
		MinimumShouldMatch(esdsl.NewMinimumShouldMatch().Int(1))

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(boolQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster()
}

// buildFilterQuery builds a filter query for search results.
func (vs *VectorStore) buildFilterQuery(filter *vectorstore.SearchFilter) types.QueryVariant {
	var filters []types.QueryVariant

	// Filter by document IDs.
	if len(filter.IDs) > 0 {
		termsQuery := esdsl.NewTermsQuery()
		fieldValues := make([]types.FieldValueVariant, len(filter.IDs))
		for i, id := range filter.IDs {
			fieldValues[i] = esdsl.NewFieldValue().String(id)
		}
		termsQuery.AddTermsQuery("id", esdsl.NewTermsQueryField().FieldValues(fieldValues...))
		filters = append(filters, termsQuery)
	}

	// Filter by metadata.
	for key, value := range filter.Metadata {
		termQuery := esdsl.NewTermQuery(fmt.Sprintf("metadata.%s", key), esdsl.NewFieldValue().String(fmt.Sprintf("%v", value)))
		filters = append(filters, termQuery)
	}

	if len(filters) == 0 {
		return nil
	}

	boolQuery := esdsl.NewBoolQuery().Filter(filters...)
	return boolQuery
}

// parseSearchResults parses Elasticsearch search response.
func (vs *VectorStore) parseSearchResults(data []byte) (*vectorstore.SearchResult, error) {
	// Use official SearchResponse struct for better type safety.
	var response search.Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	results := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(response.Hits.Hits)),
	}

	for _, hit := range response.Hits.Hits {
		// Skip hits without score.
		if hit.Score_ == nil {
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
