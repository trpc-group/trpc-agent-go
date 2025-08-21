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
	"fmt"
	"io"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	// DefaultIndexName is the default index name for documents.
	DefaultIndexName = "trpc_agent_documents"
	// DefaultVectorField is the default field name for embedding vectors.
	DefaultVectorField = "embedding"
	// DefaultContentField is the default field name for document content.
	DefaultContentField = "content"
	// DefaultMetadataField is the default field name for document metadata.
	DefaultMetadataField = "metadata"
	// DefaultScoreThreshold is the default minimum similarity score.
	DefaultScoreThreshold = 0.7
	// DefaultVectorDimension is the default dimension for embedding vectors.
	DefaultVectorDimension = 1536
	// DefaultMaxResults is the default maximum number of search results.
	DefaultMaxResults = 10
)

// IndexMapping defines the Elasticsearch index mapping structure.
type IndexMapping struct {
	Mappings IndexMappings `json:"mappings"`
	Settings IndexSettings `json:"settings"`
}

// IndexMappings defines the mappings section of the index.
type IndexMappings struct {
	Properties map[string]FieldMapping `json:"properties"`
}

// IndexSettings defines the settings section of the index.
type IndexSettings struct {
	NumberOfShards   int `json:"number_of_shards"`
	NumberOfReplicas int `json:"number_of_replicas"`
}

// FieldMapping defines a field mapping in Elasticsearch.
type FieldMapping struct {
	Type       string                  `json:"type,omitempty"`
	Dims       int                     `json:"dims,omitempty"`
	Index      bool                    `json:"index,omitempty"`
	Similarity string                  `json:"similarity,omitempty"`
	Dynamic    bool                    `json:"dynamic,omitempty"`
	Fields     map[string]FieldMapping `json:"fields,omitempty"`
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
		option.indexName = DefaultIndexName
	}

	if option.vectorDimension == 0 {
		option.vectorDimension = DefaultVectorDimension
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
	mapping := &IndexMapping{
		Mappings: IndexMappings{
			Properties: map[string]FieldMapping{
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
		Settings: IndexSettings{
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
func (vs *VectorStore) createIndex(ctx context.Context, indexName string, mapping *IndexMapping) error {
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

// Add stores a document with its embedding vector.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return fmt.Errorf("elasticsearch document cannot be nil")
	}

	if len(embedding) == 0 {
		return fmt.Errorf("elasticsearch embedding vector cannot be empty")
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d", len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for indexing.
	esDoc := map[string]any{
		"id":         doc.ID,
		"name":       doc.Name,
		"content":    doc.Content,
		"metadata":   doc.Metadata,
		"created_at": doc.CreatedAt,
		"updated_at": doc.UpdatedAt,
		"embedding":  embedding,
	}

	return vs.indexDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// indexDocument indexes a document.
func (vs *VectorStore) indexDocument(ctx context.Context, indexName, id string, document any) error {
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
		return nil, nil, fmt.Errorf("elasticsearch document ID cannot be empty")
	}

	data, err := vs.getDocument(ctx, vs.option.indexName, id)
	if err != nil {
		return nil, nil, err
	}

	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, err
	}

	source, ok := response["_source"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("elasticsearch invalid document source")
	}

	// Extract document fields.
	doc := &document.Document{
		ID:        getString(source, "id"),
		Name:      getString(source, "name"),
		Content:   getString(source, "content"),
		CreatedAt: getTime(source, "created_at"),
		UpdatedAt: getTime(source, "updated_at"),
	}

	// Extract metadata.
	if metadata, ok := source["metadata"].(map[string]any); ok {
		doc.Metadata = metadata
	}

	// Extract embedding vector.
	embeddingInterface, ok := source["embedding"]
	if !ok {
		return nil, nil, fmt.Errorf("elasticsearch embedding not found")
	}

	embedding, err := extractFloatSlice(embeddingInterface)
	if err != nil {
		return nil, nil, fmt.Errorf("elasticsearch invalid embedding format: %w", err)
	}

	return doc, embedding, nil
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
		return fmt.Errorf("elasticsearch document cannot be nil")
	}

	if len(embedding) == 0 {
		return fmt.Errorf("elasticsearch embedding vector cannot be empty")
	}

	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("elasticsearch embedding dimension %d does not match expected dimension %d", len(embedding), vs.option.vectorDimension)
	}

	// Prepare document for updating.
	esDoc := map[string]any{
		"name":       doc.Name,
		"content":    doc.Content,
		"metadata":   doc.Metadata,
		"updated_at": time.Now(),
		"embedding":  embedding,
	}

	return vs.updateDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// updateDocument updates a document.
func (vs *VectorStore) updateDocument(ctx context.Context, indexName, id string, document any) error {
	updateBody := map[string]any{
		"doc": document,
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
		return fmt.Errorf("elasticsearch document ID cannot be empty")
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
		return nil, fmt.Errorf("elasticsearch search query cannot be nil")
	}

	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("elasticsearch query vector cannot be empty")
	}

	if len(query.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("elasticsearch query vector dimension %d does not match expected dimension %d", len(query.Vector), vs.option.vectorDimension)
	}

	// Build search query based on search mode.
	var searchQuery map[string]any

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
func (vs *VectorStore) search(ctx context.Context, indexName string, query map[string]any) ([]byte, error) {
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

// buildVectorSearchQuery builds a vector similarity search query.
func (vs *VectorStore) buildVectorSearchQuery(query *vectorstore.SearchQuery) map[string]any {
	searchQuery := map[string]any{
		"query": map[string]any{
			"script_score": map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
				"script": map[string]any{
					"source": "if (doc['embedding'].size() > 0) { cosineSimilarity(params.query_vector, 'embedding') + 1.0 } else { 0.0 }",
					"params": map[string]any{
						"query_vector": query.Vector,
					},
				},
			},
		},
		"size": vs.option.maxResults,
	}

	// Add filters if specified.
	if query.Filter != nil {
		searchQuery["post_filter"] = vs.buildFilterQuery(query.Filter)
	}

	return searchQuery
}

// buildKeywordSearchQuery builds a keyword-based search query.
func (vs *VectorStore) buildKeywordSearchQuery(query *vectorstore.SearchQuery) map[string]any {
	searchQuery := map[string]any{
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  query.Query,
				"fields": []string{"content^2", "name^1.5"},
				"type":   "best_fields",
			},
		},
		"size": vs.option.maxResults,
	}

	// Add filters if specified.
	if query.Filter != nil {
		searchQuery["post_filter"] = vs.buildFilterQuery(query.Filter)
	}

	return searchQuery
}

// buildHybridSearchQuery builds a hybrid search query combining vector and keyword search.
func (vs *VectorStore) buildHybridSearchQuery(query *vectorstore.SearchQuery) map[string]any {
	// Vector similarity search (inner content for script_score).
	vectorQuery := map[string]any{
		"query": map[string]any{
			"match_all": map[string]any{},
		},
		"script": map[string]any{
			"source": "if (doc['embedding'].size() > 0) { cosineSimilarity(params.query_vector, 'embedding') + 1.0 } else { 0.0 }",
			"params": map[string]any{
				"query_vector": query.Vector,
			},
		},
	}

	// Keyword search (inner content for multi_match).
	keywordQuery := map[string]any{
		"query":  query.Query,
		"fields": []string{"content^2", "name^1.5"},
		"type":   "best_fields",
	}

	// Combine queries.
	searchQuery := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"script_score": vectorQuery},
					{"multi_match": keywordQuery},
				},
				"minimum_should_match": 1,
			},
		},
		"size": vs.option.maxResults,
	}

	// Add filters if specified.
	if query.Filter != nil {
		searchQuery["post_filter"] = vs.buildFilterQuery(query.Filter)
	}

	return searchQuery
}

