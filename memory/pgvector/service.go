//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pgvector provides a pgvector-based memory service.
// It supports vector similarity search.
package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the pgvector memory service.
// Storage structure.
// Table: memories (configurable).
// Columns: memory_id, app_name, user_id, memory_content, topics, embedding.
// created_at, updated_at, deleted_at.
// Primary key: memory_id.
// Indexes: (app_name, user_id), updated_at, deleted_at, HNSW on embedding.
type Service struct {
	opts      ServiceOpts
	db        storage.Client
	tableName string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new pgvector memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	// Validate embedder is provided.
	if opts.embedder == nil {
		return nil, fmt.Errorf("embedder is required for pgvector memory service")
	}

	// Apply auto mode defaults after all options are applied.
	// User settings via WithToolEnabled take precedence regardless of option
	// order.
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// Priority: DSN > direct connection settings > instance name.
	if opts.dsn != "" {
		// Use DSN directly if provided.
		builderOpts = append(builderOpts, storage.WithClientConnString(opts.dsn))
	} else if opts.host != "" {
		// Use direct connection settings if provided.
		builderOpts = append(builderOpts, storage.WithClientConnString(buildConnString(opts)))
	} else if opts.instanceName != "" {
		// Otherwise, use instance name if provided.
		var ok bool
		if builderOpts, ok = storage.GetPostgresInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("postgres instance %s not found", opts.instanceName)
		}
	} else {
		// Fallback to default connection string.
		builderOpts = append(builderOpts, storage.WithClientConnString(buildConnString(opts)))
	}

	db, err := storage.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create postgres client failed: %w", err)
	}
	// Build full table name with schema.
	fullTableName := buildFullTableName(opts.schema, opts.tableName)

	s := &Service{
		opts:        opts,
		db:          db,
		tableName:   fullTableName,
		cachedTools: make(map[string]tool.Tool),
	}

	// Initialize database schema unless skipped.
	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBInitTimeout)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}

	// Pre-compute tools list to avoid lock contention in Tools() method.
	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)

	// Initialize auto memory worker if extractor is configured.
	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor, opts.enabledTools,
		)
		config := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
			EnabledTools:     opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

// buildConnString builds a PostgreSQL connection string from options.
func buildConnString(opts ServiceOpts) string {
	// Default values.
	host := opts.host
	if host == "" {
		host = defaultHost
	}
	port := opts.port
	if port == 0 {
		port = defaultPort
	}
	database := opts.database
	if database == "" {
		database = defaultDatabase
	}
	sslMode := opts.sslMode
	if sslMode == "" {
		sslMode = defaultSSLMode
	}

	// Build connection string.
	var conn strings.Builder
	fmt.Fprintf(&conn, "host=%s port=%d dbname=%s sslmode=%s",
		host, port, database, sslMode)
	if opts.user != "" {
		fmt.Fprintf(&conn, " user=%s", opts.user)
	}
	if opts.password != "" {
		fmt.Fprintf(&conn, " password=%s", opts.password)
	}
	return conn.String()
}

