//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pgvector provides a PostgreSQL pgvector-based implementation of the VectorStore interface.
package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

var (
	// errDocumentRequired is the error when document is nil.
	errDocumentRequired = errors.New("pgvector document is required")
	// errDocumentIDRequired is the error when document ID is required.
	errDocumentIDRequired = errors.New("pgvector document ID is required")
	// errIDRequired is the error when ID is required.
	errIDRequired = errors.New("pgvector id is required")
)

const (
	// Batch processing constants
	metadataBatchSize = 5000 // Maximum records per batch when querying all metadata
)

// SQL templates for better maintainability and safety.
const (
	sqlCreateTable = `
		CREATE TABLE IF NOT EXISTS %s (
			%s TEXT PRIMARY KEY,            -- Unique document identifier, supports arbitrary length strings
			%s VARCHAR(255),                -- Document name for display and search
			%s TEXT,                        -- Main document content with unlimited length
			%s vector(%d),                  -- Vector embedding for similarity search
			%s JSONB,                       -- Metadata supporting complex structured data and indexing
			%s BIGINT,                      -- Creation timestamp (Unix timestamp)
			%s BIGINT                       -- Update timestamp (Unix timestamp)
		)`

	sqlCreateIndexHNSW = `
		CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s USING hnsw (%s vector_cosine_ops) WITH (m = %d, ef_construction = %d)`

	sqlCreateIndexIVFFlat = `
		CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s USING ivfflat (%s vector_cosine_ops) WITH (lists = %d)`

	sqlCreateTextIndex = `
		CREATE INDEX IF NOT EXISTS %s_content_fts_idx ON %s USING gin (to_tsvector('%s', %s))`

	sqlUpsertDocument = `
		INSERT INTO %s (%s, %s, %s, %s, %s, %s, %s)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (%s) DO UPDATE SET
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s`

	sqlSelectDocument = `SELECT *, 0.0 as score FROM %s WHERE %s = $1 LIMIT 1`

	sqlDeleteDocument = `DELETE FROM %s WHERE %s = $1`

	sqlTruncateTable = `TRUNCATE TABLE %s`
)

// VectorStore is the vector store for pgvector.
type VectorStore struct {
	client          postgres.Client
	option          options
	filterConverter searchfilter.Converter[*condConvertResult]
}

// New creates a new pgvector vector store.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}

	var client postgres.Client
	var err error
	builder := postgres.GetClientBuilder()

	// If instance name is set, use it to create postgres client
	if option.instanceName != "" {
		builderOpts, ok := postgres.GetPostgresInstance(option.instanceName)
		if !ok {
			return nil, fmt.Errorf("postgres instance %s not found", option.instanceName)
		}
		client, err = builder(context.Background(), builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create postgres client from instance name failed: %w", err)
		}
	} else {
		// Build connection string from individual parameters
		// URL encode username and password to handle special characters
		encodedUser := url.QueryEscape(option.user)
		encodedPassword := url.QueryEscape(option.password)
		connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			encodedUser, encodedPassword, option.host, option.port, option.database, option.sslMode)

		builderOpts := []postgres.ClientBuilderOpt{
			postgres.WithClientConnString(connStr),
		}
		if len(option.extraOptions) > 0 {
			builderOpts = append(builderOpts, postgres.WithExtraOptions(option.extraOptions...))
		}

		client, err = builder(context.Background(), builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create postgres client from connection string failed: %w", err)
		}
	}

	vs := &VectorStore{
		client:          client,
		option:          option,
		filterConverter: &pgVectorConverter{metadataFieldName: option.metadataFieldName},
	}

	if err := vs.initDB(context.Background()); err != nil {
		// Close client on initialization failure to prevent resource leak
		_ = client.Close()
		return nil, err
	}

	return vs, nil
}

