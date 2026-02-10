//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (MySQL syntax)
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			state JSON DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			event JSON NOT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateSessionTrackEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			track VARCHAR(255) NOT NULL,
			event JSON NOT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	// Note: no created_at column because summaries are upsert (overwrite on duplicate key).
	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			filter_key VARCHAR(255) NOT NULL DEFAULT '',
			summary JSON DEFAULT NULL,
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			` + "`key`" + ` VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			` + "`key`" + ` VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			expires_at TIMESTAMP(6) NULL DEFAULT NULL,
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	// Index creation SQL (MySQL syntax)
	// Note: MySQL doesn't support IF NOT EXISTS for indexes until MySQL 8.0.13+
	// We'll handle duplicate index errors in the creation logic

	// session_states: unique index on (app_name, user_id, session_id, deleted_at)
	sqlCreateSessionStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, deleted_at)`

	// session_states: TTL index on (expires_at)
	sqlCreateSessionStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// session_events: lookup index on (app_name, user_id, session_id, created_at)
	sqlCreateSessionEventsLookupIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, created_at)`

	// session_events: TTL index on (expires_at)
	sqlCreateSessionEventsExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// session_track_events: lookup index on (app_name, user_id, session_id, created_at)
	sqlCreateSessionTracksIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, created_at)`

	// session_track_events: TTL index on (expires_at)
	sqlCreateSessionTracksExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// session_summaries: TTL index on (expires_at)
	sqlCreateSessionSummariesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// app_states: unique index on (app_name, key, deleted_at)
	sqlCreateAppStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, ` + "`key`" + `, deleted_at)`

	// app_states: TTL index on (expires_at)
	sqlCreateAppStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// user_states: unique index on (app_name, user_id, key, deleted_at)
	sqlCreateUserStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, ` + "`key`" + `, deleted_at)`

	// user_states: TTL index on (expires_at)
	sqlCreateUserStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`
)

// mysqlVarCharIndexPrefixLen is a safe prefix length for utf8mb4 indexes.
// InnoDB has a maximum index key length of 3072 bytes. For utf8mb4, each
// character can take up to 4 bytes. To avoid Error 1071 (Specified key was
// too long), we use 191 as the prefix length, which is the standard
// industry practice:
//   - 4 columns * 191 chars * 4 bytes/char = 3056 bytes < 3072 bytes limit
//   - Using 192 would be 4 * 192 * 4 = 3072 bytes, which is exactly on the
//     boundary and may cause issues in some MySQL versions.
const mysqlVarCharIndexPrefixLen = 191

// session_summaries: unique index on (app_name, user_id, session_id, filter_key).
// Note: This index does NOT include deleted_at because MySQL treats NULL != NULL,
// which would allow duplicate active records. To ensure uniqueness, we exclude
// deleted_at from the unique index. On subsequent writes, deleted_at is reset to
// NULL (via ON DUPLICATE KEY UPDATE), effectively "reviving" the record instead
// of preserving deleted historical versions.
//
// Note: We use prefix indexes to avoid Error 1071 (max key length is 3072 bytes).
var sqlCreateSessionSummariesUniqueIndex = fmt.Sprintf(
	`
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(%d), user_id(%d), session_id(%d), filter_key(%d))`,
	mysqlVarCharIndexPrefixLen,
	mysqlVarCharIndexPrefixLen,
	mysqlVarCharIndexPrefixLen,
	mysqlVarCharIndexPrefixLen,
)

// tableDefinition defines a table with its SQL template
type tableDefinition struct {
	name     string
	template string
}

// indexDefinition defines an index with its table, suffix and SQL template
type indexDefinition struct {
	table    string
	suffix   string
	template string
}

// tableColumn represents a table column definition for schema verification
type tableColumn struct {
	name     string
	dataType string
	nullable bool
}

// tableIndex represents a table index definition for schema verification
type tableIndex struct {
	table   string   // Base table name (without prefix) like "session_states"
	suffix  string   // Index suffix like "unique_active", "lookup", "expires"
	columns []string // Expected columns in the index
	unique  bool     // Whether this is a unique index
}

