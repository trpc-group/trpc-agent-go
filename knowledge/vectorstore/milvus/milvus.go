//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package milvus provides a Milvus-based implementation of the VectorStore interface.
package milvus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	internalknowledge "trpc.group/trpc-go/trpc-agent-go/internal/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/storage/milvus"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

var (
	// errDocumentRequired is the error when document is nil.
	errDocumentRequired = errors.New("milvus document is required")
	// errDocumentIDRequired is the error when document ID is required.
	errDocumentIDRequired = errors.New("milvus document ID is required")
	// errIDRequired is the error when ID is required.
	errIDRequired = errors.New("milvus id is required")
	// errQueryRequired is the error when query is required.
	errQueryRequired = errors.New("milvus query is required")
)

// VectorStore is the vector store for Milvus.
type VectorStore struct {
	client          milvus.Client
	option          options
	filterConverter searchfilter.Converter[*convertResult]
}

// New creates a new Milvus vector store.
func New(ctx context.Context, opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}
	option.contentSparseField = fmt.Sprintf("%s_sparse", option.contentField)
	option.allFields = []string{
		option.idField,
		option.nameField,
		option.contentField,
		option.vectorField,
		option.metadataField,
		option.createdAtField,
		option.updatedAtField,
	}

	milvusClient, err := milvus.GetClientBuilder()(ctx,
		milvus.WithAddress(option.address),
		milvus.WithUsername(option.username),
		milvus.WithPassword(option.password),
		milvus.WithDBName(option.dbName),
		milvus.WithAPIKey(option.apiKey),
		milvus.WithDialOptions(option.dialOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("create milvus client failed: %w", err)
	}

	vs := &VectorStore{
		client:          milvusClient,
		option:          option,
		filterConverter: &milvusFilterConverter{metadataFieldName: option.metadataField},
	}

	if err := vs.initCollection(ctx); err != nil {
		_ = milvusClient.Close(ctx)
		return nil, fmt.Errorf("initialize milvus collection failed: %w", err)
	}

	return vs, nil
}

// initCollection initializes the Milvus collection with proper schema and indexes.
func (vs *VectorStore) initCollection(ctx context.Context) error {
	exists, err := vs.client.HasCollection(ctx, client.NewHasCollectionOption(vs.option.collectionName))
	if err != nil {
		return fmt.Errorf("check collection existence failed: %w", err)
	}

	if !exists {
		if err := vs.createCollection(ctx); err != nil {
			return err
		}
	}

	loadTask, err := vs.client.LoadCollection(ctx, client.NewLoadCollectionOption(vs.option.collectionName))
	if err != nil {
		return fmt.Errorf("load collection failed: %w", err)
	}
	if err := loadTask.Await(ctx); err != nil {
		return fmt.Errorf("wait for load collection failed: %w", err)
	}

	return nil
}

