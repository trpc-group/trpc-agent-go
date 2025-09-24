//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcvector provides a vector store for tcvectordb.
package tcvector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	tcdocument "github.com/tencent/vectordatabase-sdk-go/tcvectordb/api/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/tcvector"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

var (
	// errDocumentRequired is the error when document is required.
	errDocumentRequired = errors.New("tcvectordb document is required")
	// errDocumentIDRequired is the error when document ID is required.
	errDocumentIDRequired = errors.New("tcvectordb document ID is required")
	// errQueryRequired is the error when query is required.
	errQueryRequired = errors.New("tcvectordb query is required")
)

var (
	fieldID           = "id"
	fieldUpdatedAt    = "updated_at"
	fieldCreatedAt    = "created_at"
	fieldName         = "name"
	fieldContent      = "content"
	fieldVector       = "vector"
	fieldSparseVector = "sparse_vector"
	fieldMetadata     = "metadata"
	defaultLimit      = 10
)

const (
	// Batch processing constants
	metadataBatchSize = 5000 // Maximum records per batch when querying all metadata
)

// VectorStore is the vector store for tcvectordb.
type VectorStore struct {
	client        storage.ClientInterface
	option        options
	sparseEncoder encoder.SparseEncoder
}

// New creates a new tcvectordb vector store.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}

	// Check required options.
	if option.instanceName == "" && (option.url == "" || option.username == "" || option.password == "") {
		return nil, errors.New("tcvectordb instance name or (url, username, password) is required")
	}
	if option.database == "" {
		return nil, errors.New("tcvectordb database is required")
	}
	if option.collection == "" {
		return nil, errors.New("tcvectordb collection is required")
	}

	builderOpts := make([]storage.ClientBuilderOpt, 0)

	if option.url != "" && option.username != "" && option.password != "" {
		// url and username and password are provided, use it.
		builderOpts = append(builderOpts,
			storage.WithClientBuilderHTTPURL(option.url),
			storage.WithClientBuilderUserName(option.username),
			storage.WithClientBuilderKey(option.password))
	} else if option.instanceName != "" {
		// instance name is provided, use it.
		instanceOpts, ok := storage.GetTcVectorInstance(option.instanceName)
		if !ok {
			return nil, errors.New("tcvectordb instance name not found")
		}
		builderOpts = append(builderOpts, instanceOpts...)
	}

	c, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb new rpc client pool: %w", err)
	}
	if err = initVectorDB(c, option); err != nil {
		return nil, err
	}

	var sparseEncoder encoder.SparseEncoder
	if option.enableTSVector {
		sparseEncoder, err = encoder.NewBM25Encoder(&encoder.BM25EncoderParams{Bm25Language: option.language})
		if err != nil {
			return nil, fmt.Errorf("tcvectordb new bm25 encoder: %w", err)
		}
	}

	return &VectorStore{client: c, option: option, sparseEncoder: sparseEncoder}, nil
}

func initVectorDB(client storage.ClientInterface, options options) error {
	_, err := client.CreateDatabaseIfNotExists(context.Background(), options.database)
	if err != nil {
		return fmt.Errorf("tcvectordb create database: %w", err)
	}
	db := client.Database(options.database)
	if db == nil {
		return fmt.Errorf("tcvectordb database %s not found", options.database)
	}

	// Check collection exists.
	exists, err := db.ExistsCollection(context.Background(), options.collection)
	if err != nil {
		return fmt.Errorf("tcvectordb check collection exists: %w", err)
	}
	if exists {
		return checkIndexes(db, options)
	}

	indexes := tcvectordb.Indexes{}
	indexes.FilterIndex = append(indexes.FilterIndex, tcvectordb.FilterIndex{
		FieldName: fieldID,
		IndexType: tcvectordb.PRIMARY,
		FieldType: tcvectordb.String,
	})

	// Add filter indexes for configured filterFields.
	indexes.FilterIndex = append(indexes.FilterIndex, options.filterIndexes...)
	indexes.VectorIndex = append(indexes.VectorIndex, tcvectordb.VectorIndex{
		FilterIndex: tcvectordb.FilterIndex{
			FieldName: fieldVector,
			IndexType: tcvectordb.HNSW,
			FieldType: tcvectordb.Vector,
			ElemType:  tcvectordb.Double,
		},
		Dimension:  uint32(options.indexDimension),
		MetricType: tcvectordb.COSINE,
		Params: &tcvectordb.HNSWParam{
			M:              32,
			EfConstruction: 400,
		},
	})
	if options.enableTSVector {
		indexes.SparseVectorIndex = append(indexes.SparseVectorIndex, tcvectordb.SparseVectorIndex{
			FieldName:  fieldSparseVector,
			FieldType:  tcvectordb.SparseVector,
			IndexType:  tcvectordb.SPARSE_INVERTED,
			MetricType: tcvectordb.IP,
		})
	}

	if _, err := db.CreateCollectionIfNotExists(
		context.Background(),
		options.collection,
		options.sharding,
		options.replicas,
		"trpc-agent-go documents storage collection",
		indexes,
		nil,
	); err != nil {
		return fmt.Errorf("tcvectordb create collection: %w", err)
	}

	return nil
}