// expectedSchema defines the expected schema for each table
var expectedSchema = map[string]struct {
	columns []tableColumn
	indexes []tableIndex
}{
	sqldb.TableNameSessionStates: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"user_id", "varchar", false},
			{"session_id", "varchar", false},
			{"state", "json", true},
			{"created_at", "timestamp", false},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "session_id", "deleted_at"}, true},
			{sqldb.TableNameSessionStates, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
	sqldb.TableNameSessionEvents: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"user_id", "varchar", false},
			{"session_id", "varchar", false},
			{"event", "json", false},
			{"created_at", "timestamp", false},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup, []string{"app_name", "user_id", "session_id", "created_at"}, false},
			{sqldb.TableNameSessionEvents, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
	sqldb.TableNameSessionTrackEvents: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"user_id", "varchar", false},
			{"session_id", "varchar", false},
			{"track", "varchar", false},
			{"event", "json", false},
			{"created_at", "timestamp", false},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixLookup, []string{"app_name", "user_id", "session_id", "created_at"}, false},
			{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
	sqldb.TableNameSessionSummaries: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"user_id", "varchar", false},
			{"session_id", "varchar", false},
			{"filter_key", "varchar", false},
			{"summary", "json", true},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			// Unique index on business key only (no deleted_at) to prevent duplicate active records.
			{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "session_id", "filter_key"}, true},
			{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
	sqldb.TableNameAppStates: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"key", "varchar", false},
			{"value", "text", true},
			{"created_at", "timestamp", false},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "key", "deleted_at"}, true},
			{sqldb.TableNameAppStates, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
	sqldb.TableNameUserStates: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "varchar", false},
			{"user_id", "varchar", false},
			{"key", "varchar", false},
			{"value", "text", true},
			{"created_at", "timestamp", false},
			{"updated_at", "timestamp", false},
			{"expires_at", "timestamp", true},
			{"deleted_at", "timestamp", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "key", "deleted_at"}, true},
			{sqldb.TableNameUserStates, sqldb.IndexSuffixExpires, []string{"expires_at"}, false},
		},
	},
}

// Global table definitions
var tableDefs = []tableDefinition{
	{sqldb.TableNameSessionStates, sqlCreateSessionStatesTable},
	{sqldb.TableNameSessionEvents, sqlCreateSessionEventsTable},
	{sqldb.TableNameSessionTrackEvents, sqlCreateSessionTrackEventsTable},
	{sqldb.TableNameSessionSummaries, sqlCreateSessionSummariesTable},
	{sqldb.TableNameAppStates, sqlCreateAppStatesTable},
	{sqldb.TableNameUserStates, sqlCreateUserStatesTable},
}

// Global index definitions
var indexDefs = []indexDefinition{
	// Unique indexes
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive, sqlCreateSessionStatesUniqueIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive, sqlCreateSessionSummariesUniqueIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive, sqlCreateAppStatesUniqueIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive, sqlCreateUserStatesUniqueIndex},

	// Lookup indexes
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup, sqlCreateSessionEventsLookupIndex},
	{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixLookup, sqlCreateSessionTracksIndex},

	// TTL indexes
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixExpires, sqlCreateSessionStatesExpiresIndex},
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixExpires, sqlCreateSessionEventsExpiresIndex},
	{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixExpires, sqlCreateSessionTracksExpiresIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixExpires, sqlCreateSessionSummariesExpiresIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixExpires, sqlCreateAppStatesExpiresIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixExpires, sqlCreateUserStatesExpiresIndex},
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(ctx, "initializing mysql session database schema...")

	// Create tables
	for _, tableDef := range tableDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)
		sql := strings.ReplaceAll(tableDef.template, "{{TABLE_NAME}}", fullTableName)

		if _, err := s.mysqlClient.Exec(ctx, sql); err != nil {
			return fmt.Errorf("create table %s failed: %w", fullTableName, err)
		}
		log.InfofContext(ctx, "created table: %s", fullTableName)
	}

	// Create indexes
	for _, indexDef := range indexDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, indexDef.table)
		indexName := sqldb.BuildIndexName(s.opts.tablePrefix, indexDef.table, indexDef.suffix)
		sql := indexDef.template
		sql = strings.ReplaceAll(sql, "{{TABLE_NAME}}", fullTableName)
		sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", indexName)

		// MySQL doesn't have "IF NOT EXISTS" for indexes in older versions
		// We'll use a different approach: try to create and ignore duplicate key errors
		if _, err := s.mysqlClient.Exec(ctx, sql); err != nil {
			// Check if it's a duplicate index name error (error code 1061).
			// This means the index already exists, which is safe to skip.
			if !isDuplicateIndexNameError(err) {
				return fmt.Errorf(
					"create index %s on table %s failed: %w",
					indexName,
					fullTableName,
					err,
				)
			}
			// Index already exists, log and continue.
			log.InfofContext(ctx, "index %s already exists on table %s, skipping", indexName, fullTableName)
		} else {
			log.InfofContext(ctx, "created index: %s on table %s", indexName, fullTableName)
		}
	}

	// Verify schema
	if err := s.verifySchema(ctx); err != nil {
		return fmt.Errorf("schema verification failed: %w", err)
	}

	log.InfoContext(ctx, "mysql session database schema initialized successfully")
	return nil
}

// verifySchema verifies that the database schema matches expectations.
func (s *Service) verifySchema(ctx context.Context) error {
	// Use tableDefs order for deterministic verification
	for _, tableDef := range tableDefs {
		tableName := tableDef.name
		schema, ok := expectedSchema[tableName]
		if !ok {
			continue
		}
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableName)

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

		// Verify indexes (non-fatal, just log warnings)
		if err := s.verifyIndexes(ctx, fullTableName, schema.indexes); err != nil {
			log.WarnfContext(ctx, "verify indexes for table %s failed (non-fatal): %v", fullTableName, err)
		}
	}

	return nil
}

