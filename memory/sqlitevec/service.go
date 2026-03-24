//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlitevec provides a SQLite-backed memory service powered by
// sqlite-vec.
package sqlitevec

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

const (
	sqlVectorFromBlob       = "vec_f32(?)"
	notDeletedAtNs    int64 = 0
)

var vecInitOnce sync.Once

// Service is the sqlite-vec memory service.
type Service struct {
	opts      ServiceOpts
	db        *sql.DB
	tableName string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new sqlite-vec memory service.
//
// The service owns the passed-in db and will close it in Close().
func NewService(db *sql.DB, options ...ServiceOpt) (*Service, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}

	vecInitOnce.Do(vecAuto)

	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	if opts.embedder == nil {
		return nil, errors.New("embedder is required")
	}

	if opts.indexDimension <= 0 {
		opts.indexDimension = opts.embedder.GetDimensions()
	}
	if opts.indexDimension <= 0 {
		return nil, errors.New("indexDimension is required")
	}

	if opts.maxResults <= 0 {
		opts.maxResults = defaultMaxResults
	}

	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	s := &Service{
		opts:        opts,
		db:          db,
		tableName:   opts.tableName,
		cachedTools: make(map[string]tool.Tool),
	}

	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			defaultDBInitTimeout,
		)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			return nil, fmt.Errorf("init database: %w", err)
		}
	}

	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)

	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor,
			opts.enabledTools,
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
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	ep := memory.ResolveAddOptions(opts)

	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return fmt.Errorf(
			"embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension,
			len(embedding),
		)
	}

	blob, err := s.serializeEmbedding(embedding)
	if err != nil {
		return fmt.Errorf("serialize embedding: %w", err)
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
		return fmt.Errorf("marshal topics: %w", err)
	}
	participantsJSON, err := marshalStringSlice(mem.Participants)
	if err != nil {
		return fmt.Errorf("marshal participants: %w", err)
	}
	eventTimeNs := metadataEventTimeNS(mem.EventTime)
	location := metadataLocationValue(mem.Location)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updatedAtNs := now.UTC().UnixNano()

	existingDeletedAt, exists, err := s.getDeletedAtTx(
		ctx,
		tx,
		userKey,
		memoryID,
	)
	if err != nil {
		return err
	}

	if s.opts.memoryLimit > 0 {
		needLimit := !exists
		if exists && s.opts.softDelete &&
			existingDeletedAt != notDeletedAtNs {
			needLimit = true
		}
		if needLimit {
			if err := s.enforceMemoryLimitTx(ctx, tx, userKey); err != nil {
				return err
			}
		}
	}

	if exists {
		const updateSQL = `UPDATE %s SET
embedding = ` + sqlVectorFromBlob + `,
updated_at = ?, deleted_at = ?,
memory_content = ?, topics = ?,
memory_kind = ?, event_time = ?, participants = ?, location = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?`
		query := fmt.Sprintf(updateSQL, s.tableName)
		res, err := tx.ExecContext(
			ctx,
			query,
			blob,
			updatedAtNs,
			notDeletedAtNs,
			memoryStr,
			string(topicsJSON),
			string(mem.Kind),
			eventTimeNs,
			participantsJSON,
			location,
			userKey.AppName,
			userKey.UserID,
			memoryID,
		)
		if err != nil {
			return fmt.Errorf("update memory: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update memory rows affected: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("memory with id %s not found", memoryID)
		}
		return tx.Commit()
	}

	createdAtNs := updatedAtNs
	const insertSQL = `INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind, event_time,
participants, location
) VALUES (?, ` + sqlVectorFromBlob + `, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	query := fmt.Sprintf(insertSQL, s.tableName)
	_, err = tx.ExecContext(
		ctx,
		query,
		memoryID,
		blob,
		userKey.AppName,
		userKey.UserID,
		createdAtNs,
		updatedAtNs,
		notDeletedAtNs,
		memoryStr,
		string(topicsJSON),
		string(mem.Kind),
		eventTimeNs,
		participantsJSON,
		location,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	return tx.Commit()
}

func (s *Service) serializeEmbedding(embedding []float64) ([]byte, error) {
	out := make([]float32, len(embedding))
	for i, v := range embedding {
		out[i] = float32(v)
	}
	return vecSerializeFloat32(out)
}

func (s *Service) getDeletedAtTx(
	ctx context.Context,
	tx *sql.Tx,
	userKey memory.UserKey,
	memoryID string,
) (int64, bool, error) {
	const selectSQL = `SELECT deleted_at FROM %s
WHERE app_name = ? AND user_id = ? AND memory_id = ?
LIMIT 1`
	query := fmt.Sprintf(selectSQL, s.tableName)
	row := tx.QueryRowContext(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		memoryID,
	)

	var deletedAt int64
	if err := row.Scan(&deletedAt); errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf(
			"select existing memory: %w",
			err,
		)
	}
	return deletedAt, true, nil
}

func (s *Service) enforceMemoryLimitTx(
	ctx context.Context,
	tx *sql.Tx,
	userKey memory.UserKey,
) error {
	const countSQL = `SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ?`
	query := fmt.Sprintf(countSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}

	var count int
	row := tx.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&count); err != nil {
		return fmt.Errorf("check memory count: %w", err)
	}

	if count < s.opts.memoryLimit {
		return nil
	}

	return fmt.Errorf(
		"memory limit exceeded for user %s, limit: %d, current: %d",
		userKey.UserID,
		s.opts.memoryLimit,
		count,
	)
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

	const selectSQL = `SELECT
memory_id, memory_content, topics, memory_kind, event_time,
participants, location, created_at, updated_at
FROM %s WHERE app_name = ? AND user_id = ? AND memory_id = ?`
	selectQuery := fmt.Sprintf(selectSQL, s.tableName)
	selectArgs := []any{memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID}
	if s.opts.softDelete {
		selectQuery += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}
	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return fmt.Errorf("load memory: %w", err)
	}
	var entry *memory.Entry
	if rows.Next() {
		entry, err = scanEntry(rows, memoryKey.AppName, memoryKey.UserID)
	}
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close memory rows: %w", closeErr)
	}
	if entry == nil {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return fmt.Errorf(
			"embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension,
			len(embedding),
		)
	}

	blob, err := s.serializeEmbedding(embedding)
	if err != nil {
		return fmt.Errorf("serialize embedding: %w", err)
	}

	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		return fmt.Errorf("marshal topics: %w", err)
	}

	now := time.Now()
	updatedAtNs := now.UTC().UnixNano()
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)
	participantsJSON, err := marshalStringSlice(entry.Memory.Participants)
	if err != nil {
		return fmt.Errorf("marshal participants: %w", err)
	}
	query := fmt.Sprintf(
		`UPDATE %s SET
embedding = `+sqlVectorFromBlob+`,
updated_at = ?, memory_content = ?, topics = ?,
memory_kind = ?, event_time = ?, participants = ?, location = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?`,
		s.tableName,
	)
	args := []any{
		blob,
		updatedAtNs,
		memoryStr,
		string(topicsJSON),
		string(entry.Memory.Kind),
		metadataEventTimeNS(entry.Memory.EventTime),
		participantsJSON,
		metadataLocationValue(entry.Memory.Location),
		memoryKey.AppName,
		memoryKey.UserID,
		memoryKey.MemoryID,
	}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}
	if newID == memoryKey.MemoryID {
		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update memory: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update memory rows affected: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
	} else {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		deleteQuery := fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = ? AND user_id = ? AND memory_id = ?",
			s.tableName,
		)
		deleteArgs := []any{memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID}
		if s.opts.softDelete {
			deleteQuery += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
		}
		res, err := tx.ExecContext(ctx, deleteQuery, deleteArgs...)
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
			`INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind, event_time,
participants, location
) VALUES (?, `+sqlVectorFromBlob+`, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.tableName,
		)
		_, err = tx.ExecContext(
			ctx,
			insertQuery,
			newID,
			blob,
			memoryKey.AppName,
			memoryKey.UserID,
			entry.CreatedAt.UTC().UnixNano(),
			updatedAtNs,
			notDeletedAtNs,
			memoryStr,
			string(topicsJSON),
			string(entry.Memory.Kind),
			metadataEventTimeNS(entry.Memory.EventTime),
			participantsJSON,
			metadataLocationValue(entry.Memory.Location),
		)
		if err != nil {
			return fmt.Errorf("insert rotated memory: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit rotated memory: %w", err)
		}
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}

	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
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
				"WHERE app_name = ? AND user_id = ? AND memory_id = ? "+
				"AND deleted_at = %d",
			s.tableName,
			notDeletedAtNs,
		)
		args = []any{
			now.UTC().UnixNano(),
			memoryKey.AppName,
			memoryKey.UserID,
			memoryKey.MemoryID,
		}
	} else {
		query = fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = ? AND user_id = ? AND memory_id = ?",
			s.tableName,
		)
		args = []any{memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID}
	}

	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	if err := userKey.CheckUserKey(); err != nil {
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
				"WHERE app_name = ? AND user_id = ? AND deleted_at = %d",
			s.tableName,
			notDeletedAtNs,
		)
		args = []any{now.UTC().UnixNano(), userKey.AppName, userKey.UserID}
	} else {
		query = fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = ? AND user_id = ?",
			s.tableName,
		)
		args = []any{userKey.AppName, userKey.UserID}
	}

	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("clear memories: %w", err)
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

	const selectSQL = `SELECT
memory_id, memory_content, topics, memory_kind, event_time,
participants, location, created_at, updated_at
FROM %s WHERE app_name = ? AND user_id = ?`
	query := fmt.Sprintf(selectSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}
	query += " ORDER BY updated_at DESC, created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	defer rows.Close()

	entries := make([]*memory.Entry, 0)
	for rows.Next() {
		entry, err := scanEntry(rows, userKey.AppName, userKey.UserID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memories: %w", err)
	}

	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	queryStr string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	queryStr = strings.TrimSpace(queryStr)
	if queryStr == "" {
		return []*memory.Entry{}, nil
	}

	embedding, err := s.opts.embedder.GetEmbedding(ctx, queryStr)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return nil, fmt.Errorf(
			"query embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension,
			len(embedding),
		)
	}

	blob, err := s.serializeEmbedding(embedding)
	if err != nil {
		return nil, fmt.Errorf("serialize query embedding: %w", err)
	}

	searchOpts := memory.ResolveSearchOptions(queryStr, opts)
	candidates, err := s.searchWithOptions(ctx, userKey, searchOpts, blob)
	if err != nil {
		return nil, err
	}

	limit := resolveSearchLimit(s.opts.maxResults, searchOpts.MaxResults)
	results := applySearchFilters(candidates, searchOpts)
	if searchOpts.Kind != "" &&
		searchOpts.KindFallback &&
		len(results) < imemory.MinKindFallbackResults {
		fallbackOpts := searchOpts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallbackResults := applySearchFilters(candidates, fallbackOpts)
		if len(fallbackResults) > 0 {
			results = imemory.MergeSearchResults(
				results, fallbackResults, searchOpts.Kind, limit,
			)
		}
	}
	if searchOpts.HybridSearch {
		keywordResults, kwErr := s.executeKeywordSearch(
			ctx,
			userKey,
			searchOpts,
		)
		if kwErr == nil && len(keywordResults) > 0 {
			rrfK := searchOpts.HybridRRFK
			if rrfK <= 0 {
				rrfK = imemory.DefaultHybridRRFK
			}
			results = imemory.MergeHybridResults(
				results,
				keywordResults,
				rrfK,
				limit,
			)
		}
	}
	if searchOpts.SimilarityThreshold > 0 &&
		len(results) > 0 &&
		!searchOpts.HybridSearch {
		filtered := results[:0]
		for _, entry := range results {
			if entry.Score >= searchOpts.SimilarityThreshold {
				filtered = append(filtered, entry)
			}
		}
		results = filtered
	}
	if len(results) > 1 {
		if searchOpts.Kind != "" && searchOpts.KindFallback {
			imemory.SortSearchResultsWithKindPriority(
				results,
				searchOpts.Kind,
				searchOpts.OrderByEventTime,
			)
		} else {
			imemory.SortSearchResults(results, searchOpts.OrderByEventTime)
		}
	}
	if searchOpts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func scanEntry(
	rows *sql.Rows,
	appName string,
	userID string,
) (*memory.Entry, error) {
	var (
		memoryID      string
		memoryContent string
		topicsJSON    string
		memoryKind    sql.NullString
		eventTimeNs   sql.NullInt64
		participants  sql.NullString
		location      sql.NullString
		createdAtNs   int64
		updatedAtNs   int64
	)
	if err := rows.Scan(
		&memoryID,
		&memoryContent,
		&topicsJSON,
		&memoryKind,
		&eventTimeNs,
		&participants,
		&location,
		&createdAtNs,
		&updatedAtNs,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry: %w", err)
	}
	return buildScannedEntry(
		appName,
		userID,
		memoryID,
		memoryContent,
		topicsJSON,
		memoryKind,
		eventTimeNs,
		participants,
		location,
		createdAtNs,
		updatedAtNs,
	)
}

