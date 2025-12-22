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
	table    string   // Base table name (without prefix/schema) like "memories"
	suffix   string   // Index suffix like "app_user", "updated_at", "deleted_at"
	columns  []string // Column names in order
	template string   // SQL template for index creation
}

const tableNameMemories = "memories"

// expectedSchema defines the expected schema for the memories table.
var expectedSchema = map[string]struct {
	columns []tableColumn
	indexes []tableIndex
}{
	tableNameMemories: {
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
			{
				table:    "memories",
				suffix:   "app_user",
				columns:  []string{"app_name", "user_id"},
				template: sqlCreateMemoriesAppUserIndex,
			},
			{
				table:    "memories",
				suffix:   "updated_at",
				columns:  []string{"updated_at"},
				template: sqlCreateMemoriesUpdatedAtIndex,
			},
			{
				table:    "memories",
				suffix:   "deleted_at",
				columns:  []string{"deleted_at"},
				template: sqlCreateMemoriesDeletedAtIndex,
			},
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

// checkDDLPrivilege checks if the current user has DDL (CREATE) privilege on the schema.
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
	}, `SELECT has_schema_privilege($1, 'CREATE')`, schemaName)

	if err != nil {
		return false, fmt.Errorf("check DDL privilege on schema %s failed: %w", schemaName, err)
	}
	return hasPrivilege, nil
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(
		ctx,
		"initializing postgres memory database schema...",
	)

	// Check DDL privilege before proceeding.
	hasDDLPrivilege, err := s.checkDDLPrivilege(ctx)
	if err != nil {
		return err
	}
	// Skip DDL operations if user lacks CREATE privilege on the schema.
	if !hasDDLPrivilege {
		log.WarnContext(ctx, "skipping DDL operations: no CREATE privilege on schema")
		return nil
	}

	// Use base table name from opts (before schema prefix is applied).
	baseTableName := s.opts.tableName

	// Create table.
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

	// Index suffix constants for memories table indexes.
	const (
		indexSuffixAppUser   = "app_user"
		indexSuffixUpdatedAt = "updated_at"
		indexSuffixDeletedAt = "deleted_at"
	)

	// Create indexes.
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

	// Verify schema. Panic if schema verification fails (user has DDL privilege here).
	if err := s.verifySchema(ctx); err != nil {
		panic(fmt.Sprintf("schema verification failed with DDL privilege: %v", err))
	}

	log.InfoContext(
		ctx,
		"postgres memory database schema initialized successfully",
	)
	return nil
}

