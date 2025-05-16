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
	// Deprecated: Use GetDefinition instead for newer implementations.
	Parameters() map[string]interface{}

	// GetDefinition returns the tool definition with schema.
	// If not implemented, falls back to Parameters().
	GetDefinition() *ToolDefinition
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
	definition  *ToolDefinition
}

// NewBaseTool creates a new BaseTool.
func NewBaseTool(name, description string, parameters map[string]interface{}) *BaseTool {
	return &BaseTool{
		name:        name,
		description: description,
		parameters:  parameters,
	}
}

// NewSchemaBasedTool creates a new BaseTool with a schema-based definition.
func NewSchemaBasedTool(definition *ToolDefinition) *BaseTool {
	return &BaseTool{
		name:        definition.Name,
		description: definition.Description,
		parameters:  definition.ToJSONSchema(),
		definition:  definition,
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

// GetDefinition returns the tool definition.
func (t *BaseTool) GetDefinition() *ToolDefinition {
	if t.definition != nil {
		return t.definition
	}

	// Try to convert from parameters if definition is not set
	return ToolDefinitionFromParameters(t.name, t.description, t.parameters)
}

// Execute is a placeholder that must be implemented by the concrete tool.
func (t *BaseTool) Execute(ctx context.Context, args map[string]interface{}) (*Result, error) {
	return nil, fmt.Errorf("Execute not implemented for BaseTool")
}

// ToolDefinitionFromParameters converts legacy parameters format to ToolDefinition.
func ToolDefinitionFromParameters(name, description string, params map[string]interface{}) *ToolDefinition {
	def := NewToolDefinition(name, description)

	// Extract properties from JSON Schema
	properties, _ := params["properties"].(map[string]interface{})
	if properties == nil {
		return def
	}

	// Extract required fields
	var required []string
	if req, ok := params["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required = append(required, s)
			}
		}
	} else if req, ok := params["required"].([]string); ok {
		required = req
	}

	// Create a map for quick lookup of required parameters
	requiredMap := make(map[string]bool)
	for _, r := range required {
		requiredMap[r] = true
	}

	// Add each property to the definition
	for name, prop := range properties {
		if propMap, ok := prop.(map[string]interface{}); ok {
			// Create a property from the map
			property := propertyFromMap(propMap)
			def.AddParameter(name, property, requiredMap[name])
		}
	}

	return def
}

// propertyFromMap creates a Property from a map representation.
func propertyFromMap(propMap map[string]interface{}) *Property {
	prop := &Property{}

	// Set type
	if typeStr, ok := propMap["type"].(string); ok {
		prop.Type = typeStr
	} else {
		prop.Type = "string" // Default to string
	}

	// Set description
	if desc, ok := propMap["description"].(string); ok {
		prop.Description = desc
	}

	// Set default
	if def, ok := propMap["default"]; ok {
		prop.Default = def
	}

	// Set enum values
	if enum, ok := propMap["enum"].([]interface{}); ok {
		prop.Enum = enum
	}

	// Handle array items
	if prop.Type == "array" {
		if items, ok := propMap["items"].(map[string]interface{}); ok {
			prop.Items = propertyFromMap(items)
		}
	}

	// Handle object properties
	if prop.Type == "object" {
		if nestedProps, ok := propMap["properties"].(map[string]interface{}); ok {
			prop.Properties = make(map[string]*Property)
			for k, v := range nestedProps {
				if propDef, ok := v.(map[string]interface{}); ok {
					prop.Properties[k] = propertyFromMap(propDef)
				}
			}
		}

		// Handle additionalProperties
		if addProps, ok := propMap["additionalProperties"].(bool); ok {
			prop.AdditionalProperties = addProps
		}
	}

	return prop
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

// GetToolDefinitions returns tool definitions for all tools in the set.
func (ts *ToolSet) GetToolDefinitions() []*ToolDefinition {
	defs := make([]*ToolDefinition, 0, len(ts.tools))
	for _, tool := range ts.tools {
		defs = append(defs, tool.GetDefinition())
	}
	return defs
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