// createCollection creates a new collection with the specified schema.
func (vs *VectorStore) createCollection(ctx context.Context) error {
	// Define schema
	schema := &entity.Schema{
		CollectionName: vs.option.collectionName,
		Description:    "trpc-agent-go documents collection",
		AutoID:         false,
		Fields: []*entity.Field{
			entity.NewField().
				WithName(vs.option.idField).
				WithDataType(entity.FieldTypeVarChar).
				WithIsPrimaryKey(true).
				WithMaxLength(1024),
			entity.NewField().
				WithName(vs.option.nameField).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(1024),
			entity.NewField().
				WithName(vs.option.contentField).
				WithDataType(entity.FieldTypeVarChar).
				WithEnableAnalyzer(true).
				WithEnableMatch(true).
				WithMaxLength(65535),
			entity.NewField().
				WithName(vs.option.contentSparseField).
				WithDataType(entity.FieldTypeSparseVector),
			entity.NewField().
				WithName(vs.option.vectorField).
				WithDataType(entity.FieldTypeFloatVector).
				WithDim(int64(vs.option.dimension)),
			entity.NewField().
				WithName(vs.option.metadataField).
				WithDataType(entity.FieldTypeJSON),
			entity.NewField().
				WithName(vs.option.createdAtField).
				WithDataType(entity.FieldTypeInt64).
				WithNullable(true),
			entity.NewField().
				WithName(vs.option.updatedAtField).
				WithDataType(entity.FieldTypeInt64),
		},
	}
	// Add BM25 function for content sparse vector
	// ref: https://milvus.io/docs/zh/full-text-search.md
	schema.WithFunction(entity.NewFunction().
		WithName("text_bm25_emb").
		WithInputFields(vs.option.contentField).
		WithOutputFields(vs.option.contentSparseField).
		WithType(entity.FunctionTypeBM25))

	indexOption := client.NewCreateIndexOption(vs.option.collectionName, vs.option.contentSparseField,
		index.NewAutoIndex(entity.BM25))
	indexOpts := make([]client.CreateIndexOption, 0)
	indexOpts = append(indexOpts, indexOption)
	if vs.option.enableHNSW {
		hnswIndexOption := client.NewCreateIndexOption(vs.option.collectionName, vs.option.vectorField, index.NewHNSWIndex(vs.option.metricType, vs.option.hnswM, vs.option.hnswEfConstruction))
		indexOpts = append(indexOpts, hnswIndexOption)
	}
	if err := vs.client.CreateCollection(ctx, client.NewCreateCollectionOption(vs.option.collectionName, schema).WithIndexOptions(indexOpts...)); err != nil {
		return fmt.Errorf("create collection failed: %w", err)
	}

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
	if len(embedding) == 0 {
		return fmt.Errorf("milvus embedding is required for %s", doc.ID)
	}
	if len(embedding) != vs.option.dimension {
		return fmt.Errorf("milvus embedding dimension mismatch: expected %d, got %d", vs.option.dimension, len(embedding))
	}

	now := time.Now().Unix()
	medataBytes, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("milvus marshal metadata failed: %w", err)
	}

	insertOption := client.NewColumnBasedInsertOption(vs.option.collectionName).
		WithVarcharColumn(vs.option.idField, []string{doc.ID}).
		WithVarcharColumn(vs.option.nameField, []string{doc.Name}).
		WithVarcharColumn(vs.option.contentField, []string{doc.Content}).
		WithFloatVectorColumn(vs.option.vectorField, vs.option.dimension, [][]float32{convertToFloat32Vector(embedding)}).
		WithColumns(column.NewColumnJSONBytes(vs.option.metadataField, [][]byte{medataBytes})).
		WithInt64Column(vs.option.createdAtField, []int64{now}).
		WithInt64Column(vs.option.updatedAtField, []int64{now})
	_, err = vs.client.Insert(ctx, insertOption)
	if err != nil {
		return fmt.Errorf("milvus insert document failed: %w", err)
	}

	return nil
}

// Get retrieves a document by ID along with its embedding.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errIDRequired
	}

	cr, err := vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorEqual,
		Field:    vs.option.idField,
		Value:    id,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("convert filter condition failed: %w", err)
	}

	queryOption := client.NewQueryOption(vs.option.collectionName)
	queryOption.WithFilter(cr.exprStr)
	for key, val := range cr.params {
		queryOption.WithTemplateParam(key, val)
	}
	queryOption.WithOutputFields([]string{"*"}...)
	result, err := vs.client.Query(ctx, queryOption)
	if err != nil {
		return nil, nil, fmt.Errorf("milvus query document failed: %w", err)
	}

	if result.Len() == 0 {
		return nil, nil, fmt.Errorf("milvus document not found: %s", id)
	}

	docs, embeddings, _, err := vs.convertResultToDocument(result)
	if err != nil {
		return nil, nil, fmt.Errorf("convert result to document failed: %w", err)
	}
	if len(docs) == 0 {
		return nil, nil, fmt.Errorf("milvus document not found: %s", id)
	}
	return docs[0], embeddings[0], nil
}

// Update modifies an existing document and its embedding.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}

	exists, err := vs.documentExists(ctx, doc.ID)
	if err != nil {
		return fmt.Errorf("milvus check document existence failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("milvus document not found: %s", doc.ID)
	}

	now := time.Now().Unix()
	medataBytes, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("milvus marshal metadata failed: %w", err)
	}

	// Update document using upsert
	upsertOption := client.NewColumnBasedInsertOption(vs.option.collectionName).WithVarcharColumn(vs.option.idField, []string{doc.ID}).
		WithVarcharColumn(vs.option.nameField, []string{doc.Name}).
		WithVarcharColumn(vs.option.contentField, []string{doc.Content}).
		WithFloatVectorColumn(vs.option.vectorField, vs.option.dimension, [][]float32{convertToFloat32Vector(embedding)}).
		WithColumns(column.NewColumnJSONBytes(vs.option.metadataField, [][]byte{medataBytes})).
		WithInt64Column(vs.option.updatedAtField, []int64{now})
	upsertRes, err := vs.client.Upsert(ctx, upsertOption)
	if err != nil {
		return fmt.Errorf("milvus update document failed: %w", err)
	}
	if upsertRes.UpsertCount != 1 {
		return fmt.Errorf("milvus update document failed: affected count %d", upsertRes.UpsertCount)
	}

	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errIDRequired
	}

	deleteOption := client.NewDeleteOption(vs.option.collectionName)
	deleteOption.WithStringIDs(vs.option.idField, []string{id})
	_, err := vs.client.Delete(ctx, deleteOption)
	if err != nil {
		return fmt.Errorf("milvus delete document failed: %w", err)
	}

	return nil
}

