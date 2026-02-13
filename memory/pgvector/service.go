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
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryStr string,
	topics []string,
) error {
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
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	// Convert embedding to pgvector format.
	vector := pgvector.NewVector(convertToFloat32(embedding))

	var insertQuery string
	args := []any{
		memoryID,
		userKey.AppName,
		userKey.UserID,
		memoryStr,
		pq.Array(topics),
		vector,
		now,
		now,
	}
	if s.opts.memoryLimit > 0 {
		deletedFilter := ""
		if s.opts.softDelete {
			deletedFilter = " AND deleted_at IS NULL"
		}
		insertQuery = fmt.Sprintf(
			"WITH existing AS ("+
				"SELECT 1 FROM %s "+
				"WHERE memory_id = $1 AND app_name = $2 AND user_id = $3%s"+
				"), cnt AS ("+
				"SELECT COUNT(*) AS c FROM %s "+
				"WHERE app_name = $2 AND user_id = $3%s"+
				") "+
				"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
				"embedding, created_at, updated_at) "+
				"SELECT $1, $2, $3, $4, $5, $6, $7, $8 "+
				"WHERE (EXISTS (SELECT 1 FROM existing) OR "+
				"(SELECT c FROM cnt) < $9) "+
				"ON CONFLICT (memory_id) DO UPDATE SET "+
				"memory_content = EXCLUDED.memory_content, "+
				"topics = EXCLUDED.topics, "+
				"embedding = EXCLUDED.embedding, "+
				"deleted_at = NULL, "+
				"updated_at = EXCLUDED.updated_at",
			s.tableName,
			deletedFilter,
			s.tableName,
			deletedFilter,
			s.tableName,
		)
		args = append(args, s.opts.memoryLimit)
	} else {
		insertQuery = fmt.Sprintf(
			"INSERT INTO %s (memory_id, app_name, user_id, memory_content, topics, "+
				"embedding, created_at, updated_at) "+
				"VALUES ($1, $2, $3, $4, $5, $6, $7, $8) "+
				"ON CONFLICT (memory_id) DO UPDATE SET "+
				"memory_content = EXCLUDED.memory_content, "+
				"topics = EXCLUDED.topics, "+
				"embedding = EXCLUDED.embedding, "+
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
			return fmt.Errorf("memory limit exceeded for user %s, limit: %d",
				userKey.UserID, s.opts.memoryLimit)
		}
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

	var updateQuery strings.Builder
	fmt.Fprintf(&updateQuery,
		"UPDATE %s SET memory_content = $1, topics = $2, embedding = $3, "+
			"updated_at = $4 WHERE memory_id = $5 AND app_name = $6 AND user_id = $7",
		s.tableName,
	)
	if s.opts.softDelete {
		updateQuery.WriteString(" AND deleted_at IS NULL")
	}
	res, err := s.db.ExecContext(ctx, updateQuery.String(),
		memoryStr, pq.Array(topics), vector, now,
		memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)
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
		"SELECT memory_id, app_name, user_id, memory_content, topics, created_at, "+
			"updated_at FROM %s WHERE app_name = $1 AND user_id = $2",
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

// SearchMemories searches memories for a user using vector similarity.
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return []*memory.Entry{}, nil
	}

	// Generate embedding for the query.
	queryEmbedding, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding failed: %w", err)
	}
	if len(queryEmbedding) != s.opts.indexDimension {
		return nil, fmt.Errorf("query embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(queryEmbedding))
	}

	vector := pgvector.NewVector(convertToFloat32(queryEmbedding))

	// Use cosine distance for similarity search.
	// Order by distance ascending (smaller distance = more similar).
	var searchQuery strings.Builder
	fmt.Fprintf(&searchQuery,
		"SELECT memory_id, app_name, user_id, memory_content, topics, created_at, "+
			"updated_at, 1 - (embedding <=> $1) AS similarity "+
			"FROM %s WHERE app_name = $2 AND user_id = $3",
		s.tableName,
	)
	if s.opts.softDelete {
		searchQuery.WriteString(" AND deleted_at IS NULL")
	}
	searchQuery.WriteString(" ORDER BY embedding <=> $1")
	fmt.Fprintf(&searchQuery, " LIMIT %d", s.opts.maxResults)

	results := make([]*memory.Entry, 0)
	err = s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			entry, err := scanMemoryEntryWithSimilarity(rows)
			if err != nil {
				return err
			}
			results = append(results, entry)
		}
		return nil
	}, searchQuery.String(), vector, userKey.AppName, userKey.UserID)

	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}

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
		createdAt     time.Time
		updatedAt     time.Time
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent, &topics,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry failed: %w", err)
	}

	return &memory.Entry{
		ID:      memoryID,
		AppName: appName,
		UserID:  userID,
		Memory: &memory.Memory{
			Memory:      memoryContent,
			Topics:      []string(topics),
			LastUpdated: &updatedAt,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
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
		createdAt     time.Time
		updatedAt     time.Time
		similarity    float64
	)

	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent, &topics,
		&createdAt, &updatedAt, &similarity,
	); err != nil {
		return nil, fmt.Errorf("scan memory entry with similarity failed: %w", err)
	}

	// Note: similarity score is available but not stored in Entry.
	// It could be added to metadata if needed in the future.
	_ = similarity

	return &memory.Entry{
		ID:      memoryID,
		AppName: appName,
		UserID:  userID,
		Memory: &memory.Memory{
			Memory:      memoryContent,
			Topics:      []string(topics),
			LastUpdated: &updatedAt,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

// convertToFloat32 converts a float64 slice to float32 slice.
func convertToFloat32(embedding []float64) []float32 {
	result := make([]float32, len(embedding))
	for i, v := range embedding {
		result[i] = float32(v)
	}
	return result
}
