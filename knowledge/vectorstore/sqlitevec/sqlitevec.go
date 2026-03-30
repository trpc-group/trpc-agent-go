//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sqlitevec provides a sqlite-vec-backed implementation of the
// knowledge vector store.
package sqlitevec

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var _ vectorstore.VectorStore = (*Store)(nil)

var (
	errDocNil         = errors.New("sqlitevec: document cannot be nil")
	errDocIDEmpty     = errors.New("sqlitevec: document ID cannot be empty")
	errEmbeddingEmpty = errors.New("sqlitevec: embedding cannot be empty")
)

const (
	sqlVectorFromBlob                = "vec_f32(?)"
	defaultDBTimeout                 = 30 * time.Second
	internalEmbeddingTextMetadataKey = "__sqlitevec_embedding_text"
)

var vecInitOnce sync.Once

// Store implements vectorstore.VectorStore backed by sqlite-vec.
type Store struct {
	opts    options
	db      *sql.DB
	filterB *filterBuilder
}

// New creates a new sqlitevec vector store.
func New(opts ...Option) (*Store, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	vecInitOnce.Do(vecAuto)

	s := &Store{
		opts: o,
	}

	db, err := sql.Open(o.driverName, o.dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec: open database: %w", err)
	}
	if isSQLiteMemoryDSN(o.dsn) {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	s.db = db

	s.filterB = newFilterBuilder(o.tableName, o.metadataTableName)

	if !o.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBTimeout)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			_ = s.db.Close()
			return nil, fmt.Errorf("sqlitevec: init database: %w", err)
		}
	}

	return s, nil
}

// ---------- Add ----------

// Add stores a document with its embedding vector.
func (s *Store) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocNil
	}
	if doc.ID == "" {
		return errDocIDEmpty
	}
	if len(embedding) == 0 {
		return errEmbeddingEmpty
	}

	blob, err := s.serializeEmbedding(embedding)
	if err != nil {
		return fmt.Errorf("sqlitevec add: serialize embedding: %w", err)
	}

	now := time.Now().UTC()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}
	doc.UpdatedAt = now

	storedMetadata := withInternalMetadata(doc.Metadata, doc.EmbeddingText)
	storedMetaJSON, err := marshalMetadata(storedMetadata)
	if err != nil {
		return fmt.Errorf("sqlitevec add: marshal stored metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitevec add: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert into vec0 main table.
	insertSQL := fmt.Sprintf(`INSERT INTO %s (
	id, embedding, created_at, updated_at, name, content, metadata
	) VALUES (?, `+sqlVectorFromBlob+`, ?, ?, ?, ?, ?)`, s.opts.tableName)

	_, err = tx.ExecContext(ctx, insertSQL,
		doc.ID,
		blob,
		doc.CreatedAt.UnixNano(),
		doc.UpdatedAt.UnixNano(),
		doc.Name,
		doc.Content,
		storedMetaJSON,
	)
	if err != nil {
		return fmt.Errorf("sqlitevec add: insert vec0: %w", err)
	}

	// Delete any existing metadata rows (upsert behaviour).
	if err := s.deleteMetadataRows(ctx, tx, doc.ID); err != nil {
		return err
	}
	// Insert expanded metadata rows.
	if err := s.insertMetadataRows(ctx, tx, doc.ID, storedMetadata); err != nil {
		return err
	}

	return tx.Commit()
}

// ---------- Get ----------

// Get retrieves a document by ID along with its embedding.
func (s *Store) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocIDEmpty
	}

	query := fmt.Sprintf(`SELECT
	id, name, content, metadata, created_at, updated_at, embedding
	FROM %s WHERE id = ?`, s.opts.tableName)

	row := s.db.QueryRowContext(ctx, query, id)

	var (
		docID        string
		name         sql.NullString
		content      sql.NullString
		metadataJSON sql.NullString
		createdAtNs  int64
		updatedAtNs  int64
		embBlob      []byte
	)

	if err := row.Scan(
		&docID, &name, &content, &metadataJSON, &createdAtNs, &updatedAtNs, &embBlob,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("sqlitevec get: document not found: %s", id)
		}
		return nil, nil, fmt.Errorf("sqlitevec get: scan: %w", err)
	}

	baseStoredMetadata, _ := unmarshalMetadata(metadataJSON.String)
	storedMetadata, err := s.loadStoredMetadata(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlitevec get: load metadata: %w", err)
	}
	storedMetadata = reconcileStoredMetadata(baseStoredMetadata, storedMetadata)
	metadata, embeddingText := splitInternalMetadata(storedMetadata)

	doc := &document.Document{
		ID:            docID,
		Name:          name.String,
		Content:       content.String,
		EmbeddingText: embeddingText,
		Metadata:      metadata,
		CreatedAt:     time.Unix(0, createdAtNs).UTC(),
		UpdatedAt:     time.Unix(0, updatedAtNs).UTC(),
	}

	embedding := deserializeEmbedding(embBlob)

	return doc, embedding, nil
}

