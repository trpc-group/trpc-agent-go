//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "context"

// ToolSet defines an interface for managing a set of tools.
// It provides methods to retrieve the current tools and to perform cleanup.
type ToolSet interface {
	// Tools returns a slice of Tool instances available in the set based on the provided context.
	Tools(context.Context) []Tool

	// Close releases any resources held by the ToolSet.
	Close() error

	// Name returns the name of the ToolSet for identification and conflict resolution.
	Name() string
}

// ToolFilter defines a filter function for tools based on their names.
type ToolFilter func(string) bool

// FilterTools creates a new ToolSet that filters tools from the original ToolSet.
func FilterTools(toolset ToolSet, filter ToolFilter) ToolSet {
	return &filteredToolSet{
		original: toolset,
		filter:   filter,
	}
}

// filteredToolSet wraps a ToolSet to filter its tools based on their names.
type filteredToolSet struct {
	original ToolSet
	filter   ToolFilter
}

// Tools returns filtered tools from the original ToolSet.
func (f *filteredToolSet) Tools(ctx context.Context) []Tool {
	originalTools := f.original.Tools(ctx)
	if f.filter == nil {
		return originalTools
	}

	// Create new slice for filtered tools
	var result []Tool
	for _, tool := range originalTools {
		if f.filter(tool.Declaration().Name) {
			result = append(result, tool)
		}
	}
	return result
}

// Close implements the ToolSet interface.
func (f *filteredToolSet) Close() error {
	return f.original.Close()
}

// Name implements the ToolSet interface.
func (f *filteredToolSet) Name() string {
	return f.original.Name()
}

// IncludeNames creates a ToolFilter that includes only the specified tool names.
func IncludeNames(names ...string) ToolFilter {
	allowed := make(map[string]bool)
	for _, name := range names {
		allowed[name] = true
	}
	return func(name string) bool {
		return allowed[name]
	}
}

// ExcludeNames creates a ToolFilter that excludes the specified tool names.
func ExcludeNames(names ...string) ToolFilter {
	excluded := make(map[string]bool)
	for _, name := range names {
		excluded[name] = true
	}
	return func(name string) bool {
		return !excluded[name]
	}
}
