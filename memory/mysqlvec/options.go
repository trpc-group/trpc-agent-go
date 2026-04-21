//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// Default settings.
const (
	defaultTableName      = "memories"
	defaultIndexDimension = 1536
	defaultMaxResults     = 15
	defaultDBInitTimeout  = 30 * time.Second
)

// Default similarity threshold. Results with cosine similarity below this
// are filtered out. The default of 0.30 removes very low relevance results.
const defaultSimilarityThreshold = 0.30

var defaultOptions = ServiceOpts{
	tableName:           defaultTableName,
	indexDimension:      defaultIndexDimension,
	maxResults:          defaultMaxResults,
	memoryLimit:         imemory.DefaultMemoryLimit,
	similarityThreshold: defaultSimilarityThreshold,
	toolCreators:        imemory.AllToolCreators,
	enabledTools:        imemory.DefaultEnabledTools,
	asyncMemoryNum:      imemory.DefaultAsyncMemoryNum,
}

// ServiceOpts is the options for the mysqlvec memory service.
type ServiceOpts struct {
	// MySQL connection settings.
	dsn          string
	instanceName string
	extraOptions []any

	tableName      string
	indexDimension int
	maxResults     int
	memoryLimit    int
	softDelete     bool

	// similarityThreshold filters out search results with cosine similarity
	// below this value (range 0-1). A value of 0 disables filtering.
	similarityThreshold float64

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

	// skipDBInit skips database initialization (table and index creation).
	skipDBInit bool

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
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)

	// Initialize userExplicitlySet map (empty for new clone).
	opts.userExplicitlySet = make(map[string]struct{})

	return opts
}

// ServiceOpt is the option for the mysqlvec memory service.
type ServiceOpt func(*ServiceOpts)

// WithMySQLClientDSN sets the MySQL DSN connection string directly (recommended).
// Example: "user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"
//
// Note: WithMySQLClientDSN has the highest priority.
// If DSN is specified, WithMySQLInstance will be ignored.
func WithMySQLClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithMySQLInstance uses a MySQL instance from storage.
// The instance must be registered via storage.RegisterMySQLInstance() before use.
//
// Note: WithMySQLClientDSN has higher priority than WithMySQLInstance.
func WithMySQLInstance(instanceName string) ServiceOpt {
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
		if err := validateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		opts.tableName = tableName
	}
}

// WithIndexDimension sets the vector dimension for the embedding column.
// Default is 1536.
func WithIndexDimension(dimension int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if dimension > 0 {
			opts.indexDimension = dimension
		}
	}
}

// WithMaxResults sets the maximum number of search results.
// Default is 15.
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
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		if opts.toolCreators == nil {
			opts.toolCreators = make(map[string]memory.ToolCreator)
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools via Tools() in auto memory
// mode so the agent can call them directly. Invalid tool names are ignored.
func WithAutoMemoryExposedTools(toolNames ...string) ServiceOpt {
	return func(opts *ServiceOpts) {
		for _, toolName := range toolNames {
			WithToolExposed(toolName, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via
// Tools(). Use WithAutoMemoryExposedTools for the common auto memory case.
func WithToolExposed(toolName string, exposed bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if exposed {
			if opts.toolExposed == nil {
				opts.toolExposed = make(map[string]struct{})
			}
			opts.toolExposed[toolName] = struct{}{}
			delete(opts.toolHidden, toolName)
			return
		}
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		opts.toolHidden[toolName] = struct{}{}
		delete(opts.toolExposed, toolName)
	}
}

// WithToolEnabled sets which tool is enabled.
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
			opts.userExplicitlySet = make(map[string]struct{})
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithExtraOptions sets extra options passed to the MySQL client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips database initialization (table creation).
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
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

// WithSimilarityThreshold sets the minimum cosine similarity threshold
// for search results. Results below this threshold are filtered out.
// Value should be between 0 and 1. A value of 0 disables filtering.
func WithSimilarityThreshold(threshold float64) ServiceOpt {
	return func(opts *ServiceOpts) {
		if threshold >= 0 && threshold <= 1 {
			opts.similarityThreshold = threshold
		}
	}
}

// tableNamePattern is the regex pattern for validating table names.
var tableNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// validateTableName validates the table name to prevent SQL injection.
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
