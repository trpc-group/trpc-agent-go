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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

var (
	defaultOptions = serviceOpts{
		memoryLimit:  imemory.DefaultMemoryLimit,
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}
)

func init() {
	// Copy all tool creators.
	for name, creator := range imemory.AllToolCreators {
		defaultOptions.toolCreators[name] = creator
	}
	// Enable default tools.
	for name, enabled := range imemory.DefaultEnabledTools {
		defaultOptions.enabledTools[name] = enabled
	}
}

// serviceOpts contains options for memory service.
type serviceOpts struct {
	// memoryLimit is the limit of memories per user.
	memoryLimit int
	// toolCreators are functions to build tools after service creation.
	toolCreators map[string]memory.ToolCreator
	// enabledTools are the names of tools to enable.
	enabledTools map[string]bool
}

// ServiceOpt is the option for the in-memory memory service.
type ServiceOpt func(*serviceOpts)

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
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
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = true
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		// If the tool name is invalid, do nothing.
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.enabledTools[toolName] = enabled
	}
}
