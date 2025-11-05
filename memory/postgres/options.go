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

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// ServiceOpts is the options for the postgres memory service.
type ServiceOpts struct {
	connString   string
	instanceName string
	tableName    string
	memoryLimit  int
	softDelete   bool

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool
	extraOptions []any
}

// ServiceOpt is the option for the postgres memory service.
type ServiceOpt func(*ServiceOpts)

// WithPostgresConnString creates a postgres client from connection string and sets it to the service.
func WithPostgresConnString(connString string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.connString = connString
	}
}

// WithPostgresInstance uses a postgres instance from storage.
// Note: WithPostgresConnString has higher priority than WithPostgresInstance.
// If both are specified, WithPostgresConnString will be used.
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
// If the tool name is invalid, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
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

// WithExtraOptions sets the extra options for the postgres session service.
// this option mainly used for the customized postgres client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
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