// buildFilterQuery builds a filter query for search results.
func (vs *VectorStore) buildFilterQuery(filter *vectorstore.SearchFilter) map[string]any {
	var filters []map[string]any

	// Filter by document IDs.
	if len(filter.IDs) > 0 {
		filters = append(filters, map[string]any{
			"terms": map[string]any{
				"id": filter.IDs,
			},
		})
	}

	// Filter by metadata.
	for key, value := range filter.Metadata {
		filters = append(filters, map[string]any{
			"term": map[string]any{
				fmt.Sprintf("metadata.%s", key): value,
			},
		})
	}

	if len(filters) == 0 {
		return nil
	}

	return map[string]any{
		"bool": map[string]any{
			"filter": filters,
		},
	}
}

// parseSearchResults parses Elasticsearch search response.
func (vs *VectorStore) parseSearchResults(data []byte) (*vectorstore.SearchResult, error) {
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	hits, ok := response["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("elasticsearch invalid search response format")
	}

	hitsList, ok := hits["hits"].([]any)
	if !ok {
		return nil, fmt.Errorf("elasticsearch invalid hits format")
	}

	results := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(hitsList)),
	}

	for _, hit := range hitsList {
		hitMap, ok := hit.(map[string]any)
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]any)
		if !ok {
			continue
		}

		score, ok := hitMap["_score"].(float64)
		if !ok {
			continue
		}

		// Check score threshold.
		if score < vs.option.scoreThreshold {
			continue
		}

		// Create document.
		doc := &document.Document{
			ID:        getString(source, "id"),
			Name:      getString(source, "name"),
			Content:   getString(source, "content"),
			CreatedAt: getTime(source, "created_at"),
			UpdatedAt: getTime(source, "updated_at"),
		}

		// Extract metadata.
		if metadata, ok := source["metadata"].(map[string]any); ok {
			doc.Metadata = metadata
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

// Helper functions for extracting values from interface{}.
func getString(source map[string]any, key string) string {
	if value, ok := source[key].(string); ok {
		return value
	}
	return ""
}

func getTime(source map[string]any, key string) time.Time {
	if value, ok := source[key].(string); ok {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func extractFloatSlice(value any) ([]float64, error) {
	switch v := value.(type) {
	case []float64:
		return v, nil
	case []any:
		result := make([]float64, len(v))
		for i, item := range v {
			if f, ok := item.(float64); ok {
				result[i] = f
			} else {
				return nil, fmt.Errorf("elasticsearch invalid float value at index %d", i)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("elasticsearch unsupported type: %T", value)
	}
}
