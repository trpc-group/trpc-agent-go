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

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS session_states (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			state JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS session_events (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS session_summaries (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			filter_key VARCHAR(255) NOT NULL DEFAULT '',
			summary JSONB,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS app_states (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS user_states (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`

	// Index creation SQL
	// session_states: partial unique index on (app_name, user_id, session_id) - only for non-deleted records
	sqlCreateSessionStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_session_states_unique_active
		ON session_states(app_name, user_id, session_id)
		WHERE deleted_at IS NULL`

	// session_states: lookup index on (app_name, user_id, session_id)
	sqlCreateSessionStatesIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_states_lookup
		ON session_states(app_name, user_id, session_id)`

	// session_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_states_expires
		ON session_states(expires_at) WHERE expires_at IS NOT NULL`

	// session_events: lookup index on (app_name, user_id, session_id, created_at)
	sqlCreateSessionEventsIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_events_lookup
		ON session_events(app_name, user_id, session_id, created_at)`

	// session_events: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionEventsExpiresIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_events_expires
		ON session_events(expires_at) WHERE expires_at IS NOT NULL`

	// session_summaries: partial unique index on (app_name, user_id, session_id, filter_key) - only for non-deleted records
	sqlCreateSessionSummariesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_session_summaries_unique_active
		ON session_summaries(app_name, user_id, session_id, filter_key)
		WHERE deleted_at IS NULL`

	// session_summaries: lookup index on (app_name, user_id, session_id, filter_key)
	sqlCreateSessionSummariesIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_summaries_lookup
		ON session_summaries(app_name, user_id, session_id, filter_key)`

	// session_summaries: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionSummariesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_summaries_expires
		ON session_summaries(expires_at) WHERE expires_at IS NOT NULL`

	// app_states: partial unique index on (app_name, key) - only for non-deleted records
	sqlCreateAppStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_app_states_unique_active
		ON app_states(app_name, key)
		WHERE deleted_at IS NULL`

	// app_states: lookup index on (app_name, key)
	sqlCreateAppStatesIndex = `
		CREATE INDEX IF NOT EXISTS idx_app_states_lookup
		ON app_states(app_name, key)`

	// app_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateAppStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS idx_app_states_expires
		ON app_states(expires_at) WHERE expires_at IS NOT NULL`

	// user_states: partial unique index on (app_name, user_id, key) - only for non-deleted records
	sqlCreateUserStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_user_states_unique_active
		ON user_states(app_name, user_id, key)
		WHERE deleted_at IS NULL`

	// user_states: lookup index on (app_name, user_id, key)
	sqlCreateUserStatesIndex = `
		CREATE INDEX IF NOT EXISTS idx_user_states_lookup
		ON user_states(app_name, user_id, key)`

	// user_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateUserStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS idx_user_states_expires
		ON user_states(expires_at) WHERE expires_at IS NOT NULL`

	// session_states: soft delete index on (deleted_at) - partial index for non-null values
	sqlCreateSessionStatesDeletedIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_states_deleted
		ON session_states(deleted_at) WHERE deleted_at IS NOT NULL`

	// session_events: soft delete index on (deleted_at) - partial index for non-null values
	sqlCreateSessionEventsDeletedIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_events_deleted
		ON session_events(deleted_at) WHERE deleted_at IS NOT NULL`

	// session_summaries: soft delete index on (deleted_at) - partial index for non-null values
	sqlCreateSessionSummariesDeletedIndex = `
		CREATE INDEX IF NOT EXISTS idx_session_summaries_deleted
		ON session_summaries(deleted_at) WHERE deleted_at IS NOT NULL`

	// app_states: soft delete index on (deleted_at) - partial index for non-null values
	sqlCreateAppStatesDeletedIndex = `
		CREATE INDEX IF NOT EXISTS idx_app_states_deleted
		ON app_states(deleted_at) WHERE deleted_at IS NOT NULL`

	// user_states: soft delete index on (deleted_at) - partial index for non-null values
	sqlCreateUserStatesDeletedIndex = `
		CREATE INDEX IF NOT EXISTS idx_user_states_deleted
		ON user_states(deleted_at) WHERE deleted_at IS NOT NULL`
)

// tableColumn represents a table column definition
type tableColumn struct {
	name     string
	dataType string
	nullable bool
}

// tableIndex represents a table index definition
type tableIndex struct {
	name    string
	columns []string
}

// expectedSchema defines the expected schema for each table
var expectedSchema = map[string]struct {
	columns []tableColumn
	indexes []tableIndex
}{
	"session_states": {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"session_id", "character varying", false},
			{"state", "jsonb", true},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"idx_session_states_unique_active", []string{"app_name", "user_id", "session_id"}},
			{"idx_session_states_lookup", []string{"app_name", "user_id", "session_id"}},
			{"idx_session_states_expires", []string{"expires_at"}},
			{"idx_session_states_deleted", []string{"deleted_at"}},
		},
	},
	"session_events": {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"session_id", "character varying", false},
			{"event", "jsonb", false},
			{"created_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"idx_session_events_lookup", []string{"app_name", "user_id", "session_id", "created_at"}},
			{"idx_session_events_expires", []string{"expires_at"}},
			{"idx_session_events_deleted", []string{"deleted_at"}},
		},
	},
	"session_summaries": {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"session_id", "character varying", false},
			{"filter_key", "character varying", false},
			{"summary", "jsonb", true},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"idx_session_summaries_unique_active", []string{"app_name", "user_id", "session_id", "filter_key"}},
			{"idx_session_summaries_lookup", []string{"app_name", "user_id", "session_id", "filter_key"}},
			{"idx_session_summaries_expires", []string{"expires_at"}},
			{"idx_session_summaries_deleted", []string{"deleted_at"}},
		},
	},
	"app_states": {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"key", "character varying", false},
			{"value", "jsonb", true},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"idx_app_states_unique_active", []string{"app_name", "key"}},
			{"idx_app_states_lookup", []string{"app_name", "key"}},
			{"idx_app_states_expires", []string{"expires_at"}},
			{"idx_app_states_deleted", []string{"deleted_at"}},
		},
	},
	"user_states": {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"key", "character varying", false},
			{"value", "jsonb", true},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{"idx_user_states_unique_active", []string{"app_name", "user_id", "key"}},
			{"idx_user_states_lookup", []string{"app_name", "user_id", "key"}},
			{"idx_user_states_expires", []string{"expires_at"}},
			{"idx_user_states_deleted", []string{"deleted_at"}},
		},
	},
}

// initDB initializes the database schema
func (s *Service) initDB(ctx context.Context) error {
	// Create tables
	if err := s.createTables(ctx); err != nil {
		return fmt.Errorf("create tables failed: %w", err)
	}

	// Create indexes
	if err := s.createIndexes(ctx); err != nil {
		return fmt.Errorf("create indexes failed: %w", err)
	}

	// Verify schema
	if err := s.verifySchema(ctx); err != nil {
		return fmt.Errorf("schema verification failed: %w", err)
	}

	return nil
}

// createTables creates all required tables
func (s *Service) createTables(ctx context.Context) error {
	tables := []string{
		sqlCreateSessionStatesTable,
		sqlCreateSessionEventsTable,
		sqlCreateSessionSummariesTable,
		sqlCreateAppStatesTable,
		sqlCreateUserStatesTable,
	}

	for _, tableSQL := range tables {
		if _, err := s.pgClient.ExecContext(ctx, tableSQL); err != nil {
			return fmt.Errorf("create table failed: %w", err)
		}
	}

	return nil
}

// createIndexes creates all required indexes
func (s *Service) createIndexes(ctx context.Context) error {
	indexes := []string{
		// Partial unique indexes (only for non-deleted records)
		sqlCreateSessionStatesUniqueIndex,
		sqlCreateSessionSummariesUniqueIndex,
		sqlCreateAppStatesUniqueIndex,
		sqlCreateUserStatesUniqueIndex,
		// Lookup indexes
		sqlCreateSessionStatesIndex,
		sqlCreateSessionEventsIndex,
		sqlCreateSessionSummariesIndex,
		sqlCreateAppStatesIndex,
		sqlCreateUserStatesIndex,
		// TTL indexes
		sqlCreateSessionStatesExpiresIndex,
		sqlCreateSessionEventsExpiresIndex,
		sqlCreateSessionSummariesExpiresIndex,
		sqlCreateAppStatesExpiresIndex,
		sqlCreateUserStatesExpiresIndex,
		// Soft delete indexes
		sqlCreateSessionStatesDeletedIndex,
		sqlCreateSessionEventsDeletedIndex,
		sqlCreateSessionSummariesDeletedIndex,
		sqlCreateAppStatesDeletedIndex,
		sqlCreateUserStatesDeletedIndex,
	}

	for _, indexSQL := range indexes {
		if _, err := s.pgClient.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index failed: %w", err)
		}
	}

	return nil
}

// verifySchema verifies that the database schema matches expectations
func (s *Service) verifySchema(ctx context.Context) error {
	for tableName, schema := range expectedSchema {
		// Check if table exists
		exists, err := s.tableExists(ctx, tableName)
		if err != nil {
			return fmt.Errorf("check table %s existence failed: %w", tableName, err)
		}
		if !exists {
			return fmt.Errorf("table %s does not exist", tableName)
		}

		// Verify columns
		if err := s.verifyColumns(ctx, tableName, schema.columns); err != nil {
			return fmt.Errorf("verify columns for table %s failed: %w", tableName, err)
		}

		// Verify indexes
		if err := s.verifyIndexes(ctx, tableName, schema.indexes); err != nil {
			log.Warnf("verify indexes for table %s failed (non-fatal): %v", tableName, err)
		}
	}

	return nil
}

// tableExists checks if a table exists
func (s *Service) tableExists(ctx context.Context, tableName string) (bool, error) {
	var exists bool
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&exists)
		}
		return nil
	}, `SELECT EXISTS (
		SELECT FROM information_schema.tables
		WHERE table_schema = 'public'
		AND table_name = $1
	)`, tableName)

	return exists, err
}

// verifyColumns verifies that table columns match expectations
func (s *Service) verifyColumns(ctx context.Context, tableName string, expectedColumns []tableColumn) error {
	// Get actual columns from database
	actualColumns := make(map[string]tableColumn)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
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
		WHERE table_schema = 'public'
		AND table_name = $1
		ORDER BY ordinal_position`, tableName)

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
		if !isCompatibleType(actual.dataType, expected.dataType) {
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

// verifyIndexes verifies that table indexes exist
func (s *Service) verifyIndexes(ctx context.Context, tableName string, expectedIndexes []tableIndex) error {
	// Get actual indexes from database
	actualIndexes := make(map[string]bool)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
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
		WHERE schemaname = 'public'
		AND tablename = $1`, tableName)

	if err != nil {
		return fmt.Errorf("query indexes failed: %w", err)
	}

	// Check each expected index
	for _, expected := range expectedIndexes {
		if !actualIndexes[expected.name] {
			log.Warnf("index %s on table %s is missing", expected.name, tableName)
		}
	}

	return nil
}

// isCompatibleType checks if two PostgreSQL data types are compatible
func isCompatibleType(actual, expected string) bool {
	// Normalize type names
	typeMap := map[string]string{
		"character varying":           "varchar",
		"varchar":                     "varchar",
		"timestamp without time zone": "timestamp",
		"timestamp":                   "timestamp",
		"bigint":                      "bigint",
		"bytea":                       "bytea",
		"jsonb":                       "jsonb",
	}

	actualNorm := typeMap[actual]
	expectedNorm := typeMap[expected]

	if actualNorm == "" {
		actualNorm = actual
	}
	if expectedNorm == "" {
		expectedNorm = expected
	}

	return actualNorm == expectedNorm
}
