//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlitevec provides a SQLite-backed memory service powered by
// sqlite-vec for vector similarity search.
package sqlitevec

import (
	"fmt"
	"maps"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultDBInitTimeout  = 30 * time.Second
	defaultMaxResults     = 10
	defaultIndexDimension = 1536
)

var defaultOptions = ServiceOpts{
	tableName:      defaultTableName,
	indexDimension: defaultIndexDimension,
	maxResults:     defaultMaxResults,

	memoryLimit:      imemory.DefaultMemoryLimit,
	toolCreators:     imemory.AllToolCreators,
	enabledTools:     imemory.DefaultEnabledTools,
	asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:  imemory.DefaultMemoryQueueSize,
	memoryJobTimeout: imemory.DefaultMemoryJobTimeout,
}

// ServiceOpts is the options for the sqlite-vec memory service.
type ServiceOpts struct {
	tableName      string
	indexDimension int
	maxResults     int

	memoryLimit int
	softDelete  bool

	// Embedder for generating embeddings for memories and queries.
	embedder embedder.Embedder

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	userExplicitlySet map[string]bool

	// skipDBInit skips database initialization (table creation).
	skipDBInit bool

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o

	opts.toolCreators = make(
		map[string]memory.ToolCreator,
		len(o.toolCreators),
	)
	for name, toolCreator := range o.toolCreators {
		opts.toolCreators[name] = toolCreator
	}

	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.userExplicitlySet = make(map[string]bool)

	return opts
}

// ServiceOpt is the option for the sqlite-vec memory service.
type ServiceOpt func(*ServiceOpts)

// WithTableName sets the table name for storing memories.
// Default is "memories".
//
// Security: Uses internal/session/sqldb.ValidateTableName to prevent SQL
// injection.
func WithTableName(tableName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		opts.tableName = tableName
	}
}

// WithIndexDimension sets the vector dimension for sqlite-vec table.
// If not set, it defaults to the embedder's dimension.
func WithIndexDimension(dim int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.indexDimension = dim
	}
}

// WithMaxResults sets the max number of results returned by SearchMemories.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.maxResults = maxResults
	}
}

// WithEmbedder sets the embedder for generating embeddings.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.embedder = e
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
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

// WithSkipDBInit skips database initialization (table creation).
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools, but user
// settings via WithToolEnabled (before or after) take precedence.
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

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
	}
}

// WithToolEnabled enables or disables a memory tool by name.
// User settings take precedence over auto mode defaults regardless of
// option order.
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