// ---------- Update ----------

// Update modifies an existing document and its embedding.
func (s *Store) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocNil
	}
	if doc.ID == "" {
		return errDocIDEmpty
	}
	if len(embedding) == 0 {
		return errEmbeddingEmpty
	}

	blob, err := s.serializeEmbedding(embedding)
	if err != nil {
		return fmt.Errorf("sqlitevec update: serialize embedding: %w", err)
	}

	// Retrieve the original created_at to preserve it.
	var createdAtNs int64
	getCreatedSQL := fmt.Sprintf(`SELECT created_at FROM %s WHERE id = ?`, s.opts.tableName)
	if err := s.db.QueryRowContext(ctx, getCreatedSQL, doc.ID).Scan(&createdAtNs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlitevec update: document not found: %s", doc.ID)
		}
		return fmt.Errorf("sqlitevec update: lookup: %w", err)
	}

	now := time.Now().UTC()
	doc.CreatedAt = time.Unix(0, createdAtNs).UTC()
	doc.UpdatedAt = now

	storedMetadata := withInternalMetadata(doc.Metadata, doc.EmbeddingText)
	metaJSON, err := marshalMetadata(storedMetadata)
	if err != nil {
		return fmt.Errorf("sqlitevec update: marshal metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitevec update: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// vec0 tables do not support UPDATE on all columns uniformly.
	// We delete and re-insert.
	deleteSQL := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.opts.tableName)
	res, err := tx.ExecContext(ctx, deleteSQL, doc.ID)
	if err != nil {
		return fmt.Errorf("sqlitevec update: delete old: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("sqlitevec update: document not found: %s", doc.ID)
	}

	insertSQL := fmt.Sprintf(`INSERT INTO %s (
	id, embedding, created_at, updated_at, name, content, metadata
	) VALUES (?, `+sqlVectorFromBlob+`, ?, ?, ?, ?, ?)`, s.opts.tableName)

	_, err = tx.ExecContext(ctx, insertSQL,
		doc.ID,
		blob,
		doc.CreatedAt.UnixNano(),
		doc.UpdatedAt.UnixNano(),
		doc.Name,
		doc.Content,
		metaJSON,
	)
	if err != nil {
		return fmt.Errorf("sqlitevec update: re-insert vec0: %w", err)
	}

	// Rebuild metadata rows.
	if err := s.deleteMetadataRows(ctx, tx, doc.ID); err != nil {
		return err
	}
	if err := s.insertMetadataRows(ctx, tx, doc.ID, storedMetadata); err != nil {
		return err
	}

	return tx.Commit()
}

// ---------- Delete ----------

// Delete removes a document and its embedding.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocIDEmpty
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitevec delete: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete metadata rows first.
	if err := s.deleteMetadataRows(ctx, tx, id); err != nil {
		return err
	}

	deleteSQL := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.opts.tableName)
	res, err := tx.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		return fmt.Errorf("sqlitevec delete: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("sqlitevec delete: document not found: %s", id)
	}

	return tx.Commit()
}

// ---------- Search ----------

// Search performs similarity search and returns the most similar documents.
func (s *Store) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errors.New("sqlitevec search: query cannot be nil")
	}

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		return s.searchByVector(ctx, query)
	case vectorstore.SearchModeFilter:
		return s.searchByFilter(ctx, query)
	case vectorstore.SearchModeKeyword:
		return nil, errors.New("sqlitevec: SearchModeKeyword is not supported in v1")
	case vectorstore.SearchModeHybrid:
		return s.searchByVector(ctx, query)
	default:
		// Default to vector search for backward compatibility.
		if len(query.Vector) > 0 {
			return s.searchByVector(ctx, query)
		}
		return s.searchByFilter(ctx, query)
	}
}

