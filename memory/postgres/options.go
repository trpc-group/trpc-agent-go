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
	"errors"
	"fmt"
	"regexp"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultHost     = "localhost"
	defaultPort     = 5432
	defaultDatabase = "trpc-agent-go-pgmemory"
	defaultSSLMode  = "disable"
)

const (
	// defaultDBInitTimeout is the default timeout for database initialization.
	defaultDBInitTimeout = 30 * time.Second
)

var (
	defaultOptions = ServiceOpts{
		tableName:    "memories",
		memoryLimit:  imemory.DefaultMemoryLimit,
		toolCreators: imemory.AllToolCreators,
		enabledTools: imemory.DefaultEnabledTools,
	}
)

// ServiceOpts is the options for the postgres memory service.
type ServiceOpts struct {
	// PostgreSQL connection settings.
	dsn      string
	host     string
	port     int
	user     string
	password string
	database string
	sslMode  string

	instanceName string
	extraOptions []any

	tableName   string
	memoryLimit int
	softDelete  bool

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool

	// skipDBInit skips database initialization (table and index creation).
	// Useful when user doesn't have DDL permissions or when tables are managed externally.
	skipDBInit bool

	// schema is the PostgreSQL schema name where tables are created.
	// Default is empty string (uses default schema, typically "public").
	schema string
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o

	opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
	for name, toolCreator := range o.toolCreators {
		opts.toolCreators[name] = toolCreator
	}

	opts.enabledTools = make(map[string]bool, len(o.enabledTools))
	for name, enabled := range o.enabledTools {
		opts.enabledTools[name] = enabled
	}

	return opts
}

// ServiceOpt is the option for the postgres memory service.
type ServiceOpt func(*ServiceOpts)

// WithPostgresClientDSN sets the PostgreSQL DSN connection string directly (recommended).
// Example: "postgres://user:password@localhost:5432/dbname?sslmode=disable"
//
// Note: WithPostgresClientDSN has the highest priority.
// If DSN is specified, other connection settings (WithHost, WithPort, etc.) will be ignored.
func WithPostgresClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithHost sets the PostgreSQL host.
func WithHost(host string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.host = host
	}
}

// WithPort sets the PostgreSQL port.
func WithPort(port int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.port = port
	}
}

// WithUser sets the username for authentication.
func WithUser(user string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.user = user
	}
}

// WithPassword sets the password for authentication.
func WithPassword(password string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.password = password
	}
}

// WithDatabase sets the database name.
func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.database = database
	}
}

// WithSSLMode sets the SSL mode for connection.
func WithSSLMode(sslMode string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sslMode = sslMode
	}
}

// WithPostgresInstance uses a postgres instance from storage.
// Note: Direct connection settings (WithHost, WithPort, etc.) have higher priority than WithPostgresInstance.
// If both are specified, direct connection settings will be used.
func WithPostgresInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithTableName sets the table name for storing memories.
// Default is "memories".
// Table name must start with a letter or underscore and contain only alphanumeric characters and underscores.
// Maximum length is 63 characters (PostgreSQL limit).
// Panics if the table name is invalid.
func WithTableName(tableName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if err := validateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		opts.tableName = tableName
	}
}

// WithSoftDelete enables or disables soft delete behavior.
// When enabled, delete operations set deleted_at and queries filter deleted rows.
// Default is disabled (hard delete).
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enabled
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid or creator is nil, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = true
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.enabledTools[toolName] = enabled
	}
}

// WithExtraOptions sets the extra options for the postgres memory service.
// These options will be passed to the PostgreSQL client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips database initialization (table and index creation).
// Useful when:
// - User doesn't have DDL permissions
// - Tables are managed by migration tools
// - Running in production environment where schema is pre-created
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithSchema sets the PostgreSQL schema name where tables will be created.
// If not set, tables will be created in the default schema (typically "public").
// For example, with schema "my_schema", tables will be qualified as:
// - my_schema.memories
//
// Note: The schema must already exist in the database before using this option.
// Security: Uses internal/session/sqldb.ValidateTableName to prevent SQL injection.
func WithSchema(schema string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if schema != "" {
			// Use internal/session/sqldb validation
			sqldb.MustValidateTableName(schema)
		}
		opts.schema = schema
	}
}

// tableNamePattern is the regex pattern for validating table names.
// Only allows alphanumeric characters and underscores, must start with a letter or underscore.
var tableNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// validateTableName validates the table name to prevent SQL injection.
// Table name must:
// - Start with a letter or underscore.
// - Contain only alphanumeric characters and underscores.
// - Not be empty.
// - Not exceed 63 characters (PostgreSQL limit).
func validateTableName(tableName string) error {
	if tableName == "" {
		return errors.New("table name cannot be empty")
	}
	const maxTableNameLength = 63
	if len(tableName) > maxTableNameLength {
		return fmt.Errorf("table name too long: %d characters (max %d)", len(tableName), maxTableNameLength)
	}
	if !tableNamePattern.MatchString(tableName) {
		return fmt.Errorf("invalid table name: %s (must start with letter/underscore and contain only alphanumeric characters and underscores)", tableName)
	}
	return nil
}
