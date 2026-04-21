//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysqlvec provides a MySQL-based memory service with vector similarity
// search support. It uses MySQL 9.0+ native VECTOR type when available, and
// falls back to BLOB storage with Go-side cosine similarity for older versions.
package mysqlvec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the mysqlvec memory service.
// Storage structure:
//
//	Table: memories (configurable).
//	Columns: memory_id, app_name, user_id, memory_content, topics, embedding,
//	         memory_kind, event_time, participants, location, created_at, updated_at, deleted_at.
//	Primary key: memory_id.
//	Indexes: (app_name, user_id), updated_at, deleted_at, event_time, kind, fulltext(memory_content).
type Service struct {
	opts           ServiceOpts
	db             storage.Client
	tableName      string
	supportsVector bool

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new mysqlvec memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	// Validate embedder is provided.
	if opts.embedder == nil {
		return nil, fmt.Errorf("embedder is required for mysqlvec memory service")
	}

	// Apply auto mode defaults after all options are applied.
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderDSN(opts.dsn),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// Priority: dsn > instanceName.
	if opts.dsn == "" && opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetMySQLInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("mysql instance %s not found", opts.instanceName)
		}
	}

	db, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mysql client failed: %w", err)
	}

	s := &Service{
		opts:        opts,
		db:          db,
		tableName:   opts.tableName,
		cachedTools: make(map[string]tool.Tool),
	}

	// Always detect vector support (even when skipDBInit is set) so that
	// pre-created MySQL 9.0+ VECTOR tables are not forced onto the BLOB path.
	{
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBInitTimeout)
		defer cancel()
		s.supportsVector = s.detectVectorSupport(ctx)
	}

	// Initialize database schema unless skipped.
	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBInitTimeout)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}

	// Pre-compute tools list.
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

// AddMemory adds or updates a memory for a user (idempotent).
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

	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		return fmt.Errorf("marshal topics failed: %w", err)
	}

	// Enforce memory limit.
	if s.opts.memoryLimit > 0 {
		countQuery := fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE app_name = ? AND user_id = ?",
			s.tableName,
		)
		if s.opts.softDelete {
			countQuery += " AND deleted_at IS NULL"
		}
		var count int
		if err := s.db.QueryRow(ctx, []any{&count}, countQuery, userKey.AppName, userKey.UserID); err != nil {
			return fmt.Errorf("check memory count failed: %w", err)
		}
		if count >= s.opts.memoryLimit {
			return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
				userKey.UserID, s.opts.memoryLimit, count)
		}
	}

	embeddingBlob := serializeVector(embedding)
	ef := resolveMetadata(mem)

	var embeddingExpr string
	if s.supportsVector {
		embeddingExpr = "STRING_TO_VECTOR(?)"
	} else {
		embeddingExpr = "?"
	}

	var embeddingArg any
	if s.supportsVector {
		embeddingArg = vectorToString(embedding)
	} else {
		embeddingArg = embeddingBlob
	}

	insertQuery := fmt.Sprintf(
		"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
			"embedding, memory_kind, event_time, participants, location, "+
			"created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, "+embeddingExpr+", ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE "+
			"memory_content = VALUES(memory_content), "+
			"topics = VALUES(topics), "+
			"embedding = VALUES(embedding), "+
			"memory_kind = VALUES(memory_kind), "+
			"event_time = VALUES(event_time), "+
			"participants = VALUES(participants), "+
			"location = VALUES(location), "+
			"deleted_at = NULL, "+
			"updated_at = VALUES(updated_at)",
		s.tableName,
	)

	_, err = s.db.Exec(ctx, insertQuery,
		memoryID,
		userKey.AppName,
		userKey.UserID,
		memoryStr,
		string(topicsJSON),
		embeddingArg,
		ef.kind,
		ef.eventTime,
		ef.participants,
		ef.location,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}

	return nil
}

