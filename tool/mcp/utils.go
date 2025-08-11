//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package mcp

import (
	"encoding/json"

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
	if typeVal, ok := schemaMap["type"].(string); ok {
		schema.Type = typeVal
	}
	if descVal, ok := schemaMap["description"].(string); ok {
		schema.Description = descVal
	}
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

	return schema
}

// convertProperties converts property definitions from map[string]interface{} to map[string]*Schema.
func convertProperties(props map[string]any) map[string]*tool.Schema {
	if props == nil {
		return nil
	}

	result := make(map[string]*tool.Schema)
	for name, prop := range props {
		if propMap, ok := prop.(map[string]any); ok {
			propSchema := &tool.Schema{}
			if typeVal, ok := propMap["type"].(string); ok {
				propSchema.Type = typeVal
			}
			if descVal, ok := propMap["description"].(string); ok {
				propSchema.Description = descVal
			}
			if defaultVal, exists := propMap["default"]; exists {
				propSchema.Default = defaultVal
			}
			if enumVal, exists := propMap["enum"]; exists {
				if enumArr, ok := enumVal.([]any); ok {
					propSchema.Enum = enumArr
				}
			}
			result[name] = propSchema
		}
	}
	return result
}
