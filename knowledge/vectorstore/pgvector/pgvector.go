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
	fieldID        = "id"
	fieldUpdatedAt = "updated_at"
	fieldCreatedAt = "created_at"
	fieldName      = "name"
	fieldContent   = "content"
	fieldVector    = "embedding"
	fieldMetadata  = "metadata"
	defaultLimit   = 10
)

// SQL templates for better maintainability and safety
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

	sqlDocumentExists = `SELECT 1 FROM %s WHERE id = $1`
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

	// Build connection string
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
	// Enable pgvector extension
	_, err := vs.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("pgvector enable extension: %w", err)
	}

	// Create table if not exists with detailed column comments
	createTableSQL := fmt.Sprintf(sqlCreateTable, vs.option.table, vs.option.indexDimension)
	_, err = vs.pool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("pgvector create table: %w", err)
	}

	// Create HNSW vector index for faster similarity search
	// Using cosine distance operator for semantic similarity
	indexSQL := fmt.Sprintf(sqlCreateIndex, vs.option.table, vs.option.table)
	_, err = vs.pool.Exec(ctx, indexSQL)
	if err != nil {
		return fmt.Errorf("pgvector create vector index: %w", err)
	}

	// If tsvector is enabled, create GIN index for full-text search on content
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
	if doc.ID == "" {
		return fmt.Errorf("pgvector document ID is required")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("pgvector embedding is required")
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
		return nil, nil, fmt.Errorf("pgvector id is required")
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
	if doc.ID == "" {
		return fmt.Errorf("pgvector document ID is required")
	}

	// Check if document exists
	exists, err := vs.documentExists(ctx, doc.ID)
	if err != nil {
		return fmt.Errorf("pgvector check document existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("pgvector document not found: %s", doc.ID)
	}

	// Build update using updateBuilder
	ub := newUpdateBuilder(vs.option.table, doc.ID, vs.option.language)

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
		return fmt.Errorf("pgvector id is required")
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
		return nil, fmt.Errorf("pgvector: query is required")
	}

	if !vs.option.enableTSVector &&
		(query.SearchMode == vectorstore.SearchModeKeyword || query.SearchMode == vectorstore.SearchModeHybrid) {
		log.Infof("pgvector: keyword or hybrid search is not supported when enableTSVector is disabled, use filter/vector search instead")
		if len(query.Vector) > 0 {
			return vs.searchByVector(ctx, query)
		} else {
			return vs.searchByFilter(ctx, query)
		}
	}
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
		return nil, fmt.Errorf("pgvector: searching with a nil or empty vector is not supported")
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
	return vs.executeSearch(ctx, sql, args)
}

// searchByKeyword performs full-text search.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, fmt.Errorf("pgvector keyword is required for keyword search")
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
	return vs.executeSearch(ctx, sql, args)
}

// searchByHybrid combines vector similarity and keyword matching
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	// check vector dimension and keyword
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("pgvector vector is required for hybrid search")
	}
	if len(query.Vector) != vs.option.indexDimension {
		return nil, fmt.Errorf("pgvector vector dimension mismatch: expected %d, got %d", vs.option.indexDimension, len(query.Vector))
	}
	if query.Query == "" {
		return nil, fmt.Errorf("pgvector keyword is required for hybrid search")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build hybrid search query
	qb := newHybridQueryBuilder(vs.option.table, vs.option.language, vs.option.vectorWeight, vs.option.textWeight)
	qb.addVectorArg(pgvector.NewVector(convertToFloat32Vector(query.Vector)))
	qb.addHybridFtsCondition(query.Query)

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
	return vs.executeSearch(ctx, sql, args)
}

// searchByFilter returns documents based on filters only
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("pgvector query is required")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Build filter-only search query
	qb := newFilterQueryBuilder(vs.option.table, vs.option.language)

	// Add filters
	if query.Filter != nil && len(query.Filter.IDs) > 0 {
		qb.addIDFilter(query.Filter.IDs)
	}

	if query.Filter != nil && len(query.Filter.Metadata) > 0 {
		qb.addMetadataFilter(query.Filter.Metadata)
	}

	sql, args := qb.build(limit)
	return vs.executeSearch(ctx, sql, args)
}

// executeSearch executes the search query and returns results
func (vs *VectorStore) executeSearch(ctx context.Context, sql string, args []interface{}) (*vectorstore.SearchResult, error) {
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

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	vs.pool.Close()
	return nil
}

// Helper functions
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

func mapToJSON(m map[string]interface{}) string {
	if len(m) == 0 {
		return "{}"
	}

	data, err := json.Marshal(m)
	if err != nil {
		// Fallback to empty JSON if marshal fails
		return "{}"
	}
	return string(data)
}

func jsonToMap(jsonStr string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	if jsonStr == "{}" || jsonStr == "" {
		return result, nil
	}

	err := json.Unmarshal([]byte(jsonStr), &result)
	if err != nil {
		// Return empty map if unmarshal fails, but log the error
		return result, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return result, nil
}
