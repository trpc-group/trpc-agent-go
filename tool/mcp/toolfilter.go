//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package mcp

import (
	"context"
	"regexp"
)

// ToolFilter defines the interface for filtering tools.
type ToolFilter interface {
	Filter(ctx context.Context, tools []ToolInfo) []ToolInfo
}

// ToolInfo contains metadata about an MCP tool.
type ToolInfo struct {
	// Name is the name of the tool.
	Name string `json:"name"`
	// Description is a description of what the tool does.
	Description string `json:"description"`
}

// ToolFilterFunc is a function type that implements ToolFilter interface.
type ToolFilterFunc func(ctx context.Context, tools []ToolInfo) []ToolInfo

// Filter implements the ToolFilter interface.
func (f ToolFilterFunc) Filter(ctx context.Context, tools []ToolInfo) []ToolInfo {
	return f(ctx, tools)
}

// ToolNameFilter filters tools by a list of allowed tool names.
type ToolNameFilter struct {
	// AllowedNames is the list of tool names to filter by.
	AllowedNames []string
	// Mode specifies whether to include or exclude the listed names.
	Mode filterMode
}

// Filter implements the ToolFilter interface.
func (f *ToolNameFilter) Filter(ctx context.Context, tools []ToolInfo) []ToolInfo {
	if len(f.AllowedNames) == 0 {
		return tools
	}

	nameSet := make(map[string]bool)
	for _, name := range f.AllowedNames {
		nameSet[name] = true
	}

	var filtered []ToolInfo
	for _, tool := range tools {
		inSet := nameSet[tool.Name]

		switch f.Mode {
		case FilterModeInclude:
			if inSet {
				filtered = append(filtered, tool)
			}
		case FilterModeExclude:
			if !inSet {
				filtered = append(filtered, tool)
			}
		default:
			// Default to include mode
			if inSet {
				filtered = append(filtered, tool)
			}
		}
	}

	return filtered
}

// CompositeFilter combines multiple filters using AND logic.
type CompositeFilter struct {
	// Filters is the list of filters to combine.
	Filters []ToolFilter
}

// Filter implements the ToolFilter interface.
func (f *CompositeFilter) Filter(ctx context.Context, tools []ToolInfo) []ToolInfo {
	result := tools
	for _, filter := range f.Filters {
		result = filter.Filter(ctx, result)
	}
	return result
}

// PatternFilter filters tools using pattern matching on names and descriptions.
type PatternFilter struct {
	// NamePatterns is the list of regex patterns to match against tool names.
	NamePatterns []string
	// DescriptionPatterns is the list of regex patterns to match against descriptions.
	DescriptionPatterns []string
	// Mode specifies whether to include or exclude matches.
	Mode filterMode
}

// Filter implements the ToolFilter interface.
func (f *PatternFilter) Filter(ctx context.Context, tools []ToolInfo) []ToolInfo {
	if len(f.NamePatterns) == 0 && len(f.DescriptionPatterns) == 0 {
		return tools
	}

	var filtered []ToolInfo
	for _, tool := range tools {
		matches := f.matchesTool(tool)

		switch f.Mode {
		case FilterModeInclude:
			if matches {
				filtered = append(filtered, tool)
			}
		case FilterModeExclude:
			if !matches {
				filtered = append(filtered, tool)
			}
		default:
			// Default to include mode.
			if matches {
				filtered = append(filtered, tool)
			}
		}
	}

	return filtered
}

// matchesTool checks if a tool matches any of the patterns.
func (f *PatternFilter) matchesTool(tool ToolInfo) bool {
	// Check name patterns.
	for _, pattern := range f.NamePatterns {
		if matched, _ := regexp.MatchString(pattern, tool.Name); matched {
			return true
		}
	}

	// Check description patterns.
	for _, pattern := range f.DescriptionPatterns {
		if matched, _ := regexp.MatchString(pattern, tool.Description); matched {
			return true
		}
	}

	return false
}

// NewIncludeFilter creates a filter that only includes specified tool names.
func NewIncludeFilter(toolNames ...string) ToolFilter {
	return &ToolNameFilter{
		AllowedNames: toolNames,
		Mode:         FilterModeInclude,
	}
}

// NewExcludeFilter creates a filter that excludes specified tool names.
func NewExcludeFilter(toolNames ...string) ToolFilter {
	return &ToolNameFilter{
		AllowedNames: toolNames,
		Mode:         FilterModeExclude,
	}
}

// NewPatternIncludeFilter creates a filter that includes tools matching name patterns.
func NewPatternIncludeFilter(namePatterns ...string) ToolFilter {
	return &PatternFilter{
		NamePatterns: namePatterns,
		Mode:         FilterModeInclude,
	}
}

// NewPatternExcludeFilter creates a filter that excludes tools matching name patterns.
func NewPatternExcludeFilter(namePatterns ...string) ToolFilter {
	return &PatternFilter{
		NamePatterns: namePatterns,
		Mode:         FilterModeExclude,
	}
}

// NewDescriptionFilter creates a filter that matches tools by description patterns.
func NewDescriptionFilter(descPatterns ...string) ToolFilter {
	return &PatternFilter{
		DescriptionPatterns: descPatterns,
		Mode:                FilterModeInclude,
	}
}

// NewCompositeFilter creates a composite filter that applies multiple filters.
func NewCompositeFilter(filters ...ToolFilter) ToolFilter {
	return &CompositeFilter{
		Filters: filters,
	}
}

// NewFuncFilter creates a filter from a function.
func NewFuncFilter(filterFunc func(ctx context.Context, tools []ToolInfo) []ToolInfo) ToolFilter {
	return ToolFilterFunc(filterFunc)
}

// NoFilter returns all tools without filtering.
var NoFilter ToolFilter = ToolFilterFunc(func(ctx context.Context, tools []ToolInfo) []ToolInfo {
	return tools
})