func checkIndexes(db *tcvectordb.Database, option options) error {
	collection, err := db.DescribeCollection(context.Background(), option.collection)
	if err != nil {
		return fmt.Errorf("tcvectordb describe collection: %w", err)
	}
	if collection == nil {
		return fmt.Errorf("tcvectordb collection %s not found", option.collection)
	}
	if len(collection.Indexes.VectorIndex) == 0 {
		return fmt.Errorf("tcvectordb collection %s vector index not found, not trpc-agent-go collection, you can adjust vector index by yourself", option.collection)
	}
	vectorIndexExist := false
	for _, index := range collection.Indexes.VectorIndex {
		if index.FieldName == fieldVector {
			vectorIndexExist = true
		}
	}
	if !vectorIndexExist {
		return fmt.Errorf("tcvectordb collection %s vector index [%s] not found, not trpc-agent-go collection, you can adjust vector index by yourself", option.collection, fieldVector)
	}
	if option.enableTSVector {
		sparseVectorIndexExist := false
		for _, index := range collection.Indexes.SparseVectorIndex {
			if index.FieldName == fieldSparseVector {
				sparseVectorIndexExist = true
			}
		}
		if !sparseVectorIndexExist {
			return fmt.Errorf("tcvectordb collection %s sparse vector index [%s] not found, not trpc-agent-go collection, you can adjust sparse vector index by yourself", option.collection, fieldSparseVector)
		}
	}

	existingFilterIndex := make(map[string]struct{})
	filterIndexToAdd := make([]tcvectordb.FilterIndex, 0)
	for _, index := range collection.Indexes.FilterIndex {
		existingFilterIndex[index.FieldName] = struct{}{}
	}
	for _, index := range option.filterIndexes {
		if _, exists := existingFilterIndex[index.FieldName]; exists {
			continue
		}
		filterIndexToAdd = append(filterIndexToAdd, index)
	}
	if len(filterIndexToAdd) == 0 {
		return nil
	}

	log.Infof("tcvectordb collection %s filter index need to add %v", option.collection, filterIndexToAdd)
	addIndexParam := &tcvectordb.AddIndexParams{
		FilterIndexs:     filterIndexToAdd,
		BuildExistedData: &[]bool{true}[0],
	}
	if err := collection.AddIndex(context.Background(), addIndexParam); err != nil {
		return fmt.Errorf("tcvectordb add indexes: %w", err)
	}
	log.Infof("tcvectordb collection %s add filter indexes success, filter indexes: %v", option.collection, filterIndexToAdd)
	return nil
}

// Add stores a document with its embedding vector.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if len(embedding) != int(vs.option.indexDimension) {
		return fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(embedding))
	}
	embedding32 := covertToVector32(embedding)
	now := time.Now().Unix()
	fields := map[string]tcvectordb.Field{
		fieldName:      {Val: doc.Name},
		fieldContent:   {Val: doc.Content},
		fieldCreatedAt: {Val: now},
		fieldUpdatedAt: {Val: now},
		fieldMetadata:  {Val: doc.Metadata},
	}

	// Extract filterField data from metadata and add as separate fields.
	for _, filterField := range vs.option.filterFields {
		if value, exists := doc.Metadata[filterField]; exists {
			fields[filterField] = tcvectordb.Field{Val: value}
		}
	}

	tcDoc := tcvectordb.Document{
		Id:     doc.ID,
		Vector: embedding32,
		Fields: fields,
	}

	if vs.option.enableTSVector {
		sparseVector, err := vs.sparseEncoder.EncodeText(doc.Content)
		if err != nil {
			return fmt.Errorf("tcvectordb bm25 encode text: %w", err)
		}
		tcDoc.SparseVector = sparseVector
	}

	if _, err := vs.client.Upsert(
		ctx,
		vs.option.database,
		vs.option.collection,
		[]tcvectordb.Document{tcDoc},
	); err != nil {
		return fmt.Errorf("tcvectordb upsert document: %w", err)
	}
	return nil
}

