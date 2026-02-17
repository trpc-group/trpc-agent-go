//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysql provides the mysql memory service.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the mysql memory service.
// Storage structure:
//
//	Table: memories
//	Columns: app_name, user_id, memory_id, memory_data (JSON), created_at, updated_at.
//	Primary Key: (app_name, user_id, memory_id).
//	Index: (app_name, user_id).
type Service struct {
	opts      ServiceOpts
	db        storage.Client
	tableName string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new mysql memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	// Apply auto mode defaults after all options are applied.
	// User settings via WithToolEnabled take precedence regardless of option order.
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

	// Initialize database if needed
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
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Enforce memory limit.
	if s.opts.memoryLimit > 0 {
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE app_name = ? AND user_id = ?", s.tableName)
		if s.opts.softDelete {
			countQuery += " AND deleted_at IS NULL"
		}
		var count int
		if err := s.db.QueryRow(ctx, []any{&count}, countQuery, userKey.AppName, userKey.UserID); err != nil {
			return fmt.Errorf("mysql memory service check memory count failed: %w", err)
		}
		if count >= s.opts.memoryLimit {
			return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
				userKey.UserID, s.opts.memoryLimit, count)
		}
	}

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	entry := &memory.Entry{
		ID:        imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID),
		AppName:   userKey.AppName,
		Memory:    mem,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	memoryData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal memory entry failed: %w", err)
	}

	// Note: memory_data contains the full JSON with topics, so updating memory_data
	// will also update the topics field.
	insertQuery := fmt.Sprintf(
		"INSERT INTO `%s` (app_name, user_id, memory_id, memory_data, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE memory_data = VALUES(memory_data), updated_at = VALUES(updated_at)",
		s.tableName,
	)
	_, err = s.db.Exec(ctx, insertQuery, userKey.AppName, userKey.UserID, entry.ID, memoryData, now, now)
	if err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}

	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	// Get existing entry.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE app_name = ? AND user_id = ? AND memory_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}
	var memoryData []byte
	err := s.db.QueryRow(ctx, []any{&memoryData}, selectQuery, memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		return fmt.Errorf("get memory entry failed: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal(memoryData, entry); err != nil {
		return fmt.Errorf("unmarshal memory entry failed: %w", err)
	}

	now := time.Now()
	entry.Memory.Memory = memoryStr
	entry.Memory.Topics = topics
	entry.Memory.LastUpdated = &now
	entry.UpdatedAt = now

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}

	updateQuery := fmt.Sprintf(
		"UPDATE %s SET memory_data = ?, updated_at = ? WHERE app_name = ? AND user_id = ? AND memory_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		updateQuery += " AND deleted_at IS NULL"
	}
	_, err = s.db.Exec(ctx, updateQuery, updated, now, memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID)
	if err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}

	return nil
}

// DeleteMemory deletes a memory for a user (soft delete).
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
			"UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND memory_id = ? AND deleted_at IS NULL",
			s.tableName,
		)
		args = []any{now, memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID}
	} else {
		query = fmt.Sprintf(
			"DELETE FROM %s WHERE app_name = ? AND user_id = ? AND memory_id = ?",
			s.tableName,
		)
		args = []any{memoryKey.AppName, memoryKey.UserID, memoryKey.MemoryID}
	}
	_, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}

	return nil
}

// ClearMemories clears all memories for a user (soft delete).
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	var err error
	if s.opts.softDelete {
		now := time.Now()
		query := fmt.Sprintf(
			"UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL",
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
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	query += " ORDER BY updated_at DESC, created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	entries := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		var memoryData []byte
		if err := rows.Scan(&memoryData); err != nil {
			return fmt.Errorf("scan memory data failed: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal(memoryData, e); err != nil {
			return fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		entries = append(entries, e)
		return nil
	}, query, userKey.AppName, userKey.UserID)

	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}

	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	// Get all memories for the user.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		var memoryData []byte
		if err := rows.Scan(&memoryData); err != nil {
			return fmt.Errorf("scan memory data failed: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal(memoryData, e); err != nil {
			return fmt.Errorf("unmarshal memory entry failed: %w", err)
		}

		if imemory.MatchMemoryEntry(e, query) {
			results = append(results, e)
		}
		return nil
	}, selectQuery, userKey.AppName, userKey.UserID)

	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}

	// Stable sort by updated time desc.
	sort.Slice(results, func(i, j int) bool {
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	return results, nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), only front-end tools are returned.
// By default, only Search is enabled; Load can be enabled explicitly.
// In agentic mode, all enabled tools are returned.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async
// processing. The session contains the full transcript and state for
// incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
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