// AddMemory adds or updates a memory for a user (idempotent).
// Options may include WithMetadata for episodic metadata.
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryStr string,
	topics []string,
	opts ...memory.AddOption,
) error {
	ep := memory.ResolveAddOptions(opts)
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Generate embedding for the memory content.
	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding failed: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(embedding))
	}

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	imemory.ApplyMetadata(mem, ep)
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	// Convert embedding to pgvector format.
	vector := pgvector.NewVector(convertToFloat32(embedding))

	// Resolve metadata for SQL parameters from the normalized memory.
	ef := resolveMetadata(mem)

	var insertQuery string
	args := []any{
		memoryID,                  // $1
		userKey.AppName,           // $2
		userKey.UserID,            // $3
		memoryStr,                 // $4
		pq.Array(topics),          // $5
		vector,                    // $6
		ef.kind,                   // $7
		ef.eventTime,              // $8
		pq.Array(ef.participants), // $9
		ef.location,               // $10
		now,                       // $11
		now,                       // $12
	}
	if s.opts.memoryLimit > 0 {
		deletedFilter := ""
		if s.opts.softDelete {
			deletedFilter = " AND deleted_at IS NULL"
		}
		// Build evict CTE: when at capacity and inserting a new memory,
		// remove the least-recently-updated entry to make room.
		var evictAction string
		if s.opts.softDelete {
			evictAction = fmt.Sprintf("UPDATE %s SET deleted_at = $11", s.tableName)
		} else {
			evictAction = fmt.Sprintf("DELETE FROM %s", s.tableName)
		}
		evictCTE := fmt.Sprintf(
			", evict AS (%s "+
				"WHERE memory_id = ("+
				"SELECT memory_id FROM %s "+
				"WHERE app_name = $2 AND user_id = $3%s "+
				"ORDER BY updated_at ASC LIMIT 1) "+
				"AND NOT EXISTS (SELECT 1 FROM existing) "+
				"AND (SELECT c FROM cnt) >= $13 "+
				"RETURNING memory_id)",
			evictAction, s.tableName, deletedFilter,
		)
		insertQuery = fmt.Sprintf(
			"WITH existing AS ("+
				"SELECT 1 FROM %s "+
				"WHERE memory_id = $1 AND app_name = $2 AND user_id = $3%s"+
				"), cnt AS ("+
				"SELECT COUNT(*) AS c FROM %s "+
				"WHERE app_name = $2 AND user_id = $3%s"+
				")%s "+
				"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
				"embedding, memory_kind, event_time, participants, location, "+
				"created_at, updated_at) "+
				"SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 "+
				"WHERE (EXISTS (SELECT 1 FROM existing) OR "+
				"(SELECT c FROM cnt) < $13 OR "+
				"EXISTS (SELECT 1 FROM evict)) "+
				"ON CONFLICT (memory_id) DO UPDATE SET "+
				"memory_content = EXCLUDED.memory_content, "+
				"topics = EXCLUDED.topics, "+
				"embedding = EXCLUDED.embedding, "+
				"memory_kind = EXCLUDED.memory_kind, "+
				"event_time = EXCLUDED.event_time, "+
				"participants = EXCLUDED.participants, "+
				"location = EXCLUDED.location, "+
				"deleted_at = NULL, "+
				"updated_at = EXCLUDED.updated_at",
			s.tableName,
			deletedFilter,
			s.tableName,
			deletedFilter,
			evictCTE,
			s.tableName,
		)
		args = append(args, s.opts.memoryLimit)
	} else {
		insertQuery = fmt.Sprintf(
			"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
				"embedding, memory_kind, event_time, participants, location, "+
				"created_at, updated_at) "+
				"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) "+
				"ON CONFLICT (memory_id) DO UPDATE SET "+
				"memory_content = EXCLUDED.memory_content, "+
				"topics = EXCLUDED.topics, "+
				"embedding = EXCLUDED.embedding, "+
				"memory_kind = EXCLUDED.memory_kind, "+
				"event_time = EXCLUDED.event_time, "+
				"participants = EXCLUDED.participants, "+
				"location = EXCLUDED.location, "+
				"deleted_at = NULL, "+
				"updated_at = EXCLUDED.updated_at",
			s.tableName,
		)
	}

	res, err := s.db.ExecContext(ctx, insertQuery, args...)
	if err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}
	if s.opts.memoryLimit > 0 {
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store memory entry rows affected failed: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("memory eviction failed for user %s, limit: %d",
				userKey.UserID, s.opts.memoryLimit)
		}
	}

	return nil
}