// Get retrieves a document by ID along with its embedding.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocumentIDRequired
	}
	result, err := vs.client.Query(
		ctx,
		vs.option.database,
		vs.option.collection,
		[]string{id},
		&tcvectordb.QueryDocumentParams{
			RetrieveVector: true,
			Limit:          1,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("tcvectordb get document: %w", err)
	}
	if result.AffectedCount == 0 || len(result.Documents) == 0 {
		return nil, nil, fmt.Errorf("tcvectordb not found document id: %s", id)
	}
	if result.AffectedCount > 1 {
		return nil, nil, fmt.Errorf("tcvectordb get multiple documents, affected count: %d", result.AffectedCount)
	}

	tcDoc := result.Documents[0]
	doc, err := covertToDocument(tcDoc)
	if err != nil {
		return nil, nil, fmt.Errorf("tcvectordb covert to document: %w", err)
	}
	embedding := make([]float64, len(tcDoc.Vector))
	for i, v := range tcDoc.Vector {
		embedding[i] = float64(v)
	}
	return doc, embedding, nil
}

// Update modifies an existing document and its embedding.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if len(embedding) != int(vs.option.indexDimension) {
		return fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(embedding))
	}

	updateFields := map[string]tcvectordb.Field{}
	updateFields[fieldUpdatedAt] = tcvectordb.Field{Val: time.Now().Unix()}
	if len(doc.Name) > 0 {
		updateFields[fieldName] = tcvectordb.Field{Val: doc.Name}
	}

	var sparseVector []encoder.SparseVecItem
	var err error
	if len(doc.Content) > 0 {
		updateFields[fieldContent] = tcvectordb.Field{Val: doc.Content}
		if vs.option.enableTSVector {
			sparseVector, err = vs.sparseEncoder.EncodeText(doc.Content)
			if err != nil {
				return fmt.Errorf("tcvectordb bm25 encode text: %w", err)
			}
		}
	}
	if len(doc.Metadata) > 0 {
		updateFields[fieldMetadata] = tcvectordb.Field{Val: doc.Metadata}
		// Extract filterField data from metadata and update as separate fields.
		for _, filterField := range vs.option.filterFields {
			if value, exists := doc.Metadata[filterField]; exists {
				updateFields[filterField] = tcvectordb.Field{Val: value}
			}
		}
	}

	updateParams := tcvectordb.UpdateDocumentParams{}
	updateParams.QueryIds = []string{doc.ID}
	updateParams.UpdateFields = updateFields
	updateParams.UpdateVector = covertToVector32(embedding)
	if len(sparseVector) > 0 {
		updateParams.UpdateSparseVec = sparseVector
	}

	result, err := vs.client.Update(ctx, vs.option.database, vs.option.collection, updateParams)
	if err != nil {
		return fmt.Errorf("tcvectordb update document: %w", err)
	}
	if result.AffectedCount == 0 {
		return fmt.Errorf("tcvectordb not found document, affected count: %d, id: %s", result.AffectedCount, doc.ID)
	}
	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocumentIDRequired
	}
	if _, err := vs.client.Delete(
		ctx,
		vs.option.database,
		vs.option.collection,
		tcvectordb.DeleteDocumentParams{
			DocumentIds: []string{id},
			Limit:       1,
		},
	); err != nil {
		return fmt.Errorf("tcvectordb delete document: %w", err)
	}
	return nil
}

// Search performs similarity search and returns the most similar documents.
// Automatically chooses the appropriate search method based on query parameters.
// Tencent VectorDB not support hybrid search of structure filter and vector/sparse vector.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errQueryRequired
	}
	if !vs.option.enableTSVector && (query.SearchMode == vectorstore.SearchModeKeyword || query.SearchMode == vectorstore.SearchModeHybrid) {
		log.Infof("tcvectordb: keyword or hybrid search is not supported when enableTSVector is disabled, use filter/vector search instead")
		if len(query.Vector) > 0 {
			return vs.searchByVector(ctx, query)
		}
		return vs.searchByFilter(ctx, query)
	}

	// Default is hybrid search.
	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeKeyword:
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeHybrid:
		return vs.searchByHybrid(ctx, query)
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	default:
		return nil, fmt.Errorf("tcvectordb: invalid search mode: %d", query.SearchMode)
	}
}

