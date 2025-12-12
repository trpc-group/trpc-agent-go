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

const defsRefPrefix = "#/$defs/"

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

func schemaRef(defName string) *tool.Schema {
	return &tool.Schema{Ref: defsRefPrefix + defName}
}

func cloneProperties(props map[string]*tool.Schema) map[string]*tool.Schema {
	if len(props) == 0 {
		return nil
	}
	out := make(map[string]*tool.Schema, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}

func cloneRequired(required []string) []string {
	if len(required) == 0 {
		return nil
	}
	out := make([]string, len(required))
	copy(out, required)
	return out
}

// generateJSONSchema generates a JSON schema with recursion handling
func generateJSONSchema(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	// Handle different kinds of types.
	switch t.Kind() {
	case reflect.Struct:
		return handleStructType(t, ctx, isRoot)

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

func jsonFieldMeta(
	field reflect.StructField,
) (fieldName string, omitEmpty bool, ok bool) {
	if !field.IsExported() {
		return "", false, false
	}

	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}

	if tag == "" {
		return field.Name, false, true
	}

	commaIdx := strings.Index(tag, ",")
	if commaIdx == -1 {
		return tag, false, true
	}
	return tag[:commaIdx], strings.Contains(tag[commaIdx:], "omitempty"), true
}

func appendRequiredField(
	required []string,
	field reflect.StructField,
	fieldSchema *tool.Schema,
	fieldName string,
	isOmitEmpty bool,
) []string {
	if fieldSchema.Ref == "" {
		isRequiredByTag, err := parseJSONSchemaTag(
			field.Type, field.Tag, fieldSchema,
		)
		if err != nil {
			log.Errorf("parseJSONSchemaTag error for field %s: %v", fieldName, err)
		}

		if (field.Type.Kind() != reflect.Ptr && !isOmitEmpty) ||
			isRequiredByTag {
			return append(required, fieldName)
		}
	} else if field.Type.Kind() != reflect.Ptr && !isOmitEmpty {
		return append(required, fieldName)
	}
	return required
}

func buildStructSchema(t reflect.Type, ctx *schemaContext) *tool.Schema {
	schema := &tool.Schema{
		Type:       "object",
		Properties: make(map[string]*tool.Schema),
	}

	required := make([]string, 0)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldName, isOmitEmpty, ok := jsonFieldMeta(field)
		if !ok {
			continue
		}

		fieldSchema := generateFieldSchema(field.Type, ctx, false)
		schema.Properties[fieldName] = fieldSchema

		required = appendRequiredField(
			required, field, fieldSchema, fieldName, isOmitEmpty,
		)
	}

	if len(required) > 0 {
		schema.Required = required
	}
	return schema
}

// handleStructType handles inline and named struct schemas with recursion tracking.
func handleStructType(t reflect.Type, ctx *schemaContext, isRoot bool) *tool.Schema {
	if defName, exists := ctx.visited[t]; exists {
		return schemaRef(defName)
	}

	hasRecursion := hasRecursiveFields(t)
	if !hasRecursion {
		return buildStructSchema(t, ctx)
	}

	defName := generateDefName(t)
	ctx.visited[t] = defName
	nestedSchema := buildStructSchema(t, ctx)
	defSchema := &tool.Schema{
		Type:       nestedSchema.Type,
		Properties: cloneProperties(nestedSchema.Properties),
		Required:   cloneRequired(nestedSchema.Required),
	}
	ctx.defs[defName] = defSchema
	if isRoot {
		return &tool.Schema{
			Type:       nestedSchema.Type,
			Properties: cloneProperties(nestedSchema.Properties),
			Required:   cloneRequired(nestedSchema.Required),
		}
	}

	return schemaRef(defName)
}
