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
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// SQL templates for table creation
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			state JSONB DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionTrackEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			track VARCHAR(255) NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			filter_key VARCHAR(255) NOT NULL DEFAULT '',
			summary JSONB DEFAULT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	// Index creation SQL
	// session_states: partial unique index on (app_name, user_id, session_id) - only for non-deleted records
	sqlCreateSessionStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id)
		WHERE deleted_at IS NULL`

	// session_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`

	// session_events: lookup index on (app_name, user_id, session_id, created_at)
	sqlCreateSessionEventsIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, created_at)`

	// session_events: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionEventsExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`

	// session_track_events: lookup index on (app_name, user_id, session_id, track, created_at).
	sqlCreateSessionTracksIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, track, created_at)`

	// session_track_events: TTL index on (expires_at).
	sqlCreateSessionTracksExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`

	// session_summaries: partial unique index on (app_name, user_id, session_id, filter_key) - only for non-deleted records
	sqlCreateSessionSummariesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id, filter_key)
		WHERE deleted_at IS NULL`

	// session_summaries: TTL index on (expires_at) - partial index for non-null values
	sqlCreateSessionSummariesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`

	// app_states: partial unique index on (app_name, key) - only for non-deleted records
	sqlCreateAppStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, key)
		WHERE deleted_at IS NULL`

	// app_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateAppStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`

	// user_states: partial unique index on (app_name, user_id, key) - only for non-deleted records
	sqlCreateUserStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, key)
		WHERE deleted_at IS NULL`

	// user_states: TTL index on (expires_at) - partial index for non-null values
	sqlCreateUserStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at) WHERE expires_at IS NOT NULL`
)

// tableColumn represents a table column definition
type tableColumn struct {
	name     string
	dataType string
	nullable bool
}

// tableIndex represents a table index definition
type tableIndex struct {
	table   string // Base table name (without prefix/schema) like "session_states"
	suffix  string // Index suffix like "unique_active", "lookup", "expires"
	columns []string
}

// expectedSchema defines the expected schema for each table
var expectedSchema = map[string]struct {
	columns []tableColumn
	indexes []tableIndex
}{
	sqldb.TableNameSessionStates: {
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
			{sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "session_id"}},
			{sqldb.TableNameSessionStates, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
	sqldb.TableNameSessionEvents: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"session_id", "character varying", false},
			{"event", "jsonb", false},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup, []string{"app_name", "user_id", "session_id", "created_at"}},
			{sqldb.TableNameSessionEvents, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
	sqldb.TableNameSessionTrackEvents: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"session_id", "character varying", false},
			{"track", "character varying", false},
			{"event", "jsonb", false},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixLookup, []string{"app_name", "user_id", "session_id", "track", "created_at"}},
			{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
	sqldb.TableNameSessionSummaries: {
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
			{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "session_id", "filter_key"}},
			{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
	sqldb.TableNameAppStates: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"key", "character varying", false},
			{"value", "text", true},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "key"}},
			{sqldb.TableNameAppStates, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
	sqldb.TableNameUserStates: {
		columns: []tableColumn{
			{"id", "bigint", false},
			{"app_name", "character varying", false},
			{"user_id", "character varying", false},
			{"key", "character varying", false},
			{"value", "text", true},
			{"created_at", "timestamp without time zone", false},
			{"updated_at", "timestamp without time zone", false},
			{"expires_at", "timestamp without time zone", true},
			{"deleted_at", "timestamp without time zone", true},
		},
		indexes: []tableIndex{
			{sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive, []string{"app_name", "user_id", "key"}},
			{sqldb.TableNameUserStates, sqldb.IndexSuffixExpires, []string{"expires_at"}},
		},
	},
}

// indexDefinition defines an index with its table, suffix and SQL template
type indexDefinition struct {
	table    string
	suffix   string
	template string
}

// tableDefinition defines a table with its SQL template
type tableDefinition struct {
	name     string
	template string
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
	// Partial unique indexes (only for non-deleted records)
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive, sqlCreateSessionStatesUniqueIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive, sqlCreateSessionSummariesUniqueIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive, sqlCreateAppStatesUniqueIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive, sqlCreateUserStatesUniqueIndex},
	// Lookup indexes (only session_events needs a separate lookup index)
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup, sqlCreateSessionEventsIndex},
	{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixLookup, sqlCreateSessionTracksIndex},
	// TTL indexes
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixExpires, sqlCreateSessionStatesExpiresIndex},
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixExpires, sqlCreateSessionEventsExpiresIndex},
	{sqldb.TableNameSessionTrackEvents, sqldb.IndexSuffixExpires, sqlCreateSessionTracksExpiresIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixExpires, sqlCreateSessionSummariesExpiresIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixExpires, sqlCreateAppStatesExpiresIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixExpires, sqlCreateUserStatesExpiresIndex},
}