func (vs *VectorStore) initDB(ctx context.Context) error {
	// Enable pgvector extension.
	_, err := vs.client.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("pgvector enable extension: %w", err)
	}

	// Create table if not exists with detailed column comments.
	createTableSQL := fmt.Sprintf(sqlCreateTable, vs.option.table,
		vs.option.idFieldName,
		vs.option.nameFieldName,
		vs.option.contentFieldName,
		vs.option.embeddingFieldName, vs.option.indexDimension,
		vs.option.metadataFieldName,
		vs.option.createdAtFieldName,
		vs.option.updatedAtFieldName,
	)
	_, err = vs.client.ExecContext(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("pgvector create table: %w", err)
	}

	// Create vector index based on configured type
	if err := vs.createVectorIndex(ctx); err != nil {
		return err
	}

	// If tsvector is enabled, create GIN index for full-text search on content.
	if vs.option.enableTSVector {
		textIndexSQL := fmt.Sprintf(sqlCreateTextIndex, vs.option.table, vs.option.table, vs.option.language, vs.option.contentFieldName)
		_, err = vs.client.ExecContext(ctx, textIndexSQL)
		if err != nil {
			return fmt.Errorf("pgvector create text search index: %w", err)
		}
	}

	return nil
}

// createVectorIndex creates the appropriate vector index based on configuration.
func (vs *VectorStore) createVectorIndex(ctx context.Context) error {
	var indexSQL string

	switch vs.option.vectorIndexType {
	case VectorIndexHNSW:
		// Create HNSW index for fast approximate nearest neighbor search
		// Using cosine distance operator for semantic similarity
		m := defaultHNSWM
		efConstruction := defaultHNSWEfConstruction
		if vs.option.hnswParams != nil {
			if vs.option.hnswParams.M > 0 {
				m = vs.option.hnswParams.M
			}
			if vs.option.hnswParams.EfConstruction > 0 {
				efConstruction = vs.option.hnswParams.EfConstruction
			}
		}
		indexSQL = fmt.Sprintf(sqlCreateIndexHNSW,
			vs.option.table,
			vs.option.table,
			vs.option.embeddingFieldName,
			m,
			efConstruction,
		)
	case VectorIndexIVFFlat:
		// Create IVFFlat index for memory-efficient approximate search
		// Using cosine distance operator for semantic similarity
		lists := defaultIVFFlatLists
		if vs.option.ivfflatParams != nil && vs.option.ivfflatParams.Lists > 0 {
			lists = vs.option.ivfflatParams.Lists
		}
		indexSQL = fmt.Sprintf(sqlCreateIndexIVFFlat,
			vs.option.table,
			vs.option.table,
			vs.option.embeddingFieldName,
			lists,
		)
	default:
		return fmt.Errorf("pgvector unsupported vector index type: %s", vs.option.vectorIndexType)
	}

	_, err := vs.client.ExecContext(ctx, indexSQL)
	if err != nil {
		return fmt.Errorf("pgvector create vector index (%s): %w", vs.option.vectorIndexType, err)
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
		return fmt.Errorf("pgvector embedding is required for %s", doc.ID)
	}
	if len(embedding) != vs.option.indexDimension {
		return fmt.Errorf("pgvector embedding dimension mismatch: expected %d, got %d, table: %s", vs.option.indexDimension, len(embedding), vs.option.table)
	}

	upsertSQL := buildUpsertSQL(vs.option)
	now := time.Now().Unix()
	vector := pgvector.NewVector(convertToFloat32Vector(embedding))
	metadataJSON := mapToJSON(doc.Metadata)

	_, err := vs.client.ExecContext(ctx, upsertSQL, doc.ID, doc.Name, doc.Content, vector, metadataJSON, now, now)
	if err != nil {
		return fmt.Errorf("pgvector insert document: %w", err)
	}

	return nil
}

// Get retrieves a document by ID along with its embedding.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errIDRequired
	}

	querySQL := fmt.Sprintf(sqlSelectDocument, vs.option.table, vs.option.idFieldName)

	var scoredDoc *vectorstore.ScoredDocument
	var embedding []float64
	var found bool

	err := vs.client.Query(ctx, func(rows *sql.Rows) error {
		if !rows.Next() {
			return nil
		}
		found = true

		var err error
		scoredDoc, embedding, err = vs.option.docBuilder(rows)
		if err != nil {
			return fmt.Errorf("build document: %w", err)
		}
		return nil
	}, querySQL, id)

	if err != nil {
		return nil, nil, fmt.Errorf("pgvector get document: %w", err)
	}

	if !found || scoredDoc == nil || scoredDoc.Document == nil {
		return nil, nil, fmt.Errorf("pgvector get document: not found")
	}

	return scoredDoc.Document, embedding, nil
}

