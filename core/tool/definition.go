// Package tool provides minimal ToolDefinition for model compatibility.
package tool

// ToolDefinition represents a minimal tool definition for model compatibility.
// This is a placeholder to keep existing model interface working.
// Full implementation will be added in future PRs.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Required    []string               `json:"required_parameters,omitempty"`
}

// Property represents a tool parameter property.
// This is a placeholder for model compatibility.
type Property struct {
	Type                 string               `json:"type"`
	Description          string               `json:"description,omitempty"`
	Default              interface{}          `json:"default,omitempty"`
	Enum                 []interface{}        `json:"enum,omitempty"`
	Items                *Property            `json:"items,omitempty"`
	Properties           map[string]*Property `json:"properties,omitempty"`
	AdditionalProperties bool                 `json:"additionalProperties,omitempty"`
	Required             bool                 `json:"required,omitempty"`
}

// NewToolDefinition creates a basic tool definition.
func NewToolDefinition(name, description string) *ToolDefinition {
	return &ToolDefinition{
		Name:        name,
		Description: description,
		Parameters:  make(map[string]interface{}),
	}
}

// ToJSONSchema converts the tool definition to JSON schema format.
func (td *ToolDefinition) ToJSONSchema() map[string]interface{} {
	return td.Parameters
}

// RequiredParameters returns the list of required parameters.
func (td *ToolDefinition) RequiredParameters() []string {
	return td.Required
}
