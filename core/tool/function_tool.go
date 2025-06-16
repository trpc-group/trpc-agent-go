// Package tool provides tool implementations for the agent system.
package tool

import "context"

// FunctionTool provides a basic implementation of BaseTool.
// This is the primary tool type for creating custom tools.
type FunctionTool struct {
	name        string
	description string
	schema      map[string]interface{}
	function    func(context.Context, map[string]interface{}) (string, error)
}

// NewFunctionTool creates a new function tool with the specified name, description, schema, and function.
func NewFunctionTool(name, description string, schema map[string]interface{},
	fn func(context.Context, map[string]interface{}) (string, error)) *FunctionTool {
	return &FunctionTool{
		name:        name,
		description: description,
		schema:      schema,
		function:    fn,
	}
}

// Name returns the name of the tool.
func (ft *FunctionTool) Name() string {
	return ft.name
}

// Description returns a description of what the tool does.
func (ft *FunctionTool) Description() string {
	return ft.description
}

// InputSchema returns the JSON Schema describing the tool's parameters.
func (ft *FunctionTool) InputSchema() map[string]interface{} {
	return ft.schema
}

// Run executes the tool with the given input parameters.
func (ft *FunctionTool) Run(ctx context.Context, toolInput map[string]interface{}) (string, error) {
	return ft.function(ctx, toolInput)
}