// UpdateMemory updates an existing memory for a user.
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
			"created_at, updated_at FROM %s WHERE memory_id = ? AND app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}

	var entry *memory.Entry
	var found bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if found {
			return storage.ErrBreak
		}
		var scanErr error
		entry, scanErr = scanEntryFromRows(rows)
		found = true
		return scanErr
	}, selectQuery, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
	if err != nil {
		return fmt.Errorf("load memory entry failed: %w", err)
	}
	if !found {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	// Generate new embedding for the updated content.
	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding failed: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(embedding))
	}

	now := time.Now()
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)

	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		return fmt.Errorf("marshal topics failed: %w", err)
	}
	ef := resolveMetadata(entry.Memory)

	var embeddingExpr string
	var embeddingArg any
	if s.supportsVector {
		embeddingExpr = "STRING_TO_VECTOR(?)"
		embeddingArg = vectorToString(embedding)
	} else {
		embeddingExpr = "?"
		embeddingArg = serializeVector(embedding)
	}

	if newID == memoryKey.MemoryID {
		err = s.updateInPlace(ctx, memoryKey, memoryStr, topicsJSON, embeddingExpr, embeddingArg, ef, now)
	} else {
		err = s.rotateMemory(ctx, memoryKey, newID, memoryStr, topicsJSON,
			embeddingExpr, embeddingArg, ef, entry.CreatedAt, now)
	}
	if err != nil {
		return err
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}

	return nil
}

// updateInPlace updates a memory entry without changing its ID.
func (s *Service) updateInPlace(
	ctx context.Context,
	memoryKey memory.Key,
	memoryStr string,
	topicsJSON []byte,
	embeddingExpr string,
	embeddingArg any,
	ef metadataSQLFields,
	now time.Time,
) error {
	updateQuery := fmt.Sprintf(
		"UPDATE %s SET memory_content = ?, topics = ?, embedding = "+embeddingExpr+", "+
			"memory_kind = ?, event_time = ?, participants = ?, location = ?, updated_at = ? "+
			"WHERE memory_id = ? AND app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		updateQuery += " AND deleted_at IS NULL"
	}
	res, err := s.db.Exec(ctx, updateQuery,
		memoryStr, string(topicsJSON), embeddingArg,
		ef.kind, ef.eventTime, ef.participants, ef.location, now,
		memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID,
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
	return nil
}

// rotateMemory replaces a memory entry with a new ID via DELETE + INSERT in a transaction.
func (s *Service) rotateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	newID, memoryStr string,
	topicsJSON []byte,
	embeddingExpr string,
	embeddingArg any,
	ef metadataSQLFields,
	createdAt time.Time,
	now time.Time,
) error {
	return s.db.Transaction(ctx, func(tx *sql.Tx) error { // nolint:gosec // table name is validated
		deleteQuery := fmt.Sprintf(
			"DELETE FROM %s WHERE memory_id = ? AND app_name = ? AND user_id = ?",
			s.tableName,
		)
		if s.opts.softDelete {
			deleteQuery += " AND deleted_at IS NULL"
		}
		res, err := tx.ExecContext(ctx, deleteQuery, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
		if err != nil {
			return fmt.Errorf("delete rotated memory: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete rotated memory rows affected: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}

		insertQuery := fmt.Sprintf(
			"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
				"embedding, memory_kind, event_time, participants, location, "+
				"created_at, updated_at) "+
				"VALUES (?, ?, ?, ?, ?, "+embeddingExpr+", ?, ?, ?, ?, ?, ?)",
			s.tableName,
		)
		_, err = tx.ExecContext(ctx, insertQuery,
			newID, memoryKey.AppName, memoryKey.UserID,
			memoryStr, string(topicsJSON), embeddingArg,
			ef.kind, ef.eventTime, ef.participants, ef.location,
			createdAt, now,
		)
		if err != nil {
			return fmt.Errorf("insert rotated memory: %w", err)
		}
		return nil
	})
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
			"UPDATE %s SET deleted_at = ? "+
				"WHERE memory_id = ? AND app_name = ? AND user_id = ? "+
				"AND deleted_at IS NULL",
			s.tableName,
		)
		args = []any{now, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID}
	} else {
		query = fmt.Sprintf(
			"DELETE FROM %s WHERE memory_id = ? AND app_name = ? AND user_id = ?",
			s.tableName,
		)
		args = []any{memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID}
	}
	_, err := s.db.Exec(ctx, query, args...)
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
			"UPDATE %s SET deleted_at = ? "+
				"WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL",
			s.tableName,
		)
		_, err = s.db.Exec(ctx, query, now, userKey.AppName, userKey.UserID)
	} else {
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = ? AND user_id = ?",
			s.tableName,
		)
		_, err = s.db.Exec(ctx, query, userKey.AppName, userKey.UserID)
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
			"created_at, updated_at FROM %s WHERE app_name = ? AND user_id = ?",
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
		entry, err := scanEntryFromRows(rows)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	}, query.String(), userKey.AppName, userKey.UserID)

	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}

	return entries, nil
}

// minKindFallbackResults triggers a fallback unfiltered search when
// a kind-filtered search returns fewer results than this.
const minKindFallbackResults = 3

