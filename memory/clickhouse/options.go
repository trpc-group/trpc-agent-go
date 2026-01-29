//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// Default connection settings.
const (
	defaultTableName = "memories"
)

// Default timeout settings.
const (
	defaultDBInitTimeout = 30 * time.Second
)

var defaultOptions = ServiceOpts{
	tableName:    defaultTableName,
	memoryLimit:  imemory.DefaultMemoryLimit,
	toolCreators: imemory.AllToolCreators,
	enabledTools: imemory.DefaultEnabledTools,
}

// ServiceOpts is the options for the ClickHouse memory service.
type ServiceOpts struct {
	// ClickHouse connection settings.
	dsn          string
	instanceName string
	extraOptions []any

	tableName   string
	memoryLimit int
	softDelete  bool

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]bool
	userExplicitlySet map[string]bool

	// skipDBInit skips database initialization (table creation).
	// Useful when user doesn't have DDL permissions or when tables are managed
	// externally.
	skipDBInit bool

	// tablePrefix is the prefix for table names.
	tablePrefix string

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
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

	// Initialize userExplicitlySet map (empty for new clone).
	opts.userExplicitlySet = make(map[string]bool)

	return opts
}

// ServiceOpt is the option for the ClickHouse memory service.
type ServiceOpt func(*ServiceOpts)

// WithClickHouseDSN sets the ClickHouse DSN connection string directly
// (recommended).
// Example: "clickhouse://user:password@localhost:9000/dbname".
//
// Note: WithClickHouseDSN has the highest priority.
// If DSN is specified, instance name will be ignored.
func WithClickHouseDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithClickHouseInstance uses a ClickHouse instance from storage.
// Note: DSN has higher priority than WithClickHouseInstance.
// If both are specified, DSN will be used.
func WithClickHouseInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithTableName sets the table name for storing memories.
// Default is "memories".
//
// Panics if the table name is invalid.
func WithTableName(tableName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		sqldb.MustValidateTableName(tableName)
		opts.tableName = tableName
	}
}

// WithSoftDelete enables or disables soft delete behavior.
// When enabled, delete operations set deleted_at and queries filter deleted
// rows. Default is disabled (hard delete).
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
// User settings via WithToolEnabled take precedence over auto mode defaults.
// regardless of option order.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]bool)
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]bool)
		}
		opts.enabledTools[toolName] = enabled
		opts.userExplicitlySet[toolName] = true
	}
}

// WithExtraOptions sets the extra options for the ClickHouse memory service.
// These options will be passed to the ClickHouse client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips database initialization (table creation).
// Useful when.
// - User doesn't have DDL permissions.
// - Tables are managed by migration tools.
// - Running in production environment where schema is pre-created.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
// For example, with prefix "trpc", tables will be named "trpc_memories".
//
// Note: An underscore will be automatically added if not present.
// "trpc" and "trpc_" both result in "trpc_" prefix.
//
// Security: Uses internal/session/sqldb.ValidateTablePrefix to prevent SQL
// injection.
func WithTablePrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if prefix == "" {
			opts.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		opts.tablePrefix = prefix
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools.
// but user settings via WithToolEnabled (before or after) take precedence.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for memory jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for each memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryJobTimeout = timeout
	}
}
