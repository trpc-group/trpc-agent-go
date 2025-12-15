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

	"github.com/elastic/go-elasticsearch/v9/typedapi/core/search"
	"github.com/elastic/go-elasticsearch/v9/typedapi/core/update"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/densevectorsimilarity"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/dynamicmapping"

	istorage "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
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

// indexCreateBody is a lightweight helper used to marshal typed mappings and settings.
type indexCreateBody struct {
	Mappings *types.TypeMapping   `json:"mappings,omitempty"`
	Settings *types.IndexSettings `json:"settings,omitempty"`
}

// VectorStore implements vectorstore.VectorStore interface using Elasticsearch.
type VectorStore struct {
	client          istorage.Client
	option          options
	filterConverter searchfilter.Converter[types.QueryVariant]
}

// New creates a new Elasticsearch vector store with options.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
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
		storage.WithExtraOptions(option.extraOptions...),
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
		client:          client,
		option:          option,
		filterConverter: &esConverter{metadataFieldName: option.metadataFieldName},
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
	tm.Properties[vs.option.contentFieldName] = types.NewTextProperty()
	// metadata: object with dynamic true
	metaObj := types.NewObjectProperty()
	dm := dynamicmapping.True
	metaObj.Dynamic = &dm
	tm.Properties[vs.option.metadataFieldName] = metaObj
	// created_at / updated_at: date
	tm.Properties[vs.option.createdAtFieldName] = types.NewDateProperty()
	tm.Properties[vs.option.updatedAtFieldName] = types.NewDateProperty()
	// embedding: dense_vector with dims, index, similarity
	dv := types.NewDenseVectorProperty()
	dims := vs.option.vectorDimension
	dv.Dims = &dims
	indexed := true
	dv.Index = &indexed
	sim := densevectorsimilarity.Cosine
	dv.Similarity = &sim
	tm.Properties[vs.option.embeddingFieldName] = dv

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
	esDoc := map[string]any{
		vs.option.idFieldName:        doc.ID,
		vs.option.nameFieldName:      doc.Name,
		vs.option.contentFieldName:   doc.Content,
		vs.option.metadataFieldName:  doc.Metadata,
		vs.option.embeddingFieldName: embedding,
		vs.option.createdAtFieldName: doc.CreatedAt,
		vs.option.updatedAtFieldName: doc.UpdatedAt,
	}

	return vs.indexDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// indexDocument indexes a document.
func (vs *VectorStore) indexDocument(ctx context.Context, indexName, id string, document map[string]any) error {
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
	doc, embedding, err := vs.docBuilder(response.Source_)
	if err != nil {
		return nil, nil, fmt.Errorf("elasticsearch invalid document source: %w", err)
	}

	// Extract embedding vector.
	if len(embedding) == 0 {
		return nil, nil, fmt.Errorf("elasticsearch embedding vector not found: %s", id)
	}

	return doc, embedding, nil
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
	esDoc := map[string]any{
		vs.option.nameFieldName:      doc.Name,
		vs.option.contentFieldName:   doc.Content,
		vs.option.metadataFieldName:  doc.Metadata,
		vs.option.updatedAtFieldName: doc.UpdatedAt,
		vs.option.embeddingFieldName: embedding,
	}

	return vs.updateDocument(ctx, vs.option.indexName, doc.ID, esDoc)
}