func scanSearchEntryWithSimilarity(
	rows *sql.Rows,
	appName string,
	userID string,
) (*memory.Entry, error) {
	var (
		memoryID      string
		memoryContent string
		topicsJSON    string
		memoryKind    sql.NullString
		eventTimeNs   sql.NullInt64
		participants  sql.NullString
		location      sql.NullString
		createdAtNs   int64
		updatedAtNs   int64
		distance      float64
	)
	if err := rows.Scan(
		&memoryID,
		&memoryContent,
		&topicsJSON,
		&memoryKind,
		&eventTimeNs,
		&participants,
		&location,
		&createdAtNs,
		&updatedAtNs,
		&distance,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry: %w", err)
	}
	entry, err := buildScannedEntry(
		appName,
		userID,
		memoryID,
		memoryContent,
		topicsJSON,
		memoryKind,
		eventTimeNs,
		participants,
		location,
		createdAtNs,
		updatedAtNs,
	)
	if err != nil {
		return nil, err
	}
	entry.Score = 1 - distance
	return entry, nil
}

func buildScannedEntry(
	appName string,
	userID string,
	memoryID string,
	memoryContent string,
	topicsJSON string,
	memoryKind sql.NullString,
	eventTimeNs sql.NullInt64,
	participants sql.NullString,
	location sql.NullString,
	createdAtNs int64,
	updatedAtNs int64,
) (*memory.Entry, error) {
	topics, err := parseTopics(topicsJSON)
	if err != nil {
		return nil, err
	}
	participantList, err := parseStringSlice(participants.String)
	if err != nil {
		return nil, err
	}

	createdAt := time.Unix(0, createdAtNs).UTC()
	updatedAt := time.Unix(0, updatedAtNs).UTC()
	var eventTime *time.Time
	if eventTimeNs.Valid {
		t := time.Unix(0, eventTimeNs.Int64).UTC()
		eventTime = &t
	}

	entry := &memory.Entry{
		ID:      memoryID,
		AppName: appName,
		UserID:  userID,
		Memory: &memory.Memory{
			Memory:       memoryContent,
			Topics:       topics,
			Kind:         memory.Kind(memoryKind.String),
			EventTime:    eventTime,
			Participants: participantList,
			Location:     location.String,
			LastUpdated:  &updatedAt,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	imemory.NormalizeEntry(entry)
	return entry, nil
}

func parseTopics(in string) ([]string, error) {
	return parseStringSlice(in)
}

func parseStringSlice(in string) ([]string, error) {
	if in == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(in), &values); err != nil {
		return nil, fmt.Errorf("unmarshal string slice: %w", err)
	}
	return values, nil
}

func marshalStringSlice(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func metadataEventTimeNS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().UnixNano()
}

func metadataLocationValue(location string) any {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil
	}
	return location
}

