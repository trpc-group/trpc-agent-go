//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL template for table creation (PostgreSQL syntax)
const (
	sqlCreateMemoriesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			memory_id TEXT PRIMARY KEY,
			app_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			memory_data JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		)`

	// Index creation SQL
	sqlCreateMemoriesAppUserIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id)`

	sqlCreateMemoriesUpdatedAtIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(updated_at DESC)`

	sqlCreateMemoriesDeletedAtIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(deleted_at)`
)

// buildCreateTableSQL builds the CREATE TABLE SQL with schema and table name.
func buildCreateTableSQL(schema, tableName, template string) string {
	fullTableName := sqldb.BuildTableNameWithSchema(schema, "", tableName)
	sql := strings.ReplaceAll(template, "{{TABLE_NAME}}", fullTableName)
	return sql
}

// buildCreateIndexSQL builds the CREATE INDEX SQL with schema, table name, and index name.
func buildCreateIndexSQL(schema, tableName, indexSuffix, template string) string {
	fullTableName := sqldb.BuildTableNameWithSchema(schema, "", tableName)
	indexName := sqldb.BuildIndexName("", tableName, indexSuffix)
	sql := template
	sql = strings.ReplaceAll(sql, "{{TABLE_NAME}}", fullTableName)
	sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", indexName)
	return sql
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(
		ctx,
		"initializing postgres memory database schema...",
	)

	// Use base table name from opts (before schema prefix is applied)
	baseTableName := s.opts.tableName

	// Create table
	tableSQL := buildCreateTableSQL(s.opts.schema, baseTableName, sqlCreateMemoriesTable)
	fullTableName := sqldb.BuildTableNameWithSchema(s.opts.schema, "", baseTableName)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", fullTableName, err)
	}
	log.InfofContext(
		ctx,
		"created table: %s",
		fullTableName,
	)

	// Index suffix constants for memories table indexes
	const (
		indexSuffixAppUser   = "app_user"
		indexSuffixUpdatedAt = "updated_at"
		indexSuffixDeletedAt = "deleted_at"
	)

	// Create indexes
	indexes := []struct {
		suffix   string
		template string
	}{
		{indexSuffixAppUser, sqlCreateMemoriesAppUserIndex},
		{indexSuffixUpdatedAt, sqlCreateMemoriesUpdatedAtIndex},
		{indexSuffixDeletedAt, sqlCreateMemoriesDeletedAtIndex},
	}

	for _, idx := range indexes {
		indexSQL := buildCreateIndexSQL(s.opts.schema, baseTableName, idx.suffix, idx.template)
		if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index %s on table %s failed: %w", idx.suffix, fullTableName, err)
		}
		log.InfofContext(
			ctx,
			"created index: %s on table %s",
			idx.suffix,
			fullTableName,
		)
	}

	log.InfoContext(
		ctx,
		"postgres memory database schema initialized successfully",
	)
	return nil
}