// updateDocument updates a document.
func (vs *VectorStore) updateDocument(ctx context.Context, indexName, id string, updateDoc map[string]any) error {
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

	// Build search query based on search mode.
	var searchQuery *types.SearchRequestBody
	var err error

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		searchQuery, err = vs.buildVectorSearchQuery(query)
	case vectorstore.SearchModeKeyword:
		if !vs.option.enableTSVector {
			log.InfofContext(
				ctx,
				"elasticsearch: keyword search is not supported "+
					"when enableTSVector is disabled, use vector "+
					"search instead",
			)
			searchQuery, err = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery, err = vs.buildKeywordSearchQuery(query)
		}
	case vectorstore.SearchModeHybrid:
		if !vs.option.enableTSVector {
			log.InfofContext(
				ctx,
				"elasticsearch: hybrid search is not supported "+
					"when enableTSVector is disabled, use vector "+
					"search instead",
			)
			searchQuery, err = vs.buildVectorSearchQuery(query)
		} else {
			searchQuery, err = vs.buildHybridSearchQuery(query)
		}
	case vectorstore.SearchModeFilter:
		searchQuery, err = vs.buildFilterSearchQuery(query)
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
		doc, _, err := vs.docBuilder(hit.Source_)
		if err != nil {
			log.Errorf("elasticsearch parse search result: %v", err)
			continue
		}
		if doc == nil {
			continue
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

// Count counts the number of documents.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	// Build count query
	countQuery := vs.buildCountQuery(config.Filter)

	// Marshal count query
	queryBytes, err := json.Marshal(countQuery)
	if err != nil {
		return 0, fmt.Errorf("elasticsearch marshal count query: %w", err)
	}

	// Execute count query directly
	return vs.client.Count(ctx, vs.option.indexName, queryBytes)
}

// DeleteByFilter deletes documents by filter.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)
	if err := vs.validateDeleteConfig(config); err != nil {
		return err
	}

	if config.DeleteAll {
		return vs.deleteAll(ctx)
	}

	return vs.deleteByFilter(ctx, config)
}

// GetMetadata retrieves metadata from the vector store.
func (vs *VectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	// refresh index to ensure the metadata is up to date
	if err := vs.client.Refresh(ctx, vs.option.indexName); err != nil {
		return nil, fmt.Errorf("elasticsearch refresh index: %w", err)
	}

	if config.Limit < 0 && config.Offset < 0 {
		return vs.getAllMetadata(ctx, config)
	}

	return vs.queryMetadataBatch(ctx, config.Limit, config.Offset, config.IDs, config.Filter)
}

// buildCountQuery builds a count query with optional filters.
func (vs *VectorStore) buildCountQuery(filter map[string]any) *types.SearchRequestBody {
	query := types.NewSearchRequestBody()
	query.Size = &[]int{0}[0] // Set size to 0 for count query

	if len(filter) > 0 {
		boolQuery := types.NewBoolQuery()
		for key, value := range filter {
			termQuery := types.NewTermQuery()
			termQuery.Value = value
			mustClause := types.NewQuery()
			mustClause.Term = map[string]types.TermQuery{
				fmt.Sprintf("%s.%s", vs.option.metadataFieldName, key): *termQuery,
			}
			boolQuery.Must = append(boolQuery.Must, *mustClause)
		}
		query.Query = &types.Query{Bool: boolQuery}
	}

	return query
}

// validateDeleteConfig validates delete configuration.
func (vs *VectorStore) validateDeleteConfig(config *vectorstore.DeleteConfig) error {
	if config.DeleteAll && (len(config.DocumentIDs) > 0 || len(config.Filter) > 0) {
		return fmt.Errorf("elasticsearch delete all documents, but document ids or filter are provided")
	}
	if !config.DeleteAll && len(config.DocumentIDs) == 0 && len(config.Filter) == 0 {
		return fmt.Errorf("elasticsearch delete by filter: no filter conditions specified")
	}
	return nil
}

// deleteAll deletes all documents from the index.
func (vs *VectorStore) deleteAll(ctx context.Context) error {
	// Use delete by query with match_all
	deleteQuery := types.NewSearchRequestBody()
	matchAllQuery := types.NewMatchAllQuery()
	deleteQuery.Query = &types.Query{MatchAll: matchAllQuery}

	// Marshal delete query
	queryBytes, err := json.Marshal(deleteQuery)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal delete all query: %w", err)
	}

	// Execute delete by query
	if err := vs.client.DeleteByQuery(ctx, vs.option.indexName, queryBytes); err != nil {
		return fmt.Errorf("elasticsearch delete all documents: %w", err)
	}

	log.InfofContext(
		ctx,
		"elasticsearch deleted all documents from index %s",
		vs.option.indexName,
	)
	return nil
}