// Update modifies an existing document.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}

	// Build update using updateBuilder.
	ub := newUpdateBuilder(vs.option, doc.ID)

	if doc.Name != "" {
		ub.addField(vs.option.nameFieldName, doc.Name)
	}

	if doc.Content != "" {
		ub.addField(vs.option.contentFieldName, doc.Content)
	}

	if len(embedding) > 0 {
		if len(embedding) != vs.option.indexDimension {
			return fmt.Errorf("pgvector embedding dimension mismatch: expected %d, got %d", vs.option.indexDimension, len(embedding))
		}
		ub.addField(vs.option.embeddingFieldName, pgvector.NewVector(convertToFloat32Vector(embedding)))
	}

	if len(doc.Metadata) > 0 {
		ub.addField(vs.option.metadataFieldName, mapToJSON(doc.Metadata))
	}

	updateSQL, args := ub.build()
	result, err := vs.client.ExecContext(ctx, updateSQL, args...)
	if err != nil {
		return fmt.Errorf("pgvector update document: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pgvector get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("pgvector document not updated: %s", doc.ID)
	}
	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errIDRequired
	}

	deleteSQL := fmt.Sprintf(sqlDeleteDocument, vs.option.table, vs.option.idFieldName)

	result, err := vs.client.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		return fmt.Errorf("pgvector delete document: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pgvector get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("pgvector document not found: %s", id)
	}
	return nil
}

// Search performs similarity search and returns the most similar documents.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errors.New("pgvector: query is required")
	}

	if !vs.option.enableTSVector &&
		(query.SearchMode == vectorstore.SearchModeKeyword ||
			query.SearchMode == vectorstore.SearchModeHybrid) {
		log.Infof("pgvector: keyword or hybrid search is not supported when enableTSVector is disabled, use filter/vector search instead")
		if len(query.Vector) > 0 {
			return vs.searchByVector(ctx, query)
		}
		return vs.searchByFilter(ctx, query)
	}

	// default is hybrid search
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
		return nil, fmt.Errorf("pgvector: invalid search mode: %d", query.SearchMode)
	}
}

// searchByVector performs pure vector similarity search
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errors.New("pgvector: searching with a nil or empty vector is not supported")
	}
	if len(query.Vector) != vs.option.indexDimension {
		return nil, fmt.Errorf("pgvector vector dimension mismatch: expected %d, got %d", vs.option.indexDimension, len(query.Vector))
	}

	// Build vector search query
	qb := newVectorQueryBuilder(vs.option)

	// add vector arg, used above
	qb.addVectorArg(pgvector.NewVector(convertToFloat32Vector(query.Vector)))

	if query.MinScore > 0 {
		qb.addScoreFilter(query.MinScore)
	}
	// Add filters
	if err := vs.buildQueryFilter(qb, query.Filter); err != nil {
		return nil, err
	}

	sql, args := qb.build(vs.getMaxResult(query.Limit))
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeVector)
}

// searchByKeyword performs full-text search.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, errors.New("pgvector keyword is required for keyword search")
	}

	// Build keyword search query with full-text search
	qb := newKeywordQueryBuilder(vs.option)

	// Add keyword and score conditions
	qb.addKeywordSearchConditions(query.Query, query.MinScore)

	// Add filters
	if err := vs.buildQueryFilter(qb, query.Filter); err != nil {
		return nil, err
	}

	sql, args := qb.build(vs.getMaxResult(query.Limit))
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeKeyword)
}