// Search performs similarity search and returns the most similar documents.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errQueryRequired
	}

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		if len(query.Vector) == 0 {
			return nil, errors.New("milvus: vector is required for vector search mode")
		}
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeKeyword:
		if query.Query == "" {
			return nil, errors.New("milvus: query text is required for keyword search mode")
		}
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeHybrid:
		if len(query.Vector) == 0 || query.Query == "" {
			return nil, errors.New("milvus: both vector and query text are required for hybrid search mode")
		}
		return vs.searchByHybrid(ctx, query)
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	default:
		// Default behavior: use vector search if vector is provided, otherwise filter search
		if len(query.Vector) > 0 {
			return vs.searchByVector(ctx, query)
		}
		return vs.searchByFilter(ctx, query)
	}
}

// searchByVector performs vector similarity search.
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	var sp *index.CustomAnnParam
	if query.MinScore > 0 {
		ann := index.NewCustomAnnParam()
		ann.WithRadius(query.MinScore)
		sp = &ann
	}

	vector := convertToFloat32Vector(query.Vector)

	filterExpr := ""
	var filterParams map[string]any
	if query.Filter != nil {
		expr, params, err := vs.buildFilterExpression(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("build filter expression failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	// Perform search
	searchOption := client.NewSearchOption(vs.option.collectionName, vs.getMaxResults(query.Limit), []entity.Vector{entity.FloatVector(vector)})
	searchOption.WithANNSField(vs.option.vectorField)
	if sp != nil {
		searchOption.WithAnnParam(sp)
	}
	if filterExpr != "" {
		searchOption.WithFilter(filterExpr)
		for k, v := range filterParams {
			searchOption.WithTemplateParam(k, v)
		}
	}
	searchOption.WithOutputFields([]string{"*"}...)
	result, err := vs.client.Search(ctx, searchOption)
	if err != nil {
		return nil, fmt.Errorf("milvus vector search failed: %w", err)
	}

	return vs.convertSearchResult(result, query.SearchMode)
}

// searchByKeyword performs keyword-based search using BM25.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {

	filterExpr := ""
	var filterParams map[string]any
	if query.Filter != nil {
		expr, params, err := vs.buildFilterExpression(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("build filter expression failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	var sp *index.CustomAnnParam
	if query.MinScore > 0 {
		ann := index.NewCustomAnnParam()
		ann.WithRadius(query.MinScore)
		sp = &ann
	}

	// Perform search
	searchOption := client.NewSearchOption(vs.option.collectionName, vs.getMaxResults(query.Limit), []entity.Vector{entity.Text(query.Query)})
	searchOption.WithANNSField(vs.option.contentSparseField)
	if sp != nil {
		searchOption.WithAnnParam(sp)
	}
	if filterExpr != "" {
		searchOption.WithFilter(filterExpr)
		for k, v := range filterParams {
			searchOption.WithTemplateParam(k, v)
		}
	}
	searchOption.WithOutputFields([]string{"*"}...)
	result, err := vs.client.Search(ctx, searchOption)
	if err != nil {
		return nil, fmt.Errorf("milvus keyword search failed: %w", err)
	}

	return vs.convertSearchResult(result, query.SearchMode)
}

// searchByHybrid performs hybrid search combining vector and keyword search.
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	filterExpr := ""
	var filterParams map[string]any
	if query.Filter != nil {
		expr, params, err := vs.buildFilterExpression(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("build filter expression failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	vector := convertToFloat32Vector(query.Vector)

	annReqs := make([]*client.AnnRequest, 0)
	if len(vector) > 0 {
		annReq := client.NewAnnRequest(vs.option.vectorField, query.Limit, entity.FloatVector(vector))
		if filterExpr != "" {
			annReq.WithFilter(filterExpr)
			for k, v := range filterParams {
				annReq.WithTemplateParam(k, v)
			}
		}
		annReqs = append(annReqs, annReq)
	}

	if query.Query != "" {
		annReq := client.NewAnnRequest(vs.option.contentSparseField, query.Limit, entity.Text(query.Query)).WithANNSField(vs.option.contentSparseField)
		if filterExpr != "" {
			annReq.WithFilter(filterExpr)
			for k, v := range filterParams {
				annReq.WithTemplateParam(k, v)
			}
		}
		annReqs = append(annReqs, annReq)
	}

	hybridOption := client.NewHybridSearchOption(vs.option.collectionName, vs.getMaxResults(query.Limit), annReqs...)
	hybridOption.WithConsistencyLevel(entity.ClBounded)
	hybridOption.WithOutputFields([]string{"*"}...)
	if vs.option.reranker != nil {
		hybridOption.WithReranker(vs.option.reranker)
	}

	result, err := vs.client.HybridSearch(ctx, hybridOption)
	if err != nil {
		return nil, fmt.Errorf("milvus hybrid search failed: %w", err)
	}

	return vs.convertSearchResult(result, query.SearchMode)
}

// searchByFilter performs filter-based search.
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	filterExpr := ""
	var filterParams map[string]any
	if query.Filter != nil {
		expr, params, err := vs.buildFilterExpression(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("build filter expression failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	// Filter-only search requires a non-empty filter expression
	if filterExpr == "" {
		return nil, fmt.Errorf("empty filter condition")
	}

	queryOption := client.NewQueryOption(vs.option.collectionName)
	if filterExpr != "" {
		queryOption.WithFilter(filterExpr)
		for k, v := range filterParams {
			queryOption.WithTemplateParam(k, v)
		}
	}
	queryOption.WithOutputFields([]string{"*"}...)
	queryOption.WithLimit(vs.getMaxResults(query.Limit))
	result, err := vs.client.Query(ctx, queryOption)
	if err != nil {
		return nil, fmt.Errorf("milvus filter search failed: %w", err)
	}

	return vs.convertQueryResult(result, query.SearchMode)
}

// DeleteByFilter deletes documents from the vector store based on filter conditions.
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

func (vs *VectorStore) validateDeleteConfig(config *vectorstore.DeleteConfig) error {
	if config.DeleteAll && (len(config.DocumentIDs) > 0 || len(config.Filter) > 0) {
		return fmt.Errorf("milvus delete all documents, but document ids or filter are provided")
	}
	if !config.DeleteAll && len(config.DocumentIDs) == 0 && len(config.Filter) == 0 {
		return fmt.Errorf("milvus delete by filter: no filter conditions specified")
	}
	return nil
}

func (vs *VectorStore) deleteAll(ctx context.Context) error {
	// Delete all documents by using an empty filter (which matches all)
	deleteOption := client.NewDeleteOption(vs.option.collectionName)
	deleteOption.WithExpr("")
	_, err := vs.client.Delete(ctx, deleteOption)
	if err != nil {
		return fmt.Errorf("milvus delete all documents failed: %w", err)
	}
	return nil
}

func (vs *VectorStore) deleteByFilter(ctx context.Context, config *vectorstore.DeleteConfig) error {
	filterExpr, err := vs.buildDeleteFilterExpression(config)
	if err != nil {
		return fmt.Errorf("build delete filter expression failed: %w", err)
	}

	deleteOption := client.NewDeleteOption(vs.option.collectionName)
	deleteOption.WithExpr(filterExpr)
	_, err = vs.client.Delete(ctx, deleteOption)
	if err != nil {
		return fmt.Errorf("milvus delete by filter failed: %w", err)
	}

	return nil
}

// Count counts the number of documents in the vector store.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	filterExpr := ""
	var filterParams map[string]any
	if config.Filter != nil {
		expr, params, err := vs.buildFilterExpression(&vectorstore.SearchFilter{
			Metadata: config.Filter,
		})
		if err != nil {
			return 0, fmt.Errorf("build filter expression failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	queryOption := client.NewQueryOption(vs.option.collectionName)
	if filterExpr != "" {
		queryOption.WithFilter(filterExpr)
		for k, v := range filterParams {
			queryOption.WithTemplateParam(k, v)
		}
	}
	queryOption.WithOutputFields([]string{vs.option.idField}...)
	result, err := vs.client.Query(ctx, queryOption)
	if err != nil {
		return 0, fmt.Errorf("milvus count documents failed: %w", err)
	}

	return result.Len(), nil
}

// GetMetadata retrieves metadata from the vector store with pagination support.
func (vs *VectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	if config.Limit < 0 && config.Offset < 0 {
		return vs.getAllMetadata(ctx, config)
	}

	return vs.queryMetadataBatch(ctx, config.Limit, config.Offset, config.IDs, config.Filter)
}

func (vs *VectorStore) getAllMetadata(ctx context.Context, config *vectorstore.GetMetadataConfig) (map[string]vectorstore.DocumentMetadata, error) {
	result := make(map[string]vectorstore.DocumentMetadata)

	for offset := 0; ; offset += metadataBatchSize {
		batch, err := vs.queryMetadataBatch(ctx, metadataBatchSize, offset, config.IDs, config.Filter)
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

func (vs *VectorStore) queryMetadataBatch(ctx context.Context, limit, offset int, ids []string, filter map[string]any) (map[string]vectorstore.DocumentMetadata, error) {
	filterExpr := ""
	var filterParams map[string]any
	if len(filter) > 0 || len(ids) > 0 {
		expr, params, err := vs.buildFilterExpression(&vectorstore.SearchFilter{
			IDs:      ids,
			Metadata: filter,
		})
		if err != nil {
			return nil, fmt.Errorf("build metadata filter failed: %w", err)
		}
		filterExpr = expr
		filterParams = params
	}

	queryOption := client.NewQueryOption(vs.option.collectionName)
	if filterExpr != "" {
		queryOption.WithFilter(filterExpr)
		for k, v := range filterParams {
			queryOption.WithTemplateParam(k, v)
		}
	}
	queryOption.WithOutputFields([]string{"*"}...)
	if limit > 0 {
		queryOption.WithLimit(limit)
	}
	if offset > 0 {
		queryOption.WithOffset(offset)
	}
	result, err := vs.client.Query(ctx, queryOption)
	if err != nil {
		return nil, fmt.Errorf("milvus get metadata batch failed: %w", err)
	}

	return vs.convertMetadataResult(result)
}

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	if vs.client == nil {
		return nil
	}
	return vs.client.Close(context.Background())
}

func (vs *VectorStore) documentExists(ctx context.Context, id string) (bool, error) {
	cr, err := vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorEqual,
		Field:    vs.option.idField,
		Value:    id,
	})
	if err != nil {
		return false, fmt.Errorf("convert filter condition failed: %w", err)
	}

	queryOption := client.NewQueryOption(vs.option.collectionName)
	queryOption.WithFilter(cr.exprStr)
	for key, val := range cr.params {
		queryOption.WithTemplateParam(key, val)
	}
	queryOption.WithOutputFields([]string{vs.option.idField}...)
	queryOption.WithLimit(1)
	result, err := vs.client.Query(ctx, queryOption)
	if err != nil {
		return false, fmt.Errorf("milvus check document existence failed: %w", err)
	}

	return result.Len() > 0, nil
}

func (vs *VectorStore) getMaxResults(maxResults int) int {
	if maxResults <= 0 {
		return vs.option.maxResults
	}
	return maxResults
}

// buildFilterExpression builds a filter expression from SearchFilter.
// Returns the filter expression string and parameter map for templated queries.
func (vs *VectorStore) buildFilterExpression(filter *vectorstore.SearchFilter) (string, map[string]any, error) {
	if filter == nil {
		return "", nil, nil
	}

	var filters []*searchfilter.UniversalFilterCondition
	// Filter by document IDs.
	if len(filter.IDs) > 0 {
		filters = append(filters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorIn,
			Field:    vs.option.idField,
			Value:    filter.IDs,
		})
	}

	// Filter by metadata.
	for key, value := range filter.Metadata {
		// Ensure metadata keys have the correct prefix for the converter
		if !strings.HasPrefix(key, source.MetadataFieldPrefix) {
			key = source.MetadataFieldPrefix + key
		}
		filters = append(filters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorEqual,
			Field:    key,
			Value:    value,
		})
	}

	if filter.FilterCondition != nil {
		filters = append(filters, filter.FilterCondition)
	}

	if len(filters) == 0 {
		return "", nil, nil
	}

	condFilter, err := vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    filters,
	})
	if err != nil {
		return "", nil, err
	}
	if condFilter == nil || condFilter.exprStr == "" {
		return "", nil, errors.New("empty filter expression")
	}
	return condFilter.exprStr, condFilter.params, nil
}

// buildDeleteFilterExpression milvus delete does not support template parameters
// so we need to build the filter expression manually
func (vs *VectorStore) buildDeleteFilterExpression(filter *vectorstore.DeleteConfig) (string, error) {
	if filter == nil {
		return "", fmt.Errorf("filter cannot be nil")
	}

	var conditions []string

	if len(filter.DocumentIDs) > 0 {
		ids := make([]string, 0, len(filter.DocumentIDs))
		for _, id := range filter.DocumentIDs {
			ids = append(ids, formatValue(id))
		}
		expr := fmt.Sprintf("%s in [%s]", vs.option.idField, strings.Join(ids, ","))
		conditions = append(conditions, expr)
	}
	if len(filter.Filter) > 0 {
		for key, value := range filter.Filter {
			expr := fmt.Sprintf("%s[\"%s\"] == %s", vs.option.metadataField, key, formatValue(value))
			conditions = append(conditions, expr)
		}
	}
	if len(conditions) == 0 {
		return "", fmt.Errorf("empty filter condition")
	}

	var finalExpr string
	if len(conditions) == 1 {
		finalExpr = conditions[0]
	} else {
		finalExpr = "(" + strings.Join(conditions, " and ") + ")"
	}

	return finalExpr, nil
}

func (vs *VectorStore) convertResultToDocument(result client.ResultSet) ([]*document.Document, [][]float64, []float64, error) {
	if result.Len() == 0 {
		return nil, nil, nil, errors.New("no results found")
	}
	resultLen := result.Fields[0].Len()

	var docs []*document.Document
	var embeddings [][]float64
	var scores []float64
	for _, score := range result.Scores {
		scores = append(scores, float64(score))
	}
	if vs.option.docBuilder != nil {
		for i := 0; i < resultLen; i++ {
			row := make([]column.Column, 0, len(result.Fields))
			for _, field := range result.Fields {
				col := result.GetColumn(field.Name())
				if col == nil || col.Len() == 0 {
					continue
				}
				_, err := col.Get(i)
				if err != nil {
					continue
				}
				newCol, err := column.FieldDataColumn(col.FieldData(), i, i+1)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("make column failed: %w", err)
				}
				row = append(row, newCol)
			}
			doc, embedding, err := vs.option.docBuilder(row)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("build document failed: %w", err)
			}
			if doc == nil {
				continue
			}
			docs = append(docs, doc)
			embeddings = append(embeddings, embedding)
		}
		return docs, embeddings, scores, nil
	}
	for i := 0; i < resultLen; i++ {
		docs = append(docs, &document.Document{})
	}
	for _, field := range vs.option.allFields {
		columns := result.GetColumn(field)
		if columns == nil || columns.Len() == 0 {
			continue
		}
		if field == vs.option.idField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get id failed: %w", err)
				}
				docs[i].ID = val
			}
		}
		if field == vs.option.nameField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get name failed: %w", err)
				}
				docs[i].Name = val
			}
		}
		if field == vs.option.contentField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get content failed: %w", err)
				}
				docs[i].Content = val
			}
		}
		if field == vs.option.vectorField {
			vectorColumn, ok := columns.(*column.ColumnDoubleArray)
			if !ok {
				continue
			}
			for i := 0; i < vectorColumn.Len(); i++ {
				val, err := vectorColumn.Value(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get vector failed: %w", err)
				}
				embedding := make([]float64, len(val))
				for j, v := range val {
					embedding[j] = float64(v)
				}
				embeddings = append(embeddings, embedding)
			}
		}
		if field == vs.option.metadataField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.Get(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get metadata failed: %w", err)
				}
				if metadataBytes, ok := val.([]byte); ok {
					var metadata map[string]any
					if err := json.Unmarshal(metadataBytes, &metadata); err == nil {
						docs[i].Metadata = metadata
					}
				} else if metadata, ok := val.(map[string]any); ok {
					docs[i].Metadata = metadata
				}
			}
		}
		if field == vs.option.createdAtField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsInt64(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get created at failed: %w", err)
				}
				docs[i].CreatedAt = time.Unix(val, 0)
			}
		}
		if field == vs.option.updatedAtField {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsInt64(i)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("get updated at failed: %w", err)
				}
				docs[i].UpdatedAt = time.Unix(val, 0)
			}
		}
	}

	return docs, embeddings, scores, nil
}

