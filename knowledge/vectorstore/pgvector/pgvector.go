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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
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

var (
	fieldID        = "id"
	fieldUpdatedAt = "updated_at"
	fieldCreatedAt = "created_at"
	fieldName      = "name"
	fieldContent   = "content"
	fieldVector    = "embedding"
	fieldMetadata  = "metadata"
	defaultLimit   = 10
)

const (
	// Batch processing constants
	metadataBatchSize = 5000 // Maximum records per batch when querying all metadata
)

// SQL templates for better maintainability and safety.
const (
	sqlCreateTable = `
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,                    -- Unique document identifier, supports arbitrary length strings
			name VARCHAR(255),                      -- Document name for display and search
			content TEXT,                           -- Main document content with unlimited length
			embedding vector(%d),                   -- Vector embedding for similarity search
			metadata JSONB,                         -- Metadata supporting complex structured data and indexing
			created_at BIGINT,                      -- Creation timestamp (Unix timestamp)
			updated_at BIGINT                       -- Update timestamp (Unix timestamp)
		)`

	sqlCreateIndex = `
		CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s USING hnsw (embedding vector_cosine_ops) WITH (m = 32, ef_construction = 400)`

	sqlCreateTextIndex = `
		CREATE INDEX IF NOT EXISTS %s_content_fts_idx ON %s USING gin (to_tsvector('%s', content))`

	sqlUpsertDocument = `
		INSERT INTO %s (id, name, content, embedding, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			metadata = EXCLUDED.metadata,
			updated_at = EXCLUDED.updated_at`

	sqlSelectDocument = `SELECT id, name, content, embedding, metadata, created_at, updated_at FROM %s WHERE id = $1 LIMIT 1`

	sqlDeleteDocument = `DELETE FROM %s WHERE id = $1`

	sqlTruncateTable = `TRUNCATE TABLE %s`

	sqlDocumentExists = `SELECT 1 FROM %s WHERE id = $1`

	// Metadata query templates
	sqlGetAllMetadata = `SELECT id, metadata FROM %s ORDER BY created_at LIMIT $1 OFFSET $2`
)

// VectorStore is the vector store for pgvector.
type VectorStore struct {
	pool   *pgxpool.Pool
	option options
}

// New creates a new pgvector vector store.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}

	if option.user == "" {
		return nil, errors.New("pgvector user is required")
	}
	if option.password == "" {
		return nil, errors.New("pgvector password is required")
	}

	// Build connection string.
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		option.host, option.port, option.user, option.password, option.database, option.sslMode)

	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		return nil, fmt.Errorf("pgvector create connection pool: %w", err)
	}

	vs := &VectorStore{
		pool:   pool,
		option: option,
	}

	if err := vs.initDB(context.Background()); err != nil {
		return nil, err
	}

	return vs, nil
}

func (vs *VectorStore) initDB(ctx context.Context) error {
	// Enable pgvector extension.
	_, err := vs.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("pgvector enable extension: %w", err)
	}

	// Create table if not exists with detailed column comments.
	createTableSQL := fmt.Sprintf(sqlCreateTable, vs.option.table, vs.option.indexDimension)
	_, err = vs.pool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("pgvector create table: %w", err)
	}

	// Create HNSW vector index for faster similarity search.
	// Using cosine distance operator for semantic similarity.
	indexSQL := fmt.Sprintf(sqlCreateIndex, vs.option.table, vs.option.table)
	_, err = vs.pool.Exec(ctx, indexSQL)
	if err != nil {
		return fmt.Errorf("pgvector create vector index: %w", err)
	}

	// If tsvector is enabled, create GIN index for full-text search on content.
	if vs.option.enableTSVector {
		textIndexSQL := fmt.Sprintf(sqlCreateTextIndex, vs.option.table, vs.option.table, vs.option.language)
		_, err = vs.pool.Exec(ctx, textIndexSQL)
		if err != nil {
			return fmt.Errorf("pgvector create text search index: %w", err)
		}
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

	upsertSQL := fmt.Sprintf(sqlUpsertDocument, vs.option.table)
	now := time.Now().Unix()
	vector := pgvector.NewVector(convertToFloat32Vector(embedding))
	metadataJSON := mapToJSON(doc.Metadata)

	_, err := vs.pool.Exec(ctx, upsertSQL, doc.ID, doc.Name, doc.Content, vector, metadataJSON, now, now)
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

	querySQL := fmt.Sprintf(sqlSelectDocument, vs.option.table)
	var docID, name, content, metadataJSON string
	var embedding pgvector.Vector
	var createdAt, updatedAt int64

	err := vs.pool.QueryRow(ctx, querySQL, id).Scan(&docID, &name, &content, &embedding, &metadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("pgvector get document: %w", err)
	}

	metadata, err := jsonToMap(metadataJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("pgvector parse metadata: %w", err)
	}

	doc := &document.Document{
		ID:        docID,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Unix(createdAt, 0),
		UpdatedAt: time.Unix(updatedAt, 0),
	}

	return doc, convertToFloat64Vector(embedding.Slice()), nil
}

