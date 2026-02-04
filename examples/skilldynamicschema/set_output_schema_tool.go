//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type setOutputSchemaInput struct {
	// Schema is the JSON schema object.
	Schema json.RawMessage `json:"schema,omitempty"`
}

type setOutputSchemaTool struct{}

func (t *setOutputSchemaTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "set_output_schema",
		Description: "Set structured output JSON schema for subsequent model calls. " +
			"Always pass `schema` as a JSON object.",
		InputSchema: &tool.Schema{
			Type:                 "object",
			Description:          "Set structured output JSON schema",
			Required:             []string{"schema"},
			AdditionalProperties: false,
			Properties: map[string]*tool.Schema{
				"schema": {
					Type:                 "object",
					Description:          "JSON Schema object",
					AdditionalProperties: true,
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"ok":    {Type: "boolean"},
				"name":  {Type: "string"},
				"error": {Type: "string"},
			},
		},
	}
}

func (t *setOutputSchemaTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var in setOutputSchemaInput
	if err := json.Unmarshal(jsonArgs, &in); err != nil {
		return map[string]any{"ok": false, "error": fmt.Sprintf("invalid args: %v", err)}, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, fmt.Errorf("invocation not found in context")
	}

	if len(in.Schema) == 0 {
		// Be tolerant: if a schema is already set for this invocation, treat this
		// as a no-op (some models call setter tools redundantly).
		if inv.StructuredOutput != nil &&
			inv.StructuredOutput.Type == model.StructuredOutputJSONSchema &&
			inv.StructuredOutput.JSONSchema != nil &&
			inv.StructuredOutput.JSONSchema.Schema != nil {
			return map[string]any{"ok": true, "name": inv.StructuredOutput.JSONSchema.Name}, nil
		}
		// Return a normal tool result (not an error) to avoid noisy framework logs.
		return map[string]any{
			"ok":    false,
			"error": "schema is required; extract it from SKILL.md under the section \"Output JSON Schema\" and call set_output_schema with {\"schema\": <object>}",
		}, nil
	}

	var schema map[string]any
	if err := json.Unmarshal(in.Schema, &schema); err != nil {
		return map[string]any{
			"ok": false,
			"error": "schema must be a JSON object (not a JSON string). " +
				"Extract the object from SKILL.md and call set_output_schema with {\"schema\": <object>}",
		}, nil
	}
	if len(schema) == 0 {
		return map[string]any{"ok": false, "error": "schema must be a non-empty object"}, nil
	}

	inv.StructuredOutputType = nil
	inv.StructuredOutput = &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "output",
			Schema:      schema,
			Strict:      true,
			Description: "Structured output schema set by tool",
		},
	}
	return map[string]any{"ok": true, "name": "output"}, nil
}

var _ tool.Tool = (*setOutputSchemaTool)(nil)
var _ tool.CallableTool = (*setOutputSchemaTool)(nil)
