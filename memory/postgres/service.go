//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package postgres provides the postgres memory service.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the postgres memory service.
// Storage structure:
//
//	Table: memories (configurable)
//	Columns: app_name, user_id, memory_id, memory_data (JSONB), created_at, updated_at.
//	Primary Key: memory_id.
//	Index: (app_name, user_id).
type Service struct {
	opts      ServiceOpts
	db        storage.Client
	tableName string

	cachedTools map[string]tool.Tool
}

// NewService creates a new postgres memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// Priority: DSN > direct connection settings > instance name
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
	fullTableName := sqldb.BuildTableNameWithSchema(opts.schema, "", opts.tableName)

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
	connString := fmt.Sprintf("host=%s port=%d dbname=%s sslmode=%s",
		host, port, database, sslMode)

	if opts.user != "" {
		connString += fmt.Sprintf(" user=%s", opts.user)
	}
	if opts.password != "" {
		connString += fmt.Sprintf(" password=%s", opts.password)
	}

	return connString
}

// AddMemory adds a new memory for a user.
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Enforce memory limit if set.
	if s.opts.memoryLimit > 0 {
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE app_name = $1 AND user_id = $2", s.tableName)
		if s.opts.softDelete {
			countQuery += " AND deleted_at IS NULL"
		}
		var count int
		err := s.db.Query(ctx, func(rows *sql.Rows) error {
			if rows.Next() {
				return rows.Scan(&count)
			}
			return nil
		}, countQuery, userKey.AppName, userKey.UserID)
		if err != nil {
			return fmt.Errorf("postgres memory service check memory count failed: %w", err)
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

	insertQuery := fmt.Sprintf(
		"INSERT INTO %s (memory_id, app_name, user_id, memory_data, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)",
		s.tableName,
	)
	_, err = s.db.ExecContext(ctx, insertQuery, entry.ID, userKey.AppName, userKey.UserID, memoryData, now, now)
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

	// Get existing entry if exists.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE memory_id = $1 AND app_name = $2 AND user_id = $3",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}
	var memoryData []byte
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&memoryData)
		}
		return sql.ErrNoRows
	}, selectQuery, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
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
		"UPDATE %s SET memory_data = $1, updated_at = $2 WHERE memory_id = $3 AND app_name = $4 AND user_id = $5",
		s.tableName,
	)
	if s.opts.softDelete {
		updateQuery += " AND deleted_at IS NULL"
	}
	_, err = s.db.ExecContext(ctx, updateQuery, updated, now, memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
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

	// Delete memory entry.
	var (
		query string
		args  []any
	)
	if s.opts.softDelete {
		now := time.Now()
		query = fmt.Sprintf(
			"UPDATE %s SET deleted_at = $1 WHERE memory_id = $2 AND app_name = $3 AND user_id = $4 AND deleted_at IS NULL",
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

// ClearMemories clears all memories for a user (soft delete).
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Clear memories.
	var err error
	if s.opts.softDelete {
		now := time.Now()
		query := fmt.Sprintf(
			"UPDATE %s SET deleted_at = $1 WHERE app_name = $2 AND user_id = $3 AND deleted_at IS NULL",
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
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE app_name = $1 AND user_id = $2",
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
		for rows.Next() {
			var memoryData []byte
			if err := rows.Scan(&memoryData); err != nil {
				return fmt.Errorf("scan memory data failed: %w", err)
			}

			e := &memory.Entry{}
			if err := json.Unmarshal(memoryData, e); err != nil {
				return fmt.Errorf("unmarshal memory entry failed: %w", err)
			}
			entries = append(entries, e)
		}
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

	// Get all memories for the user if exists.
	selectQuery := fmt.Sprintf(
		"SELECT memory_data FROM %s WHERE app_name = $1 AND user_id = $2",
		s.tableName,
	)
	if s.opts.softDelete {
		selectQuery += " AND deleted_at IS NULL"
	}

	results := make([]*memory.Entry, 0)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
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
	// Protect tool creators/enabled flags and cache with a single lock at
	// call-site by converting to a local snapshot first (no struct-level
	// mutex exists). We assume opts are immutable after construction.
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
