package tool

import (
	"encoding/json"
	"fmt"
)

// ToolDefinition represents a standard OpenAI-compatible function schema.
type ToolDefinition struct {
	// Name is the name of the tool.
	Name string `json:"name"`

	// Description is a human-readable description of what the tool does.
	Description string `json:"description"`

	// Parameters is a map of parameter name to parameter schema.
	Parameters map[string]*Property `json:"parameters"`
}

// NewToolDefinition creates a new tool definition.
func NewToolDefinition(name, description string) *ToolDefinition {
	return &ToolDefinition{
		Name:        name,
		Description: description,
		Parameters:  make(map[string]*Property),
	}
}

// Property represents a parameter in a tool definition.
type Property struct {
	// Type is the JSON Schema type of the parameter.
	Type string `json:"type"`

	// Description is a human-readable description of the parameter.
	Description string `json:"description,omitempty"`

	// Default is the default value for the parameter if not provided.
	Default interface{} `json:"default,omitempty"`

	// Enum is an optional list of allowed values for the parameter.
	Enum []interface{} `json:"enum,omitempty"`

	// Required indicates whether this parameter is required.
	Required bool `json:"required,omitempty"`

	// Items is the schema for items in an array parameter.
	Items *Property `json:"items,omitempty"`

	// Properties is a map of property name to property schema for object parameters.
	Properties map[string]*Property `json:"properties,omitempty"`

	// AdditionalProperties indicates whether additional properties are allowed for objects.
	AdditionalProperties bool `json:"additionalProperties,omitempty"`
}

// NewProperty creates a new property with the given settings.
func NewProperty(typeName, description string, defaultValue interface{}, required bool) *Property {
	return &Property{
		Type:        typeName,
		Description: description,
		Default:     defaultValue,
		Required:    required,
	}
}

// NewStringProperty creates a new string property.
func NewStringProperty(description string, defaultValue interface{}, required bool) *Property {
	return NewProperty("string", description, defaultValue, required)
}

// NewNumberProperty creates a new number property.
func NewNumberProperty(description string, defaultValue interface{}, required bool) *Property {
	return NewProperty("number", description, defaultValue, required)
}

// NewIntegerProperty creates a new integer property.
func NewIntegerProperty(description string, defaultValue interface{}, required bool) *Property {
	return NewProperty("integer", description, defaultValue, required)
}

// NewBooleanProperty creates a new boolean property.
func NewBooleanProperty(description string, defaultValue interface{}, required bool) *Property {
	return NewProperty("boolean", description, defaultValue, required)
}

// NewArrayProperty creates a new array property.
func NewArrayProperty(description string, items *Property, defaultValue interface{}, required bool) *Property {
	p := NewProperty("array", description, defaultValue, required)
	p.Items = items
	return p
}

// NewObjectProperty creates a new object property.
func NewObjectProperty(description string, properties map[string]*Property, defaultValue interface{}, required bool) *Property {
	p := NewProperty("object", description, defaultValue, required)
	p.Properties = properties
	return p
}

// AddParameter adds a parameter to the tool definition.
func (d *ToolDefinition) AddParameter(name string, property *Property, required bool) {
	property.Required = required
	d.Parameters[name] = property
}

// ToJSONSchema converts the ToolDefinition to a JSON Schema map.
func (d *ToolDefinition) ToJSONSchema() map[string]interface{} {
	// OpenAI format
	schema := map[string]interface{}{
		"type":       "object",
		"properties": make(map[string]interface{}),
		"required":   []string{},
	}

	properties := schema["properties"].(map[string]interface{})
	required := schema["required"].([]string)

	for name, prop := range d.Parameters {
		properties[name] = propertyToSchemaMap(prop)
		if prop.Required {
			required = append(required, name)
		}
	}

	schema["required"] = required
	return schema
}

// propertyToSchemaMap converts a Property to a JSON Schema map.
func propertyToSchemaMap(prop *Property) map[string]interface{} {
	result := map[string]interface{}{
		"type":        prop.Type,
		"description": prop.Description,
	}

	if prop.Default != nil {
		result["default"] = prop.Default
	}

	if len(prop.Enum) > 0 {
		result["enum"] = prop.Enum
	}

	if prop.Type == "array" && prop.Items != nil {
		result["items"] = propertyToSchemaMap(prop.Items)
	}

	if prop.Type == "object" && prop.Properties != nil {
		propMap := make(map[string]interface{})
		for name, p := range prop.Properties {
			propMap[name] = propertyToSchemaMap(p)
		}
		result["properties"] = propMap

		if prop.AdditionalProperties {
			result["additionalProperties"] = true
		}

		// Add required properties
		var required []string
		for name, p := range prop.Properties {
			if p.Required {
				required = append(required, name)
			}
		}
		if len(required) > 0 {
			result["required"] = required
		}
	}

	return result
}

// ToJSON returns the tool definition as a JSON string.
func (d *ToolDefinition) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", fmt.Errorf("error marshaling tool definition: %w", err)
	}
	return string(bytes), nil
}

// String returns a string representation of the tool definition.
func (d *ToolDefinition) String() string {
	s, err := d.ToJSON()
	if err != nil {
		return fmt.Sprintf("Error marshaling tool definition: %v", err)
	}
	return s
}

// RequiredParameters returns a list of required parameter names.
func (d *ToolDefinition) RequiredParameters() []string {
	var required []string
	for name, prop := range d.Parameters {
		if prop.Required {
			required = append(required, name)
		}
	}
	return required
}
