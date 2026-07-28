//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gormmemory provides a GORM-backed memory.Service implementation.
package gormmemory

import (
	"fmt"
	"maps"
	"time"

	"gorm.io/gorm"
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
	enabledTools:     maps.Clone(imemory.DefaultEnabledTools),
	asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:  imemory.DefaultMemoryQueueSize,
	memoryJobTimeout: imemory.DefaultMemoryJobTimeout,
}

// ServiceOpts configures the GORM memory service.
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

	// skipDBInit skips AutoMigrate when tables are managed externally.
	skipDBInit bool

	// GORM connection settings (priority: db > dialector > instance name).
	db           *gorm.DB
	dialector    gorm.Dialector
	instanceName string
	extraOptions []any

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
	opts.userExplicitlySet = make(map[string]struct{})

	return opts
}

// ServiceOpt configures the GORM memory service.
type ServiceOpt func(*ServiceOpts)

// WithTableName sets the table name for storing memories.
// Default is "memories".
func WithTableName(tableName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		opts.tableName = tableName
	}
}

// WithSkipDBInit skips database initialization (AutoMigrate).
// Use when DDL is owned by the host application.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithDB injects a shared *gorm.DB. The caller owns the DB lifecycle unless
// the service opened the connection via WithDialector or WithGormInstance.
func WithDB(db *gorm.DB) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.db = db
	}
}

// WithDialector opens a new GORM connection using storage/gorm.
func WithDialector(d gorm.Dialector) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dialector = d
	}
}

// WithGormInstance uses a registered storage/gorm instance by name.
func WithGormInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions passes opaque options to the storage/gorm client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSoftDelete enables or disables soft delete behavior.
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enabled
	}
}

// WithMemoryLimit sets the maximum number of memories per user.
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

// WithSearchMinScore is deprecated; use WithMinSearchScore instead.
func WithSearchMinScore(score float64) ServiceOpt {
	return WithMinSearchScore(score)
}

// WithMaxSearchResults is deprecated; use WithMaxResults instead.
func WithMaxSearchResults(max int) ServiceOpt {
	return WithMaxResults(max)
}

// WithToolEnabled sets which tool is enabled.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.userExplicitlySet[toolName] = struct{}{}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
			return
		}
		delete(opts.enabledTools, toolName)
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via Tools().
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

// WithToolHidden hides a tool from Tools() even when enabled.
func WithToolHidden(toolName string, hidden bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		if hidden {
			opts.toolHidden[toolName] = struct{}{}
			return
		}
		delete(opts.toolHidden, toolName)
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
func WithExtractor(ext extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = ext
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the async memory job queue size.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for async memory jobs.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryJobTimeout = timeout
	}
}
