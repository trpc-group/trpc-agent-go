//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides internal utilities for tool schema generation and
// management in the trpc-agent-go framework.
package tool

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
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

			// Parse jsonschema tag to customize the schema
			isRequiredByTag, err := parseJSONSchemaTag(field.Type, field.Tag, fieldSchema)
			if err != nil {
				log.Errorf("parseJSONSchemaTag error for field %s: %v", fieldName, err)
				// Continue execution with the field schema as is
			}

			properties[fieldName] = fieldSchema

			// Check if field is required (not a pointer and no omitempty, or explicitly marked as required by jsonschema tag).
			if (field.Type.Kind() != reflect.Ptr && !isOmitEmpty) || isRequiredByTag {
				required = append(required, fieldName)
			}
		}

		schema.Properties = properties
		if len(required) > 0 {
			schema.Required = required
		}

	case reflect.Ptr:
		// For function tool parameters, we typically use value types
		// So we can just return the element type schema.
		return GenerateFieldSchema(t.Elem())

	default:
		return GenerateFieldSchema(t)
	}

	return schema
}

// parseJSONSchemaTag parses jsonschema struct tag and applies the settings to the schema.
// Supported struct tags:
// 1. jsonschema: "description=xxx"
// 2. jsonschema: "enum=xxx,enum=yyy", or "enum=1,enum=2", or "enum=3.14,enum=3.15", etc.
// NOTE: will convert actual enum value such as "1" or "3.14" to actual field type defined in struct.
// NOTE: enum only supports string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool.
// 3. jsonschema: "required"
func parseJSONSchemaTag(fieldType reflect.Type, tag reflect.StructTag, schema *tool.Schema) (bool, error) {
	jsonSchemaTag := tag.Get("jsonschema")
	if len(jsonSchemaTag) == 0 {
		return false, nil
	}

	isRequiredByTag := false
	tags := strings.Split(jsonSchemaTag, ",")
	for _, tagItem := range tags {
		kv := strings.Split(tagItem, "=")
		if len(kv) == 2 {
			key, value := kv[0], kv[1]
			if key == "description" {
				schema.Description = value
			} else if key == "enum" {
				if schema.Enum == nil {
					schema.Enum = make([]any, 0)
				}

				switch fieldType.Kind() {
				case reflect.String:
					schema.Enum = append(schema.Enum, value)
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					v, err := strconv.ParseInt(value, 10, 64)
					if err != nil {
						return false, fmt.Errorf("parse enum value %v to int64 failed: %w", value, err)
					}
					schema.Enum = append(schema.Enum, v)
				case reflect.Float32, reflect.Float64:
					v, err := strconv.ParseFloat(value, 64)
					if err != nil {
						return false, fmt.Errorf("parse enum value %v to float64 failed: %w", value, err)
					}
					schema.Enum = append(schema.Enum, v)
				case reflect.Bool:
					v, err := strconv.ParseBool(value)
					if err != nil {
						return false, fmt.Errorf("parse enum value %v to bool failed: %w", value, err)
					}
					schema.Enum = append(schema.Enum, v)
				default:
					return false, fmt.Errorf("enum tag unsupported for field type: %v", fieldType)
				}
			}
		} else if len(kv) == 1 {
			key := kv[0]
			if key == "required" {
				isRequiredByTag = true
			}
		}
	}

	return isRequiredByTag, nil
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
		// For function tool parameters, we typically use value types
		// So we can just return the element type schema
		return GenerateFieldSchema(t.Elem())
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
