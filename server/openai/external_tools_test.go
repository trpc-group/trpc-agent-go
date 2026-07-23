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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestExternalToolsFromOpenAIRequest_ValidFunction(t *testing.T) {
	const (
		toolName        = "client_search"
		toolDescription = "Search a frontend-owned source."
		argName         = "query"
		argType         = "string"
	)
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        toolName,
					Description: toolDescription,
					Parameters: json.RawMessage(`{
						"type": "object",
						"properties": {"query": {"type": "string"}},
						"required": ["query"]
					}`),
				},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	decl := tools[0].Declaration()
	require.NotNil(t, decl)
	assert.Equal(t, toolName, decl.Name)
	assert.Equal(t, toolDescription, decl.Description)
	require.NotNil(t, decl.InputSchema)
	assert.Equal(t, jsonSchemaTypeObject, decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, argName)
	assert.Equal(t, argType, decl.InputSchema.Properties[argName].Type)
	assert.Equal(t, []string{argName}, decl.InputSchema.Required)
}

func TestExternalToolsFromOpenAIRequest_EmptyRequest(t *testing.T) {
	tools, err := externalToolsFromOpenAIRequest(nil)
	require.NoError(t, err)
	assert.Nil(t, tools)

	tools, err = externalToolsFromOpenAIRequest(&openAIRequest{})
	require.NoError(t, err)
	assert.Nil(t, tools)
}

func TestExternalToolsFromOpenAIRequest_DefaultsNilParameters(t *testing.T) {
	const toolName = "client_notify"
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name: toolName,
				},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	decl := tools[0].Declaration()
	require.NotNil(t, decl)
	require.NotNil(t, decl.InputSchema)
	assert.Equal(t, toolName, decl.Name)
	assert.Equal(t, jsonSchemaTypeObject, decl.InputSchema.Type)
}

func TestExternalToolsFromOpenAIRequest_DefaultsExplicitNullParameters(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:       "client_null",
					Parameters: json.RawMessage("null"),
				},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.NotNil(t, tools[0].Declaration().InputSchema)
	assert.Equal(t, jsonSchemaTypeObject, tools[0].Declaration().InputSchema.Type)
}

func TestExternalToolsFromOpenAIRequest_DefaultsMissingSchemaType(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:       "client_no_type",
					Parameters: json.RawMessage(`{"properties": {"q": {"type": "string"}}}`),
				},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, jsonSchemaTypeObject, tools[0].Declaration().InputSchema.Type)
}

func TestExternalToolsFromOpenAIRequest_AcceptsEmptyType(t *testing.T) {
	// OpenAI schema requires "type": "function" but be permissive when the
	// caller omits it entirely, matching common client SDK behaviour.
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Function: openAIFunction{Name: "client_default_type"},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "client_default_type", tools[0].Declaration().Name)
}

func TestExternalToolsFromOpenAIRequest_RejectsUnsupportedType(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type:     "code_interpreter",
				Function: openAIFunction{Name: "irrelevant"},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.Error(t, err)
	assert.Nil(t, tools)
	assert.ErrorContains(t, err, "openai tool[0]")
	assert.ErrorContains(t, err, "code_interpreter")
}

func TestExternalToolsFromOpenAIRequest_RejectsMissingName(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type:     "function",
				Function: openAIFunction{Description: "no name"},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.Error(t, err)
	assert.Nil(t, tools)
	assert.ErrorContains(t, err, "openai tool[0]")
	assert.ErrorContains(t, err, errOpenAIToolFunctionName)
}

func TestExternalToolsFromOpenAIRequest_RejectsInvalidParametersJSON(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:       "bad_params",
					Parameters: json.RawMessage(`{"type": 123}`),
				},
			},
		},
	}

	tools, err := externalToolsFromOpenAIRequest(req)

	require.Error(t, err)
	assert.Nil(t, tools)
	assert.ErrorContains(t, err, "openai tool[0]")
	assert.ErrorContains(t, err, "bad_params")
	assert.ErrorContains(t, err, "parse function.parameters")
}

func TestAppendExternalToolRunOption_NoTools(t *testing.T) {
	existing := []agent.RunOption{agent.WithModelName("existing")}
	got, err := appendExternalToolRunOption(existing, &openAIRequest{})
	require.NoError(t, err)
	// The slice should be returned unchanged: same length, same contents.
	require.Len(t, got, len(existing))
	opts := agent.NewRunOptions(got...)
	assert.Empty(t, opts.ExternalTools)
	assert.Equal(t, "existing", opts.ModelName)
}

