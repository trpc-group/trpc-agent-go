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
	"database/sql"
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

// tableColumn represents a table column definition.
type tableColumn struct {
	name     string
	dataType string
	nullable bool
}

// tableIndex represents a table index definition.
type tableIndex struct {
	table   string // Base table name (without prefix/schema) like "memories"
	suffix  string // Index suffix like "app_user", "updated_at", "deleted_at"
	columns []string
}

// expectedSchema defines the expected schema for the memories table.
var expectedSchema = map[string]struct {
	columns []tableColumn
	indexes []tableIndex
}{
	"memories": {
		columns: []tableColumn{
			{"memory_id", "text", false},
			{"app_name", "text", false},
			{"user_id", "text", false},
			{"memory_data", "jsonb", false},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"memories", "app_user", []string{"app_name", "user_id"}},
			{"memories", "updated_at", []string{"updated_at"}},
			{"memories", "deleted_at", []string{"deleted_at"}},
		},
	},
}

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
	log.Info("initializing postgres memory database schema...")

	// Use base table name from opts (before schema prefix is applied)
	baseTableName := s.opts.tableName

	// Create table
	tableSQL := buildCreateTableSQL(s.opts.schema, baseTableName, sqlCreateMemoriesTable)
	fullTableName := sqldb.BuildTableNameWithSchema(s.opts.schema, "", baseTableName)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", fullTableName, err)
	}
	log.Infof("created table: %s", fullTableName)

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
		log.Infof("created index: %s on table %s", idx.suffix, fullTableName)
	}

	// Verify schema
	if err := s.verifySchema(ctx); err != nil {
		return fmt.Errorf("schema verification failed: %w", err)
	}

	log.Info("postgres memory database schema initialized successfully")
	return nil
}

// verifySchema verifies that the database schema matches expectations.
func (s *Service) verifySchema(ctx context.Context) error {
	for tableName, schema := range expectedSchema {
		fullTableName := sqldb.BuildTableNameWithSchema(s.opts.schema, "", tableName)

		// Check if table exists
		exists, err := s.tableExists(ctx, fullTableName)
		if err != nil {
			return fmt.Errorf("check table %s existence failed: %w", fullTableName, err)
		}
		if !exists {
			return fmt.Errorf("table %s does not exist", fullTableName)
		}

		// Verify columns
		if err := s.verifyColumns(ctx, fullTableName, schema.columns); err != nil {
			return fmt.Errorf("verify columns for table %s failed: %w", fullTableName, err)
		}

		// Verify indexes
		if err := s.verifyIndexes(ctx, fullTableName, schema.indexes); err != nil {
			log.Warnf("verify indexes for table %s failed (non-fatal): %v", fullTableName, err)
		}
	}

	return nil
}

// tableExists checks if a table exists.
func (s *Service) tableExists(ctx context.Context, fullTableName string) (bool, error) {
	schema, tableName := parseTableName(fullTableName)
	var exists bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&exists)
		}
		return nil
	}, `SELECT EXISTS (
		SELECT FROM information_schema.tables
		WHERE table_schema = $1
		AND table_name = $2
	)`, schema, tableName)

	return exists, err
}

// verifyColumns verifies that table columns match expectations.
func (s *Service) verifyColumns(ctx context.Context, fullTableName string, expectedColumns []tableColumn) error {
	schema, tableName := parseTableName(fullTableName)
	// Get actual columns from database
	actualColumns := make(map[string]tableColumn)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var name, dataType string
			var isNullable string
			if err := rows.Scan(&name, &dataType, &isNullable); err != nil {
				return err
			}
			actualColumns[name] = tableColumn{
				name:     name,
				dataType: dataType,
				nullable: isNullable == "YES",
			}
		}
		return nil
	}, `SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = $1
		AND table_name = $2
		ORDER BY ordinal_position`, schema, tableName)

	if err != nil {
		return fmt.Errorf("query columns failed: %w", err)
	}

	// Check each expected column
	for _, expected := range expectedColumns {
		actual, exists := actualColumns[expected.name]
		if !exists {
			return fmt.Errorf("column %s.%s is missing", tableName, expected.name)
		}

		// Check data type
		if actual.dataType != expected.dataType {
			return fmt.Errorf("column %s.%s has type %s, expected %s",
				tableName, expected.name, actual.dataType, expected.dataType)
		}

		// Check nullable
		if actual.nullable != expected.nullable {
			return fmt.Errorf("column %s.%s nullable mismatch: got %v, expected %v",
				tableName, expected.name, actual.nullable, expected.nullable)
		}
	}

	return nil
}

// verifyIndexes verifies that table indexes exist.
func (s *Service) verifyIndexes(ctx context.Context, fullTableName string, expectedIndexes []tableIndex) error {
	schema, tableName := parseTableName(fullTableName)
	// Get actual indexes from database
	actualIndexes := make(map[string]bool)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var indexName string
			if err := rows.Scan(&indexName); err != nil {
				return err
			}
			actualIndexes[indexName] = true
		}
		return nil
	}, `SELECT indexname
		FROM pg_indexes
		WHERE schemaname = $1
		AND tablename = $2`, schema, tableName)

	if err != nil {
		return fmt.Errorf("query indexes failed: %w", err)
	}

	// Check each expected index
	for _, expected := range expectedIndexes {
		// Use sqldb.BuildIndexName to construct the expected index name
		expectedIndexName := sqldb.BuildIndexName("", expected.table, expected.suffix)

		if !actualIndexes[expectedIndexName] {
			log.Warnf("index %s on table %s is missing", expectedIndexName, fullTableName)
		}
	}

	return nil
}

// parseTableName parses a full table name into schema and table components.
// Examples:
// - "memories" -> ("public", "memories")
// - "myschema.memories" -> ("myschema", "memories")
func parseTableName(fullTableName string) (schema, tableName string) {
	parts := strings.Split(fullTableName, ".")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "public", fullTableName
}