func resolveSearchLimit(defaultMax, override int) int {
	if override > 0 {
		return override
	}
	return defaultMax
}

func resolveSearchCandidateLimit(
	defaultMax int,
	override int,
	memoryLimit int,
	opts memory.SearchOptions,
) int {
	limit := resolveSearchLimit(defaultMax, override)
	if opts.Kind != "" || opts.TimeAfter != nil || opts.TimeBefore != nil ||
		opts.OrderByEventTime || opts.KindFallback || opts.Deduplicate {
		if memoryLimit > limit {
			return memoryLimit
		}
	}
	return limit
}

func applySearchFilters(
	results []*memory.Entry,
	opts memory.SearchOptions,
) []*memory.Entry {
	filtered := make([]*memory.Entry, 0, len(results))
	for _, entry := range results {
		if entry == nil || entry.Memory == nil {
			continue
		}
		if opts.Kind != "" && entry.Memory.Kind != opts.Kind {
			continue
		}
		if opts.TimeAfter != nil &&
			entry.Memory.EventTime != nil &&
			entry.Memory.EventTime.Before(*opts.TimeAfter) {
			continue
		}
		if opts.TimeBefore != nil &&
			entry.Memory.EventTime != nil &&
			entry.Memory.EventTime.After(*opts.TimeBefore) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func (s *Service) executeKeywordSearch(
	ctx context.Context,
	userKey memory.UserKey,
	searchOpts memory.SearchOptions,
) ([]*memory.Entry, error) {
	entries, err := s.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return nil, err
	}
	candidateLimit, err := s.resolveSearchCandidateLimit(
		ctx,
		userKey,
		searchOpts,
	)
	if err != nil {
		return nil, err
	}
	keywordOpts := searchOpts
	keywordOpts.OrderByEventTime = false
	keywordOpts.KindFallback = false
	keywordOpts.Deduplicate = false
	keywordOpts.HybridSearch = false
	keywordOpts.SimilarityThreshold = 0
	keywordOpts.MaxResults = candidateLimit
	return imemory.SearchEntries(
		entries,
		keywordOpts,
		imemory.DefaultSearchMinScore,
		candidateLimit,
	), nil
}

func (s *Service) searchWithOptions(
	ctx context.Context,
	userKey memory.UserKey,
	searchOpts memory.SearchOptions,
	blob []byte,
) ([]*memory.Entry, error) {
	candidateLimit, err := s.resolveSearchCandidateLimit(
		ctx,
		userKey,
		searchOpts,
	)
	if err != nil {
		return nil, err
	}

	const searchSQL = `SELECT
memory_id, memory_content, topics, memory_kind, event_time,
participants, location, created_at, updated_at, distance
FROM %s
WHERE embedding MATCH ` + sqlVectorFromBlob + `
AND k = ?
AND app_name = ? AND user_id = ?`
	query := fmt.Sprintf(searchSQL, s.tableName)
	args := []any{
		blob,
		candidateLimit,
		userKey.AppName,
		userKey.UserID,
	}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	defer rows.Close()

	results := make([]*memory.Entry, 0)
	for rows.Next() {
		entry, err := scanSearchEntryWithSimilarity(
			rows,
			userKey.AppName,
			userKey.UserID,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memories: %w", err)
	}
	return results, nil
}

func (s *Service) resolveSearchCandidateLimit(
	ctx context.Context,
	userKey memory.UserKey,
	searchOpts memory.SearchOptions,
) (int, error) {
	limit := resolveSearchCandidateLimit(
		s.opts.maxResults,
		searchOpts.MaxResults,
		s.opts.memoryLimit,
		searchOpts,
	)
	if !(searchOpts.Kind != "" || searchOpts.TimeAfter != nil ||
		searchOpts.TimeBefore != nil || searchOpts.OrderByEventTime ||
		searchOpts.KindFallback || searchOpts.Deduplicate) {
		return limit, nil
	}

	count, err := s.countMemories(ctx, userKey)
	if err != nil {
		return 0, err
	}
	if count > limit {
		return count, nil
	}
	return limit, nil
}

func (s *Service) countMemories(
	ctx context.Context,
	userKey memory.UserKey,
) (int, error) {
	const countSQL = `SELECT COUNT(*) FROM %s WHERE app_name = ? AND user_id = ?`
	query := fmt.Sprintf(countSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}

	var count int
	row := s.db.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return count, nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), memory_search is exposed by default,
// memory_load is exposed once enabled, and other enabled tools remain hidden
// unless explicitly exposed.
// Without an extractor, enabled tools are exposed directly.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory job.
func (s *Service) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close closes the service and releases resources.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