// verifySchema verifies that the database schema matches expectations.
func (s *Service) verifySchema(ctx context.Context) error {
	// Use actual table name from opts instead of hardcoded "memories".
	baseTableName := s.opts.tableName
	fullTableName := sqldb.BuildTableNameWithSchema(s.opts.schema, "", baseTableName)

	// Get schema definition for "memories" table type.
	// Note: expectedSchema is a compile-time constant, so this lookup always succeeds.
	schema := expectedSchema[tableNameMemories]

	// Check if table exists.
	exists, err := s.tableExists(ctx, fullTableName)
	if err != nil {
		return fmt.Errorf("check table %s existence failed: %w", fullTableName, err)
	}
	if !exists {
		return fmt.Errorf("table %s does not exist", fullTableName)
	}

	// Verify columns.
	if err := s.verifyColumns(ctx, fullTableName, schema.columns); err != nil {
		return fmt.Errorf("verify columns for table %s failed: %w", fullTableName, err)
	}

	// Verify indexes (use actual table name for index definitions).
	actualIndexes := make([]tableIndex, len(schema.indexes))
	for i, idx := range schema.indexes {
		actualIndexes[i] = tableIndex{
			table:    baseTableName, // Use actual table name instead of "memories".
			suffix:   idx.suffix,
			columns:  idx.columns,
			template: idx.template,
		}
	}
	if err := s.verifyIndexes(ctx, fullTableName, actualIndexes); err != nil {
		log.WarnfContext(
			ctx,
			"verify indexes for table %s failed (non-fatal): %v",
			fullTableName,
			err,
		)
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
	// Get actual columns from database.
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

	// Check each expected column.
	for _, expected := range expectedColumns {
		actual, exists := actualColumns[expected.name]
		if !exists {
			return fmt.Errorf("column %s.%s is missing", tableName, expected.name)
		}

		// Check data type.
		if actual.dataType != expected.dataType {
			return fmt.Errorf("column %s.%s has type %s, expected %s",
				tableName, expected.name, actual.dataType, expected.dataType)
		}

		// Check nullable.
		if actual.nullable != expected.nullable {
			return fmt.Errorf("column %s.%s nullable mismatch: got %v, expected %v",
				tableName, expected.name, actual.nullable, expected.nullable)
		}
	}

	return nil
}

// indexDetail represents database index details.
type indexDetail struct {
	name    string
	columns []string
}

// verifyIndexes verifies that table indexes exist and match expectations.
func (s *Service) verifyIndexes(
	ctx context.Context,
	fullTableName string,
	expectedIndexes []tableIndex,
) error {
	schema, tableName := parseTableName(fullTableName)

	// Get actual indexes from database with column information.
	actualIndexes := make(map[string]indexDetail)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var indexName, columnName string
			var ordinalPosition int
			if err := rows.Scan(&indexName, &columnName, &ordinalPosition); err != nil {
				return err
			}

			idx, exists := actualIndexes[indexName]
			if !exists {
				idx = indexDetail{
					name:    indexName,
					columns: make([]string, 0),
				}
			}
			idx.columns = append(idx.columns, columnName)
			actualIndexes[indexName] = idx
		}
		return nil
	}, `SELECT
			i.indexname,
			a.attname AS column_name,
			a.attnum AS ordinal_position
		FROM pg_indexes i
		JOIN pg_class c ON c.relname = i.indexname
		JOIN pg_index ix ON ix.indexrelid = c.oid
		JOIN pg_attribute a ON a.attrelid = ix.indrelid
			AND a.attnum = ANY(ix.indkey)
		WHERE i.schemaname = $1
			AND i.tablename = $2
		ORDER BY i.indexname, a.attnum`, schema, tableName)

	if err != nil {
		return fmt.Errorf("query indexes failed: %w", err)
	}

	// Check each expected index.
	for _, expected := range expectedIndexes {
		expectedIndexName := sqldb.BuildIndexName("", expected.table, expected.suffix)

		actual, exists := actualIndexes[expectedIndexName]
		if !exists {
			// Generate the CREATE INDEX SQL for this missing index.
			createSQL := buildCreateIndexSQL(
				s.opts.schema,
				expected.table,
				expected.suffix,
				expected.template,
			)
			log.WarnfContext(
				ctx,
				"index %s on table %s is missing, create it with: %s",
				expectedIndexName,
				fullTableName,
				createSQL,
			)
			continue
		}

		// Verify index column order.
		if !equalStringSlices(actual.columns, expected.columns) {
			log.WarnfContext(
				ctx,
				"index %s on table %s has incorrect columns: got %v, expected %v",
				expectedIndexName,
				fullTableName,
				actual.columns,
				expected.columns,
			)
		}

		// Mark as verified.
		delete(actualIndexes, expectedIndexName)
	}

	// Report unexpected indexes (excluding primary key and unique constraints).
	for indexName := range actualIndexes {
		// Skip primary key indexes (usually named <tablename>_pkey).
		if strings.HasSuffix(indexName, "_pkey") {
			continue
		}
		log.WarnfContext(
			ctx,
			"unexpected index %s found on table %s",
			indexName,
			fullTableName,
		)
	}

	return nil
}

// equalStringSlices compares two string slices for equality.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// parseTableName parses a full table name into schema and table components.
// Examples:
// - "memories" -> ("public", "memories")
// - "myschema.memories" -> ("myschema", "memories")
// - "myschema.prefix.table" -> ("myschema", "prefix.table")
func parseTableName(fullTableName string) (schema, tableName string) {
	parts := strings.SplitN(fullTableName, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "public", fullTableName
}