// Update modifies an existing document.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}

	// Check if document exists.
	exists, err := vs.documentExists(ctx, doc.ID)
	if err != nil {
		return fmt.Errorf("pgvector check document existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("pgvector document not found: %s", doc.ID)
	}

	// Build update using updateBuilder.
	ub := newUpdateBuilder(vs.option.table, doc.ID)

	if doc.Name != "" {
		ub.addField("name", doc.Name)
	}

	if doc.Content != "" {
		ub.addField("content", doc.Content)
	}

	if len(embedding) > 0 {
		if len(embedding) != vs.option.indexDimension {
			return fmt.Errorf("pgvector embedding dimension mismatch: expected %d, got %d", vs.option.indexDimension, len(embedding))
		}
		ub.addField("embedding", pgvector.NewVector(convertToFloat32Vector(embedding)))
	}

	if len(doc.Metadata) > 0 {
		ub.addField("metadata", mapToJSON(doc.Metadata))
	}

	updateSQL, args := ub.build()
	result, err := vs.pool.Exec(ctx, updateSQL, args...)
	if err != nil {
		return fmt.Errorf("pgvector update document: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("pgvector document not updated: %s", doc.ID)
	}
	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errIDRequired
	}

	deleteSQL := fmt.Sprintf(sqlDeleteDocument, vs.option.table)

	result, err := vs.pool.Exec(ctx, deleteSQL, id)
	if err != nil {
		return fmt.Errorf("pgvector delete document: %w", err)
	}

	if result.RowsAffected() == 0 {
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

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build vector search query
	qb := newVectorQueryBuilder(vs.option.table, vs.option.language)

	// add vector arg, used above
	qb.addVectorArg(pgvector.NewVector(convertToFloat32Vector(query.Vector)))

	// Add filters
	if query.Filter != nil && len(query.Filter.IDs) > 0 {
		qb.addIDFilter(query.Filter.IDs)
	}

	if query.Filter != nil && len(query.Filter.Metadata) > 0 {
		qb.addMetadataFilter(query.Filter.Metadata)
	}

	if query.MinScore > 0 {
		qb.addScoreFilter(query.MinScore)
	}

	sql, args := qb.build(limit)
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeVector)
}

// searchByKeyword performs full-text search.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, errors.New("pgvector keyword is required for keyword search")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build keyword search query with full-text search
	qb := newKeywordQueryBuilder(vs.option.table, vs.option.language)

	// Add keyword and score conditions
	qb.addKeywordSearchConditions(query.Query, query.MinScore)

	// Add filters
	if query.Filter != nil && len(query.Filter.IDs) > 0 {
		qb.addIDFilter(query.Filter.IDs)
	}

	if query.Filter != nil && len(query.Filter.Metadata) > 0 {
		qb.addMetadataFilter(query.Filter.Metadata)
	}

	sql, args := qb.build(limit)
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

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build hybrid search query.
	qb := newHybridQueryBuilder(vs.option.table, vs.option.language, vectorWeight, textWeight)
	qb.addVectorArg(pgvector.NewVector(convertToFloat32Vector(query.Vector)))

	// Add full-text search condition only if query text is provided.
	if query.Query != "" {
		qb.addHybridFtsCondition(query.Query)
	}

	// Add filters.
	if query.Filter != nil && len(query.Filter.IDs) > 0 {
		qb.addIDFilter(query.Filter.IDs)
	}

	if query.Filter != nil && len(query.Filter.Metadata) > 0 {
		qb.addMetadataFilter(query.Filter.Metadata)
	}

	if query.MinScore > 0 {
		qb.addScoreFilter(query.MinScore)
	}

	sql, args := qb.build(limit)
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeHybrid)
}

