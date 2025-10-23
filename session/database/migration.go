//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package database

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// columnInfo represents database column information
type columnInfo struct {
	Field   string
	Type    string
	Null    string
	Key     string
	Default *string
	Extra   string
}

// indexInfo represents database index information
type indexInfo struct {
	Table       string
	NonUnique   int
	KeyName     string
	SeqInIndex  int
	ColumnName  string
	Collation   string
	Cardinality int64
	SubPart     *int
	Packed      *string
	Null        string
	IndexType   string
	Comment     string
}

// expectedColumn represents expected column definition
type expectedColumn struct {
	Name     string
	Type     string
	Nullable bool
}

// expectedIndex represents expected index definition
type expectedIndex struct {
	Name    string
	Columns []string
	Unique  bool
}

// getTableColumns retrieves column information from a table
func getTableColumns(db *gorm.DB, tableName string) (map[string]*columnInfo, error) {
	// Use GORM's Migrator to get column types (works across databases)
	columnTypes, err := db.Migrator().ColumnTypes(tableName)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*columnInfo)
	for _, col := range columnTypes {
		nullable, _ := col.Nullable()
		nullStr := "NO"
		if nullable {
			nullStr = "YES"
		}

		result[col.Name()] = &columnInfo{
			Field: col.Name(),
			Type:  col.DatabaseTypeName(),
			Null:  nullStr,
		}
	}
	return result, nil
}

// getTableIndexes retrieves index information from a table
func getTableIndexes(db *gorm.DB, tableName string) (map[string]*expectedIndex, error) {
	dialectName := db.Dialector.Name()

	switch dialectName {
	case "mysql":
		return getMySQLIndexes(db, tableName)
	case "postgres":
		return getPostgreSQLIndexes(db, tableName)
	case "sqlite":
		return getSQLiteIndexes(db, tableName)
	default:
		// For unsupported databases, return empty map (skip index check)
		log.Warnf("Index checking not supported for database type: %s", dialectName)
		return make(map[string]*expectedIndex), nil
	}
}

// getMySQLIndexes retrieves index information from MySQL
func getMySQLIndexes(db *gorm.DB, tableName string) (map[string]*expectedIndex, error) {
	var indexes []indexInfo
	if err := db.Raw("SHOW INDEX FROM " + tableName).Scan(&indexes).Error; err != nil {
		return nil, err
	}

	result := make(map[string]*expectedIndex)
	for _, idx := range indexes {
		if idx.KeyName == "PRIMARY" {
			continue // Skip primary key
		}

		if existing, ok := result[idx.KeyName]; ok {
			existing.Columns = append(existing.Columns, idx.ColumnName)
		} else {
			result[idx.KeyName] = &expectedIndex{
				Name:    idx.KeyName,
				Columns: []string{idx.ColumnName},
				Unique:  idx.NonUnique == 0,
			}
		}
	}
	return result, nil
}

// getPostgreSQLIndexes retrieves index information from PostgreSQL
func getPostgreSQLIndexes(db *gorm.DB, tableName string) (map[string]*expectedIndex, error) {
	type pgIndex struct {
		IndexName  string
		ColumnName string
		IsUnique   bool
	}

	var indexes []pgIndex
	query := `
		SELECT 
			i.relname AS index_name,
			a.attname AS column_name,
			ix.indisunique AS is_unique
		FROM pg_class t
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
		WHERE t.relname = $1 AND i.relname NOT LIKE 'pg_%'
		ORDER BY i.relname, a.attnum
	`
	if err := db.Raw(query, tableName).Scan(&indexes).Error; err != nil {
		return nil, err
	}

	result := make(map[string]*expectedIndex)
	for _, idx := range indexes {
		if existing, ok := result[idx.IndexName]; ok {
			existing.Columns = append(existing.Columns, idx.ColumnName)
		} else {
			result[idx.IndexName] = &expectedIndex{
				Name:    idx.IndexName,
				Columns: []string{idx.ColumnName},
				Unique:  idx.IsUnique,
			}
		}
	}
	return result, nil
}