func (vs *VectorStore) convertSearchResult(result []client.ResultSet, searchMode vectorstore.SearchMode) (*vectorstore.SearchResult, error) {
	searchResult := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0),
	}

	if len(result) == 0 {
		return searchResult, nil
	}

	if len(result) > 1 {
		return nil, fmt.Errorf("milvus search returned multiple result sets, expected 1, got: %d", len(result))
	}

	docs, _, scores, err := vs.convertResultToDocument(result[0])
	if err != nil {
		return nil, fmt.Errorf("convert result to document failed: %w", err)
	}

	// Normalize scores based on metric type
	normalizedScores := vs.normalizeScores(scores, searchMode)

	for i, doc := range docs {
		searchResult.Results = append(searchResult.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    normalizedScores[i],
		})
	}

	return searchResult, nil
}

func (vs *VectorStore) convertQueryResult(result client.ResultSet, searchMode vectorstore.SearchMode) (*vectorstore.SearchResult, error) {
	searchResult := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, result.Len()),
	}

	if result.Len() == 0 {
		return searchResult, nil
	}

	docs, _, scores, err := vs.convertResultToDocument(result)
	if err != nil {
		return nil, fmt.Errorf("convert result to document failed: %w", err)
	}

	// For filter-only queries, Query API doesn't return scores
	// Assign default score of 1.0 to all documents
	if len(scores) == 0 {
		for _, doc := range docs {
			searchResult.Results = append(searchResult.Results, &vectorstore.ScoredDocument{
				Document: doc,
				Score:    1.0,
			})
		}
		return searchResult, nil
	}

	// Normalize scores based on metric type
	normalizedScores := vs.normalizeScores(scores, searchMode)

	for i, doc := range docs {
		searchResult.Results = append(searchResult.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    normalizedScores[i],
		})
	}
	return searchResult, nil
}

