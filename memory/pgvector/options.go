//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"maps"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// Default connection settings.
const (
	defaultHost     = "localhost"
	defaultPort     = 5432
	defaultDatabase = "trpc-agent-go-pgmemory"
	defaultSSLMode  = "disable"
)

// Default table and index settings.
const (
	defaultTableName      = "memories"
	defaultIndexDimension = 1536
	defaultMaxResults     = 10
)

// Default HNSW index parameters.
const (
	defaultHNSWM              = 16
	defaultHNSWEfConstruction = 64
)

// Default timeout settings.
const (
	defaultDBInitTimeout = 30 * time.Second
)

// HNSWIndexParams contains parameters for HNSW index.
type HNSWIndexParams struct {
	// M is the maximum number of connections per layer (default: 16, range: 2-100).
	M int
	// EfConstruction is the size of dynamic candidate list for construction.
	// Default: 64, range: 4-1000.
	EfConstruction int
}

var defaultOptions = ServiceOpts{
	tableName:      defaultTableName,
	indexDimension: defaultIndexDimension,
	maxResults:     defaultMaxResults,
	memoryLimit:    imemory.DefaultMemoryLimit,
	toolCreators:   imemory.AllToolCreators,
	enabledTools:   imemory.DefaultEnabledTools,
	asyncMemoryNum: imemory.DefaultAsyncMemoryNum,
	hnswParams: &HNSWIndexParams{
		M:              defaultHNSWM,
		EfConstruction: defaultHNSWEfConstruction,
	},
}

// ServiceOpts is the options for the pgvector memory service.
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

	tableName      string
	indexDimension int
	maxResults     int
	memoryLimit    int
	softDelete     bool

	// Vector index configuration.
	hnswParams *HNSWIndexParams

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	userExplicitlySet map[string]bool

	// skipDBInit skips database initialization (table and index creation).
	// Useful when user doesn't have DDL permissions or when tables are managed
	// externally.
	skipDBInit bool

	// schema is the PostgreSQL schema name where tables are created.
	// Default is empty string (uses default schema, typically "public").
	schema string

	// Embedder for generating memory embeddings.
	embedder embedder.Embedder

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

	opts.enabledTools = maps.Clone(o.enabledTools)

	// Initialize userExplicitlySet map (empty for new clone).
	opts.userExplicitlySet = make(map[string]bool)

	// Clone HNSW params if present.
	if o.hnswParams != nil {
		opts.hnswParams = &HNSWIndexParams{
			M:              o.hnswParams.M,
			EfConstruction: o.hnswParams.EfConstruction,
		}
	}

	return opts
}

// ServiceOpt is the option for the pgvector memory service.
type ServiceOpt func(*ServiceOpts)

// WithPGVectorClientDSN sets the PostgreSQL DSN connection string directly.
// (recommended).
// Example: "postgres://user:password@localhost:5432/dbname?sslmode=disable".
//
// Note: WithPGVectorClientDSN has the highest priority.
// If DSN is specified, other connection settings (WithHost, WithPort, etc.).
// will be ignored.
func WithPGVectorClientDSN(dsn string) ServiceOpt {
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
// Note: Direct connection settings (WithHost, WithPort, etc.) have higher.
// priority than WithPostgresInstance.
// If both are specified, direct connection settings will be used.
func WithPostgresInstance(instanceName string) ServiceOpt {
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

// WithIndexDimension sets the vector dimension for the index.
// Default is 1536.
func WithIndexDimension(dimension int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if dimension > 0 {
			opts.indexDimension = dimension
		}
	}
}

// WithMaxResults sets the maximum number of search results.
// Default is 10.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if maxResults > 0 {
			opts.maxResults = maxResults
		}
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
		opts.enabledTools[toolName] = struct{}{}
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
// User settings via WithToolEnabled take precedence over auto mode
// defaults, regardless of option order.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]bool)
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		opts.userExplicitlySet[toolName] = true
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
// Useful when.
// - User doesn't have DDL permissions.
// - Tables are managed by migration tools.
// - Running in production environment where schema is pre-created.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithSchema sets the PostgreSQL schema name where tables will be created.
// If not set, tables will be created in the default schema (typically "public").
//
// Note: The schema must already exist in the database before using this option.
func WithSchema(schema string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if schema != "" {
			sqldb.MustValidateTableName(schema)
		}
		opts.schema = schema
	}
}

// WithEmbedder sets the embedder for generating memory embeddings.
// This is required for vector-based memory search.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.embedder = e
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

// WithHNSWIndexParams sets HNSW index parameters.
func WithHNSWIndexParams(params *HNSWIndexParams) ServiceOpt {
	return func(opts *ServiceOpts) {
		if params != nil {
			opts.hnswParams = params
		}
	}
}
