//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite provides the sqlite memory service.
package sqlite

import (
	"fmt"
	"maps"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const defaultDBInitTimeout = 30 * time.Second

var defaultOptions = ServiceOpts{
	tableName:        defaultTableName,
	memoryLimit:      imemory.DefaultMemoryLimit,
	searchMinScore:   imemory.DefaultSearchMinScore,
	maxSearchResults: imemory.DefaultMaxSearchResults,
	toolCreators:     imemory.AllToolCreators,
	enabledTools:     imemory.DefaultEnabledTools,
	asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:  imemory.DefaultMemoryQueueSize,
	memoryJobTimeout: imemory.DefaultMemoryJobTimeout,
}

// ServiceOpts is the options for the sqlite memory service.
type ServiceOpts struct {
	tableName   string
	memoryLimit int
	softDelete  bool
	// keyword-search settings.
	searchMinScore   float64
	maxSearchResults int

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

	// skipDBInit skips database initialization (table and index creation).
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
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)
	opts.userExplicitlySet = make(map[string]struct{})

	return opts
}

// ServiceOpt is the option for the sqlite memory service.
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

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithMinSearchScore sets the minimum keyword-search score. Scores below
// this value are filtered out. Default is 0.3.
func WithMinSearchScore(score float64) ServiceOpt {
	return func(opts *ServiceOpts) {
		if score < 0 {
			return
		}
		opts.searchMinScore = score
	}
}

// WithMaxResults sets the maximum number of keyword-search results.
// Default is 10. Use 0 to disable truncation.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if maxResults < 0 {
			return
		}
		opts.maxSearchResults = maxResults
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

// WithSkipDBInit skips database initialization (table and index creation).
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