func (vs *VectorStore) convertMetadataResult(result client.ResultSet) (map[string]vectorstore.DocumentMetadata, error) {
	metadataMap := make(map[string]vectorstore.DocumentMetadata)

	if result.Len() == 0 {
		return metadataMap, nil
	}

	docs, _, _, err := vs.convertResultToDocument(result)
	if err != nil {
		return nil, fmt.Errorf("convert result to document failed: %w", err)
	}
	for _, doc := range docs {
		metadataMap[doc.ID] = vectorstore.DocumentMetadata{
			Metadata: doc.Metadata,
		}
	}
	return metadataMap, nil
}

func convertToFloat32Vector(embedding []float64) []float32 {
	vector32 := make([]float32, len(embedding))
	for i, v := range embedding {
		vector32[i] = float32(v)
	}
	return vector32
}

// normalizeScores normalizes raw scores based on the metric type and search mode.
// After normalization, higher scores always indicate better similarity (range [0, 1]).
func (vs *VectorStore) normalizeScores(scores []float64, searchMode int) []float64 {
	if len(scores) == 0 {
		return scores
	}

	result := make([]float64, len(scores))

	// Determine metric type based on search mode
	var metricType internalknowledge.MetricType
	switch searchMode {
	case vectorstore.SearchModeKeyword:
		// BM25 sparse vector search
		metricType = internalknowledge.MetricTypeBM25
	case vectorstore.SearchModeHybrid:
		// Hybrid search scores are already fused by reranker
		// Use min-max normalization for hybrid results
		return internalknowledge.MinMaxNormalize(scores)
	default:
		// Vector search - use configured metric type
		metricType = vs.metricTypeToInternal()
	}

	for i, score := range scores {
		result[i] = internalknowledge.NormalizeScore(score, metricType)
	}
	return result
}

// metricTypeToInternal converts Milvus entity.MetricType to internal MetricType.
func (vs *VectorStore) metricTypeToInternal() internalknowledge.MetricType {
	switch vs.option.metricType {
	case entity.L2:
		return internalknowledge.MetricTypeL2
	case entity.IP:
		return internalknowledge.MetricTypeIP
	case entity.COSINE:
		return internalknowledge.MetricTypeCosine
	default:
		return internalknowledge.MetricTypeIP
	}
}