// searchByFilter returns documents based on filters only
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build filter-only search query.
	qb := newFilterQueryBuilder(vs.option.table, vs.option.language)

	// Add filters.
	if query.Filter != nil && len(query.Filter.IDs) > 0 {
		qb.addIDFilter(query.Filter.IDs)
	}

	if query.Filter != nil && len(query.Filter.Metadata) > 0 {
		qb.addMetadataFilter(query.Filter.Metadata)
	}

	sql, args := qb.build(limit)
	return vs.executeSearch(ctx, sql, args, vectorstore.SearchModeFilter)
}

// executeSearch executes the search query and returns results.
func (vs *VectorStore) executeSearch(ctx context.Context, sql string, args []any, searchMode vectorstore.SearchMode) (*vectorstore.SearchResult, error) {
	rows, err := vs.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector search documents: %w", err)
	}
	defer rows.Close()

	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0),
	}

	for rows.Next() {
		var docID, name, content, metadataJSON string
		var embedding pgvector.Vector
		var createdAt, updatedAt int64
		var score float64

		err := rows.Scan(&docID, &name, &content, &embedding, &metadataJSON, &createdAt, &updatedAt, &score)
		if err != nil {
			return nil, fmt.Errorf("pgvector scan document: %w", err)
		}

		metadata, err := jsonToMap(metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("pgvector parse metadata: %w", err)
		}

		log.Debugf("pgvector search result: score: %v id: %v searchMode: %v, sql: %v", score, docID, searchMode, sql)
		doc := &document.Document{
			ID:        docID,
			Name:      name,
			Content:   content,
			Metadata:  metadata,
			CreatedAt: time.Unix(createdAt, 0),
			UpdatedAt: time.Unix(updatedAt, 0),
		}

		result.Results = append(result.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    score,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector iterate rows: %w", err)
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
	if _, err := vs.pool.Exec(ctx, truncateSQL); err != nil {
		return fmt.Errorf("pgvector delete all documents: %w", err)
	}
	log.Infof("pgvector truncated all documents from table %s", vs.option.table)
	return nil
}

func (vs *VectorStore) deleteByFilter(ctx context.Context, config *vectorstore.DeleteConfig) error {
	dsb := newDeleteSQLBuilder(vs.option.table)

	if len(config.DocumentIDs) > 0 {
		dsb.addIDFilter(config.DocumentIDs)
	}
	if len(config.Filter) > 0 {
		dsb.addMetadataFilter(config.Filter)
	}

	deleteSQL, args := dsb.build()
	if deleteSQL == "" {
		return fmt.Errorf("pgvector delete by filter: failed to build delete query")
	}

	result, err := vs.pool.Exec(ctx, deleteSQL, args...)
	if err != nil {
		return fmt.Errorf("pgvector delete by filter: %w", err)
	}

	log.Infof("pgvector deleted %d documents by filter", result.RowsAffected())
	return nil
}

// Count counts the number of documents in the vector store.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	// Create a count query builder
	cqb := newCountQueryBuilder(vs.option.table)

	// Add metadata filter if provided
	if len(config.Filter) > 0 {
		cqb.addMetadataFilter(config.Filter)
	}

	// Build and execute the count query
	query, args := cqb.build()

	var count int
	err := vs.pool.QueryRow(ctx, query, args...).Scan(&count)
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
	qb := newMetadataQueryBuilder(vs.option.table)

	// Add ID filter if provided
	if len(docIDs) > 0 {
		qb.addIDFilter(docIDs)
	}

	// Add metadata filter if provided
	if len(filters) > 0 {
		qb.addMetadataFilter(filters)
	}

	// Build the query with pagination
	query, args := qb.buildWithPagination(limit, offset)

	rows, err := vs.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector get metadata batch: %w", err)
	}
	defer rows.Close()

	result := make(map[string]vectorstore.DocumentMetadata)
	for rows.Next() {
		var docID string
		var metadataJSON string

		err := rows.Scan(&docID, &metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("pgvector scan metadata: %w", err)
		}

		metadata, err := jsonToMap(metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("pgvector parse metadata: %w", err)
		}

		result[docID] = vectorstore.DocumentMetadata{
			Metadata: metadata,
		}
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector iterate metadata rows: %w", err)
	}

	return result, nil
}

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	vs.pool.Close()
	return nil
}

// Helper functions.
func (vs *VectorStore) documentExists(ctx context.Context, id string) (bool, error) {
	querySQL := fmt.Sprintf(sqlDocumentExists, vs.option.table)
	var exists int
	err := vs.pool.QueryRow(ctx, querySQL, id).Scan(&exists)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
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