// tableExists checks if a table exists in the database.
func (s *Service) tableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := s.mysqlClient.QueryRow(ctx,
		[]any{&count},
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		tableName)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// verifyColumns verifies that table columns match expectations.
func (s *Service) verifyColumns(ctx context.Context, tableName string, expectedColumns []tableColumn) error {
	// Get actual columns from database
	actualColumns := make(map[string]tableColumn)
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		var name, dataType, isNullable string
		if err := rows.Scan(&name, &dataType, &isNullable); err != nil {
			return err
		}
		actualColumns[name] = tableColumn{
			name:     name,
			dataType: dataType,
			nullable: isNullable == "YES",
		}
		return nil
	}, `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		AND table_name = ?
		ORDER BY ORDINAL_POSITION`, tableName)

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
	// Build map of expected index names
	expectedIndexNames := make(map[string]bool)
	for _, expected := range expectedIndexes {
		expectedIndexName := sqldb.BuildIndexName(s.opts.tablePrefix, expected.table, expected.suffix)
		expectedIndexNames[expectedIndexName] = true
	}

	// Get actual indexes from database
	actualIndexes := make(map[string][]string)
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		var indexName, columnName string
		if err := rows.Scan(&indexName, &columnName); err != nil {
			return err
		}
		actualIndexes[indexName] = append(actualIndexes[indexName], columnName)
		return nil
	}, `SELECT INDEX_NAME, COLUMN_NAME
		FROM information_schema.statistics
		WHERE table_schema = DATABASE()
		AND table_name = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`, fullTableName)

	if err != nil {
		return fmt.Errorf("query indexes failed: %w", err)
	}

	// Check each expected index
	for _, expected := range expectedIndexes {
		expectedIndexName := sqldb.BuildIndexName(s.opts.tablePrefix, expected.table, expected.suffix)
		actualColumns, exists := actualIndexes[expectedIndexName]
		if !exists {
			// Build CREATE INDEX statement for user reference.
			columnsStr := buildIndexColumnsStr(expected.table, expected.suffix, expected.columns)
			createSQL := buildCreateIndexSQL(expectedIndexName, fullTableName, columnsStr, expected.unique)
			log.WarnfContext(ctx, "index %s on table %s is missing, please run: %s",
				expectedIndexName, fullTableName, createSQL)
			continue
		}

		if !stringSlicesEqual(actualColumns, expected.columns) {
			// Build DROP and CREATE INDEX statements for user reference.
			columnsStr := buildIndexColumnsStr(expected.table, expected.suffix, expected.columns)
			dropSQL := fmt.Sprintf("DROP INDEX %s ON %s;", expectedIndexName, fullTableName)
			createSQL := buildCreateIndexSQL(expectedIndexName, fullTableName, columnsStr, expected.unique)
			log.WarnfContext(ctx, "index %s on table %s has wrong columns: got %v, want %v. "+
				"Please drop and recreate: %s %s",
				expectedIndexName, fullTableName, actualColumns, expected.columns, dropSQL, createSQL)
		}
	}

	// Check for extra/unexpected indexes
	for actualName := range actualIndexes {
		if actualName == "PRIMARY" {
			continue
		}
		if !expectedIndexNames[actualName] {
			dropSQL := fmt.Sprintf("DROP INDEX %s ON %s;", actualName, fullTableName)
			log.WarnfContext(ctx, "unexpected index %s found on table %s (wrong name or deprecated index), "+
				"consider removing: %s", actualName, fullTableName, dropSQL)
		}
	}

	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// buildIndexColumnsStr builds a comma-separated column list with appropriate
// prefix lengths for indexes that require them.
func buildIndexColumnsStr(table, suffix string, columns []string) string {
	// session_summaries unique_active index requires prefix lengths to avoid Error 1071.
	if table == sqldb.TableNameSessionSummaries && suffix == sqldb.IndexSuffixUniqueActive {
		var prefixed []string
		for _, col := range columns {
			prefixed = append(prefixed, fmt.Sprintf("%s(%d)", col, mysqlVarCharIndexPrefixLen))
		}
		return strings.Join(prefixed, ", ")
	}
	// For all other indexes, use columns as-is.
	return strings.Join(columns, ", ")
}

// buildCreateIndexSQL builds a CREATE INDEX SQL statement.
func buildCreateIndexSQL(indexName, tableName, columns string, unique bool) string {
	if unique {
		return fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s(%s);", indexName, tableName, columns)
	}
	return fmt.Sprintf("CREATE INDEX %s ON %s(%s);", indexName, tableName, columns)
}

// isDuplicateIndexNameError checks if the error is a MySQL duplicate index name error (1061).
// This is used when creating indexes - if the index name already exists, we can safely skip.
// Note: This should NOT match error 1062 (duplicate entry), which indicates a data constraint
// violation and should not be silently ignored.
func isDuplicateIndexNameError(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == sqldb.MySQLErrDuplicateKeyName
	}

	return false
}
