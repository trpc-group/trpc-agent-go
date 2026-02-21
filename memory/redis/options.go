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
	"maps"
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
	// keyPrefix is the prefix for all redis keys.
	// If set, all keys will be prefixed with this value
	// followed by a colon. For example, if keyPrefix is
	// "myapp", key "mem:{app:user}" becomes
	// "myapp:mem:{app:user}".
	keyPrefix string

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	userExplicitlySet map[string]bool
	extraOptions      []any

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

// WithExtraOptions sets the extra options for the redis session service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools,
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

// WithKeyPrefix sets the prefix for all redis keys.
// If set, all keys will be prefixed with this value
// followed by a colon. For example, if keyPrefix is
// "myapp", key "mem:{app:user}" becomes
// "myapp:mem:{app:user}".
func WithKeyPrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.keyPrefix = prefix
	}
}
