// Package tool provides interfaces and implementations for tools that agents can use.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the name of the tool.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// Execute runs the tool with the given arguments.
	Execute(ctx context.Context, args map[string]interface{}) (*Result, error)

	// Parameters returns the JSON Schema describing the tool's parameters.
	Parameters() map[string]interface{}
}

// Result represents the result of a tool execution.
type Result struct {
	// Output is the raw output from the tool.
	Output interface{} `json:"output"`

	// ContentType is the MIME type of the output (e.g., "text/plain", "application/json").
	ContentType string `json:"content_type,omitempty"`

	// Metadata contains additional information about the result.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// NewResult creates a new Result with the given output.
func NewResult(output interface{}) *Result {
	return &Result{
		Output:      output,
		ContentType: "text/plain",
		Metadata:    make(map[string]interface{}),
	}
}

// NewJSONResult creates a new Result with JSON content type.
func NewJSONResult(output interface{}) *Result {
	return &Result{
		Output:      output,
		ContentType: "application/json",
		Metadata:    make(map[string]interface{}),
	}
}

// String returns a string representation of the Result.
func (r *Result) String() string {
	switch v := r.Output.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		data, err := json.Marshal(r.Output)
		if err != nil {
			return fmt.Sprintf("Error marshaling result: %v", err)
		}
		return string(data)
	}
}

// BaseTool provides a basic implementation of the Tool interface.
// It can be embedded in other tool implementations to reduce boilerplate.
type BaseTool struct {
	name        string
	description string
	parameters  map[string]interface{}
}

// NewBaseTool creates a new BaseTool.
func NewBaseTool(name, description string, parameters map[string]interface{}) *BaseTool {
	return &BaseTool{
		name:        name,
		description: description,
		parameters:  parameters,
	}
}

// Name returns the name of the tool.
func (t *BaseTool) Name() string {
	return t.name
}

// Description returns a description of what the tool does.
func (t *BaseTool) Description() string {
	return t.description
}

// Parameters returns the JSON Schema describing the tool's parameters.
func (t *BaseTool) Parameters() map[string]interface{} {
	return t.parameters
}

// Execute is a placeholder that must be implemented by the concrete tool.
func (t *BaseTool) Execute(ctx context.Context, args map[string]interface{}) (*Result, error) {
	return nil, fmt.Errorf("Execute not implemented for BaseTool")
}

// ToolSet is a collection of tools identified by name.
type ToolSet struct {
	tools map[string]Tool
}

// NewToolSet creates a new empty tool set.
func NewToolSet() *ToolSet {
	return &ToolSet{
		tools: make(map[string]Tool),
	}
}

// Add adds a tool to the set.
func (ts *ToolSet) Add(tool Tool) error {
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	if _, exists := ts.tools[name]; exists {
		return fmt.Errorf("tool with name %s already exists", name)
	}
	ts.tools[name] = tool
	return nil
}

// Get returns a tool by name.
func (ts *ToolSet) Get(name string) (Tool, bool) {
	tool, exists := ts.tools[name]
	return tool, exists
}

// Remove removes a tool from the set.
func (ts *ToolSet) Remove(name string) {
	delete(ts.tools, name)
}

// List returns a list of all tools in the set.
func (ts *ToolSet) List() []Tool {
	tools := make([]Tool, 0, len(ts.tools))
	for _, tool := range ts.tools {
		tools = append(tools, tool)
	}
	return tools
}

// Names returns a list of all tool names in the set.
func (ts *ToolSet) Names() []string {
	names := make([]string, 0, len(ts.tools))
	for name := range ts.tools {
		names = append(names, name)
	}
	return names
}

// Size returns the number of tools in the set.
func (ts *ToolSet) Size() int {
	return len(ts.tools)
} 