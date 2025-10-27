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
	// Use a context to track visited types and handle recursion
	ctx := &schemaContext{
		visited: make(map[reflect.Type]string),
		defs:    make(map[string]*tool.Schema),
	}

	schema := generateJSONSchema(t, ctx, true)

	// Add $defs to the root schema if we have any definitions
	if len(ctx.defs) > 0 {
		schema.Defs = ctx.defs
	}

	return schema
}

// schemaContext tracks the state during schema generation to handle recursion
type schemaContext struct {
	visited map[reflect.Type]string // Maps types to their definition names
	defs    map[string]*tool.Schema // Stores reusable schema definitions
}

// generateJSONSchema generates a JSON schema with recursion handling
func generateJSONSchema(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	// Handle different kinds of types.
	switch t.Kind() {
	case reflect.Struct:
		return handleGenerateJSONSchemaStruct(t, ctx, isRoot)

	case reflect.Ptr:
		// For function tool parameters, we typically use value types
		// So we can just return the element type schema.
		return generateFieldSchema(t.Elem(), ctx, isRoot)

	default:
		return generateFieldSchema(t, ctx, isRoot)
	}
}

// hasRecursiveFields checks if a struct type has fields that reference itself
func hasRecursiveFields(t reflect.Type) bool {
	return checkRecursion(t, t, make(map[reflect.Type]bool))
}

// handleGenerateJSONSchemaStruct contains the previously large struct-handling
// logic extracted from generateJSONSchema to reduce cyclomatic complexity.
func handleGenerateJSONSchemaStruct(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	// Check if we've already seen this struct type
	if defName, exists := ctx.visited[t]; exists {
		// Return a reference to the existing definition
		return &tool.Schema{Ref: "#/$defs/" + defName}
	}

	// Generate a unique name for this type and mark it visited
	defName := generateDefName(t)
	ctx.visited[t] = defName

	// Create the schema for this struct
	schema := &tool.Schema{Type: "object"}
	properties := map[string]*tool.Schema{}
	required := make([]string, 0)

	// Check if this struct has recursive fields
	hasRecursion := hasRecursiveFields(t)

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
		fieldSchema := generateFieldSchema(field.Type, ctx, false)
		properties[fieldName] = fieldSchema

		// Parse jsonschema tag to customize the schema
		// Only apply jsonschema tags if the field schema is not a reference
		if fieldSchema.Ref == "" {
			isRequiredByTag, err := parseJSONSchemaTag(field.Type, field.Tag, fieldSchema)
			if err != nil {
				log.Errorf("parseJSONSchemaTag error for field %s: %v", fieldName, err)
				// Continue execution with the field schema as is
			}

			// Check if field is required (not a pointer and no omitempty, or explicitly marked as required by jsonschema tag).
			if (field.Type.Kind() != reflect.Ptr && !isOmitEmpty) || isRequiredByTag {
				required = append(required, fieldName)
			}
		} else {
			// For reference fields, check if they should be required based on type and omitempty
			if field.Type.Kind() != reflect.Ptr && !isOmitEmpty {
				required = append(required, fieldName)
			}
		}
	}

	schema.Properties = properties
	if len(required) > 0 {
		schema.Required = required
	}

	// Store the definition if we have recursion or if it's not the root
	if hasRecursion || !isRoot {
		// Create a copy of the schema for the definition to avoid circular references
		defSchema := &tool.Schema{
			Type:       schema.Type,
			Properties: make(map[string]*tool.Schema),
			Required:   schema.Required,
		}

		// Copy properties but ensure we use references for recursive types
		for propName, propSchema := range schema.Properties {
			defSchema.Properties[propName] = propSchema
		}

		ctx.defs[defName] = defSchema
	}

	// For the root type with recursion, return the actual schema
	// For nested recursive types, return a reference
	if isRoot {
		return schema
	}

	return &tool.Schema{Ref: "#/$defs/" + defName}
}

