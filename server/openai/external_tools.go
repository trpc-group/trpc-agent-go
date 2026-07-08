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

	// openAIToolChoiceNone disables tool calling per the OpenAI Chat
	// Completions API even when tools are present in the request.
	openAIToolChoiceNone = "none"

	// openAIToolChoiceAuto lets the model decide whether to call a tool.
	// This is the only non-"none" tool_choice semantics this adapter
	// implements; it is also the default OpenAI behavior when tools are
	// present and tool_choice is omitted.
	openAIToolChoiceAuto = "auto"

	// openAIToolChoiceRequired forces the model to call at least one tool.
	// This adapter does not implement that constraint (see
	// openAIToolChoiceRequiresUnsupportedSemantics) and rejects it instead
	// of silently degrading to "auto".
	openAIToolChoiceRequired = "required"

	// jsonSchemaTypeObject is the default JSON Schema type used when the
	// caller omits function.parameters.
	jsonSchemaTypeObject = "object"

	errOpenAIToolAt                = "openai tool[%d]: %s"
	errOpenAIToolWithNameAt        = "openai tool[%d] %q: parse function.parameters: %w"
	errOpenAIToolFunctionName      = "function.name is required"
	errOpenAIToolUnsupportedType   = "unsupported tool type %q"
	errOpenAIToolChoiceUnsupported = "unsupported tool_choice %v: this server only " +
		"supports \"none\" or \"auto\" (omitted defaults to \"auto\"); " +
		"\"required\" and forced-function tool_choice are not implemented"
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
	if openAIToolChoiceDisablesTools(req) {
		return opts, nil
	}
	if openAIToolChoiceRequiresUnsupportedSemantics(req) {
		return nil, fmt.Errorf(errOpenAIToolChoiceUnsupported, req.ToolChoice)
	}
	externalTools, err := externalToolsFromOpenAIRequest(req)
	if err != nil {
		return nil, err
	}
	if len(externalTools) == 0 {
		return opts, nil
	}
	return append(opts, agent.WithExternalTools(externalTools)), nil
}

// openAIToolChoiceDisablesTools reports whether the request's tool_choice
// disables tool calling. Per OpenAI Chat Completions API, "none" means the
// model must not call tools even when tools are present in the request.
func openAIToolChoiceDisablesTools(req *openAIRequest) bool {
	if req == nil || req.ToolChoice == nil {
		return false
	}
	choice, ok := req.ToolChoice.(string)
	return ok && choice == openAIToolChoiceNone
}

// openAIToolChoiceRequiresUnsupportedSemantics reports whether req.ToolChoice
// requests OpenAI-compatible behavior that this adapter does not implement:
// "required" (must call at least one tool) or a forced-function object
// (e.g. {"type":"function","function":{"name":"foo"}}). Only checked when
// req.Tools is non-empty, since tool_choice is meaningless without tools.
//
// The adapter exposes request tools to the model via WithExternalTools and
// always lets the model freely decide whether to call one, which matches
// "auto" but not "required" or a forced function. Rather than silently
// treating those unsupported values as "auto" — which could make a caller
// relying on forced tool selection believe its request succeeded while
// receiving a plain assistant reply instead — appendExternalToolRunOption
// rejects them with an error that the server surfaces as HTTP 400.
func openAIToolChoiceRequiresUnsupportedSemantics(req *openAIRequest) bool {
	if req == nil || req.ToolChoice == nil || len(req.Tools) == 0 {
		return false
	}
	choice, ok := req.ToolChoice.(string)
	if !ok {
		// A non-string tool_choice is a forced-function selection object.
		return true
	}
	switch choice {
	case openAIToolChoiceNone, openAIToolChoiceAuto:
		return false
	case openAIToolChoiceRequired:
		return true
	default:
		// Unrecognized string values are rejected the same as "required"
		// rather than silently falling back to "auto".
		return true
	}
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
