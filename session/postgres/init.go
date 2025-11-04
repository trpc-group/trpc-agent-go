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
			value JSONB DEFAULT NULL,
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
			value JSONB DEFAULT NULL,
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

// buildCreateTableSQL builds CREATE TABLE SQL with table prefix.
func buildCreateTableSQL(prefix, tableName, template string) string {
	fullTableName := prefix + tableName
	return strings.ReplaceAll(template, "{{TABLE_NAME}}", fullTableName)
}

// buildIndexSQL builds CREATE INDEX SQL with table and index prefix.
func buildIndexSQL(prefix, tableName, template string) string {
	fullTableName := prefix + tableName
	// Extract original index name from template (e.g., idx_session_states_unique_active)
	// and prepend the prefix
	sql := strings.ReplaceAll(template, "{{TABLE_NAME}}", fullTableName)

	// Replace {{INDEX_NAME}} placeholder with prefixed index name
	// The index name pattern: idx_{prefix}{table}_{suffix}
	sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", "idx_"+fullTableName)

	return sql
}

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
			{"idx_session_states_expires", []string{"expires_at"}},
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
			{"idx_session_summaries_expires", []string{"expires_at"}},
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
			{"idx_app_states_expires", []string{"expires_at"}},
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
			{"idx_user_states_expires", []string{"expires_at"}},
		},
	},
}

// initDB initializes the database schema
func (s *Service) initDB(ctx context.Context) {
	// Create tables
	if err := createTables(ctx, s.pgClient, s.opts.tablePrefix); err != nil {
		panic(fmt.Sprintf("create tables failed: %v", err))
	}

	// Create indexes
	if err := createIndexes(ctx, s.pgClient, s.opts.tablePrefix); err != nil {
		panic(fmt.Sprintf("create indexes failed: %v", err))
	}

	// Verify schema
	if err := s.verifySchema(ctx); err != nil {
		panic(fmt.Sprintf("schema verification failed: %v", err))
	}
}

// createTables creates all required tables with the given prefix.
// This function can be used by both Service and standalone InitDB.
func createTables(ctx context.Context, client storage.Client, prefix string) error {
	tables := []struct {
		name     string
		template string
	}{
		{"session_states", sqlCreateSessionStatesTable},
		{"session_events", sqlCreateSessionEventsTable},
		{"session_summaries", sqlCreateSessionSummariesTable},
		{"app_states", sqlCreateAppStatesTable},
		{"user_states", sqlCreateUserStatesTable},
	}

	for _, table := range tables {
		tableSQL := buildCreateTableSQL(prefix, table.name, table.template)
		if _, err := client.ExecContext(ctx, tableSQL); err != nil {
			return fmt.Errorf("create table %s%s failed: %w", prefix, table.name, err)
		}
	}

	return nil
}

// createIndexes creates all required indexes with the given prefix.
// This function can be used by both Service and standalone InitDB.
func createIndexes(ctx context.Context, client storage.Client, prefix string) error {
	indexes := []struct {
		table    string
		template string
	}{
		// Partial unique indexes (only for non-deleted records)
		{"session_states", sqlCreateSessionStatesUniqueIndex},
		{"session_summaries", sqlCreateSessionSummariesUniqueIndex},
		{"app_states", sqlCreateAppStatesUniqueIndex},
		{"user_states", sqlCreateUserStatesUniqueIndex},
		// Lookup indexes (only session_events needs a separate lookup index)
		{"session_events", sqlCreateSessionEventsIndex},
		// TTL indexes
		{"session_states", sqlCreateSessionStatesExpiresIndex},
		{"session_events", sqlCreateSessionEventsExpiresIndex},
		{"session_summaries", sqlCreateSessionSummariesExpiresIndex},
		{"app_states", sqlCreateAppStatesExpiresIndex},
		{"user_states", sqlCreateUserStatesExpiresIndex},
	}

	for _, idx := range indexes {
		indexSQL := buildIndexSQL(prefix, idx.table, idx.template)
		if _, err := client.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index on %s%s failed: %w", prefix, idx.table, err)
		}
	}

	return nil
}

// verifySchema verifies that the database schema matches expectations
func (s *Service) verifySchema(ctx context.Context) error {
	prefix := s.opts.tablePrefix
	for tableName, schema := range expectedSchema {
		fullTableName := prefix + tableName

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

// InitDBConfig contains configuration for standalone database initialization.
type InitDBConfig struct {
	host         string
	port         int
	user         string
	password     string
	database     string
	sslMode      string
	tablePrefix  string
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
// Security: Only alphanumeric characters and underscore are allowed to prevent SQL injection.
func WithInitDBTablePrefix(prefix string) InitDBOpt {
	return func(c *InitDBConfig) {
		if err := validateTablePrefix(prefix); err != nil {
			panic(fmt.Sprintf("invalid table prefix: %v", err))
		}
		c.tablePrefix = prefix
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
	if err := createTables(ctx, pgClient, config.tablePrefix); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Create indexes using shared function
	if err := createIndexes(ctx, pgClient, config.tablePrefix); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
}