// searchByVector performs vector similarity search with optional filters.
func (s *Store) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errors.New("sqlitevec: query vector cannot be empty for vector search")
	}

	blob, err := s.serializeEmbedding(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec search: serialize embedding: %w", err)
	}

	limit := query.Limit
	if limit <= 0 {
		limit = s.opts.maxResults
	}

	// Build the base vec0 MATCH query.
	// vec0 requires: embedding MATCH vec_f32(?) AND k = ?
	// Additional filters go after the mandatory k clause.
	var whereParts []string
	var params []any

	whereParts = append(whereParts, "v.embedding MATCH "+sqlVectorFromBlob)
	params = append(params, blob)

	whereParts = append(whereParts, "v.k = ?")
	params = append(params, limit)

	// Apply filters.
	if query.Filter != nil {
		filterSQL, filterParams, err := s.filterB.buildFilterClauses(
			query.Filter.IDs,
			query.Filter.Metadata,
			query.Filter.FilterCondition,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlitevec vector search: build filter: %w", err)
		}
		if filterSQL != "" {
			whereParts = append(whereParts, filterSQL)
			params = append(params, filterParams...)
		}
	}

	selectSQL := fmt.Sprintf(`SELECT
	v.id, v.name, v.content, v.metadata, v.created_at, v.updated_at, v.distance
	FROM %s v
	WHERE %s`, s.opts.tableName, strings.Join(whereParts, " AND "))

	rows, err := s.db.QueryContext(ctx, selectSQL, params...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec vector search: %w", err)
	}
	defer rows.Close()

	type vectorSearchRow struct {
		id           string
		name         sql.NullString
		content      sql.NullString
		metadataJSON sql.NullString
		createdAtNs  int64
		updatedAtNs  int64
		distance     float64
	}

	var scanned []vectorSearchRow
	for rows.Next() {
		var item vectorSearchRow
		if err := rows.Scan(
			&item.id,
			&item.name,
			&item.content,
			&item.metadataJSON,
			&item.createdAtNs,
			&item.updatedAtNs,
			&item.distance,
		); err != nil {
			return nil, fmt.Errorf("sqlitevec scan scored row: %w", err)
		}
		scanned = append(scanned, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec vector search iterate: %w", err)
	}
	_ = rows.Close()

	var results []*vectorstore.ScoredDocument
	for _, item := range scanned {
		sd, err := s.buildScoredDocument(
			ctx,
			item.id,
			item.name,
			item.content,
			item.metadataJSON,
			item.createdAtNs,
			item.updatedAtNs,
			item.distance,
		)
		if err != nil {
			return nil, err
		}
		sd.Score = 1.0 - sd.Score/2.0
		if sd.Score >= query.MinScore {
			results = append(results, sd)
		}
	}

	return &vectorstore.SearchResult{Results: results}, nil
}