// SearchMemories searches memories for a user using vector similarity.
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

	queryEmbedding, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding failed: %w", err)
	}
	if len(queryEmbedding) != s.opts.indexDimension {
		return nil, fmt.Errorf("query embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(queryEmbedding))
	}

	maxResults := s.opts.maxResults
	if opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}

	results, err := s.doVectorOrBruteSearch(ctx, userKey, opts, queryEmbedding, maxResults)
	if err != nil {
		return nil, err
	}

	results = s.applyKindFallback(ctx, results, userKey, opts, queryEmbedding, maxResults)
	results = s.applyHybridSearch(ctx, results, userKey, opts, maxResults)
	results = s.applyPostSearchFilters(results, opts, maxResults)

	return results, nil
}

// doVectorOrBruteSearch dispatches to native VECTOR or brute-force search.
func (s *Service) doVectorOrBruteSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryEmbedding []float64,
	maxResults int,
) ([]*memory.Entry, error) {
	if s.supportsVector {
		return s.vectorSearch(ctx, userKey, opts, queryEmbedding, maxResults)
	}
	return s.bruteForceSearch(ctx, userKey, opts, queryEmbedding, maxResults)
}

// applyKindFallback merges unfiltered results when kind-filtered search returns too few.
func (s *Service) applyKindFallback(
	ctx context.Context,
	results []*memory.Entry,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryEmbedding []float64,
	maxResults int,
) []*memory.Entry {
	if opts.Kind == "" || !opts.KindFallback || len(results) >= minKindFallbackResults {
		return results
	}
	fallbackOpts := opts
	fallbackOpts.Kind = ""
	fallbackOpts.KindFallback = false
	fallbackResults, err := s.doVectorOrBruteSearch(ctx, userKey, fallbackOpts, queryEmbedding, maxResults)
	if err == nil && len(fallbackResults) > 0 {
		return imemory.MergeSearchResults(results, fallbackResults, opts.Kind, maxResults)
	}
	return results
}

// applyHybridSearch performs keyword fulltext search and merges via RRF.
func (s *Service) applyHybridSearch(
	ctx context.Context,
	results []*memory.Entry,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) []*memory.Entry {
	if !opts.HybridSearch {
		return results
	}
	keywordResults, kwErr := s.executeKeywordSearch(ctx, userKey, opts, maxResults)
	if kwErr != nil || len(keywordResults) == 0 {
		return results
	}
	rrfK := opts.HybridRRFK
	if rrfK <= 0 {
		rrfK = imemory.DefaultHybridRRFK
	}
	return imemory.MergeHybridResults(results, keywordResults, rrfK, maxResults)
}