// getSQLiteIndexes retrieves index information from SQLite
func getSQLiteIndexes(db *gorm.DB, tableName string) (map[string]*expectedIndex, error) {
	type sqliteIndex struct {
		Name   string
		Unique int
	}

	var indexes []sqliteIndex
	if err := db.Raw("SELECT name, `unique` FROM sqlite_master WHERE type='index' AND tbl_name=?", tableName).
		Scan(&indexes).Error; err != nil {
		return nil, err
	}

	result := make(map[string]*expectedIndex)
	for _, idx := range indexes {
		// Skip auto-created indexes
		if strings.HasPrefix(idx.Name, "sqlite_autoindex") {
			continue
		}

		// Get columns for this index
		type indexColumn struct {
			Name string
		}
		var columns []indexColumn
		db.Raw("PRAGMA index_info(?)", idx.Name).Scan(&columns)

		cols := make([]string, len(columns))
		for i, col := range columns {
			cols[i] = col.Name
		}

		result[idx.Name] = &expectedIndex{
			Name:    idx.Name,
			Columns: cols,
			Unique:  idx.Unique == 1,
		}
	}
	return result, nil
}

// tableExists checks if a table exists
func tableExists(db *gorm.DB, tableName string) (bool, error) {
	// Use GORM's Migrator which works across different databases
	return db.Migrator().HasTable(tableName), nil
}

// getExpectedColumns returns expected columns for each table
func getExpectedColumns() map[string][]expectedColumn {
	return map[string][]expectedColumn{
		"session_states": {
			{Name: "id", Type: "bigint", Nullable: false},
			{Name: "app_name", Type: "varchar", Nullable: false},
			{Name: "user_id", Type: "varchar", Nullable: false},
			{Name: "session_id", Type: "varchar", Nullable: false},
			{Name: "state", Type: "mediumblob", Nullable: true},
			{Name: "created_at", Type: "datetime", Nullable: false},
			{Name: "updated_at", Type: "datetime", Nullable: false},
			{Name: "expires_at", Type: "datetime", Nullable: true},
		},
		"session_events": {
			{Name: "id", Type: "bigint", Nullable: false},
			{Name: "app_name", Type: "varchar", Nullable: false},
			{Name: "user_id", Type: "varchar", Nullable: false},
			{Name: "session_id", Type: "varchar", Nullable: false},
			{Name: "event_data", Type: "mediumblob", Nullable: false},
			{Name: "timestamp", Type: "datetime", Nullable: false},
			{Name: "created_at", Type: "datetime", Nullable: false},
			{Name: "expires_at", Type: "datetime", Nullable: true},
		},
		"session_summaries": {
			{Name: "id", Type: "bigint", Nullable: false},
			{Name: "app_name", Type: "varchar", Nullable: false},
			{Name: "user_id", Type: "varchar", Nullable: false},
			{Name: "session_id", Type: "varchar", Nullable: false},
			{Name: "filter_key", Type: "varchar", Nullable: false},
			{Name: "summary", Type: "blob", Nullable: false},
			{Name: "updated_at", Type: "datetime", Nullable: false},
			{Name: "expires_at", Type: "datetime", Nullable: true},
		},
		"app_states": {
			{Name: "id", Type: "bigint", Nullable: false},
			{Name: "app_name", Type: "varchar", Nullable: false},
			{Name: "state_key", Type: "varchar", Nullable: false},
			{Name: "value", Type: "mediumblob", Nullable: false},
			{Name: "updated_at", Type: "datetime", Nullable: false},
			{Name: "expires_at", Type: "datetime", Nullable: true},
		},
		"user_states": {
			{Name: "id", Type: "bigint", Nullable: false},
			{Name: "app_name", Type: "varchar", Nullable: false},
			{Name: "user_id", Type: "varchar", Nullable: false},
			{Name: "state_key", Type: "varchar", Nullable: false},
			{Name: "value", Type: "mediumblob", Nullable: false},
			{Name: "updated_at", Type: "datetime", Nullable: false},
			{Name: "expires_at", Type: "datetime", Nullable: true},
		},
	}
}

