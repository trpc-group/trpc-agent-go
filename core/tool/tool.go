// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"
	"reflect"
	"strings"
)

type Tool interface {
	// Declaration returns the metadata describing the tool.
	Declaration() *Declaration
}

type UnaryTool interface {
	// Call calls the tool with the provided context and arguments.
	// Returns the result of execution or an error if the operation fails.
	Call(ctx context.Context, jsonArgs []byte) (any, error)

	Tool
}

// StreamableTool defines the interface for tools that support streaming operations.
// This interface extends the basic UnaryTool interface to provide streaming capabilities,
// allowing tools to return data progressively rather than all at once.
type StreamableTool interface {
	// StreamableCall initiates a call to the tool that supports streaming.
	// It takes a context for cancellation and timeout control, and JSON-encoded
	// arguments for the tool. Returns a StreamReader for consuming the streaming
	// results or an error if the call fails to initialize.
	StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error)
	Tool
}

// Declaration describes the metadata of a tool, such as its name, description, and expected arguments.
type Declaration struct {
	// Name is the unique identifier of the tool
	Name string `json:"name"`

	// Description explains the tool's purpose and functionality
	Description string `json:"description"`

	// InputSchema defines the expected input for the tool in JSON schema format.
	InputSchema *Schema `json:"inputSchema"`

	// OutputSchema defines the expected output for the tool in JSON schema format.
	OutputSchema *Schema `json:"outputSchema,omitempty"`
}

// Schema represents the structure of JSON Schema used for defining arguments and responses.
// It follows the JSON Schema standard, supporting various types, properties, and validation rules.
// This structure is typically used to define the expected format of arguments for tools or functions
// and to validate that incoming data conforms to the expected structure.
type Schema struct {
	//  Type Specifies the data type (e.g., "object", "array", "string", "number")
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    []string `json:"required,omitempty"`
	// Properties of the arguments, each with its own schema
	Properties map[string]*Schema `json:"properties,omitempty"`
	// For array types, defines the schema of items in the array
	Items *Schema `json:"items,omitempty"`
	// AdditionalProperties: Controls whether properties not defined in Properties are allowed
	AdditionalProperties any `json:"additionalProperties,omitempty"`
}

// generateJSONSchema generates a basic JSON schema from a reflect.Type.
func generateJSONSchema(t reflect.Type) *Schema {
	schema := &Schema{Type: "object"}

	// Handle different kinds of types.
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]*Schema{}
		required := make([]string, 0)

		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			// Get JSON tag or use field name.
			jsonTag := field.Tag.Get("json")
			if jsonTag == "-" {
				continue // Skip fields marked with json:"-"
			}

			fieldName := field.Name
			isOmitEmpty := false

			if jsonTag != "" {
				// Parse json tag (handle omitempty, etc.)
				if commaIdx := strings.Index(jsonTag, ","); commaIdx != -1 {
					fieldName = jsonTag[:commaIdx]
					isOmitEmpty = strings.Contains(jsonTag[commaIdx:], "omitempty")
				} else {
					fieldName = jsonTag
				}
			}

			// Generate schema for field type.
			fieldSchema := generateFieldSchema(field.Type)

			properties[fieldName] = fieldSchema

			// Check if field is required (not a pointer and no omitempty).
			if field.Type.Kind() != reflect.Ptr && !isOmitEmpty {
				required = append(required, fieldName)
			}
		}

		schema.Properties = properties
		if len(required) > 0 {
			schema.Required = required
		}

	case reflect.Ptr:
		elemSchema := generateFieldSchema(t.Elem())
		elemSchema.Type = elemSchema.Type + ",null"
		return elemSchema

	default:
		return generateFieldSchema(t)
	}

	return schema
}

// generateFieldSchema generates schema for a specific field type.
func generateFieldSchema(t reflect.Type) *Schema {
	switch t.Kind() {
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &Schema{Type: "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Slice, reflect.Array:
		return &Schema{
			Type:  "array",
			Items: generateFieldSchema(t.Elem()),
		}
	case reflect.Map:
		return &Schema{
			Type:                 "object",
			AdditionalProperties: generateFieldSchema(t.Elem()),
		}
	case reflect.Ptr:
		elemSchema := generateFieldSchema(t.Elem())
		// Pointers are nullable
		elemSchema.Type = elemSchema.Type + ",null"
		return elemSchema
	case reflect.Struct:
		nestedSchema := &Schema{
			Type:       "object",
			Properties: make(map[string]*Schema),
		}

		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			jsonTag := field.Tag.Get("json")
			if jsonTag == "-" {
				continue
			}

			fieldName := field.Name
			if jsonTag != "" {
				if commaIdx := strings.Index(jsonTag, ","); commaIdx != -1 {
					fieldName = jsonTag[:commaIdx]
				} else {
					fieldName = jsonTag
				}
			}

			nestedSchema.Properties[fieldName] = generateFieldSchema(field.Type)
		}

		return nestedSchema
	default:
		// Default to any type
		return &Schema{Type: "object"}
	}
}