// searchByHybrid combines vector similarity and keyword matching.
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	// Check vector dimension and keyword.
	if len(query.Vector) == 0 {
		return nil, errors.New("pgvector vector is required for hybrid search")
	}
	if len(query.Vector) != vs.option.indexDimension {
		return nil, fmt.Errorf("pgvector vector dimension mismatch: expected %d, got %d", vs.option.indexDimension, len(query.Vector))
	}

	vectorWeight := vs.option.vectorWeight
	textWeight := vs.option.textWeight
	if query.Query == "" {
		vectorWeight = 1.0
		textWeight = 0.0
	}

	// Build hybrid search query.
	qb := newHybridQueryBuilder(vs.option, vectorWeight, textWeight)
	qb.addVectorArg(pgvector.NewVector(convertToFloat32Vector(query.Vector)))

	// Add full-text search condition only if query text is provided.
	if query.Query != "" {
		qb.addHybridFtsCondition(query.Query)
	}

	if query.MinScore > 0 {
		qb.addScoreFilter(query.MinScore)
	}

	// Add filters
	if err := vs.buildQueryFilter(qb, query.Filter); err != nil {
		return nil, err
	}

	sql, args := qb.build(vs.getMaxResult(query.Limit))
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeHybrid)
}

// searchByFilter returns documents based on filters only
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	// Build filter-only search query.
	qb := newFilterQueryBuilder(vs.option)

	// Add filters
	if err := vs.buildQueryFilter(qb, query.Filter); err != nil {
		return nil, err
	}

	sql, args := qb.build(vs.getMaxResult(query.Limit))
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeFilter)
}

// executeSearch executes the search query and returns results.
func (vs *VectorStore) executeSearch(ctx context.Context, query string, args []any, searchMode vectorstore.SearchMode) (*vectorstore.SearchResult, error) {
	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0),
	}

	err := vs.client.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			scoredDoc, _, err := vs.option.docBuilder(rows)
			if err != nil {
				return fmt.Errorf("parse document: %w", err)
			}
			var score float64
			var id string
			if scoredDoc != nil && scoredDoc.Document != nil {
				score = scoredDoc.Score
				id = scoredDoc.Document.ID
				result.Results = append(result.Results, scoredDoc)
			}
			log.Debugf("pgvector search result: score: %v id: %v searchMode: %v, query: %v", score, id, searchMode, query)
		}
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("pgvector search documents: %w", err)
	}

	return result, nil
}

// DeleteByFilter deletes documents from the vector store based on filter conditions.
// It supports deletion by document IDs, metadata filters, or all documents.
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
		return fmt.Errorf("pgvector delete all documents, but document ids or filter are provided")
	}
	if !config.DeleteAll && len(config.DocumentIDs) == 0 && len(config.Filter) == 0 {
		return fmt.Errorf("pgvector delete by filter: no filter conditions specified")
	}
	return nil
}

func (vs *VectorStore) deleteAll(ctx context.Context) error {
	truncateSQL := fmt.Sprintf(sqlTruncateTable, vs.option.table)
	if _, err := vs.client.ExecContext(ctx, truncateSQL); err != nil {
		return fmt.Errorf("pgvector delete all documents: %w", err)
	}
	log.Infof("pgvector truncated all documents from table %s", vs.option.table)
	return nil
}

func (vs *VectorStore) deleteByFilter(ctx context.Context, config *vectorstore.DeleteConfig) error {
	dsb := newDeleteSQLBuilder(vs.option)
	if err := vs.buildQueryFilter(dsb, &vectorstore.SearchFilter{
		Metadata: config.Filter,
		IDs:      config.DocumentIDs,
	}); err != nil {
		return fmt.Errorf("pgvector delete by filter: %w", err)
	}

	deleteSQL, args := dsb.build()
	if deleteSQL == "" {
		return fmt.Errorf("pgvector delete by filter: failed to build delete query")
	}

	result, err := vs.client.ExecContext(ctx, deleteSQL, args...)
	if err != nil {
		return fmt.Errorf("pgvector delete by filter: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pgvector get rows affected: %w", err)
	}
	log.Infof("pgvector deleted %d documents by filter", rowsAffected)
	return nil
}