// UpdateMemory updates an existing memory for a user.
// Options may include WithUpdateMetadata for episodic metadata.
func (s *Service) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryStr string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	ep := memory.ResolveUpdateOptions(opts)

	selectQuery := fmt.Sprintf(
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at FROM %s WHERE memory_id = $1 AND app_name = $2 AND user_id = $3",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}
	var entry *memory.Entry
	if err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if !rows.Next() {
			return sql.ErrNoRows
		}
		var scanErr error
		entry, scanErr = scanMemoryEntry(rows)
		return scanErr
	}, selectQuery, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		return fmt.Errorf("load memory entry failed: %w", err)
	}

	// Generate new embedding for the updated memory content.
	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding failed: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(embedding))
	}

	now := time.Now()
	vector := pgvector.NewVector(convertToFloat32(embedding))
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)
	ef := resolveMetadata(entry.Memory)

	updateQuery := fmt.Sprintf(
		"UPDATE %s SET memory_id = $1, memory_content = $2, topics = $3, embedding = $4, "+
			"memory_kind = $5, event_time = $6, participants = $7, location = $8, updated_at = $9 "+
			"WHERE memory_id = $10 AND app_name = $11 AND user_id = $12",
		s.tableName,
	)
	if s.opts.softDelete {
		updateQuery += " AND deleted_at IS NULL"
	}
	res, err := s.db.ExecContext(
		ctx,
		updateQuery,
		newID,
		memoryStr,
		pq.Array(topics),
		vector,
		ef.kind,
		ef.eventTime,
		pq.Array(ef.participants),
		ef.location,
		now,
		memoryKey.MemoryID,
		memoryKey.AppName,
		memoryKey.UserID,
	)
	if err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update memory entry rows affected failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}

	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	var (
		query string
		args  []any
	)
	if s.opts.softDelete {
		now := time.Now()
		query = fmt.Sprintf(
			"UPDATE %s SET deleted_at = $1 "+
				"WHERE memory_id = $2 AND app_name = $3 AND user_id = $4 "+
				"AND deleted_at IS NULL",
			s.tableName,
		)
		args = []any{now, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID}
	} else {
		query = fmt.Sprintf(
			"DELETE FROM %s WHERE memory_id = $1 AND app_name = $2 AND user_id = $3",
			s.tableName,
		)
		args = []any{memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID}
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}

	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	var err error
	if s.opts.softDelete {
		now := time.Now()
		query := fmt.Sprintf(
			"UPDATE %s SET deleted_at = $1 "+
				"WHERE app_name = $2 AND user_id = $3 AND deleted_at IS NULL",
			s.tableName,
		)
		_, err = s.db.ExecContext(ctx, query, now, userKey.AppName, userKey.UserID)
	} else {
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = $1 AND user_id = $2",
			s.tableName,
		)
		_, err = s.db.ExecContext(ctx, query, userKey.AppName, userKey.UserID)
	}
	if err != nil {
		return fmt.Errorf("clear memories failed: %w", err)
	}

	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	var query strings.Builder
	fmt.Fprintf(&query,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at FROM %s WHERE app_name = $1 AND user_id = $2",
		s.tableName,
	)
	if s.opts.softDelete {
		query.WriteString(" AND deleted_at IS NULL")
	}
	query.WriteString(" ORDER BY updated_at DESC, created_at DESC")
	if limit > 0 {
		fmt.Fprintf(&query, " LIMIT %d", limit)
	}

	entries := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			entry, err := scanMemoryEntry(rows)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		return nil
	}, query.String(), userKey.AppName, userKey.UserID)

	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}

	return entries, nil
}

// minKindFallbackResults is the threshold below which a kind-filtered
// search triggers a fallback unfiltered search when KindFallback is enabled.
const minKindFallbackResults = 3