// vectorSearch performs pure vector similarity search using dense embeddings
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errors.New("tcvectordb: searching with a nil or empty vector is not supported")
	}
	if len(query.Vector) != int(vs.option.indexDimension) {
		return nil, fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(query.Vector))
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	var cond *tcvectordb.Filter
	if query.Filter != nil {
		cond = getCondFromQuery(query.Filter.IDs, query.Filter.Metadata)
	}

	queryParams := tcvectordb.SearchDocumentParams{
		Filter:         cond,
		Limit:          int64(limit),
		RetrieveVector: true,
	}

	// Set minimum score threshold if specified.
	if query.MinScore > 0 {
		radius := float32(query.MinScore)
		queryParams.Radius = &radius
	}

	vector32 := covertToVector32(query.Vector)
	searchResult, err := vs.client.Search(
		ctx,
		vs.option.database,
		vs.option.collection,
		[][]float32{vector32},
		&queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb vector search: %w", err)
	}

	return vs.convertSearchResult(vectorstore.SearchModeVector, searchResult)
}

// keywordSearch performs pure keyword search using BM25 sparse vectors.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, errors.New("tcvectordb keyword is required for keyword search")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	var cond *tcvectordb.Filter
	if query.Filter != nil {
		cond = getCondFromQuery(query.Filter.IDs, query.Filter.Metadata)
	}

	querySparseVector, err := vs.sparseEncoder.EncodeQueries([]string{query.Query})
	if err != nil {
		return nil, fmt.Errorf("tcvectordb encode query text: %w", err)
	}
	queryParams := tcvectordb.FullTextSearchParams{
		Filter:         cond,
		Limit:          &limit,
		RetrieveVector: true,
		Match: &tcvectordb.FullTextSearchMatchOption{
			FieldName: fieldSparseVector,
			Data:      querySparseVector,
		},
	}

	searchResult, err := vs.client.FullTextSearch(
		ctx,
		vs.option.database,
		vs.option.collection,
		queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb keyword search: %w", err)
	}

	return vs.convertSearchResult(vectorstore.SearchModeKeyword, searchResult)
}

// hybridSearch performs hybrid search combining dense vector similarity and BM25 keyword matching.
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errors.New("tcvectordb vector is required for hybrid search")
	}

	vectorWeight := vs.option.vectorWeight
	textWeight := vs.option.textWeight
	if query.Query == "" {
		vectorWeight = 1.0
		textWeight = 0.0
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	var cond *tcvectordb.Filter
	if query.Filter != nil {
		cond = getCondFromQuery(query.Filter.IDs, query.Filter.Metadata)
	}

	// Encode the query text using BM25 for sparse vector.
	querySparseVector, err := vs.sparseEncoder.EncodeQuery(query.Query)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb encode query text: %w", err)
	}

	vector32 := covertToVector32(query.Vector)

	queryParams := tcvectordb.HybridSearchDocumentParams{
		Limit:          &limit,
		RetrieveVector: true,
		AnnParams: []*tcvectordb.AnnParam{
			{
				FieldName: fieldVector,
				Data:      vector32,
			},
		},
		Match: []*tcvectordb.MatchOption{
			{
				FieldName: fieldSparseVector,
				Data:      querySparseVector,
			},
		},
		Filter: cond,
		// Use weighted rerank
		Rerank: &tcvectordb.RerankOption{
			Method:    tcvectordb.RerankWeighted,
			FieldList: []string{fieldVector, fieldSparseVector},
			Weight:    []float32{float32(vectorWeight), float32(textWeight)},
		},
	}
	searchResult, err := vs.client.HybridSearch(
		ctx,
		vs.option.database,
		vs.option.collection,
		queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb hybrid search: %w", err)
	}

	return vs.convertSearchResult(vectorstore.SearchModeHybrid, searchResult)
}

// filterSearch performs filter-only search when no vector or keyword is provided.
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Filter == nil {
		return &vectorstore.SearchResult{Results: make([]*vectorstore.ScoredDocument, 0)}, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	var cond *tcvectordb.Filter
	if query.Filter != nil {
		cond = getCondFromQuery(query.Filter.IDs, query.Filter.Metadata)
	}
	queryParams := tcvectordb.QueryDocumentParams{
		Filter:         cond,
		Limit:          int64(limit),
		RetrieveVector: true,
	}
	result, err := vs.client.Query(
		ctx,
		vs.option.database,
		vs.option.collection,
		nil,
		&queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb filter search: %w", err)
	}
	return vs.convertQueryResult(result)
}