// applyPostSearchFilters applies threshold, sorting, dedup, and limit.
func (s *Service) applyPostSearchFilters(
	results []*memory.Entry,
	opts memory.SearchOptions,
	maxResults int,
) []*memory.Entry {
	// Similarity threshold (skip for hybrid since RRF uses different scores).
	if !opts.HybridSearch {
		threshold := s.opts.similarityThreshold
		if opts.SimilarityThreshold > 0 {
			threshold = opts.SimilarityThreshold
		}
		if threshold > 0 {
			filtered := results[:0]
			for _, r := range results {
				if r.Score >= threshold {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}
	}

	if len(results) > 1 {
		if opts.Kind != "" && opts.KindFallback {
			imemory.SortSearchResultsWithKindPriority(results, opts.Kind, opts.OrderByEventTime)
		} else {
			imemory.SortSearchResults(results, opts.OrderByEventTime)
		}
	}

	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

// vectorSearch uses MySQL 9.0+ native VECTOR distance function.
func (s *Service) vectorSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryEmbedding []float64,
	maxResults int,
) ([]*memory.Entry, error) {
	var searchQuery strings.Builder
	vecStr := vectorToString(queryEmbedding)
	// Args order matches SQL placeholders: SELECT(vecStr), WHERE(appName, userID), ..., ORDER BY(vecStr).
	args := []any{vecStr, userKey.AppName, userKey.UserID}

	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, "+
			"(1 - DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE')) AS similarity "+
			"FROM %s WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}

	if opts.Kind != "" {
		if opts.Kind == memory.KindFact {
			searchQuery.WriteString(" AND (memory_kind = ? OR memory_kind = '')")
		} else {
			searchQuery.WriteString(" AND memory_kind = ?")
		}
		args = append(args, string(opts.Kind))
	}
	if opts.TimeAfter != nil {
		searchQuery.WriteString(" AND (event_time >= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeAfter)
	}
	if opts.TimeBefore != nil {
		searchQuery.WriteString(" AND (event_time <= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeBefore)
	}

	// Append vecStr again for the ORDER BY DISTANCE clause.
	args = append(args, vecStr)
	fmt.Fprintf(&searchQuery,
		" ORDER BY DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE') LIMIT %d",
		maxResults,
	)

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		entry, scanErr := scanEntryWithSimilarityFromRows(rows)
		if scanErr != nil {
			return scanErr
		}
		results = append(results, entry)
		return nil
	}, searchQuery.String(), args...)

	if err != nil {
		return nil, fmt.Errorf("vector search memories failed: %w", err)
	}
	return results, nil
}

// bruteForceSearch loads all embeddings and computes cosine similarity in Go.
// Used as fallback for MySQL 8.x without native VECTOR support.
func (s *Service) bruteForceSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryEmbedding []float64,
	maxResults int,
) ([]*memory.Entry, error) {
	var searchQuery strings.Builder
	args := []any{userKey.AppName, userKey.UserID}

	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, embedding FROM %s "+
			"WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}
	if opts.Kind != "" {
		if opts.Kind == memory.KindFact {
			searchQuery.WriteString(" AND (memory_kind = ? OR memory_kind = '')")
		} else {
			searchQuery.WriteString(" AND memory_kind = ?")
		}
		args = append(args, string(opts.Kind))
	}
	if opts.TimeAfter != nil {
		searchQuery.WriteString(" AND (event_time >= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeAfter)
	}
	if opts.TimeBefore != nil {
		searchQuery.WriteString(" AND (event_time <= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeBefore)
	}

	type scoredEntry struct {
		entry *memory.Entry
		score float64
	}
	var scored []scoredEntry

	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		entry, embeddingBlob, scanErr := scanEntryWithEmbeddingFromRows(rows)
		if scanErr != nil {
			return scanErr
		}
		docEmbedding, err := deserializeVector(embeddingBlob)
		if err != nil {
			return fmt.Errorf("deserialize embedding: %w", err)
		}
		sim := cosineSimilarity(queryEmbedding, docEmbedding)
		entry.Score = sim
		scored = append(scored, scoredEntry{entry: entry, score: sim})
		return nil
	}, searchQuery.String(), args...)

	if err != nil {
		return nil, fmt.Errorf("brute force search memories failed: %w", err)
	}

	// Sort by similarity descending.
	slices.SortFunc(scored, func(a, b scoredEntry) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})

	results := make([]*memory.Entry, 0, min(maxResults, len(scored)))
	for i, s := range scored {
		if maxResults > 0 && i >= maxResults {
			break
		}
		results = append(results, s.entry)
	}
	return results, nil
}

// executeKeywordSearch uses MySQL FULLTEXT index for hybrid search.
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

	// Use MATCH AGAINST with natural language mode.
	var searchQuery strings.Builder
	args := []any{query, userKey.AppName, userKey.UserID}

	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, "+
			"MATCH(memory_content) AGAINST(? IN NATURAL LANGUAGE MODE) AS relevance "+
			"FROM %s WHERE app_name = ? AND user_id = ? "+
			"AND MATCH(memory_content) AGAINST(? IN NATURAL LANGUAGE MODE)",
		s.tableName,
	)
	args = append(args, query)

	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}
	if opts.Kind != "" {
		if opts.Kind == memory.KindFact {
			searchQuery.WriteString(" AND (memory_kind = ? OR memory_kind = '')")
		} else {
			searchQuery.WriteString(" AND memory_kind = ?")
		}
		args = append(args, string(opts.Kind))
	}
	if opts.TimeAfter != nil {
		searchQuery.WriteString(" AND (event_time >= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeAfter)
	}
	if opts.TimeBefore != nil {
		searchQuery.WriteString(" AND (event_time <= ? OR event_time IS NULL)")
		args = append(args, *opts.TimeBefore)
	}

	searchQuery.WriteString(" ORDER BY relevance DESC")
	fmt.Fprintf(&searchQuery, " LIMIT %d", maxResults)

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		entry, scanErr := scanEntryWithSimilarityFromRows(rows)
		if scanErr != nil {
			return scanErr
		}
		results = append(results, entry)
		return nil
	}, searchQuery.String(), args...)

	if err != nil {
		// Keyword search failure is non-fatal.
		return []*memory.Entry{}, nil
	}
	return results, nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async processing.
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

// --- Helper functions ---

