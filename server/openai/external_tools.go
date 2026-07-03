//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bytes"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// openAIToolTypeFunction is the only tool type currently supported by
	// the OpenAI Chat Completions adapter for external tools.
	openAIToolTypeFunction = "function"

	// jsonSchemaTypeObject is the default JSON Schema type used when the
	// caller omits function.parameters.
	jsonSchemaTypeObject = "object"

	errOpenAIToolAt              = "openai tool[%d]: %s"
	errOpenAIToolWithNameAt      = "openai tool[%d] %q: parse function.parameters: %w"
	errOpenAIToolFunctionName    = "function.name is required"
	errOpenAIToolUnsupportedType = "unsupported tool type %q"
)

// appendExternalToolRunOption converts req.Tools into caller-executed tools
// and appends an agent.WithExternalTools run option when any are present.
//
// External tools are declaration-only: the framework exposes them to the
// model but never executes them. When the model calls one, the run ends
// with a tool_call assistant message and the caller is expected to execute
// the tool externally and continue with a role="tool" message.
func appendExternalToolRunOption(
	opts []agent.RunOption,
	req *openAIRequest,
) ([]agent.RunOption, error) {
	externalTools, err := externalToolsFromOpenAIRequest(req)
	if err != nil {
		return nil, err
	}
	if len(externalTools) == 0 {
		return opts, nil
	}
	return append(opts, agent.WithExternalTools(externalTools)), nil
}

// externalToolsFromOpenAIRequest converts req.Tools into framework tools.
// Only tools with type="function" and a non-empty function.name are accepted.
func externalToolsFromOpenAIRequest(req *openAIRequest) ([]agenttool.Tool, error) {
	if req == nil || len(req.Tools) == 0 {
		return nil, nil
	}
	tools := make([]agenttool.Tool, 0, len(req.Tools))
	for i, t := range req.Tools {
		// OpenAI requires type="function", but several client SDKs omit the field.
		// Treat empty type as function for compatibility.
		if t.Type != "" && t.Type != openAIToolTypeFunction {
			return nil, fmt.Errorf(
				errOpenAIToolAt,
				i,
				fmt.Sprintf(errOpenAIToolUnsupportedType, t.Type),
			)
		}
		if t.Function.Name == "" {
			return nil, fmt.Errorf(
				errOpenAIToolAt,
				i,
				errOpenAIToolFunctionName,
			)
		}
		schema, err := openAIToolParametersToSchema(t.Function.Parameters)
		if err != nil {
			return nil, fmt.Errorf(
				errOpenAIToolWithNameAt,
				i,
				t.Function.Name,
				err,
			)
		}
		tools = append(tools, &declarationOnlyTool{
			declaration: &agenttool.Declaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: schema,
			},
		})
	}
	return tools, nil
}

// openAIToolParametersToSchema converts the raw JSON parameters field of an
// OpenAI tool definition into a framework tool schema. Empty or nil inputs
// yield the default {"type":"object"} schema.
func openAIToolParametersToSchema(params json.RawMessage) (*agenttool.Schema, error) {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return &agenttool.Schema{Type: jsonSchemaTypeObject}, nil
	}
	var schema agenttool.Schema
	if err := json.Unmarshal(trimmed, &schema); err != nil {
		return nil, err
	}
	if schema.Type == "" {
		schema.Type = jsonSchemaTypeObject
	}
	return &schema, nil
}

// declarationOnlyTool is a framework tool that only exposes its declaration
// to the model. It intentionally does not implement CallableTool so the
// framework never executes it; the caller runs the tool externally.
type declarationOnlyTool struct {
	declaration *agenttool.Declaration
}

// Declaration returns the tool metadata visible to the model.
func (t *declarationOnlyTool) Declaration() *agenttool.Declaration {
	return t.declaration
}