// getExpectedIndexes returns expected indexes for each table
func getExpectedIndexes() map[string][]expectedIndex {
	return map[string][]expectedIndex{
		"session_states": {
			{Name: "idx_app_user_session", Columns: []string{"app_name", "user_id", "session_id"}, Unique: true},
			{Name: "idx_expires_at", Columns: []string{"expires_at"}, Unique: false},
		},
		"session_events": {
			{Name: "idx_app_user_session_event", Columns: []string{"app_name", "user_id", "session_id", "timestamp"}, Unique: false},
			{Name: "idx_expires_at", Columns: []string{"expires_at"}, Unique: false},
		},
		"session_summaries": {
			{Name: "idx_app_user_session_filter", Columns: []string{"app_name", "user_id", "session_id", "filter_key"}, Unique: false},
			{Name: "idx_expires_at", Columns: []string{"expires_at"}, Unique: false},
		},
		"app_states": {
			{Name: "idx_app_key", Columns: []string{"app_name", "state_key"}, Unique: true},
			{Name: "idx_expires_at", Columns: []string{"expires_at"}, Unique: false},
		},
		"user_states": {
			{Name: "idx_app_user_key", Columns: []string{"app_name", "user_id", "state_key"}, Unique: true},
			{Name: "idx_expires_at", Columns: []string{"expires_at"}, Unique: false},
		},
	}
}

// normalizeType normalizes database type string for comparison across different databases
func normalizeType(t string) string {
	t = strings.ToLower(t)
	t = strings.TrimSpace(t)

	// Remove size specifications and parentheses (e.g., "varchar(255)" -> "varchar")
	if idx := strings.Index(t, "("); idx != -1 {
		t = strings.TrimSpace(t[:idx])
	}

	// Integer types (MySQL: BIGINT/INT/TINYINT, PostgreSQL: BIGINT/INTEGER, SQLite: INTEGER)
	if t == "bigint" || t == "bigint unsigned" || t == "integer" ||
		t == "int" || t == "int unsigned" || t == "tinyint" || t == "smallint" {
		return "int"
	}

	// String types (MySQL: VARCHAR, PostgreSQL: CHARACTER VARYING/VARCHAR, SQLite: TEXT)
	if t == "varchar" || t == "character varying" || t == "char" {
		return "varchar"
	}

	// Text types
	if t == "text" || t == "mediumtext" || t == "longtext" {
		return "text"
	}

	// Binary/Blob types (MySQL: BLOB/MEDIUMBLOB, PostgreSQL: BYTEA, SQLite: BLOB)
	if t == "blob" || t == "mediumblob" || t == "longblob" || t == "tinyblob" || t == "bytea" {
		return "blob"
	}

	// Datetime types (MySQL: DATETIME, PostgreSQL: TIMESTAMP, SQLite: DATETIME)
	// Handle "timestamp without time zone" and "timestamp with time zone"
	if t == "datetime" || t == "timestamp" ||
		strings.HasPrefix(t, "timestamp without") || strings.HasPrefix(t, "timestamp with") {
		return "datetime"
	}

	return t
}

// checkTableSchema checks if table schema matches expected definition
func checkTableSchema(db *gorm.DB, tableName string, expectedColumns []expectedColumn) error {
	columns, err := getTableColumns(db, tableName)
	if err != nil {
		return fmt.Errorf("failed to get columns for table %s: %w", tableName, err)
	}

	// Check if all expected columns exist with correct type
	for _, expected := range expectedColumns {
		actual, exists := columns[expected.Name]
		if !exists {
			return fmt.Errorf("table %s: missing column '%s'", tableName, expected.Name)
		}

		// Normalize types for comparison
		expectedType := normalizeType(expected.Type)
		actualType := normalizeType(actual.Type)

		if expectedType != actualType {
			return fmt.Errorf("table %s: column '%s' type mismatch (expected: %s, actual: %s)",
				tableName, expected.Name, expected.Type, actual.Type)
		}

		// Check nullable constraint
		isNullable := actual.Null == "YES"
		if expected.Nullable != isNullable {
			return fmt.Errorf("table %s: column '%s' nullable mismatch (expected: %v, actual: %v)",
				tableName, expected.Name, expected.Nullable, isNullable)
		}
	}

	return nil
}

