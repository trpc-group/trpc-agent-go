package tool

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NamedToolSet wraps a ToolSet to automatically prefix tool names with the toolset name.
// This prevents tool name conflicts when multiple toolsets provide tools with the same name.
type NamedToolSet struct {
	toolSet tool.ToolSet
}

// NewNamedToolSet creates a new named toolset wrapper.
// If the toolSet is already a NamedToolSet, it returns itself to avoid double-wrapping.
func NewNamedToolSet(toolSet tool.ToolSet) *NamedToolSet {
	if t, ok := toolSet.(*NamedToolSet); ok {
		return t
	}
	return &NamedToolSet{
		toolSet: toolSet,
	}
}

// Tools returns tools with names prefixed by the toolset name to avoid conflicts.
func (s *NamedToolSet) Tools(ctx context.Context) []tool.Tool {
	tools := s.toolSet.Tools(ctx)

	toolSetName := s.toolSet.Name()
	if toolSetName == "" {
		return tools
	}

	// Create tools with prefixed names to avoid conflicts
	prefixedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		prefixedTool := &NamedTool{
			original: t,
			name:     toolSetName,
		}
		prefixedTools = append(prefixedTools, prefixedTool)
	}

	return prefixedTools
}

// Close implements the ToolSet interface.
func (s *NamedToolSet) Close() error {
	return s.toolSet.Close()
}

// Name implements the ToolSet interface.
func (s *NamedToolSet) Name() string {
	return s.toolSet.Name()
}

// NamedTool wraps an original tool with a prefixed name to avoid conflicts.
type NamedTool struct {
	original tool.Tool
	name     string
}

// Declaration returns the tool declaration with a prefixed name.
func (t *NamedTool) Declaration() *tool.Declaration {
	decl := t.original.Declaration()
	name := decl.Name
	if t.name != "" {
		name = t.name + "_" + name
	}

	return &tool.Declaration{
		Name:         name,
		Description:  decl.Description,
		InputSchema:  decl.InputSchema,
		OutputSchema: decl.OutputSchema,
	}
}

// Original returns the underlying Tool instance wrapped by the NamedTool.
func (t *NamedTool) Original() tool.Tool {
	return t.original
}

// Call delegates to the original tool's Call method.
func (t *NamedTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if callable, ok := t.original.(tool.CallableTool); ok {
		return callable.Call(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not callable")
}

// StreamableCall delegates to the original tool's StreamableCall method.
func (t *NamedTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	if streamable, ok := t.original.(tool.StreamableTool); ok {
		return streamable.StreamableCall(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not streamable")
}

// SkipSummarization delegates to the original tool when it implements
// a SkipSummarization() bool preference; otherwise returns false.
func (t *NamedTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	if s, ok := t.original.(skipper); ok {
		return s.SkipSummarization()
	}
	return false
}