// DeleteByFilter deletes documents from the vector store based on filter conditions.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	options := vectorstore.ApplyDeleteOptions(opts...)

	if err := vs.validateDeleteOptions(options); err != nil {
		return err
	}

	if options.DeleteAll {
		return vs.deleteAll(ctx)
	}

	return vs.deleteByFilter(ctx, options)
}

func (vs *VectorStore) validateDeleteOptions(options *vectorstore.DeleteConfig) error {
	if options.DeleteAll && (len(options.DocumentIDs) > 0 || len(options.Filter) > 0) {
		return fmt.Errorf("tcvectordb delete all documents, but document ids or filter are provided")
	}
	if !options.DeleteAll && len(options.DocumentIDs) == 0 && len(options.Filter) == 0 {
		return fmt.Errorf("tcvectordb delete by filter: no filter conditions specified")
	}
	return nil
}

func (vs *VectorStore) deleteAll(ctx context.Context) error {
	db := vs.client.Database(vs.option.database)
	if _, err := db.TruncateCollection(ctx, vs.option.collection); err != nil {
		return fmt.Errorf("tcvectordb truncate collection: %w", err)
	}
	return nil
}

func (vs *VectorStore) deleteByFilter(ctx context.Context, options *vectorstore.DeleteConfig) error {
	deleteParams := tcvectordb.DeleteDocumentParams{
		DocumentIds: options.DocumentIDs,
	}

	if len(options.Filter) > 0 {
		deleteParams.Filter = getCondFromQuery([]string{}, options.Filter)
	}

	if _, err := vs.client.Delete(ctx, vs.option.database, vs.option.collection, deleteParams); err != nil {
		return fmt.Errorf("tcvectordb delete documents by filter: %w", err)
	}
	return nil
}

// Count counts the number of documents in the vector store.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	options := vectorstore.ApplyCountOptions(opts...)
	filter := options.Filter

	countParams := tcvectordb.CountDocumentParams{}
	if filter != nil {
		tcFilter := getCondFromQuery([]string{}, filter)
		countParams.CountFilter = tcFilter
	}

	countResult, err := vs.client.Count(ctx, vs.option.database, vs.option.collection, countParams)
	if err != nil {
		return 0, fmt.Errorf("tcvectordb count documents: %w", err)
	}
	result := int(countResult.Count)
	return result, nil
}

// GetMetadata retrieves metadata from the vector store with pagination support.
// If limit < 0, retrieves all metadata in batches ordered by created_at.
func (vs *VectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
	options, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	if options.Limit < 0 && options.Offset < 0 {
		return vs.getAllMetadata(ctx, options)
	}

	return vs.queryMetadataBatch(ctx, options.Limit, options.Offset, options.IDs, options.Filter)
}

func (vs *VectorStore) getAllMetadata(ctx context.Context, options *vectorstore.GetMetadataConfig) (map[string]vectorstore.DocumentMetadata, error) {
	result := make(map[string]vectorstore.DocumentMetadata)

	for offset := 0; ; offset += metadataBatchSize {
		batch, err := vs.queryMetadataBatch(ctx, metadataBatchSize, offset, options.IDs, options.Filter)
		if err != nil {
			return nil, err
		}

		for docID, metadata := range batch {
			result[docID] = metadata
		}

		if len(batch) < metadataBatchSize {
			break
		}
	}

	return result, nil
}

// queryMetadataBatch executes a single metadata query with the given limit and offset
func (vs *VectorStore) queryMetadataBatch(
	ctx context.Context,
	limit,
	offset int,
	ids []string,
	filter map[string]interface{},
) (map[string]vectorstore.DocumentMetadata, error) {
	QueryDocumentParams := tcvectordb.QueryDocumentParams{
		Offset:       int64(offset),
		Limit:        int64(limit),
		Filter:       getCondFromQuery(ids, filter),
		OutputFields: []string{fieldMetadata, fieldID},
		Sort: []tcdocument.SortRule{
			{
				FieldName: fieldCreatedAt,
				Direction: "asc",
			},
		},
	}

	queryResult, err := vs.client.Query(ctx, vs.option.database, vs.option.collection, nil, &QueryDocumentParams)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb get metadata batch: %w", err)
	}

	result := make(map[string]vectorstore.DocumentMetadata)
	for _, tcDoc := range queryResult.Documents {
		metadata := make(map[string]interface{})
		if field, ok := tcDoc.Fields[fieldMetadata]; ok {
			if metaField, ok := field.Val.(map[string]interface{}); ok {
				metadata = metaField
			}
		}
		result[tcDoc.Id] = vectorstore.DocumentMetadata{
			Metadata: metadata,
		}
	}

	return result, nil
}

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	vs.client.Close()
	return nil
}