// checkTableIndexes checks if table indexes match expected definition
func checkTableIndexes(db *gorm.DB, tableName string, expectedIndexes []expectedIndex) {
	indexes, err := getTableIndexes(db, tableName)
	if err != nil {
		log.Warnf("Failed to get indexes for table %s: %v", tableName, err)
		return
	}

	// Check for missing or mismatched indexes
	for _, expected := range expectedIndexes {
		actual, exists := indexes[expected.Name]
		if !exists {
			log.Infof("Table %s: index '%s' does not exist (expected columns: %v)",
				tableName, expected.Name, expected.Columns)
			continue
		}

		// Check if columns match
		if len(actual.Columns) != len(expected.Columns) {
			log.Infof("Table %s: index '%s' column count mismatch (expected: %v, actual: %v)",
				tableName, expected.Name, expected.Columns, actual.Columns)
			continue
		}

		for i, col := range expected.Columns {
			if actual.Columns[i] != col {
				log.Infof("Table %s: index '%s' column mismatch at position %d (expected: %s, actual: %s)",
					tableName, expected.Name, i, col, actual.Columns[i])
				break
			}
		}

		// Check if unique constraint matches
		if actual.Unique != expected.Unique {
			log.Infof("Table %s: index '%s' unique constraint mismatch (expected: %v, actual: %v)",
				tableName, expected.Name, expected.Unique, actual.Unique)
		}
	}

	// Check for extra indexes (informational only)
	for indexName := range indexes {
		found := false
		for _, expected := range expectedIndexes {
			if expected.Name == indexName {
				found = true
				break
			}
		}
		if !found {
			log.Infof("Table %s: found unexpected index '%s'", tableName, indexName)
		}
	}
}

// initializeTables handles table initialization based on configuration
func initializeTables(db *gorm.DB, autoCreateTable, autoMigrate bool) error {
	allModels := []interface{}{
		&sessionStateModel{},
		&sessionEventModel{},
		&sessionSummaryModel{},
		&appStateModel{},
		&userStateModel{},
	}

	tableNames := []string{
		"session_states",
		"session_events",
		"session_summaries",
		"app_states",
		"user_states",
	}

	expectedColumns := getExpectedColumns()
	expectedIndexes := getExpectedIndexes()
	for i, tableName := range tableNames {
		exists, err := tableExists(db, tableName)
		if err != nil {
			return fmt.Errorf("failed to check if table %s exists: %w", tableName, err)
		}

		if !exists {
			// Table doesn't exist
			if !autoCreateTable {
				return fmt.Errorf("table %s does not exist and auto-create is disabled", tableName)
			}

			// Create table using GORM AutoMigrate
			log.Infof("Creating table %s...", tableName)
			if err := db.AutoMigrate(allModels[i]); err != nil {
				return fmt.Errorf("failed to create table %s: %w", tableName, err)
			}
			log.Infof("Table %s created successfully", tableName)
		} else {
			// Table exists, check schema
			log.Debugf("Checking schema for table %s...", tableName)
			if err := checkTableSchema(db, tableName, expectedColumns[tableName]); err != nil {
				return fmt.Errorf("table schema validation failed: %w", err)
			}

			// Check indexes (informational only)
			checkTableIndexes(db, tableName, expectedIndexes[tableName])

			// If auto migrate is enabled, run GORM AutoMigrate
			if autoMigrate {
				log.Infof("Running auto-migration for table %s...", tableName)
				if err := db.AutoMigrate(allModels[i]); err != nil {
					return fmt.Errorf("failed to migrate table %s: %w", tableName, err)
				}
			}
		}
	}

	return nil
}
