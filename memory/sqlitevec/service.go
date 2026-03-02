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

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
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

	vecInitOnce.Do(func() { vec.Auto() })

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
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
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

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		return fmt.Errorf("marshal topics: %w", err)
	}

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
memory_content = ?, topics = ?
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
memory_content, topics
) VALUES (?, ` + sqlVectorFromBlob + `, ?, ?, ?, ?, ?, ?, ?)`
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
	return vec.SerializeFloat32(out)
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
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
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

	const updateSQL = `UPDATE %s SET
embedding = ` + sqlVectorFromBlob + `,
updated_at = ?, memory_content = ?, topics = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?`

	query := fmt.Sprintf(updateSQL, s.tableName)
	args := []any{
		blob,
		updatedAtNs,
		memoryStr,
		string(topicsJSON),
		memoryKey.AppName,
		memoryKey.UserID,
		memoryKey.MemoryID,
	}
	if s.opts.softDelete {
		query += fmt.Sprintf(" AND deleted_at = %d", notDeletedAtNs)
	}

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
memory_id, memory_content, topics, created_at, updated_at
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

	const searchSQL = `SELECT
memory_id, memory_content, topics, created_at, updated_at
FROM %s
WHERE embedding MATCH ` + sqlVectorFromBlob + `
AND k = ?
AND app_name = ? AND user_id = ?`
	query := fmt.Sprintf(searchSQL, s.tableName)
	args := []any{
		blob,
		s.opts.maxResults,
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
		entry, err := scanEntry(rows, userKey.AppName, userKey.UserID)
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

func scanEntry(
	rows *sql.Rows,
	appName string,
	userID string,
) (*memory.Entry, error) {
	var (
		memoryID      string
		memoryContent string
		topicsJSON    string
		createdAtNs   int64
		updatedAtNs   int64
	)
	if err := rows.Scan(
		&memoryID,
		&memoryContent,
		&topicsJSON,
		&createdAtNs,
		&updatedAtNs,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry: %w", err)
	}

	topics, err := parseTopics(topicsJSON)
	if err != nil {
		return nil, err
	}

	createdAt := time.Unix(0, createdAtNs).UTC()
	updatedAt := time.Unix(0, updatedAtNs).UTC()

	return &memory.Entry{
		ID:      memoryID,
		AppName: appName,
		UserID:  userID,
		Memory: &memory.Memory{
			Memory:      memoryContent,
			Topics:      topics,
			LastUpdated: &updatedAt,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func parseTopics(in string) ([]string, error) {
	if in == "" {
		return nil, nil
	}
	var topics []string
	if err := json.Unmarshal([]byte(in), &topics); err != nil {
		return nil, fmt.Errorf("unmarshal topics: %w", err)
	}
	return topics, nil
}

// Tools returns the list of available memory tools.
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
