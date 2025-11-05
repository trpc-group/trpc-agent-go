package tool

import "context"

// ToolFilter defines a filter function for tools based on their names.
type FilterFunc func(ctx context.Context, tool Tool) bool

// FilterTools creates a new ToolSet that filters tools from the original ToolSet.
func FilterTools(ctx context.Context, toolset []Tool, filter FilterFunc) []Tool {
	filtered := make([]Tool, 0, len(toolset))
	for _, tool := range toolset {
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
		_, isAllowed := allowedNames[tool.Declaration().Name]
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
		_, isExcluded := excludedNames[tool.Declaration().Name]
		return !isExcluded
	}
}