// SearchMemories searches memories for a user using vector similarity.
// Options may include WithSearchOptions for advanced filtering
// (kind, time range, hybrid search, etc.).
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	searchOpts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	opts := memory.ResolveSearchOptions(query, searchOpts)
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query = strings.TrimSpace(opts.Query)
	if query == "" {
		return []*memory.Entry{}, nil
	}

	// Generate embedding for the query (reused across fallback searches).
	queryEmbedding, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding failed: %w", err)
	}
	if len(queryEmbedding) != s.opts.indexDimension {
		return nil, fmt.Errorf("query embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(queryEmbedding))
	}

	vector := pgvector.NewVector(convertToFloat32(queryEmbedding))

	maxResults := s.opts.maxResults
	if opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}

	results, err := s.executeVectorSearch(ctx, userKey, opts, vector, maxResults)
	if err != nil {
		return nil, err
	}

	// Kind fallback: when kind filter was applied but returned too few
	// results, retry without the kind filter and merge both result sets.
	if opts.Kind != "" && opts.KindFallback && len(results) < minKindFallbackResults {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallbackResults, fallbackErr := s.executeVectorSearch(
			ctx, userKey, fallbackOpts, vector, maxResults,
		)
		if fallbackErr == nil && len(fallbackResults) > 0 {
			results = mergeSearchResults(results, fallbackResults, opts.Kind, maxResults)
		}
	}

	// Hybrid search: run keyword search and merge with vector results
	// using Reciprocal Rank Fusion (RRF) to improve recall for exact
	// entity names, book titles, etc.
	if opts.HybridSearch {
		keywordResults, kwErr := s.executeKeywordSearch(ctx, userKey, opts, maxResults)
		if kwErr == nil && len(keywordResults) > 0 {
			rrfK := opts.HybridRRFK
			if rrfK <= 0 {
				rrfK = defaultRRFK
			}
			results = mergeHybridResults(results, keywordResults, rrfK, maxResults)
		}
	}

	// Apply similarity threshold filtering.
	// Skip when hybrid search is active because RRF scores use a
	// different range than cosine similarity.
	threshold := s.opts.similarityThreshold
	if opts.SimilarityThreshold > 0 {
		threshold = opts.SimilarityThreshold
	}
	if threshold > 0 && len(results) > 0 && !opts.HybridSearch {
		filtered := results[:0]
		for _, r := range results {
			if r.Score >= threshold {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	if len(results) > 1 {
		if opts.Kind != "" && opts.KindFallback {
			imemory.SortSearchResultsWithKindPriority(
				results,
				opts.Kind,
				opts.OrderByEventTime,
			)
		} else {
			imemory.SortSearchResults(results, opts.OrderByEventTime)
		}
	}

	// Content-based deduplication of near-identical memories.
	if opts.Deduplicate && len(results) > 1 {
		results = deduplicateResults(results)
	}
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

// executeVectorSearch runs a single vector similarity search against pgvector.
func (s *Service) executeVectorSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	vector pgvector.Vector,
	maxResults int,
) ([]*memory.Entry, error) {
	var searchQuery strings.Builder
	args := []any{vector, userKey.AppName, userKey.UserID}
	argIdx := 4

	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, 1 - (embedding <=> $1) AS similarity "+
			"FROM %s WHERE app_name = $2 AND user_id = $3",
		s.tableName,
	)
	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}

	if opts.Kind != "" {
		if opts.Kind == memory.KindFact {
			fmt.Fprintf(&searchQuery, " AND (memory_kind = $%d OR memory_kind = '')", argIdx)
		} else {
			fmt.Fprintf(&searchQuery, " AND memory_kind = $%d", argIdx)
		}
		args = append(args, string(opts.Kind))
		argIdx++
	}

	if opts.TimeAfter != nil {
		fmt.Fprintf(&searchQuery, " AND (event_time >= $%d OR event_time IS NULL)", argIdx)
		args = append(args, *opts.TimeAfter)
		argIdx++
	}
	if opts.TimeBefore != nil {
		fmt.Fprintf(&searchQuery, " AND (event_time <= $%d OR event_time IS NULL)", argIdx)
		args = append(args, *opts.TimeBefore)
		argIdx++
	}

	searchQuery.WriteString(" ORDER BY embedding <=> $1")
	if opts.OrderByEventTime {
		searchQuery.WriteString(", event_time ASC NULLS LAST")
	}
	fmt.Fprintf(&searchQuery, " LIMIT %d", maxResults)

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			entry, scanErr := scanMemoryEntryWithSimilarity(rows)
			if scanErr != nil {
				return scanErr
			}
			results = append(results, entry)
		}
		return nil
	}, searchQuery.String(), args...)

	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}
	return results, nil
}

// defaultRRFK is the standard Reciprocal Rank Fusion constant.
const defaultRRFK = imemory.DefaultHybridRRFK

