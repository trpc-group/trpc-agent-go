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
	"errors"
	"fmt"
	"regexp"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
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

// ServiceOpts is the options for the mysql memory service.
type ServiceOpts struct {
	dsn          string
	instanceName string
	memoryLimit  int
	tableName    string
	softDelete   bool

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool
	extraOptions []any

	// skipDBInit skips database initialization (table creation).
	// Useful when user doesn't have DDL permissions or when tables are managed externally.
	skipDBInit bool
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

// ServiceOpt is the option for the mysql memory service.
type ServiceOpt func(*ServiceOpts)

// WithMySQLClientDSN sets the MySQL DSN connection string directly (recommended).
// Example: "user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"
//
// This is the preferred way to connect to MySQL as it:
// - Simplifies configuration (all connection params in one string)
// - Supports all MySQL connection parameters
// - Is consistent with session/mysql and storage/mysql
func WithMySQLClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithMySQLInstance uses a MySQL instance from storage.
// The instance must be registered via storage.RegisterMySQLInstance() before use.
//
// Note: WithMySQLClientDSN has higher priority than WithMySQLInstance.
// If both are specified, DSN will be used.
func WithMySQLInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithTableName sets the table name for storing memories.
// Default is "memories".
// Table name must start with a letter or underscore and contain only alphanumeric characters and underscores.
// Maximum length is 64 characters.
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

// WithExtraOptions sets the extra options for the MySQL memory service.
// These options will be passed to the MySQL client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips database initialization (table creation).
// Useful when:
// - User doesn't have DDL permissions
// - Tables are managed by migration tools
// - Running in production environment where schema is pre-created
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
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
// - Not exceed 64 characters (MySQL limit).
func validateTableName(tableName string) error {
	if tableName == "" {
		return errors.New("table name cannot be empty")
	}
	const maxTableNameLength = 64
	if len(tableName) > maxTableNameLength {
		return fmt.Errorf("table name too long: %d characters (max %d)", len(tableName), maxTableNameLength)
	}
	if !tableNamePattern.MatchString(tableName) {
		return fmt.Errorf("invalid table name: %s (must start with letter/underscore and contain only alphanumeric characters and underscores)", tableName)
	}
	return nil
}