// searchByFilter performs filter-only search (no vector matching).
func (s *Store) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = s.opts.maxResults
	}

	var whereParts []string
	var params []any

	if query.Filter != nil {
		filterSQL, filterParams, err := s.filterB.buildFilterClauses(
			query.Filter.IDs,
			query.Filter.Metadata,
			query.Filter.FilterCondition,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlitevec filter search: build filter: %w", err)
		}
		if filterSQL != "" {
			whereParts = append(whereParts, filterSQL)
			params = append(params, filterParams...)
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	selectSQL := fmt.Sprintf(`SELECT
	v.id, v.name, v.content, v.metadata, v.created_at, v.updated_at
	FROM %s v
	%s
	ORDER BY v.updated_at DESC
LIMIT ?`, s.opts.tableName, whereClause)
	params = append(params, limit)

	rows, err := s.db.QueryContext(ctx, selectSQL, params...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec filter search: %w", err)
	}
	defer rows.Close()

	type filterSearchRow struct {
		id           string
		name         sql.NullString
		content      sql.NullString
		metadataJSON sql.NullString
		createdAtNs  int64
		updatedAtNs  int64
	}

	var scanned []filterSearchRow
	for rows.Next() {
		var item filterSearchRow
		if err := rows.Scan(
			&item.id,
			&item.name,
			&item.content,
			&item.metadataJSON,
			&item.createdAtNs,
			&item.updatedAtNs,
		); err != nil {
			return nil, fmt.Errorf("sqlitevec scan filter row: %w", err)
		}
		scanned = append(scanned, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec filter search iterate: %w", err)
	}
	_ = rows.Close()

	var results []*vectorstore.ScoredDocument
	for _, item := range scanned {
		sd, err := s.buildScoredDocument(
			ctx,
			item.id,
			item.name,
			item.content,
			item.metadataJSON,
			item.createdAtNs,
			item.updatedAtNs,
			0,
		)
		if err != nil {
			return nil, err
		}
		sd.Score = 1.0
		results = append(results, sd)
	}

	return &vectorstore.SearchResult{Results: results}, nil
}

// ---------- DeleteByFilter ----------

// DeleteByFilter deletes documents by filter.
func (s *Store) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)

	if config.DeleteAll && (len(config.DocumentIDs) > 0 || len(config.Filter) > 0) {
		return errors.New("sqlitevec: delete all conflicts with document ids or filter")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitevec delete by filter: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if config.DeleteAll {
		// Delete all metadata rows first.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, s.opts.metadataTableName)); err != nil {
			return fmt.Errorf("sqlitevec delete all metadata: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, s.opts.tableName)); err != nil {
			return fmt.Errorf("sqlitevec delete all: %w", err)
		}
		return tx.Commit()
	}

	if len(config.DocumentIDs) == 0 && len(config.Filter) == 0 {
		return errors.New("sqlitevec: delete by filter: no filter conditions specified")
	}

	// Collect matching IDs.
	ids, err := s.collectFilteredIDs(ctx, config.DocumentIDs, config.Filter, nil)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.deleteMetadataRows(ctx, tx, id); err != nil {
			return err
		}
		delSQL := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.opts.tableName)
		if _, err := tx.ExecContext(ctx, delSQL, id); err != nil {
			return fmt.Errorf("sqlitevec delete by filter id=%s: %w", id, err)
		}
	}

	return tx.Commit()
}

// ---------- UpdateByFilter ----------

// UpdateByFilter updates documents matching the filter with the specified field values.
func (s *Store) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	config, err := vectorstore.ApplyUpdateByFilterOptions(opts...)
	if err != nil {
		return 0, fmt.Errorf("sqlitevec update by filter: %w", err)
	}

	for field := range config.Updates {
		if err := validateUpdateField(field); err != nil {
			return 0, err
		}
	}

	// Collect matching IDs.
	ids, err := s.collectFilteredIDsFromCondition(ctx, config.DocumentIDs, config.FilterCondition)
	if err != nil {
		return 0, err
	}

	if len(ids) == 0 {
		return 0, nil
	}

	// Check: if content is being updated, embedding must also be provided.
	_, hasContent := config.Updates["content"]
	_, hasEmbedding := config.Updates["embedding"]
	if hasContent && !hasEmbedding {
		return 0, errors.New("sqlitevec: updating content requires providing embedding")
	}

	var count int64
	for _, id := range ids {
		doc, emb, err := s.Get(ctx, id)
		if err != nil {
			continue
		}

		// Apply updates to document fields.
		for key, val := range config.Updates {
			switch key {
			case "name":
				if v, ok := val.(string); ok {
					doc.Name = v
				}
			case "content":
				if v, ok := val.(string); ok {
					doc.Content = v
				}
			case "embedding_text":
				if v, ok := val.(string); ok {
					doc.EmbeddingText = v
				}
			case "embedding":
				if v, ok := val.([]float64); ok {
					emb = v
				}
			default:
				if strings.HasPrefix(key, source.MetadataFieldPrefix) {
					metaKey := strings.TrimPrefix(key, source.MetadataFieldPrefix)
					if doc.Metadata == nil {
						doc.Metadata = make(map[string]any)
					}
					doc.Metadata[metaKey] = val
				}
			}
		}

		if err := s.Update(ctx, doc, emb); err != nil {
			return count, fmt.Errorf("sqlitevec update by filter id=%s: %w", id, err)
		}
		count++
	}

	return count, nil
}

func validateUpdateField(field string) error {
	forbiddenFields := map[string]bool{
		"id":         true,
		"created_at": true,
		"updated_at": true,
	}

	if forbiddenFields[field] {
		return fmt.Errorf("field %q cannot be updated", field)
	}

	switch field {
	case "name", "content", "embedding_text", "embedding":
		return nil
	}

	if strings.HasPrefix(field, source.MetadataFieldPrefix) {
		if strings.TrimPrefix(field, source.MetadataFieldPrefix) == "" {
			return fmt.Errorf("invalid metadata field: %q", field)
		}
		return nil
	}

	return fmt.Errorf("field %q cannot be updated", field)
}