// executeKeywordSearch runs a full-text search using PostgreSQL
// tsvector/tsquery alongside the vector search results.
func (s *Service) executeKeywordSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return []*memory.Entry{}, nil
	}

	var searchQuery strings.Builder
	args := []any{query, userKey.AppName, userKey.UserID}
	argIdx := 4

	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, "+
			"ts_rank(search_vector, plainto_tsquery('english', $1)) AS similarity "+
			"FROM %s WHERE app_name = $2 AND user_id = $3 "+
			"AND search_vector @@ plainto_tsquery('english', $1)",
		s.tableName,
	)
	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}

	if opts.Kind != "" {
		if opts.Kind == memory.KindFact {
			fmt.Fprintf(&searchQuery, " AND (memory_kind = $%d OR memory_kind = '')", argIdx)
		} else {
			fmt.Fprintf(&searchQuery, " AND memory_kind = $%d", argIdx)
		}
		args = append(args, string(opts.Kind))
		argIdx++
	}

	if opts.TimeAfter != nil {
		fmt.Fprintf(&searchQuery, " AND (event_time >= $%d OR event_time IS NULL)", argIdx)
		args = append(args, *opts.TimeAfter)
		argIdx++
	}
	if opts.TimeBefore != nil {
		fmt.Fprintf(&searchQuery, " AND (event_time <= $%d OR event_time IS NULL)", argIdx)
		args = append(args, *opts.TimeBefore)
		argIdx++
	}

	searchQuery.WriteString(" ORDER BY similarity DESC")
	fmt.Fprintf(&searchQuery, " LIMIT %d", maxResults)

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			entry, scanErr := scanMemoryEntryWithSimilarity(rows)
			if scanErr != nil {
				return scanErr
			}
			results = append(results, entry)
		}
		return nil
	}, searchQuery.String(), args...)

	if err != nil {
		// Keyword search failure is non-fatal; log and return empty.
		return []*memory.Entry{}, nil
	}
	return results, nil
}

// mergeHybridResults combines vector and keyword search results using
// Reciprocal Rank Fusion (RRF). Each result gets score = 1/(k+rank)
// from each search method. Combined scores determine final ranking.
func mergeHybridResults(
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	return imemory.MergeHybridResults(
		vectorResults,
		keywordResults,
		k,
		maxResults,
	)
}

// mergeSearchResults merges kind-filtered results with fallback results.
// Results matching the preferred kind are ranked higher. Duplicates are
// removed by memory ID.
func mergeSearchResults(
	primary, fallback []*memory.Entry,
	preferredKind memory.Kind,
	maxResults int,
) []*memory.Entry {
	seen := make(map[string]bool, len(primary))
	for _, e := range primary {
		seen[e.ID] = true
	}

	// Split fallback into matching-kind and other-kind.
	var kindMatch, kindOther []*memory.Entry
	for _, e := range fallback {
		if seen[e.ID] {
			continue
		}
		if e.Memory != nil && e.Memory.Kind == preferredKind {
			kindMatch = append(kindMatch, e)
		} else {
			kindOther = append(kindOther, e)
		}
	}

	// Build merged list: primary (kind-filtered) → fallback matching kind → fallback other kind.
	merged := make([]*memory.Entry, 0, len(primary)+len(kindMatch)+len(kindOther))
	merged = append(merged, primary...)
	merged = append(merged, kindMatch...)
	merged = append(merged, kindOther...)

	if len(merged) > maxResults {
		merged = merged[:maxResults]
	}
	return merged
}