// checkRecursion recursively checks if targetType appears in the fields of currentType
func checkRecursion(targetType, currentType reflect.Type, visited map[reflect.Type]bool) bool {
	if visited[currentType] {
		return false
	}
	visited[currentType] = true

	switch currentType.Kind() {
	case reflect.Struct:
		for i := 0; i < currentType.NumField(); i++ {
			field := currentType.Field(i)
			if !field.IsExported() {
				continue
			}

			fieldType := field.Type
			// Check through pointers, slices, and arrays
			for fieldType.Kind() == reflect.Ptr || fieldType.Kind() == reflect.Slice || fieldType.Kind() == reflect.Array {
				fieldType = fieldType.Elem()
			}

			if fieldType == targetType {
				return true
			}

			if fieldType.Kind() == reflect.Struct && checkRecursion(targetType, fieldType, visited) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		elemType := currentType.Elem()
		for elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
		}
		if elemType == targetType {
			return true
		}
		if elemType.Kind() == reflect.Struct && checkRecursion(targetType, elemType, visited) {
			return true
		}
	case reflect.Ptr:
		elemType := currentType.Elem()
		if elemType == targetType {
			return true
		}
		if elemType.Kind() == reflect.Struct && checkRecursion(targetType, elemType, visited) {
			return true
		}
	}

	return false
}

// generateDefName creates a unique definition name for a type
func generateDefName(t reflect.Type) string {
	// Use the type name if available, otherwise use a generic name
	if t.Name() != "" {
		return strings.ToLower(t.Name())
	}
	return "anonymousStruct"
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

// generateFieldSchema generates schema for a specific field type with recursion handling.
func generateFieldSchema(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	// Delegate to smaller focused helpers to reduce cyclomatic complexity.
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Bool:
		return handlePrimitiveType(t)
	case reflect.Slice, reflect.Array:
		return handleArrayOrSlice(t, ctx)
	case reflect.Map:
		return handleMapType(t, ctx, isRoot)
	case reflect.Ptr:
		return handlePointerType(t, ctx, isRoot)
	case reflect.Struct:
		return handleStructType(t, ctx, isRoot)
	default:
		return &tool.Schema{Type: "object"}
	}
}

// handlePrimitiveType returns a simple schema for primitive kinds.
func handlePrimitiveType(t reflect.Type) *tool.Schema {
	switch t.Kind() {
	case reflect.String:
		return &tool.Schema{Type: "string"}
	case reflect.Bool:
		return &tool.Schema{Type: "boolean"}
	case reflect.Float32, reflect.Float64:
		return &tool.Schema{Type: "number"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &tool.Schema{Type: "integer"}
	default:
		return &tool.Schema{Type: "object"}
	}
}

// handleArrayOrSlice builds schema for arrays and slices.
func handleArrayOrSlice(t reflect.Type, ctx *schemaContext) *tool.Schema {
	// For struct element types we might prefer references; generateFieldSchema will
	// handle nested struct recursion correctly.
	return &tool.Schema{
		Type:  "array",
		Items: generateFieldSchema(t.Elem(), ctx, false),
	}
}

// handleMapType builds schema for map types using additionalProperties.
func handleMapType(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	valueSchema := generateFieldSchema(t.Elem(), ctx, false)
	if valueSchema == nil {
		valueSchema = &tool.Schema{Type: "object"}
	}

	schema := &tool.Schema{
		Type:                 "object",
		AdditionalProperties: valueSchema,
	}

	if isRoot && len(ctx.defs) > 0 {
		schema.Defs = ctx.defs
	}

	return schema
}

// handlePointerType returns the element type schema for pointer types.
func handlePointerType(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	return generateFieldSchema(t.Elem(), ctx, isRoot)
}

// handleStructType handles inline and named struct schemas with recursion tracking.
func handleStructType(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	// If we've already created a definition for this type, return a reference.
	if defName, exists := ctx.visited[t]; exists {
		return &tool.Schema{Ref: "#/$defs/" + defName}
	}

	hasRecursion := hasRecursiveFields(t)

	// Inline schema when there is no recursion (backwards compat).
	if !hasRecursion {
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
			nestedSchema.Properties[fieldName] = generateFieldSchema(field.Type, ctx, false)
		}
		return nestedSchema
	}

	// Named struct with recursion: create definition and return a reference.
	defName := generateDefName(t)
	ctx.visited[t] = defName

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
		nestedSchema.Properties[fieldName] = generateFieldSchema(field.Type, ctx, false)
	}

	// Store the definition
	ctx.defs[defName] = nestedSchema

	return &tool.Schema{Ref: "#/$defs/" + defName}
}