// ---------- Count ----------

// Count counts documents in the vector store.
func (s *Store) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	var whereParts []string
	var params []any

	if len(config.Filter) > 0 {
		filterSQL, filterParams, err := s.filterB.buildFilterClauses(nil, config.Filter, nil)
		if err != nil {
			return 0, fmt.Errorf("sqlitevec count: build filter: %w", err)
		}
		if filterSQL != "" {
			whereParts = append(whereParts, filterSQL)
			params = append(params, filterParams...)
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s v %s`, s.opts.tableName, whereClause)
	var count int
	if err := s.db.QueryRowContext(ctx, query, params...).Scan(&count); err != nil {
		return 0, fmt.Errorf("sqlitevec count: %w", err)
	}
	return count, nil
}

// ---------- GetMetadata ----------

// GetMetadata retrieves metadata from the vector store.
func (s *Store) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	var whereParts []string
	var params []any

	if len(config.IDs) > 0 {
		placeholders := make([]string, len(config.IDs))
		for i, id := range config.IDs {
			placeholders[i] = "?"
			params = append(params, id)
		}
		whereParts = append(whereParts, fmt.Sprintf("v.id IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(config.Filter) > 0 {
		filterSQL, filterParams, err := s.filterB.buildFilterClauses(nil, config.Filter, nil)
		if err != nil {
			return nil, fmt.Errorf("sqlitevec get metadata: build filter: %w", err)
		}
		if filterSQL != "" {
			whereParts = append(whereParts, filterSQL)
			params = append(params, filterParams...)
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT v.id, v.metadata FROM %s v %s ORDER BY v.updated_at DESC, v.id ASC`,
		s.opts.tableName, whereClause,
	)

	// Pagination.
	if config.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", config.Limit)
		if config.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", config.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec get metadata: %w", err)
	}
	defer rows.Close()

	type metadataResultRow struct {
		id       string
		metaJSON sql.NullString
	}

	var scanned []metadataResultRow
	for rows.Next() {
		var item metadataResultRow
		if err := rows.Scan(&item.id, &item.metaJSON); err != nil {
			return nil, fmt.Errorf("sqlitevec get metadata scan: %w", err)
		}
		scanned = append(scanned, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec get metadata iterate: %w", err)
	}
	_ = rows.Close()

	result := make(map[string]vectorstore.DocumentMetadata, len(scanned))
	for _, item := range scanned {
		baseStoredMeta, _ := unmarshalMetadata(item.metaJSON.String)
		storedMeta, err := s.loadStoredMetadata(ctx, item.id)
		if err != nil {
			return nil, fmt.Errorf("sqlitevec get metadata load: %w", err)
		}
		storedMeta = reconcileStoredMetadata(baseStoredMeta, storedMeta)
		meta, _ := splitInternalMetadata(storedMeta)
		result[item.id] = vectorstore.DocumentMetadata{Metadata: meta}
	}

	return result, nil
}

// ---------- Close ----------

// Close closes the vector store connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// ---------- internal helpers ----------

// serializeEmbedding converts []float64 to the blob format expected by
// vec_f32(?).
func (s *Store) serializeEmbedding(embedding []float64) ([]byte, error) {
	f32 := make([]float32, len(embedding))
	for i, v := range embedding {
		f32[i] = float32(v)
	}
	return vecSerializeFloat32(f32)
}

// collectFilteredIDs returns document IDs matching the given simple filters.
// For simple ID-only filters, it returns the IDs directly without querying.
// For metadata or condition filters, it uses the filter search path.
func (s *Store) collectFilteredIDs(
	ctx context.Context,
	ids []string,
	metadata map[string]any,
	cond *searchfilter.UniversalFilterCondition,
) ([]string, error) {
	// Fast path: if only IDs are provided, return them directly.
	if len(ids) > 0 && len(metadata) == 0 && cond == nil {
		return ids, nil
	}

	var whereParts []string
	var params []any
	filterSQL, filterParams, err := s.filterB.buildFilterClauses(ids, metadata, cond)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec collect IDs: build filter: %w", err)
	}
	if filterSQL != "" {
		whereParts = append(whereParts, filterSQL)
		params = append(params, filterParams...)
	}

	query := fmt.Sprintf(`SELECT v.id FROM %s v`, s.opts.tableName)
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	query += " ORDER BY v.updated_at DESC"

	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec collect IDs: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlitevec collect IDs scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec collect IDs iterate: %w", err)
	}

	return out, nil
}