// buildCreateTableSQL builds CREATE TABLE SQL with table prefix.
func buildCreateTableSQL(schema, prefix, tableName, template string) string {
	fullTableName := sqldb.BuildTableNameWithSchema(schema, prefix, tableName)
	return strings.ReplaceAll(template, "{{TABLE_NAME}}", fullTableName)
}

// buildCreateIndexSQL builds CREATE INDEX SQL with table and index names.
func buildCreateIndexSQL(schema, prefix, tableName, suffix, template string) string {
	fullTableName := sqldb.BuildTableNameWithSchema(schema, prefix, tableName)
	indexName := sqldb.BuildIndexNameWithSchema(schema, prefix, tableName, suffix)

	sql := strings.ReplaceAll(template, "{{TABLE_NAME}}", fullTableName)
	sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", indexName)
	return sql
}

// parseTableName parses a full table name into schema and table components.
// Examples:
// - "session_states" -> ("public", "session_states")
// - "myschema.session_states" -> ("myschema", "session_states")
func parseTableName(fullTableName string) (schema, tableName string) {
	parts := strings.Split(fullTableName, ".")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "public", fullTableName
}

// initDB initializes the database schema. It returns an error instead of
// panicking so NewService can surface a startup failure (e.g. the runtime
// account lacks DDL privilege) rather than crashing the process.
func (s *Service) initDB(ctx context.Context) error {
	// Create tables
	if err := createTables(ctx, s.pgClient, s.opts.schema, s.opts.tablePrefix); err != nil {
		return fmt.Errorf("create tables failed: %w", err)
	}

	// Create indexes
	if err := createIndexes(ctx, s.pgClient, s.opts.schema, s.opts.tablePrefix); err != nil {
		return fmt.Errorf("create indexes failed: %w", err)
	}

	// Verify schema
	if err := s.verifySchema(ctx); err != nil {
		return fmt.Errorf("schema verification failed: %w", err)
	}
	return nil
}

// createTables creates all required tables with the given prefix.
// This function can be used by both Service and standalone InitDB.
func createTables(ctx context.Context, client storage.Client, schema, prefix string) error {
	for _, table := range tableDefs {
		tableSQL := buildCreateTableSQL(schema, prefix, table.name, table.template)
		fullTableName := sqldb.BuildTableNameWithSchema(schema, prefix, table.name)
		if _, err := client.ExecContext(ctx, tableSQL); err != nil {
			return fmt.Errorf("create table %s failed: %w", fullTableName, err)
		}
	}

	return nil
}

// createIndexes creates all required indexes with the given prefix.
// This function can be used by both Service and standalone InitDB.
func createIndexes(ctx context.Context, client storage.Client, schema, prefix string) error {
	for _, idx := range indexDefs {
		indexSQL := buildCreateIndexSQL(schema, prefix, idx.table, idx.suffix, idx.template)
		fullTableName := sqldb.BuildTableNameWithSchema(schema, prefix, idx.table)
		if _, err := client.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index on %s failed: %w", fullTableName, err)
		}
	}

	return nil
}

// verifySchema verifies that the database schema matches expectations
func (s *Service) verifySchema(ctx context.Context) error {
	for tableName, schema := range expectedSchema {
		fullTableName := sqldb.BuildTableNameWithSchema(s.opts.schema, s.opts.tablePrefix, tableName)

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

		// Verify indexes. A missing UNIQUE index is fatal (uniqueness is no longer
		// enforced); other index drift is logged as a warning inside verifyIndexes.
		if err := s.verifyIndexes(ctx, fullTableName, schema.indexes); err != nil {
			return fmt.Errorf("verify indexes for table %s failed: %w", fullTableName, err)
		}
	}

	return nil
}