// Count counts the number of documents in the vector store.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	// Create a count query builder
	cqb := newCountQueryBuilder(vs.option)
	if err := vs.buildQueryFilter(cqb, &vectorstore.SearchFilter{
		Metadata: config.Filter,
	}); err != nil {
		return 0, fmt.Errorf("pgvector count documents: %w", err)
	}

	// Build and execute the count query
	query, args := cqb.build()

	var count int
	err := vs.client.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			if err := rows.Scan(&count); err != nil {
				return fmt.Errorf("scan count: %w", err)
			}
		}
		return nil
	}, query, args...)

	if err != nil {
		return 0, fmt.Errorf("pgvector count documents: %w", err)
	}

	return count, nil
}

// GetMetadata retrieves metadata from the vector store with pagination support.
// If limit < 0, retrieves all metadata in batches of 5000 records ordered by created_at.
func (vs *VectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
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

// queryMetadataBatch executes a single metadata query with the given limit and offset
func (vs *VectorStore) queryMetadataBatch(
	ctx context.Context,
	limit,
	offset int,
	docIDs []string,
	filters map[string]any,
) (map[string]vectorstore.DocumentMetadata, error) {
	// Create a metadata query builder
	qb := newMetadataQueryBuilder(vs.option)
	if err := vs.buildQueryFilter(qb, &vectorstore.SearchFilter{
		Metadata: filters,
		IDs:      docIDs,
	}); err != nil {
		return nil, fmt.Errorf("pgvector get metadata: %w", err)
	}

	// Build the query with pagination
	query, args := qb.buildWithPagination(limit, offset)

	result := make(map[string]vectorstore.DocumentMetadata)
	err := vs.client.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			scoredDoc, _, err := vs.option.docBuilder(rows)
			if err != nil {
				return fmt.Errorf("build document: %w", err)
			}
			if scoredDoc == nil || scoredDoc.Document == nil {
				continue
			}

			docID := scoredDoc.Document.ID
			metadata := scoredDoc.Document.Metadata

			result[docID] = vectorstore.DocumentMetadata{
				Metadata: metadata,
			}
		}
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("pgvector get metadata batch: %w", err)
	}

	return result, nil
}

func (vs *VectorStore) getMaxResult(maxResults int) int {
	if maxResults <= 0 {
		return vs.option.maxResults
	}
	return maxResults
}

// Close closes the vector store connection.
// Each VectorStore instance owns its client and should close it when done.
func (vs *VectorStore) Close() error {
	if vs.client == nil {
		return nil
	}
	return vs.client.Close()
}

func (vs *VectorStore) buildQueryFilter(qb queryFilterBuilder, cond *vectorstore.SearchFilter) error {
	if cond == nil {
		return nil
	}

	if len(cond.IDs) > 0 {
		qb.addIDFilter(cond.IDs)
	}
	if len(cond.Metadata) > 0 {
		qb.addMetadataFilter(cond.Metadata)
	}

	if cond.FilterCondition == nil {
		return nil
	}

	filter, err := vs.filterConverter.Convert(cond.FilterCondition)
	if err != nil {
		return err
	}

	qb.addFilterCondition(filter)

	return nil
}

func convertToFloat32Vector(embedding []float64) []float32 {
	vector32 := make([]float32, len(embedding))
	for i, v := range embedding {
		vector32[i] = float32(v)
	}
	return vector32
}

func convertToFloat64Vector(embedding []float32) []float64 {
	vector64 := make([]float64, len(embedding))
	for i, v := range embedding {
		vector64[i] = float64(v)
	}
	return vector64
}

func mapToJSON(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}

	data, err := json.Marshal(m)
	if err != nil {
		// Fallback to empty JSON if marshal fails.
		return "{}"
	}
	return string(data)
}

func jsonToMap(jsonStr string) (map[string]any, error) {
	result := make(map[string]any)
	if jsonStr == "{}" || jsonStr == "" {
		return result, nil
	}

	err := json.Unmarshal([]byte(jsonStr), &result)
	if err != nil {
		// Return empty map if unmarshal fails, but log the error.
		return result, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return result, nil
}