// convertSearchResult converts tcvectordb search result to vectorstore result.
func (vs *VectorStore) convertSearchResult(
	searchMode vectorstore.SearchMode,
	searchResult *tcvectordb.SearchDocumentResult,
) (*vectorstore.SearchResult, error) {
	if len(searchResult.Documents) == 0 {
		return &vectorstore.SearchResult{
			Results: make([]*vectorstore.ScoredDocument, 0),
		}, nil
	}

	if len(searchResult.Documents) > 1 {
		return nil, fmt.Errorf(
			"tcvectordb search returned multiple document lists, expected 1, got: %d",
			len(searchResult.Documents),
		)
	}

	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(searchResult.Documents[0])),
	}

	for _, tcDoc := range searchResult.Documents[0] {
		log.Debugf("tcvectordb search result: score %v id %v searchMode %v", tcDoc.Score, tcDoc.Id, searchMode)
		doc, err := covertToDocument(tcDoc)
		if err != nil {
			return nil, fmt.Errorf("tcvectordb convert to document: %w", err)
		}
		result.Results = append(result.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    float64(tcDoc.Score),
		})
	}

	return result, nil
}

// convertQueryResult converts tcvectordb query result to vectorstore result.
func (vs *VectorStore) convertQueryResult(queryResult *tcvectordb.QueryDocumentResult) (*vectorstore.SearchResult, error) {
	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(queryResult.Documents)),
	}

	for _, tcDoc := range queryResult.Documents {
		doc, err := covertToDocument(tcDoc)
		if err != nil {
			return nil, fmt.Errorf("tcvectordb convert to document: %w", err)
		}
		// For query results, we assign a default score of 1.0.
		result.Results = append(result.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    1.0,
		})
	}

	return result, nil
}

// covertToDocument converts tcvectordb document to document.Document.
func covertToDocument(tcDoc tcvectordb.Document) (*document.Document, error) {
	doc := &document.Document{
		ID: tcDoc.Id,
	}
	if field, ok := tcDoc.Fields[fieldName]; ok {
		doc.Name = field.String()
	}
	if field, ok := tcDoc.Fields[fieldContent]; ok {
		doc.Content = field.String()
	}
	if field, ok := tcDoc.Fields[fieldCreatedAt]; ok {
		u := min(field.Uint64(), uint64(math.MaxInt64))
		//nolint:gosec // u is not overflowed and the conversion is safe.
		doc.CreatedAt = time.Unix(int64(u), 0)
	}
	if field, ok := tcDoc.Fields[fieldUpdatedAt]; ok {
		u := min(field.Uint64(), uint64(math.MaxInt64))
		//nolint:gosec // u is not overflowed and the conversion is safe.
		doc.UpdatedAt = time.Unix(int64(u), 0)
	}
	if field, ok := tcDoc.Fields[fieldMetadata]; ok {
		if metadata, ok := field.Val.(map[string]any); ok {
			doc.Metadata = metadata
		}
	}

	embedding := make([]float64, len(tcDoc.Vector))
	for i, v := range tcDoc.Vector {
		embedding[i] = float64(v)
	}
	return doc, nil
}

// covertToVector32 converts float64 slice to float32 slice.
func covertToVector32(embedding []float64) []float32 {
	vector32 := make([]float32, len(embedding))
	for i, v := range embedding {
		vector32[i] = float32(v)
	}
	return vector32
}

// getCondFromQuery converts filter to tcvectordb filter.
func getCondFromQuery(ids []string, filter map[string]any) *tcvectordb.Filter {
	if filter == nil && len(ids) == 0 {
		return nil
	}
	cond := tcvectordb.NewFilter("")
	for k, v := range filter {
		cond.And(fmt.Sprintf(`%s = "%v"`, k, v))
	}
	if len(ids) > 0 {
		cond.And(tcvectordb.In(fieldID, ids))
	}
	return cond
}