// tableExists checks if a table exists
func (s *Service) tableExists(ctx context.Context, fullTableName string) (bool, error) {
	schema, tableName := parseTableName(fullTableName)
	var exists bool
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
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

// verifyColumns verifies that table columns match expectations
func (s *Service) verifyColumns(ctx context.Context, fullTableName string, expectedColumns []tableColumn) error {
	schema, tableName := parseTableName(fullTableName)
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

// verifyIndexes verifies that table indexes exist
func (s *Service) verifyIndexes(ctx context.Context, fullTableName string, expectedIndexes []tableIndex) error {
	schema, tableName := parseTableName(fullTableName)
	// Get each index's uniqueness and full definition. Verifying the columns and
	// partial predicate (via pg_get_indexdef), not just the name and uniqueness,
	// catches a same-name index that is non-unique, on the wrong columns, or
	// missing the `deleted_at IS NULL` predicate — any of which would leave the
	// uniqueness contract unenforced.
	type indexInfo struct {
		unique bool
		def    string
	}
	actual := make(map[string]indexInfo)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var name, def string
			var isUnique bool
			if err := rows.Scan(&name, &isUnique, &def); err != nil {
				return err
			}
			actual[name] = indexInfo{unique: isUnique, def: def}
		}
		return nil
	}, `SELECT i.relname, ix.indisunique, pg_get_indexdef(i.oid)
		FROM pg_class t
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		WHERE n.nspname = $1 AND t.relname = $2`, schema, tableName)

	if err != nil {
		return fmt.Errorf("query indexes failed: %w", err)
	}

	// normalizeIndexDef lowercases, strips quotes and collapses whitespace so the
	// expected column list / predicate can be matched against pg_get_indexdef's
	// normalized output without being tripped up by quoting or spacing.
	normalizeIndexDef := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, `"`, "")), " "))
	}

	// Check each expected index. The required partial UNIQUE index must exist, be
	// unique, cover the expected columns, and carry the `deleted_at IS NULL`
	// predicate; any deviation is fatal. Other missing indexes are only warnings.
	var invalidUnique []string
	for _, expected := range expectedIndexes {
		// Use sqldb.BuildIndexNameWithSchema to construct the expected index.
		expectedIndexName := sqldb.BuildIndexNameWithSchema(
			s.opts.schema,
			s.opts.tablePrefix,
			expected.table,
			expected.suffix,
		)

		info, exists := actual[expectedIndexName]

		if expected.suffix != sqldb.IndexSuffixUniqueActive {
			if !exists {
				log.WarnfContext(ctx, "index %s on table %s is missing", expectedIndexName, fullTableName)
			}
			continue
		}

		switch {
		case !exists:
			log.ErrorfContext(ctx, "UNIQUE index %s on table %s is missing; uniqueness is NOT enforced",
				expectedIndexName, fullTableName)
			invalidUnique = append(invalidUnique, expectedIndexName)
		case !info.unique:
			log.ErrorfContext(ctx, "index %s on table %s exists but is NOT unique; uniqueness is NOT enforced",
				expectedIndexName, fullTableName)
			invalidUnique = append(invalidUnique, expectedIndexName)
		default:
			def := normalizeIndexDef(info.def)
			wantCols := normalizeIndexDef("(" + strings.Join(expected.columns, ", ") + ")")
			if !strings.Contains(def, wantCols) || !strings.Contains(def, "deleted_at is null") {
				log.ErrorfContext(ctx, "UNIQUE index %s on table %s does not match the expected definition "+
					"(want columns %v with predicate deleted_at IS NULL); uniqueness may not be enforced as "+
					"intended. Actual: %s", expectedIndexName, fullTableName, expected.columns, info.def)
				invalidUnique = append(invalidUnique, expectedIndexName)
			}
		}
	}

	if len(invalidUnique) > 0 {
		return fmt.Errorf("required unique index(es) invalid on table %s: %v", fullTableName, invalidUnique)
	}
	return nil
}

// InitDBConfig contains configuration for standalone database initialization.
type InitDBConfig struct {
	host         string
	port         int
	user         string
	password     string
	database     string
	sslMode      string
	tablePrefix  string
	schema       string
	instanceName string
	extraOptions []any
}

// InitDBOpt is the option for InitDB.
type InitDBOpt func(*InitDBConfig)

// WithInitDBHost sets the PostgreSQL host.
func WithInitDBHost(host string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.host = host
	}
}

// WithInitDBPort sets the PostgreSQL port.
func WithInitDBPort(port int) InitDBOpt {
	return func(c *InitDBConfig) {
		c.port = port
	}
}

// WithInitDBUser sets the database user.
func WithInitDBUser(user string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.user = user
	}
}

// WithInitDBPassword sets the database password.
func WithInitDBPassword(password string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.password = password
	}
}

// WithInitDBDatabase sets the database name.
func WithInitDBDatabase(database string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.database = database
	}
}

// WithInitDBSSLMode sets the SSL mode.
func WithInitDBSSLMode(sslMode string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.sslMode = sslMode
	}
}

