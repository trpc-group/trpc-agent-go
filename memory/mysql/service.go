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
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
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

	cachedTools map[string]tool.Tool
}

// NewService creates a new mysql memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{
		memoryLimit:  imemory.DefaultMemoryLimit,
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
		tableName:    "memories",
	}
	// Enable default tools.
	for name, creator := range imemory.DefaultEnabledTools {
		opts.toolCreators[name] = creator
		opts.enabledTools[name] = true
	}
	for _, option := range options {
		option(&opts)
	}

	builder := storage.GetClientBuilder()
	var (
		db  storage.Client
		err error
	)

	// If instance name set, and dsn not set, use instance name to create mysql client.
	if opts.dsn == "" && opts.instanceName != "" {
		builderOpts, ok := storage.GetMySQLInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("mysql instance %s not found", opts.instanceName)
		}
		db, err = builder(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create mysql client from instance name failed: %w", err)
		}
	} else {
		db, err = builder(
			storage.WithClientBuilderDSN(opts.dsn),
			storage.WithExtraOptions(opts.extraOptions...),
		)
		if err != nil {
			return nil, fmt.Errorf("create mysql client from dsn failed: %w", err)
		}
	}

	s := &Service{
		opts:        opts,
		db:          db,
		tableName:   opts.tableName,
		cachedTools: make(map[string]tool.Tool),
	}

	// Always initialize table.
	if err := s.initTable(context.Background()); err != nil {
		panic(fmt.Sprintf("failed to initialize table: %v", err))
	}

	return s, nil
}

// initTable creates the memories table if it doesn't exist.
func (s *Service) initTable(ctx context.Context) error {
	// Table name is validated in WithTableName.
	// #nosec G201
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			memory_id VARCHAR(64) NOT NULL,
			memory_data JSON NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (app_name, user_id, memory_id),
			INDEX idx_app_user (app_name, user_id),
			INDEX idx_deleted_at (deleted_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
	`, s.tableName)

	_, err := s.db.Exec(ctx, query)
	return err
}

// AddMemory adds a new memory for a user.
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Enforce memory limit.
	if s.opts.memoryLimit > 0 {
		// Table name is validated in WithTableName.
		// #nosec G201
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

	now := time.Now().UTC()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	entry := &memory.Entry{
		ID:        generateMemoryID(mem),
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

	// Table name is validated in WithTableName.
	// #nosec G201
	insertQuery := fmt.Sprintf(
		"INSERT INTO `%s` (app_name, user_id, memory_id, memory_data, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
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
	// Table name is validated in WithTableName.
	// #nosec G201
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

	now := time.Now().UTC()
	entry.Memory.Memory = memoryStr
	entry.Memory.Topics = topics
	entry.Memory.LastUpdated = &now
	entry.UpdatedAt = now

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}

	// Table name is validated in WithTableName.
	// #nosec G201
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

	// Table name is validated in WithTableName.
	// #nosec G201
	var (
		query string
		args  []any
	)
	if s.opts.softDelete {
		now := time.Now().UTC()
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

	// Table name is validated in WithTableName.
	// #nosec G201
	var err error
	if s.opts.softDelete {
		now := time.Now().UTC()
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

	// Table name is validated in WithTableName.
	// #nosec G201
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
	// Table name is validated in WithTableName.
	// #nosec G201
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
func (s *Service) Tools() []tool.Tool {
	// Concurrency-safe and stable order by name.
	// Protect tool creators/enabled flags and cache with a single lock at call-site
	// by converting to a local snapshot first (no struct-level mutex exists).
	// We assume opts are immutable after construction.
	names := make([]string, 0, len(s.opts.toolCreators))
	for name := range s.opts.toolCreators {
		if s.opts.enabledTools[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if _, ok := s.cachedTools[name]; !ok {
			s.cachedTools[name] = s.opts.toolCreators[name]()
		}
		tools = append(tools, s.cachedTools[name])
	}
	return tools
}

// Close closes the database connection.
func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// generateMemoryID generates a memory ID from memory content.
// Uses SHA256 to match the in-memory implementation for consistency.
func generateMemoryID(mem *memory.Memory) string {
	content := fmt.Sprintf("memory:%s", mem.Memory)
	if len(mem.Topics) > 0 {
		content += fmt.Sprintf("|topics:%s", strings.Join(mem.Topics, ","))
	}
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", hash)
}
