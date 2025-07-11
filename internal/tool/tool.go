package tool

import (
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// GenerateJSONSchema generates a basic JSON schema from a reflect.Type.
func GenerateJSONSchema(t reflect.Type) *tool.Schema {
	schema := &tool.Schema{Type: "object"}

	// Handle different kinds of types.
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]*tool.Schema{}
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
			fieldSchema := GenerateFieldSchema(field.Type)

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
		elemSchema := GenerateFieldSchema(t.Elem())
		elemSchema.Type = elemSchema.Type + ",null"
		return elemSchema

	default:
		return GenerateFieldSchema(t)
	}

	return schema
}

// GenerateFieldSchema generates schema for a specific field type.
func GenerateFieldSchema(t reflect.Type) *tool.Schema {
	switch t.Kind() {
	case reflect.String:
		return &tool.Schema{Type: "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &tool.Schema{Type: "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &tool.Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &tool.Schema{Type: "number"}
	case reflect.Bool:
		return &tool.Schema{Type: "boolean"}
	case reflect.Slice, reflect.Array:
		return &tool.Schema{
			Type:  "array",
			Items: GenerateFieldSchema(t.Elem()),
		}
	case reflect.Map:
		return &tool.Schema{
			Type:                 "object",
			AdditionalProperties: GenerateFieldSchema(t.Elem()),
		}
	case reflect.Ptr:
		elemSchema := GenerateFieldSchema(t.Elem())
		// Pointers are nullable
		elemSchema.Type = elemSchema.Type + ",null"
		return elemSchema
	case reflect.Struct:
		nestedSchema := &tool.Schema{
			Type:       "object",
			Properties: make(map[string]*tool.Schema),
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

			nestedSchema.Properties[fieldName] = GenerateFieldSchema(field.Type)
		}

		return nestedSchema
	default:
		// Default to any type
		return &tool.Schema{Type: "object"}
	}
}