// WithInitDBTablePrefix sets the table name prefix.
// Note: An underscore will be automatically added if not present.
// "trpc" and "trpc_" both result in "trpc_" prefix.
//
// Security: Uses internal/session/sqldb.ValidateTablePrefix to prevent SQL injection.
func WithInitDBTablePrefix(prefix string) InitDBOpt {
	return func(c *InitDBConfig) {
		if prefix == "" {
			c.tablePrefix = ""
			return
		}

		// Use internal/session/sqldb validation
		sqldb.MustValidateTablePrefix(prefix)

		// Automatically add underscore if not present
		if !strings.HasSuffix(prefix, "_") {
			prefix += "_"
		}
		c.tablePrefix = prefix
	}
}

// WithInitDBSchema sets the PostgreSQL schema name where tables will be created.
// Note: The schema must already exist in the database before calling InitDB.
// Security: Uses internal/session/sqldb.ValidateTableName to prevent SQL injection.
func WithInitDBSchema(schema string) InitDBOpt {
	return func(c *InitDBConfig) {
		if schema != "" {
			// Use internal/session/sqldb validation
			sqldb.MustValidateTableName(schema)
		}
		c.schema = schema
	}
}

// WithInitDBInstanceName uses a postgres instance from storage.
// Note: Direct connection settings (WithInitDBHost, WithInitDBPort, etc.) have higher priority.
// If both are specified, direct connection settings will be used.
func WithInitDBInstanceName(instanceName string) InitDBOpt {
	return func(c *InitDBConfig) {
		c.instanceName = instanceName
	}
}

// WithInitDBExtraOptions sets extra options for the postgres client builder.
// This option is mainly used for customized postgres client builders.
func WithInitDBExtraOptions(extraOptions ...any) InitDBOpt {
	return func(c *InitDBConfig) {
		c.extraOptions = append(c.extraOptions, extraOptions...)
	}
}

// InitDB initializes the database schema with tables and indexes.
// This is a standalone function that can be used independently of the Service.
// It's useful for:
// - Manual database setup/migration
// - CI/CD pipelines
// - Initial deployment setup
//
// Note: You must import a PostgreSQL driver before calling this function:
//
//	import _ "github.com/lib/pq"
//
// Example usage:
//
//	err := postgres.InitDB(context.Background(),
//	    postgres.WithInitDBHost("localhost"),
//	    postgres.WithInitDBPort(5432),
//	    postgres.WithInitDBUser("admin"),
//	    postgres.WithInitDBPassword("secret"),
//	    postgres.WithInitDBDatabase("sessions"),
//	    postgres.WithInitDBSSLMode("disable"),
//	    postgres.WithInitDBTablePrefix("trpc_"),
//	)
//	if err != nil {
//	    panic(err)
//	}
//
// Or use registered instance:
//
//	err := postgres.InitDB(context.Background(),
//	    postgres.WithInitDBInstanceName("my-postgres"),
//	    postgres.WithInitDBTablePrefix("trpc_"),
//	)
func InitDB(ctx context.Context, opts ...InitDBOpt) error {
	config := &InitDBConfig{
		host:     defaultHost,
		port:     defaultPort,
		database: defaultDatabase,
		sslMode:  defaultSSLMode,
	}

	for _, opt := range opts {
		opt(config)
	}

	// Get postgres client builder
	builder := storage.GetClientBuilder()
	var pgClient storage.Client
	var err error

	// Priority: direct connection settings > instance name
	// If direct connection settings are provided, use them
	if config.host != "" || config.port != 0 || config.database != "" {
		serviceOpts := ServiceOpts{
			host:     config.host,
			port:     config.port,
			user:     config.user,
			password: config.password,
			database: config.database,
			sslMode:  config.sslMode,
		}
		connString := buildConnString(serviceOpts)

		pgClient, err = builder(ctx,
			storage.WithClientConnString(connString),
			storage.WithExtraOptions(config.extraOptions...),
		)
		if err != nil {
			return fmt.Errorf("create postgres client from connection settings failed: %w", err)
		}
	} else if config.instanceName != "" {
		// Otherwise, use instance name if provided
		builderOpts, ok := storage.GetPostgresInstance(config.instanceName)
		if !ok {
			return fmt.Errorf("postgres instance %s not found", config.instanceName)
		}

		// Append extra options if provided
		if len(config.extraOptions) > 0 {
			builderOpts = append(builderOpts, storage.WithExtraOptions(config.extraOptions...))
		}

		pgClient, err = builder(ctx, builderOpts...)
		if err != nil {
			return fmt.Errorf("create postgres client from instance name failed: %w", err)
		}
	} else {
		return fmt.Errorf("either connection settings or instance name must be provided")
	}
	defer pgClient.Close()

	// Create tables using shared function
	if err := createTables(ctx, pgClient, config.schema, config.tablePrefix); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Create indexes using shared function
	if err := createIndexes(ctx, pgClient, config.schema, config.tablePrefix); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
}