func TestAppendExternalToolRunOption_AppendsExternalTools(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        "client_search",
					Description: "search",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
	}
	got, err := appendExternalToolRunOption(nil, req)
	require.NoError(t, err)
	opts := agent.NewRunOptions(got...)
	require.Len(t, opts.ExternalTools, 1)
	assert.Equal(t, "client_search", opts.ExternalTools[0].Declaration().Name)
	// The framework must not treat declaration-only tools as executable.
	assert.False(t, opts.ShouldExecuteTool(nil, opts.ExternalTools[0]))
}

func TestAppendExternalToolRunOption_PropagatesError(t *testing.T) {
	req := &openAIRequest{
		Tools: []openAITool{
			{Type: "function", Function: openAIFunction{}},
		},
	}
	got, err := appendExternalToolRunOption(nil, req)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestAppendExternalToolRunOption_SkipsWhenToolChoiceNone(t *testing.T) {
	req := &openAIRequest{
		ToolChoice: openAIToolChoiceNone,
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        "client_search",
					Description: "search",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
	}
	got, err := appendExternalToolRunOption(nil, req)
	require.NoError(t, err)
	opts := agent.NewRunOptions(got...)
	assert.Empty(t, opts.ExternalTools)
}

func TestOpenAIToolChoiceDisablesTools(t *testing.T) {
	assert.False(t, openAIToolChoiceDisablesTools(nil))
	assert.False(t, openAIToolChoiceDisablesTools(&openAIRequest{}))
	assert.False(t, openAIToolChoiceDisablesTools(&openAIRequest{ToolChoice: "auto"}))
	assert.True(t, openAIToolChoiceDisablesTools(&openAIRequest{ToolChoice: openAIToolChoiceNone}))
	assert.False(t, openAIToolChoiceDisablesTools(&openAIRequest{ToolChoice: 0}))
}

func oneOpenAITool() []openAITool {
	return []openAITool{
		{Type: "function", Function: openAIFunction{Name: "client_search"}},
	}
}

func TestOpenAIToolChoiceRequiresUnsupportedSemantics(t *testing.T) {
	tests := []struct {
		name string
		req  *openAIRequest
		want bool
	}{
		{"nil request", nil, false},
		{"no tool_choice, no tools", &openAIRequest{}, false},
		{"no tool_choice, with tools", &openAIRequest{Tools: oneOpenAITool()}, false},
		{
			"none with no tools",
			&openAIRequest{ToolChoice: openAIToolChoiceNone},
			false,
		},
		{
			"none with tools",
			&openAIRequest{ToolChoice: openAIToolChoiceNone, Tools: oneOpenAITool()},
			false,
		},
		{
			"auto with tools",
			&openAIRequest{ToolChoice: openAIToolChoiceAuto, Tools: oneOpenAITool()},
			false,
		},
		{
			"required with no tools",
			&openAIRequest{ToolChoice: openAIToolChoiceRequired},
			false,
		},
		{
			"required with tools",
			&openAIRequest{ToolChoice: openAIToolChoiceRequired, Tools: oneOpenAITool()},
			true,
		},
		{
			"unrecognized string with tools",
			&openAIRequest{ToolChoice: "bogus", Tools: oneOpenAITool()},
			true,
		},
		{
			"forced-function object with tools",
			&openAIRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "client_search"},
				},
				Tools: oneOpenAITool(),
			},
			true,
		},
		{
			"forced-function object with no tools",
			&openAIRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "client_search"},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, openAIToolChoiceRequiresUnsupportedSemantics(tt.req))
		})
	}
}

func TestAppendExternalToolRunOption_RejectsRequiredToolChoice(t *testing.T) {
	req := &openAIRequest{
		ToolChoice: openAIToolChoiceRequired,
		Tools:      oneOpenAITool(),
	}
	got, err := appendExternalToolRunOption(nil, req)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "required")
}

func TestAppendExternalToolRunOption_RejectsForcedFunctionToolChoice(t *testing.T) {
	req := &openAIRequest{
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "client_search"},
		},
		Tools: oneOpenAITool(),
	}
	got, err := appendExternalToolRunOption(nil, req)
	require.Error(t, err)
	assert.Nil(t, got)
}