// deduplicateResults removes near-duplicate memories based on word-level
// Jaccard similarity. When two results have >80% word overlap, the
// lower-scored one is dropped.
func deduplicateResults(results []*memory.Entry) []*memory.Entry {
	const jaccardThreshold = 0.80

	type wordSet map[string]struct{}
	sets := make([]wordSet, len(results))
	for i, r := range results {
		ws := make(wordSet)
		for _, w := range strings.Fields(strings.ToLower(r.Memory.Memory)) {
			ws[w] = struct{}{}
		}
		sets[i] = ws
	}

	keep := make([]bool, len(results))
	for i := range keep {
		keep[i] = true
	}

	for i := 0; i < len(results); i++ {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(results); j++ {
			if !keep[j] {
				continue
			}
			if jaccardSimilarity(sets[i], sets[j]) >= jaccardThreshold {
				// Drop the lower-scored duplicate.
				if results[i].Score >= results[j].Score {
					keep[j] = false
				} else {
					keep[i] = false
					break
				}
			}
		}
	}

	deduped := make([]*memory.Entry, 0, len(results))
	for i, r := range results {
		if keep[i] {
			deduped = append(deduped, r)
		}
	}
	return deduped
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for w := range a {
		if _, ok := b[w]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), memory_search is exposed by default,
// memory_load is exposed once enabled, and other enabled tools remain hidden
// unless explicitly exposed.
// Without an extractor, enabled tools are exposed directly.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async processing.
// The session contains the full transcript and state for incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close closes the database connection and stops async workers.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// scanMemoryEntry scans a memory entry from database rows.
func scanMemoryEntry(rows *sql.Rows) (*memory.Entry, error) {
	var (
		memoryID      string
		appName       string
		userID        string
		memoryContent string
		topics        pq.StringArray
		memoryKind    string
		eventTime     sql.NullTime
		participants  pq.StringArray
		location      sql.NullString
		createdAt     time.Time
		updatedAt     time.Time
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent, &topics,
		&memoryKind, &eventTime, &participants, &location,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry failed: %w", err)
	}

	return buildEntry(memoryID, appName, userID, memoryContent,
		topics, memoryKind, eventTime, participants, location,
		createdAt, updatedAt), nil
}

// scanMemoryEntryWithSimilarity scans a memory entry with similarity score.
// It reads the score from database rows.
func scanMemoryEntryWithSimilarity(rows *sql.Rows) (*memory.Entry, error) {
	var (
		memoryID      string
		appName       string
		userID        string
		memoryContent string
		topics        pq.StringArray
		memoryKind    string
		eventTime     sql.NullTime
		participants  pq.StringArray
		location      sql.NullString
		createdAt     time.Time
		updatedAt     time.Time
		similarity    float64
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent, &topics,
		&memoryKind, &eventTime, &participants, &location,
		&createdAt, &updatedAt, &similarity,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry with similarity failed: %w", err)
	}

	entry := buildEntry(memoryID, appName, userID, memoryContent,
		topics, memoryKind, eventTime, participants, location,
		createdAt, updatedAt)
	entry.Score = similarity
	return entry, nil
}

// buildEntry constructs a memory.Entry from scanned row fields.
func buildEntry(
	memoryID, appName, userID, memoryContent string,
	topics pq.StringArray,
	memoryKind string,
	eventTime sql.NullTime,
	participants pq.StringArray,
	location sql.NullString,
	createdAt, updatedAt time.Time,
) *memory.Entry {
	mem := &memory.Memory{
		Memory:      memoryContent,
		Topics:      []string(topics),
		LastUpdated: &updatedAt,
		Kind:        memory.Kind(memoryKind),
	}
	if eventTime.Valid {
		mem.EventTime = &eventTime.Time
	}
	if len(participants) > 0 {
		mem.Participants = []string(participants)
	}
	if location.Valid {
		mem.Location = location.String
	}
	imemory.NormalizeMemory(mem)

	return &memory.Entry{
		ID:        memoryID,
		AppName:   appName,
		UserID:    userID,
		Memory:    mem,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
}

// convertToFloat32 converts a float64 slice to float32 slice.
func convertToFloat32(embedding []float64) []float32 {
	result := make([]float32, len(embedding))
	for i, v := range embedding {
		result[i] = float32(v)
	}
	return result
}

// metadataSQLFields holds metadata field values resolved
// for SQL parameters.
type metadataSQLFields struct {
	kind         string
	eventTime    *time.Time
	participants []string
	location     *string
}

// resolveMetadata converts a stored memory object to SQL-ready metadata values.
func resolveMetadata(mem *memory.Memory) metadataSQLFields {
	f := metadataSQLFields{
		participants: []string{},
	}
	if mem == nil {
		return f
	}
	imemory.NormalizeMemory(mem)
	f.kind = string(mem.Kind)
	f.eventTime = mem.EventTime
	if len(mem.Participants) > 0 {
		f.participants = mem.Participants
	}
	if mem.Location != "" {
		location := mem.Location
		f.location = &location
	}
	return f
}