// deleteByFilter deletes documents by filter conditions.
func (vs *VectorStore) deleteByFilter(ctx context.Context, config *vectorstore.DeleteConfig) error {
	deleteQuery := types.NewSearchRequestBody()
	boolQuery := types.NewBoolQuery()

	// Add document ID filters
	if len(config.DocumentIDs) > 0 {
		idsQuery := types.NewIdsQuery()
		idsQuery.Values = config.DocumentIDs
		mustClause := types.NewQuery()
		mustClause.Ids = idsQuery
		boolQuery.Must = append(boolQuery.Must, *mustClause)
	}

	// Add metadata filters
	for key, value := range config.Filter {
		termQuery := types.NewTermQuery()
		termQuery.Value = value
		mustClause := types.NewQuery()
		mustClause.Term = map[string]types.TermQuery{
			fmt.Sprintf("%s.%s", vs.option.metadataFieldName, key): *termQuery,
		}
		boolQuery.Must = append(boolQuery.Must, *mustClause)
	}

	deleteQuery.Query = &types.Query{Bool: boolQuery}

	// Marshal delete query
	queryBytes, err := json.Marshal(deleteQuery)
	if err != nil {
		return fmt.Errorf("elasticsearch marshal delete query: %w", err)
	}

	// Execute delete by query
	if err := vs.client.DeleteByQuery(ctx, vs.option.indexName, queryBytes); err != nil {
		return fmt.Errorf("elasticsearch delete by filter: %w", err)
	}

	log.InfofContext(
		ctx,
		"elasticsearch executed delete by filter query",
	)
	return nil
}

// getAllMetadata retrieves all metadata in batches.
func (vs *VectorStore) getAllMetadata(ctx context.Context, config *vectorstore.GetMetadataConfig) (map[string]vectorstore.DocumentMetadata, error) {
	result := make(map[string]vectorstore.DocumentMetadata)
	const batchSize = 5000

	for offset := 0; ; offset += batchSize {
		batch, err := vs.queryMetadataBatch(ctx, batchSize, offset, config.IDs, config.Filter)
		if err != nil {
			return nil, err
		}

		for docID, metadata := range batch {
			result[docID] = metadata
		}

		if len(batch) < batchSize {
			break
		}
	}

	return result, nil
}

// queryMetadataBatch executes a single metadata query with the given limit and offset.
func (vs *VectorStore) queryMetadataBatch(
	ctx context.Context,
	limit,
	offset int,
	docIDs []string,
	filters map[string]any,
) (map[string]vectorstore.DocumentMetadata, error) {
	metadataQuery := types.NewSearchRequestBody()
	metadataQuery.Size = &limit
	metadataQuery.From = &offset

	finalQuery, err := vs.buildFilterQuery(&vectorstore.SearchFilter{
		IDs:      docIDs,
		Metadata: filters,
	})
	if err != nil {
		return nil, err
	}
	if finalQuery != nil {
		metadataQuery.Query = finalQuery.QueryCaster()
	}

	data, err := vs.search(ctx, vs.option.indexName, metadataQuery)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search metadata: %w", err)
	}

	var response search.Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("elasticsearch unmarshal metadata response: %w", err)
	}

	result := make(map[string]vectorstore.DocumentMetadata)
	for _, hit := range response.Hits.Hits {
		if hit.Id_ == nil || len(hit.Source_) == 0 {
			continue
		}

		doc, _, err := vs.docBuilder(hit.Source_)
		if err != nil {
			log.WarnfContext(
				ctx,
				"elasticsearch doc builder failed: %v",
				err,
			)
			continue // Skip invalid documents
		}
		if doc == nil || len(doc.Metadata) == 0 {
			continue
		}
		result[*hit.Id_] = vectorstore.DocumentMetadata{
			Metadata: doc.Metadata,
		}
	}

	return result, nil
}
