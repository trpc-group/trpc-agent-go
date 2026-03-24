//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"maps"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

var (
	defaultOptions = serviceOpts{
		memoryLimit:      imemory.DefaultMemoryLimit,
		searchMinScore:   imemory.DefaultSearchMinScore,
		maxSearchResults: imemory.DefaultMaxSearchResults,
		toolCreators:     imemory.AllToolCreators,
		enabledTools:     imemory.DefaultEnabledTools,
		asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	}
)

// serviceOpts contains options for memory service.
type serviceOpts struct {
	// memoryLimit is the limit of memories per user.
	memoryLimit int
	// searchMinScore is the minimum keyword-search score.
	searchMinScore float64
	// maxSearchResults limits keyword-search results. Zero disables the cap.
	maxSearchResults int
	// toolCreators are functions to build tools after service creation.
	toolCreators map[string]memory.ToolCreator
	// enabledTools are the names of tools to enable.
	enabledTools map[string]struct{}
	// toolExposed tracks tools explicitly exposed via Tools().
	toolExposed map[string]struct{}
	// toolHidden tracks tools explicitly hidden from Tools().
	toolHidden map[string]struct{}
	// userExplicitlySet tracks which tools were explicitly overridden by user options.
	userExplicitlySet map[string]struct{}

	// Memory extractor for auto memory mode.
	// When set, write tools are hidden from agent by default unless exposed explicitly.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum   int           // Number of async workers, default 3.
	memoryQueueSize  int           // Queue size per worker, default 100.
	memoryJobTimeout time.Duration // Timeout per job, default 30s.
}

func (o serviceOpts) clone() serviceOpts {
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

// ServiceOpt is the option for the in-memory memory service.
type ServiceOpt func(*serviceOpts)

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
	}
}

// WithMinSearchScore sets the minimum keyword-search score. Scores below
// this value are filtered out. Default is 0.3.
func WithMinSearchScore(score float64) ServiceOpt {
	return func(opts *serviceOpts) {
		if score < 0 {
			return
		}
		opts.searchMinScore = score
	}
}

// WithMaxResults sets the maximum number of keyword-search results.
// Default is 10. Use 0 to disable truncation.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *serviceOpts) {
		if maxResults < 0 {
			return
		}
		opts.maxSearchResults = maxResults
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid or creator is nil, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *serviceOpts) {
		// If the tool name is invalid or creator is nil, do nothing.
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

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
// User settings via WithToolEnabled take precedence over auto mode
// defaults, regardless of option order.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		// If the tool name is invalid, do nothing.
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
	return func(opts *serviceOpts) {
		for _, toolName := range toolNames {
			WithToolExposed(toolName, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via
// Tools(). Use WithAutoMemoryExposedTools for the common auto memory case.
func WithToolExposed(toolName string, exposed bool) ServiceOpt {
	return func(opts *serviceOpts) {
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

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools,
// but user settings via WithToolEnabled (before or after) take precedence.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for memory jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for each memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryJobTimeout = timeout
	}
}
