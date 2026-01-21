//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"database/sql"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (PostgreSQL with pgvector syntax).
const (
	sqlCreateExtension    = "CREATE EXTENSION IF NOT EXISTS vector"
	sqlCheckDDLPrivilege  = "SELECT has_schema_privilege($1, 'CREATE')"
	sqlCreateTablePattern = "CREATE TABLE IF NOT EXISTS %s (" +
		"memory_id TEXT PRIMARY KEY," +
		"app_name TEXT NOT NULL," +
		"user_id TEXT NOT NULL," +
		"memory_content TEXT NOT NULL," +
		"topics TEXT[]," +
		"embedding vector(%d)," +
		"created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"deleted_at TIMESTAMP NULL DEFAULT NULL" +
		")"

	sqlCreateAppUserIndexPattern   = "CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id)"
	sqlCreateUpdatedAtIndexPattern = "CREATE INDEX IF NOT EXISTS %s ON %s(updated_at DESC)"
	sqlCreateDeletedAtIndexPattern = "CREATE INDEX IF NOT EXISTS %s ON %s(deleted_at)"

	sqlCreateHNSWIndexPattern = "CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw " +
		"(embedding vector_cosine_ops) WITH (m = %d, ef_construction = %d)"
)

// buildFullTableName builds the full table name with optional schema prefix.
func buildFullTableName(schema, tableName string) string {
	return sqldb.BuildTableNameWithSchema(schema, "", tableName)
}

// buildIndexName builds the index name from table name and suffix.
func buildIndexName(tableName, suffix string) string {
	return sqldb.BuildIndexName("", tableName, suffix)
}

// buildCreateTableSQL builds the CREATE TABLE SQL.
func buildCreateTableSQL(schema, tableName string, dimension int) string {
	fullTableName := buildFullTableName(schema, tableName)
	return fmt.Sprintf(sqlCreateTablePattern, fullTableName, dimension)
}

// buildCreateIndexSQL builds the CREATE INDEX SQL.
func buildCreateIndexSQL(schema, tableName, indexSuffix, template string) string {
	fullTableName := buildFullTableName(schema, tableName)
	indexName := buildIndexName(tableName, indexSuffix)
	return fmt.Sprintf(template, indexName, fullTableName)
}

// buildCreateHNSWIndexSQL builds the CREATE HNSW INDEX SQL.
func buildCreateHNSWIndexSQL(schema, tableName string, params *HNSWIndexParams) string {
	fullTableName := buildFullTableName(schema, tableName)
	indexName := buildIndexName(tableName, "embedding_hnsw")

	m := defaultHNSWM
	efConstruction := defaultHNSWEfConstruction
	if params != nil {
		if params.M > 0 {
			m = params.M
		}
		if params.EfConstruction > 0 {
			efConstruction = params.EfConstruction
		}
	}

	return fmt.Sprintf(sqlCreateHNSWIndexPattern, indexName, fullTableName, m,
		efConstruction)
}

// checkDDLPrivilege checks if the current user has DDL (CREATE) privilege.
func (s *Service) checkDDLPrivilege(ctx context.Context) (bool, error) {
	schemaName := s.opts.schema
	if schemaName == "" {
		schemaName = "public"
	}

	var hasPrivilege bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&hasPrivilege)
		}
		// If no rows returned, hasPrivilege remains false (default).
		return nil
	}, sqlCheckDDLPrivilege, schemaName)

	if err != nil {
		return false, fmt.Errorf("check DDL privilege on schema %s failed: %w",
			schemaName, err)
	}
	return hasPrivilege, nil
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(ctx, "initializing pgvector memory database schema...")

	// Enable pgvector extension.
	if _, err := s.db.ExecContext(ctx, sqlCreateExtension); err != nil {
		return fmt.Errorf("enable pgvector extension failed: %w", err)
	}

	// Check DDL privilege before proceeding.
	hasDDLPrivilege, err := s.checkDDLPrivilege(ctx)
	if err != nil {
		return err
	}
	if !hasDDLPrivilege {
		log.WarnContext(ctx, "skipping DDL operations: no CREATE privilege on schema")
		return nil
	}

	// Use base table name from opts (before schema prefix is applied).
	baseTableName := s.opts.tableName
	fullTableName := buildFullTableName(s.opts.schema, baseTableName)

	// Create table.
	tableSQL := buildCreateTableSQL(s.opts.schema, baseTableName, s.opts.indexDimension)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", fullTableName, err)
	}
	log.InfofContext(ctx, "created table: %s", fullTableName)

	// Index suffix constants.
	const (
		indexSuffixAppUser   = "app_user"
		indexSuffixUpdatedAt = "updated_at"
		indexSuffixDeletedAt = "deleted_at"
	)

	// Create regular indexes.
	indexes := []struct {
		suffix   string
		template string
	}{
		{indexSuffixAppUser, sqlCreateAppUserIndexPattern},
		{indexSuffixUpdatedAt, sqlCreateUpdatedAtIndexPattern},
		{indexSuffixDeletedAt, sqlCreateDeletedAtIndexPattern},
	}

	for _, idx := range indexes {
		indexSQL := buildCreateIndexSQL(s.opts.schema, baseTableName, idx.suffix,
			idx.template)
		if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index %s on table %s failed: %w",
				idx.suffix, fullTableName, err)
		}
		log.InfofContext(ctx, "created index: %s on table %s", idx.suffix,
			fullTableName)
	}

	// Create HNSW vector index.
	hnswSQL := buildCreateHNSWIndexSQL(s.opts.schema, baseTableName, s.opts.hnswParams)
	if _, err := s.db.ExecContext(ctx, hnswSQL); err != nil {
		return fmt.Errorf("create HNSW index on table %s failed: %w", fullTableName,
			err)
	}
	log.InfofContext(ctx, "created HNSW index on table %s", fullTableName)

	log.InfoContext(ctx, "pgvector memory database schema initialized successfully")
	return nil
}