// collectFilteredIDsFromCondition returns document IDs matching the given
// UniversalFilterCondition plus optional ID list.
func (s *Store) collectFilteredIDsFromCondition(
	ctx context.Context,
	ids []string,
	cond *searchfilter.UniversalFilterCondition,
) ([]string, error) {
	return s.collectFilteredIDs(ctx, ids, nil, cond)
}

// scanScoredRow scans a row from the vector search query (includes distance).
func (s *Store) buildScoredDocument(
	ctx context.Context,
	id string,
	name sql.NullString,
	content sql.NullString,
	metadataJSON sql.NullString,
	createdAtNs int64,
	updatedAtNs int64,
	score float64,
) (*vectorstore.ScoredDocument, error) {
	baseStoredMetadata, _ := unmarshalMetadata(metadataJSON.String)
	storedMetadata, err := s.loadStoredMetadata(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec load document metadata: %w", err)
	}
	storedMetadata = reconcileStoredMetadata(baseStoredMetadata, storedMetadata)
	metadata, embeddingText := splitInternalMetadata(storedMetadata)

	return &vectorstore.ScoredDocument{
		Document: &document.Document{
			ID:            id,
			Name:          name.String,
			Content:       content.String,
			EmbeddingText: embeddingText,
			Metadata:      metadata,
			CreatedAt:     time.Unix(0, createdAtNs).UTC(),
			UpdatedAt:     time.Unix(0, updatedAtNs).UTC(),
		},
		Score: score,
	}, nil
}

// deserializeEmbedding converts a sqlite-vec blob back to []float64.
func deserializeEmbedding(blob []byte) []float64 {
	if len(blob) == 0 {
		return nil
	}
	// sqlite-vec stores float32 as little-endian, 4 bytes each.
	n := len(blob) / 4
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		bits := uint32(blob[i*4]) |
			uint32(blob[i*4+1])<<8 |
			uint32(blob[i*4+2])<<16 |
			uint32(blob[i*4+3])<<24
		out[i] = float64(math.Float32frombits(bits))
	}
	return out
}

// marshalMetadata serialises a metadata map to JSON.
func marshalMetadata(metadata map[string]any) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// unmarshalMetadata deserialises a JSON string to a metadata map.
func unmarshalMetadata(s string) (map[string]any, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func withInternalMetadata(metadata map[string]any, embeddingText string) map[string]any {
	if len(metadata) == 0 && embeddingText == "" {
		return nil
	}

	stored := cloneMetadata(metadata)
	if embeddingText != "" {
		if stored == nil {
			stored = make(map[string]any, 1)
		}
		stored[internalEmbeddingTextMetadataKey] = embeddingText
	}
	return stored
}

func splitInternalMetadata(stored map[string]any) (map[string]any, string) {
	if len(stored) == 0 {
		return nil, ""
	}

	embeddingText, _ := stored[internalEmbeddingTextMetadataKey].(string)
	clean := make(map[string]any, len(stored))
	for key, value := range stored {
		if key == internalEmbeddingTextMetadataKey {
			continue
		}
		clean[key] = value
	}
	if len(clean) == 0 {
		return nil, embeddingText
	}
	return clean, embeddingText
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func isSQLiteMemoryDSN(dsn string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dsn))
	switch {
	case normalized == ":memory:":
		return true
	case strings.HasPrefix(normalized, "file::memory:"):
		return true
	case strings.HasPrefix(normalized, "file:") &&
		strings.Contains(normalized, "mode=memory"):
		return true
	default:
		return false
	}
}

func reconcileStoredMetadata(base, loaded map[string]any) map[string]any {
	if loaded == nil {
		return cloneMetadata(base)
	}
	if base == nil {
		return cloneMetadata(loaded)
	}

	out := cloneMetadata(base)
	for key, value := range loaded {
		if baseValue, ok := base[key]; ok {
			if _, isBaseSlice := baseValue.([]any); isBaseSlice {
				if _, isLoadedSlice := value.([]any); !isLoadedSlice {
					out[key] = []any{value}
					continue
				}
			}
		}
		out[key] = value
	}
	return out
}
