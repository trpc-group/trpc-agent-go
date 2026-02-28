//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite provides a SQLite-backed memory service implementation.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the sqlite memory service.
type Service struct {
	opts      ServiceOpts
	db        *sql.DB
	tableName string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new sqlite memory service.
//
// The service owns the passed-in db and will close it in Close().
func NewService(db *sql.DB, options ...ServiceOpt) (*Service, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}

	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
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

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	if s.opts.memoryLimit > 0 {
		deletedAt, exists, err := s.getDeletedAt(
			ctx,
			userKey,
			memoryID,
		)
		if err != nil {
			return err
		}

		needLimit := !exists
		if exists && s.opts.softDelete && deletedAt.Valid {
			needLimit = true
		}
		if needLimit {
			if err := s.enforceMemoryLimit(ctx, userKey); err != nil {
				return err
			}
		}
	}

	entry := &memory.Entry{
		ID:        memoryID,
		AppName:   userKey.AppName,
		Memory:    mem,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	memoryData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal memory entry: %w", err)
	}

	const insertSQL = `
INSERT INTO %s (
  memory_id, app_name, user_id, memory_data, created_at, updated_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT(memory_id) DO UPDATE SET
  memory_data = excluded.memory_data,
  updated_at = excluded.updated_at,
  deleted_at = NULL;`

	query := fmt.Sprintf(insertSQL, s.tableName)
	_, err = s.db.ExecContext(
		ctx,
		query,
		entry.ID,
		userKey.AppName,
		userKey.UserID,
		memoryData,
		now.UTC().UnixNano(),
		now.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("store memory entry: %w", err)
	}

	return nil
}

func (s *Service) getDeletedAt(
	ctx context.Context,
	userKey memory.UserKey,
	memoryID string,
) (sql.NullInt64, bool, error) {
	const selectSQL = `SELECT deleted_at FROM %s
WHERE app_name = ? AND user_id = ? AND memory_id = ?
LIMIT 1`
	query := fmt.Sprintf(selectSQL, s.tableName)
	row := s.db.QueryRowContext(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		memoryID,
	)

	var deletedAt sql.NullInt64
	if err := row.Scan(&deletedAt); errors.Is(err, sql.ErrNoRows) {
		return sql.NullInt64{}, false, nil
	} else if err != nil {
		return sql.NullInt64{}, false, fmt.Errorf(
			"select existing memory: %w",
			err,
		)
	}
	return deletedAt, true, nil
}

func (s *Service) enforceMemoryLimit(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	if s.opts.memoryLimit <= 0 {
		return nil
	}

	const countSQL = `SELECT COUNT(*) FROM %s
WHERE app_name = ? AND user_id = ?`
	query := fmt.Sprintf(countSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}

	var count int
	row := s.db.QueryRowContext(ctx, query, args...)
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

	entry, err := s.getEntry(ctx, memoryKey)
	if err != nil {
		return err
	}

	now := time.Now()
	entry.Memory.Memory = memoryStr
	entry.Memory.Topics = topics
	entry.Memory.LastUpdated = &now
	entry.UpdatedAt = now

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry: %w", err)
	}

	const updateSQL = `UPDATE %s
SET memory_data = ?, updated_at = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?`
	query := fmt.Sprintf(updateSQL, s.tableName)
	args := []any{
		updated,
		now.UTC().UnixNano(),
		memoryKey.AppName,
		memoryKey.UserID,
		memoryKey.MemoryID,
	}
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update memory entry: %w", err)
	}

	return nil
}

func (s *Service) getEntry(
	ctx context.Context,
	memoryKey memory.Key,
) (*memory.Entry, error) {
	const selectSQL = `SELECT memory_data FROM %s
WHERE app_name = ? AND user_id = ? AND memory_id = ?`
	query := fmt.Sprintf(selectSQL, s.tableName)
	args := []any{
		memoryKey.AppName,
		memoryKey.UserID,
		memoryKey.MemoryID,
	}
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}

	var memoryData []byte
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&memoryData)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf(
			"memory with id %s not found",
			memoryKey.MemoryID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get memory entry: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal(memoryData, entry); err != nil {
		return nil, fmt.Errorf("unmarshal memory entry: %w", err)
	}
	return entry, nil
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
		const softDeleteSQL = `UPDATE %s
SET deleted_at = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?
AND deleted_at IS NULL`
		query = fmt.Sprintf(softDeleteSQL, s.tableName)
		args = []any{
			time.Now().UTC().UnixNano(),
			memoryKey.AppName,
			memoryKey.UserID,
			memoryKey.MemoryID,
		}
	} else {
		const hardDeleteSQL = `DELETE FROM %s
WHERE app_name = ? AND user_id = ? AND memory_id = ?`
		query = fmt.Sprintf(hardDeleteSQL, s.tableName)
		args = []any{
			memoryKey.AppName,
			memoryKey.UserID,
			memoryKey.MemoryID,
		}
	}

	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete memory entry: %w", err)
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
		const softDeleteSQL = `UPDATE %s
SET deleted_at = ?
WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL`
		query = fmt.Sprintf(softDeleteSQL, s.tableName)
		args = []any{
			time.Now().UTC().UnixNano(),
			userKey.AppName,
			userKey.UserID,
		}
	} else {
		const hardDeleteSQL = `DELETE FROM %s
WHERE app_name = ? AND user_id = ?`
		query = fmt.Sprintf(hardDeleteSQL, s.tableName)
		args = []any{userKey.AppName, userKey.UserID}
	}

	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
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

	const selectSQL = `SELECT memory_data FROM %s
WHERE app_name = ? AND user_id = ?`

	query := fmt.Sprintf(selectSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	query += " ORDER BY updated_at DESC, created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	entries := make([]*memory.Entry, 0)
	for rows.Next() {
		var memoryData []byte
		if err := rows.Scan(&memoryData); err != nil {
			return nil, fmt.Errorf("scan memory data: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal(memoryData, e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry: %w", err)
		}
		entries = append(entries, e)
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

	const selectSQL = `SELECT memory_data FROM %s
WHERE app_name = ? AND user_id = ?`
	query := fmt.Sprintf(selectSQL, s.tableName)
	args := []any{userKey.AppName, userKey.UserID}
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	defer rows.Close()

	results := make([]*memory.Entry, 0)
	for rows.Next() {
		var memoryData []byte
		if err := rows.Scan(&memoryData); err != nil {
			return nil, fmt.Errorf("scan memory data: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal(memoryData, e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry: %w", err)
		}

		if imemory.MatchMemoryEntry(e, queryStr) {
			results = append(results, e)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memories: %w", err)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	return results, nil
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
