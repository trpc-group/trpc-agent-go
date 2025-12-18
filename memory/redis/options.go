//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

var (
	defaultOptions = ServiceOpts{
		memoryLimit:    imemory.DefaultMemoryLimit,
		toolCreators:   imemory.AllToolCreators,
		enabledTools:   imemory.DefaultEnabledTools,
		asyncMemoryNum: imemory.DefaultAsyncMemoryNum,
	}
)

// ServiceOpts is the options for the redis memory service.
type ServiceOpts struct {
	url          string
	instanceName string
	memoryLimit  int

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool
	extraOptions []any

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum      int
	memoryQueueSize     int
	memoryJobTimeout    time.Duration
	maxExistingMemories int
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

// ServiceOpt is the option for the redis memory service.
type ServiceOpt func(*ServiceOpts)

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.url = url
	}
}

// WithRedisInstance uses a redis instance from storage.
// Note: WithRedisClientURL has higher priority than WithRedisInstance.
// If both are specified, WithRedisClientURL will be used.
func WithRedisInstance(instanceName string) ServiceOpt {
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

// WithExtraOptions sets the extra options for the redis session service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, write tools (add/update/delete) are not exposed to the agent.
// Read tools (load/search) are always exposed to the agent.
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

// WithMaxExistingMemories sets the maximum number of existing memories to read
// when performing auto memory extraction for deduplication.
// This is different from WithPreloadMemory (Agent layer) which controls
// how many memories are injected into the system prompt for the LLM.
// Default is 50.
func WithMaxExistingMemories(max int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if max < 1 {
			max = imemory.DefaultMaxExistingMemories
		}
		opts.maxExistingMemories = max
	}
}
