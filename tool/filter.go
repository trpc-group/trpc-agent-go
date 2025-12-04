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

// FilterFunc is a function that filters tools based on a context and a tool.
type FilterFunc func(ctx context.Context, tool Tool) bool

// FilterTools filters tools from a list of tools based on a filter function.
func FilterTools(ctx context.Context, tools []Tool, filter FilterFunc) []Tool {
	filtered := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if filter(ctx, tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// FilterToolSet creates a new ToolSet that filters tools from the original ToolSet.
func FilterToolSet(toolset ToolSet, filter FilterFunc) ToolSet {
	return &filteredToolSet{
		original: toolset,
		filter:   filter,
	}
}

// filteredToolSet wraps a ToolSet to filter its tools based on their names.
type filteredToolSet struct {
	original ToolSet
	filter   FilterFunc
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
		if f.filter(ctx, tool) {
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

// NewIncludeToolNamesFilter creates a FilterFunc that includes only the specified tool names.
func NewIncludeToolNamesFilter(names ...string) FilterFunc {
	allowedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowedNames[name] = struct{}{}
	}
	return func(ctx context.Context, tool Tool) bool {
		declaration := tool.Declaration()
		if declaration == nil {
			return false
		}

		_, isAllowed := allowedNames[declaration.Name]
		return isAllowed
	}
}

// NewExcludeToolNamesFilter creates a FilterFunc that excludes the specified tool names.
func NewExcludeToolNamesFilter(names ...string) FilterFunc {
	excludedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		excludedNames[name] = struct{}{}
	}
	return func(ctx context.Context, tool Tool) bool {
		declaration := tool.Declaration()
		if declaration == nil {
			return false
		}

		_, isExcluded := excludedNames[declaration.Name]
		return !isExcluded
	}
}
