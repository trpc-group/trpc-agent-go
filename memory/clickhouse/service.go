//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clickhouse provides a ClickHouse-based memory service.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the ClickHouse memory service.
// Storage structure.
// Table: memories (configurable).
// Columns: memory_id, app_name, user_id, memory_data, created_at, updated_at,
// deleted_at.
// Engine: ReplacingMergeTree(updated_at).
// Order by: (app_name, user_id, memory_id).
type Service struct {
	opts      ServiceOpts
	chClient  storage.Client
	tableName string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new ClickHouse memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
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
	// Priority: DSN > instance name.
	if opts.dsn != "" {
		builderOpts = append(builderOpts, storage.WithClientBuilderDSN(opts.dsn))
	} else if opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetClickHouseInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("clickhouse instance %s not found", opts.instanceName)
		}
	} else {
		return nil, fmt.Errorf("either DSN or instance name must be provided")
	}

	chClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create clickhouse client failed: %w", err)
	}

	// Build full table name with prefix.
	fullTableName := buildFullTableName(opts.tablePrefix, opts.tableName)

	s := &Service{
		opts:        opts,
		chClient:    chClient,
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
		s.cachedTools,
	)

	// Initialize auto memory worker if extractor is configured.
	if opts.extractor != nil {
		config := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

// buildFullTableName builds the full table name with optional prefix.
func buildFullTableName(prefix, tableName string) string {
	if prefix == "" {
		return tableName
	}
	if !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}
	return prefix + tableName
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

	// Enforce memory limit if set.
	if s.opts.memoryLimit > 0 {
		var count uint64
		countQuery := fmt.Sprintf(
			"SELECT COUNT(*) FROM %s FINAL WHERE app_name = ? AND user_id = ?",
			s.tableName,
		)
		if s.opts.softDelete {
			countQuery += " AND deleted_at IS NULL"
		}
		err := s.chClient.QueryRow(ctx, []any{&count}, countQuery,
			userKey.AppName, userKey.UserID)
		if err != nil {
			return fmt.Errorf("check memory count failed: %w", err)
		}
		if int(count) >= s.opts.memoryLimit {
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

	// Insert into ClickHouse (ReplacingMergeTree will handle deduplication).
	insertQuery := fmt.Sprintf(
		"INSERT INTO %s (memory_id, app_name, user_id, memory_data, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		s.tableName,
	)
	err = s.chClient.Exec(ctx, insertQuery,
		entry.ID, userKey.AppName, userKey.UserID, string(memoryData), now, now)
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
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	// Get existing entry using FINAL for deduplication.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data, created_at FROM %s FINAL "+
			"WHERE memory_id = ? AND app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}

	rows, err := s.chClient.Query(ctx, selectQuery,
		memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
	if err != nil {
		return fmt.Errorf("get memory entry failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	var memoryData string
	var createdAt time.Time
	if err := rows.Scan(&memoryData, &createdAt); err != nil {
		return fmt.Errorf("scan memory entry failed: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal([]byte(memoryData), entry); err != nil {
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

	// Insert new version (ReplacingMergeTree will handle replacement).
	insertQuery := fmt.Sprintf(
		"INSERT INTO %s (memory_id, app_name, user_id, memory_data, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		s.tableName,
	)
	err = s.chClient.Exec(ctx, insertQuery,
		memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID,
		string(updated), createdAt, now)
	if err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	if s.opts.softDelete {
		// Soft delete: get current data and insert with deleted_at set.
		selectQuery := fmt.Sprintf(
			"SELECT memory_data, created_at FROM %s FINAL "+
				"WHERE memory_id = ? AND app_name = ? AND user_id = ? "+
				"AND deleted_at IS NULL",
			s.tableName,
		)
		rows, err := s.chClient.Query(ctx, selectQuery,
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
		if err != nil {
			return fmt.Errorf("get memory entry for delete failed: %w", err)
		}
		defer rows.Close()

		if !rows.Next() {
			// Not found or already deleted.
			return nil
		}

		var memoryData string
		var createdAt time.Time
		if err := rows.Scan(&memoryData, &createdAt); err != nil {
			return fmt.Errorf("scan memory entry failed: %w", err)
		}

		now := time.Now()
		insertQuery := fmt.Sprintf(
			"INSERT INTO %s (memory_id, app_name, user_id, memory_data, "+
				"created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			s.tableName,
		)
		err = s.chClient.Exec(ctx, insertQuery,
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID,
			memoryData, createdAt, now, now)
		if err != nil {
			return fmt.Errorf("soft delete memory entry failed: %w", err)
		}
	} else {
		// Hard delete using ALTER TABLE DELETE.
		deleteQuery := fmt.Sprintf(
			"ALTER TABLE %s DELETE WHERE memory_id = ? AND app_name = ? AND user_id = ?",
			s.tableName,
		)
		err := s.chClient.Exec(ctx, deleteQuery,
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
		if err != nil {
			return fmt.Errorf("delete memory entry failed: %w", err)
		}
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	if s.opts.softDelete {
		// Soft delete: get all current entries and reinsert with deleted_at.
		selectQuery := fmt.Sprintf(
			"SELECT memory_id, memory_data, created_at FROM %s FINAL "+
				"WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL",
			s.tableName,
		)
		rows, err := s.chClient.Query(ctx, selectQuery,
			userKey.AppName, userKey.UserID)
		if err != nil {
			return fmt.Errorf("get memories for clear failed: %w", err)
		}
		defer rows.Close()

		type memoryRecord struct {
			memoryID, memoryData string
			createdAt            time.Time
		}
		var records []memoryRecord

		for rows.Next() {
			var rec memoryRecord
			if err := rows.Scan(&rec.memoryID, &rec.memoryData, &rec.createdAt); err != nil {
				return fmt.Errorf("scan memory entry failed: %w", err)
			}
			records = append(records, rec)
		}

		if len(records) > 0 {
			now := time.Now()
			err = s.chClient.BatchInsert(ctx,
				fmt.Sprintf("INSERT INTO %s (memory_id, app_name, user_id, "+
					"memory_data, created_at, updated_at, deleted_at)",
					s.tableName),
				func(batch driver.Batch) error {
					for _, rec := range records {
						if err := batch.Append(rec.memoryID, userKey.AppName,
							userKey.UserID, rec.memoryData, rec.createdAt, now, now); err != nil {
							return err
						}
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf("batch soft delete memories failed: %w", err)
			}
		}
	} else {
		// Hard delete using ALTER TABLE DELETE.
		deleteQuery := fmt.Sprintf(
			"ALTER TABLE %s DELETE WHERE app_name = ? AND user_id = ?",
			s.tableName,
		)
		err := s.chClient.Exec(ctx, deleteQuery, userKey.AppName, userKey.UserID)
		if err != nil {
			return fmt.Errorf("clear memories failed: %w", err)
		}
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

	query := fmt.Sprintf(
		"SELECT memory_data FROM %s FINAL WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	query += " ORDER BY updated_at DESC, created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.chClient.Query(ctx, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}
	defer rows.Close()

	entries := make([]*memory.Entry, 0)
	for rows.Next() {
		var memoryData string
		if err := rows.Scan(&memoryData); err != nil {
			return nil, fmt.Errorf("scan memory data failed: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(memoryData), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	// Get all memories for the user.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data FROM %s FINAL WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}

	rows, err := s.chClient.Query(ctx, selectQuery,
		userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}
	defer rows.Close()

	results := make([]*memory.Entry, 0)
	for rows.Next() {
		var memoryData string
		if err := rows.Scan(&memoryData); err != nil {
			return nil, fmt.Errorf("scan memory data failed: %w", err)
		}

		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(memoryData), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}

		if imemory.MatchMemoryEntry(e, query) {
			results = append(results, e)
		}
	}
	return results, nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), only front-end tools are returned.
// By default, only Search is enabled; Load can be enabled explicitly.
// In agentic mode, all enabled tools are returned.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return s.precomputedTools
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async
// processing. The session contains the full transcript and state for
// incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close closes the ClickHouse connection and stops async workers.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.chClient != nil {
		return s.chClient.Close()
	}
	return nil
}
