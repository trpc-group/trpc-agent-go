//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"encoding/json"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// convertMCPSchemaToSchema converts MCP's JSON schema to our Schema format.
func convertMCPSchemaToSchema(mcpSchema any) *tool.Schema {
	schemaBytes, err := json.Marshal(mcpSchema)
	if err != nil {
		return &tool.Schema{
			Type: "object",
		}
	}

	var schemaMap map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return &tool.Schema{
			Type: "object",
		}
	}

	schema := &tool.Schema{}
	applyJSONSchemaType(schema, schemaMap["type"])
	if descVal, ok := schemaMap["description"].(string); ok {
		schema.Description = descVal
	}
	if patternVal, ok := schemaMap["pattern"].(string); ok {
		schema.Pattern = patternVal
	}
	applyValueConstraints(schema, schemaMap)
	if propsVal, ok := schemaMap["properties"].(map[string]any); ok {
		schema.Properties = convertProperties(propsVal)
	}
	if reqVal, ok := schemaMap["required"].([]any); ok {
		required := make([]string, len(reqVal))
		for i, req := range reqVal {
			if reqStr, ok := req.(string); ok {
				required[i] = reqStr
			}
		}
		schema.Required = required
	}
	if refVal, ok := schemaMap["$ref"].(string); ok {
		schema.Ref = refVal
	}
	if defsVal, ok := schemaMap["$defs"].(map[string]any); ok {
		schema.Defs = convertDefs(defsVal)
	}

	return schema
}

// applyJSONSchemaType copies a JSON Schema type keyword onto schema.
// A string is stored in Type. A type array becomes one anyOf branch per
// string member, preserving order and null alternatives. Non-string array
// members and other representations of type are ignored.
func applyJSONSchemaType(schema *tool.Schema, typeValue any) {
	switch typed := typeValue.(type) {
	case string:
		schema.Type = typed
	case []any:
		for _, value := range typed {
			name, ok := value.(string)
			if !ok {
				continue
			}
			schema.AnyOf = append(schema.AnyOf, &tool.Schema{Type: name})
		}
	}
}

// applyValueConstraints copies the format, numeric bound and array length
// keywords from a decoded JSON Schema map. Values with an unexpected type are
// ignored.
func applyValueConstraints(schema *tool.Schema, schemaMap map[string]any) {
	if formatVal, ok := schemaMap["format"].(string); ok {
		schema.Format = formatVal
	}
	if minVal, ok := schemaMap["minimum"].(float64); ok {
		schema.Minimum = &minVal
	}
	if maxVal, ok := schemaMap["maximum"].(float64); ok {
		schema.Maximum = &maxVal
	}
	schema.MinItems = toItemCount(schemaMap["minItems"])
	schema.MaxItems = toItemCount(schemaMap["maxItems"])
	if prefixVal, ok := schemaMap["prefixItems"].([]any); ok {
		schema.PrefixItems = convertPrefixItems(prefixVal)
	}
}

// convertPrefixItems converts prefixItems entries in order. Each entry is
// matched to an argument by position, so a true schema becomes an empty
// schema. If any entry cannot be represented, such as a false schema, the
// keyword is dropped rather than shifting later entries to the wrong position.
func convertPrefixItems(items []any) []*tool.Schema {
	result := make([]*tool.Schema, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			result = append(result, convertPropertySchema(typed))
		case bool:
			if !typed {
				return nil
			}
			result = append(result, &tool.Schema{})
		default:
			return nil
		}
	}
	return result
}

// toItemCount converts a decoded JSON number to a non-negative item count.
// Values that do not fit in an int are ignored.
func toItemCount(value any) *int {
	f, ok := value.(float64)
	if !ok || f < 0 || f != math.Trunc(f) || f >= float64(math.MaxInt) {
		return nil
	}
	n := int(f)
	return &n
}

// convertDefs converts JSON Schema $defs definitions from map[string]any to map[string]*Schema.
func convertDefs(defs map[string]any) map[string]*tool.Schema {
	result := make(map[string]*tool.Schema, len(defs))
	for name, def := range defs {
		if defMap, ok := def.(map[string]any); ok {
			result[name] = convertMCPSchemaToSchema(defMap)
		}
	}
	return result
}

// convertProperties converts property definitions from map[string]any to map[string]*Schema.
func convertProperties(props map[string]any) map[string]*tool.Schema {
	if props == nil {
		return nil
	}

	result := make(map[string]*tool.Schema)
	for name, prop := range props {
		if propMap, ok := prop.(map[string]any); ok {
			result[name] = convertPropertySchema(propMap)
		}
	}
	return result
}

// convertPropertySchema converts a single property definition, including its
// nested properties and items.
func convertPropertySchema(propMap map[string]any) *tool.Schema {
	propSchema := &tool.Schema{}
	applyJSONSchemaType(propSchema, propMap["type"])
	if descVal, ok := propMap["description"].(string); ok {
		propSchema.Description = descVal
	}
	if patternVal, ok := propMap["pattern"].(string); ok {
		propSchema.Pattern = patternVal
	}
	applyValueConstraints(propSchema, propMap)
	if defaultVal, exists := propMap["default"]; exists {
		propSchema.Default = defaultVal
	}
	if enumVal, exists := propMap["enum"]; exists {
		if enumArr, ok := enumVal.([]any); ok {
			propSchema.Enum = enumArr
		}
	}
	// Recursively process nested properties.
	if nestedPropsVal, ok := propMap["properties"].(map[string]any); ok {
		propSchema.Properties = convertProperties(nestedPropsVal)
	}
	// Handle required field.
	if reqVal, ok := propMap["required"].([]any); ok {
		required := make([]string, len(reqVal))
		for i, req := range reqVal {
			if reqStr, ok := req.(string); ok {
				required[i] = reqStr
			}
		}
		propSchema.Required = required
	}
	// Handle items field (for array types).
	if itemsVal, exists := propMap["items"]; exists {
		if itemsMap, ok := itemsVal.(map[string]any); ok {
			propSchema.Items = convertMCPSchemaToSchema(itemsMap)
		}
	}
	// Handle additionalProperties field.
	if additionalPropsVal, exists := propMap["additionalProperties"]; exists {
		propSchema.AdditionalProperties = additionalPropsVal
	}
	if refVal, ok := propMap["$ref"].(string); ok {
		propSchema.Ref = refVal
	}
	if defsVal, ok := propMap["$defs"].(map[string]any); ok {
		propSchema.Defs = convertDefs(defsVal)
	}
	return propSchema
}
