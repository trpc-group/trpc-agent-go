package tool

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NamedToolSet wraps a ToolSet with named prefixing to avoid tool name conflicts.
// It automatically prefixes all tool names from the wrapped toolset with the toolset name.
type NamedToolSet struct {
	toolSet tool.ToolSet
}

// NewNamedToolSet creates a new named toolset wrapper.
func NewNamedToolSet(toolSet tool.ToolSet) *NamedToolSet {
	return &NamedToolSet{
		toolSet: toolSet,
	}
}

// Tools implements the ToolSet interface.
func (s *NamedToolSet) Tools(ctx context.Context) []tool.Tool {
	tools := s.toolSet.Tools(ctx)

	toolSetName := s.toolSet.Name()
	if toolSetName == "" {
		return tools
	}

	// Create named copies of tools
	namedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		namedTool := &NamedTool{
			original: t,
			named:    toolSetName,
		}
		namedTools = append(namedTools, namedTool)
	}

	return namedTools
}

// Close implements the ToolSet interface.
func (s *NamedToolSet) Close() error {
	return s.toolSet.Close()
}

// Name implements the ToolSet interface.
func (s *NamedToolSet) Name() string {
	return s.toolSet.Name()
}

// NamedTool wraps an original tool with a named prefix.
type NamedTool struct {
	original tool.Tool
	named    string
}

// Declaration implements the Tool interface.
func (t *NamedTool) Declaration() *tool.Declaration {
	decl := t.original.Declaration()
	return &tool.Declaration{
		Name:         t.named + "_" + decl.Name,
		Description:  decl.Description,
		InputSchema:  decl.InputSchema,
		OutputSchema: decl.OutputSchema,
	}
}

// Call implements the CallableTool interface.
func (t *NamedTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if callable, ok := t.original.(tool.CallableTool); ok {
		return callable.Call(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not callable")
}

// StreamableCall implements the StreamableTool interface.
func (t *NamedTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	if streamable, ok := t.original.(tool.StreamableTool); ok {
		return streamable.StreamableCall(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not streamable")
}