// scanEntryFromRows scans a memory entry from SQL rows (without similarity/embedding).
func scanEntryFromRows(rows *sql.Rows) (*memory.Entry, error) {
	var (
		memoryID         string
		appName          string
		userID           string
		memoryContent    string
		topicsJSON       sql.NullString
		memoryKind       string
		eventTime        sql.NullTime
		participantsJSON sql.NullString
		location         sql.NullString
		createdAt        sql.NullTime
		updatedAt        sql.NullTime
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent,
		&topicsJSON, &memoryKind, &eventTime,
		&participantsJSON, &location,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry failed: %w", err)
	}

	return buildEntry(memoryID, appName, userID, memoryContent,
		topicsJSON, memoryKind, eventTime, participantsJSON,
		location, createdAt, updatedAt), nil
}

// scanEntryWithSimilarityFromRows scans a memory entry with a similarity score.
func scanEntryWithSimilarityFromRows(rows *sql.Rows) (*memory.Entry, error) {
	var (
		memoryID         string
		appName          string
		userID           string
		memoryContent    string
		topicsJSON       sql.NullString
		memoryKind       string
		eventTime        sql.NullTime
		participantsJSON sql.NullString
		location         sql.NullString
		createdAt        sql.NullTime
		updatedAt        sql.NullTime
		similarity       float64
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent,
		&topicsJSON, &memoryKind, &eventTime,
		&participantsJSON, &location,
		&createdAt, &updatedAt,
		&similarity,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry with similarity failed: %w", err)
	}

	entry := buildEntry(memoryID, appName, userID, memoryContent,
		topicsJSON, memoryKind, eventTime, participantsJSON,
		location, createdAt, updatedAt)
	entry.Score = similarity
	return entry, nil
}

// scanEntryWithEmbeddingFromRows scans a memory entry and its embedding blob.
func scanEntryWithEmbeddingFromRows(rows *sql.Rows) (*memory.Entry, []byte, error) {
	var (
		memoryID         string
		appName          string
		userID           string
		memoryContent    string
		topicsJSON       sql.NullString
		memoryKind       string
		eventTime        sql.NullTime
		participantsJSON sql.NullString
		location         sql.NullString
		createdAt        sql.NullTime
		updatedAt        sql.NullTime
		embedding        []byte
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent,
		&topicsJSON, &memoryKind, &eventTime,
		&participantsJSON, &location,
		&createdAt, &updatedAt,
		&embedding,
	); err != nil {
		return nil, nil, fmt.Errorf("scan memory entry with embedding failed: %w", err)
	}

	entry := buildEntry(memoryID, appName, userID, memoryContent,
		topicsJSON, memoryKind, eventTime, participantsJSON,
		location, createdAt, updatedAt)
	return entry, embedding, nil
}

// buildEntry constructs a memory.Entry from scanned row fields.
func buildEntry(
	memoryID, appName, userID, memoryContent string,
	topicsJSON sql.NullString,
	memoryKind string,
	eventTime sql.NullTime,
	participantsJSON sql.NullString,
	location sql.NullString,
	createdAt, updatedAt sql.NullTime,
) *memory.Entry {
	topics := parseJSONStringSlice(topicsJSON.String)
	participants := parseJSONStringSlice(participantsJSON.String)

	var ca, ua time.Time
	if createdAt.Valid {
		ca = createdAt.Time
	}
	if updatedAt.Valid {
		ua = updatedAt.Time
	}

	mem := &memory.Memory{
		Memory:       memoryContent,
		Topics:       topics,
		LastUpdated:  &ua,
		Kind:         memory.Kind(memoryKind),
		Participants: participants,
	}
	if eventTime.Valid {
		mem.EventTime = &eventTime.Time
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
		CreatedAt: ca,
		UpdatedAt: ua,
	}
}

// metadataSQLFields holds metadata field values resolved for SQL parameters.
type metadataSQLFields struct {
	kind         string
	eventTime    *time.Time
	participants *string
	location     *string
}

// resolveMetadata converts a stored memory object to SQL-ready metadata values.
func resolveMetadata(mem *memory.Memory) metadataSQLFields {
	f := metadataSQLFields{}
	if mem == nil {
		return f
	}
	imemory.NormalizeMemory(mem)
	f.kind = string(mem.Kind)
	f.eventTime = mem.EventTime
	if len(mem.Participants) > 0 {
		data, _ := json.Marshal(mem.Participants)
		s := string(data)
		f.participants = &s
	}
	if mem.Location != "" {
		location := mem.Location
		f.location = &location
	}
	return f
}

// parseJSONStringSlice parses a JSON array string into a string slice.
func parseJSONStringSlice(s string) []string {
	if s == "" || s == "null" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil
	}
	return result
}
