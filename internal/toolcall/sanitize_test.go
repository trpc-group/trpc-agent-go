//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolcall

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type stubTool struct {
	decl *tool.Declaration
}

func (s stubTool) Declaration() *tool.Declaration { return s.decl }

func TestSanitizeMessagesWithTools_DowngradesInvalidToolCallAndResult(t *testing.T) {
	in := []model.Message{
		model.NewUserMessage("hi"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte("{a:1}"),
					},
				},
			},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call_1",
			ToolName: "test_tool",
			Content:  "tool error",
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 3) {
		assert.Equal(t, model.RoleUser, out[0].Role)
		assert.Equal(t, model.RoleUser, out[1].Role)
		assert.Contains(t, out[1].Content, invalidToolCallTag)
		assert.Equal(t, model.RoleUser, out[2].Role)
		assert.Contains(t, out[2].Content, invalidToolResultTag)
	}
	for _, msg := range out {
		assert.NotEqual(t, model.RoleTool, msg.Role)
		assert.Empty(t, msg.ToolCalls)
	}
}

func TestSanitizeMessagesWithTools_PreservesNilMessagesSlice(t *testing.T) {
	var in []model.Message
	out := SanitizeMessagesWithTools(in, nil)
	assert.Nil(t, out)
}

func TestSanitizeMessagesWithTools_PreservesEmptyMessagesSlice(t *testing.T) {
	in := make([]model.Message, 0)
	out := SanitizeMessagesWithTools(in, nil)
	assert.NotNil(t, out)
	assert.Len(t, out, 0)
}

func TestSanitizeMessagesWithTools_PreservesValidToolRound(t *testing.T) {
	in := []model.Message{
		model.NewUserMessage("hi"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`{"a":1}`),
					},
				},
			},
		},
		{
			Role:    model.RoleTool,
			ToolID:  "call_1",
			Content: `{"ok":true}`,
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 3) {
		assert.Equal(t, model.RoleAssistant, out[1].Role)
		if assert.Len(t, out[1].ToolCalls, 1) {
			assert.Equal(t, []byte(`{"a":1}`), out[1].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, model.RoleTool, out[2].Role)
		assert.Equal(t, "call_1", out[2].ToolID)
	}
}

func TestSanitizeMessagesWithTools_NormalizesEmptyArgumentsToEmptyObject(t *testing.T) {
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "no_args_tool",
						Arguments: []byte(""),
					},
				},
			},
		},
		{
			Role:   model.RoleTool,
			ToolID: "call_1",
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 2) && assert.Len(t, out[0].ToolCalls, 1) {
		assert.Equal(t, []byte("{}"), out[0].ToolCalls[0].Function.Arguments)
	}
}

func TestSanitizeMessagesWithTools_SplitsMixedValidityToolRound(t *testing.T) {
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_ok",
					Function: model.FunctionDefinitionParam{
						Name:      "ok_tool",
						Arguments: []byte(`{"a":1}`),
					},
				},
				{
					ID: "call_bad",
					Function: model.FunctionDefinitionParam{
						Name:      "bad_tool",
						Arguments: []byte("not-json"),
					},
				},
			},
		},
		{
			Role:   model.RoleTool,
			ToolID: "call_ok",
		},
		{
			Role:    model.RoleTool,
			ToolID:  "call_bad",
			Content: "bad tool error",
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 4) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, "call_ok", out[0].ToolCalls[0].ID)
		}
		assert.Equal(t, model.RoleTool, out[1].Role)
		assert.Equal(t, "call_ok", out[1].ToolID)
		assert.Equal(t, model.RoleUser, out[2].Role)
		assert.Contains(t, out[2].Content, invalidToolCallTag)
		assert.Equal(t, model.RoleUser, out[3].Role)
		assert.Contains(t, out[3].Content, invalidToolResultTag)
	}
}

func TestSanitizeMessagesWithTools_DowngradesOrphanToolResult(t *testing.T) {
	in := []model.Message{
		{
			Role:    model.RoleTool,
			ToolID:  "call_orphan",
			Content: "orphan",
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 1) {
		assert.Equal(t, model.RoleUser, out[0].Role)
		assert.Contains(t, out[0].Content, orphanToolResultTag)
	}
}

func TestSanitizeMessagesWithTools_PreservesNonObjectJSONArgumentsWhenToolsUnknown(t *testing.T) {
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`"string"`),
					},
				},
			},
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 1) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, []byte(`"string"`), out[0].ToolCalls[0].Function.Arguments)
		}
	}
}

func TestSanitizeMessagesWithTools_DowngradesSchemaTypeMismatch(t *testing.T) {
	type input struct {
		A int `json:"a"`
	}
	fn := function.NewFunctionTool(
		func(context.Context, input) (string, error) { return "", nil },
		function.WithName("test_tool"),
		function.WithDescription("test tool"),
	)
	tools := map[string]tool.Tool{
		"test_tool": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`{"a":"not-an-int"}`),
					},
				},
			},
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 1) {
		assert.Equal(t, model.RoleUser, out[0].Role)
		assert.Contains(t, out[0].Content, invalidToolCallTag)
		assert.Contains(t, out[0].Content, "expected integer")
		assert.Contains(t, out[0].Content, "$.a")
	}
}

func TestSanitizeMessagesWithTools_DowngradesNonObjectJSONArgumentsWhenSchemaExpectsObject(t *testing.T) {
	type input struct {
		A int `json:"a"`
	}
	fn := function.NewFunctionTool(
		func(context.Context, input) (string, error) { return "", nil },
		function.WithName("test_tool"),
		function.WithDescription("test tool"),
	)
	tools := map[string]tool.Tool{
		"test_tool": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`"string"`),
					},
				},
			},
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 1) {
		assert.Equal(t, model.RoleUser, out[0].Role)
		assert.Contains(t, out[0].Content, invalidToolCallTag)
		assert.Contains(t, out[0].Content, "expected object")
	}
}

func TestSanitizeMessagesWithTools_PreservesStringArgumentsWhenSchemaAllows(t *testing.T) {
	fn := function.NewFunctionTool(
		func(context.Context, string) (string, error) { return "", nil },
		function.WithName("echo"),
		function.WithDescription("echo tool"),
	)
	tools := map[string]tool.Tool{
		"echo": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "echo",
						Arguments: []byte(`"hi"`),
					},
				},
			},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call_1",
			ToolName: "echo",
			Content:  "ok",
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 2) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, []byte(`"hi"`), out[0].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, model.RoleTool, out[1].Role)
		assert.Equal(t, "call_1", out[1].ToolID)
	}
}

func TestSanitizeMessagesWithTools_PreservesArrayArgumentsWhenSchemaAllows(t *testing.T) {
	fn := function.NewFunctionTool(
		func(context.Context, []string) ([]string, error) { return nil, nil },
		function.WithName("echo_list"),
		function.WithDescription("echo list tool"),
	)
	tools := map[string]tool.Tool{
		"echo_list": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "echo_list",
						Arguments: []byte(`["a","b"]`),
					},
				},
			},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call_1",
			ToolName: "echo_list",
			Content:  "ok",
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 2) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, []byte(`["a","b"]`), out[0].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, model.RoleTool, out[1].Role)
		assert.Equal(t, "call_1", out[1].ToolID)
	}
}

func TestSanitizeMessagesWithTools_PreservesNullArgumentsWhenToolsUnknown(t *testing.T) {
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`null`),
					},
				},
			},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call_1",
			ToolName: "test_tool",
			Content:  "ok",
		},
	}
	out := SanitizeMessagesWithTools(in, nil)
	if assert.Len(t, out, 2) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, []byte(`null`), out[0].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, model.RoleTool, out[1].Role)
		assert.Equal(t, "call_1", out[1].ToolID)
	}
}

func TestSanitizeMessagesWithTools_DowngradesNullArgumentsWhenSchemaExpectsObject(t *testing.T) {
	type input struct {
		A int `json:"a"`
	}
	fn := function.NewFunctionTool(
		func(context.Context, input) (string, error) { return "", nil },
		function.WithName("test_tool"),
		function.WithDescription("test tool"),
	)
	tools := map[string]tool.Tool{
		"test_tool": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "test_tool",
						Arguments: []byte(`null`),
					},
				},
			},
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 1) {
		assert.Equal(t, model.RoleUser, out[0].Role)
		assert.Contains(t, out[0].Content, invalidToolCallTag)
		assert.Contains(t, out[0].Content, "expected object")
		assert.Contains(t, out[0].Content, "$")
	}
}

func TestSanitizeMessagesWithTools_PreservesNullArgumentsWhenSchemaAllowsNull(t *testing.T) {
	fn := function.NewFunctionTool(
		func(context.Context, any) (string, error) { return "", nil },
		function.WithName("nil_tool"),
		function.WithDescription("nil tool"),
		function.WithInputSchema(&tool.Schema{Type: "null"}),
	)
	tools := map[string]tool.Tool{
		"nil_tool": fn,
	}
	in := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "nil_tool",
						Arguments: []byte(`null`),
					},
				},
			},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call_1",
			ToolName: "nil_tool",
			Content:  "ok",
		},
	}
	out := SanitizeMessagesWithTools(in, tools)
	if assert.Len(t, out, 2) {
		assert.Equal(t, model.RoleAssistant, out[0].Role)
		if assert.Len(t, out[0].ToolCalls, 1) {
			assert.Equal(t, []byte(`null`), out[0].ToolCalls[0].Function.Arguments)
		}
		assert.Equal(t, model.RoleTool, out[1].Role)
		assert.Equal(t, "call_1", out[1].ToolID)
	}
}

func TestResolveSchemaRef(t *testing.T) {
	defs := map[string]*tool.Schema{
		"Input": {Type: "object"},
	}
	assert.NotNil(t, resolveSchemaRef("#/$defs/Input", defs))
	assert.Nil(t, resolveSchemaRef("#/$defs/Missing", defs))
	assert.Nil(t, resolveSchemaRef("#/$defs/", defs))
	assert.Nil(t, resolveSchemaRef("https://example.com/schema.json", defs))
	assert.Nil(t, resolveSchemaRef("#/$defs/Input", nil))
}

func TestInferSchemaType(t *testing.T) {
	assert.Equal(t, "boolean", inferSchemaType(&tool.Schema{Type: "boolean"}))
	assert.Equal(t, "object", inferSchemaType(&tool.Schema{Properties: map[string]*tool.Schema{"a": {Type: "string"}}}))
	assert.Equal(t, "array", inferSchemaType(&tool.Schema{Items: &tool.Schema{Type: "string"}}))
	assert.Equal(t, "", inferSchemaType(&tool.Schema{}))
	assert.Equal(t, "", inferSchemaType(nil))
}

func TestValidateArgumentsAgainstSchema_NullArgsRefToObject(t *testing.T) {
	schema := &tool.Schema{
		Ref: "#/$defs/Input",
		Defs: map[string]*tool.Schema{
			"Input": {Properties: map[string]*tool.Schema{"a": {Type: "integer"}}},
		},
	}
	ok, reason := validateArgumentsAgainstSchema(nil, schema)
	assert.False(t, ok)
	assert.Contains(t, reason, "expected object")
}

func TestValidateArgumentsAgainstSchema_NullArgsUnknownRef(t *testing.T) {
	schema := &tool.Schema{
		Ref:  "#/$defs/Missing",
		Defs: map[string]*tool.Schema{"Other": {Type: "object"}},
	}
	ok, reason := validateArgumentsAgainstSchema(nil, schema)
	assert.True(t, ok)
	assert.Empty(t, reason)
}

func TestValidateArgumentsAgainstSchema_NullArgsSchemaTypes(t *testing.T) {
	tests := []struct {
		schema *tool.Schema
		substr string
	}{
		{schema: &tool.Schema{Type: "array"}, substr: "expected array"},
		{schema: &tool.Schema{Type: "string"}, substr: "expected string"},
		{schema: &tool.Schema{Type: "boolean"}, substr: "expected boolean"},
		{schema: &tool.Schema{Type: "integer"}, substr: "expected integer"},
		{schema: &tool.Schema{Type: "number"}, substr: "expected number"},
	}
	for _, tt := range tests {
		ok, reason := validateArgumentsAgainstSchema(nil, tt.schema)
		assert.False(t, ok)
		assert.Contains(t, reason, tt.substr)
	}
}

func TestValidateToolCallArguments_SkipsWhenDeclarationMissing(t *testing.T) {
	tests := []struct {
		name  string
		tools map[string]tool.Tool
	}{
		{
			name: "nil declaration",
			tools: map[string]tool.Tool{
				"t": stubTool{decl: nil},
			},
		},
		{
			name: "nil input schema",
			tools: map[string]tool.Tool{
				"t": stubTool{decl: &tool.Declaration{Name: "t", InputSchema: nil}},
			},
		},
		{
			name:  "tool missing",
			tools: map[string]tool.Tool{},
		},
	}
	for _, tt := range tests {
		ok, reason := validateToolCallArguments("t", map[string]any{"a": 1}, tt.tools)
		assert.True(t, ok)
		assert.Empty(t, reason)
	}
}

func TestValidateValueAgainstSchema_ScalarTypes(t *testing.T) {
	ok, reason := validateValueAgainstSchema(true, &tool.Schema{Type: "boolean"}, nil, "$")
	assert.True(t, ok)
	assert.Empty(t, reason)

	ok, reason = validateValueAgainstSchema("x", &tool.Schema{Type: "boolean"}, nil, "$")
	assert.False(t, ok)
	assert.Contains(t, reason, "expected boolean")

	ok, reason = validateValueAgainstSchema(json.Number("1.25"), &tool.Schema{Type: "number"}, nil, "$")
	assert.True(t, ok)
	assert.Empty(t, reason)

	ok, reason = validateValueAgainstSchema(json.Number("1e309"), &tool.Schema{Type: "number"}, nil, "$")
	assert.False(t, ok)
	assert.Contains(t, reason, "expected number")

	ok, reason = validateValueAgainstSchema(json.Number("1.25"), &tool.Schema{Type: "integer"}, nil, "$")
	assert.False(t, ok)
	assert.Contains(t, reason, "expected integer")

	ok, reason = validateValueAgainstSchema(1.0, &tool.Schema{Type: "integer"}, nil, "$")
	assert.False(t, ok)
	assert.Contains(t, reason, "expected integer")
}

func TestValidateValueAgainstSchema_ArrayItemsNil(t *testing.T) {
	ok, reason := validateValueAgainstSchema([]any{json.Number("1")}, &tool.Schema{Type: "array"}, nil, "$")
	assert.True(t, ok)
	assert.Empty(t, reason)
}

func TestValidateValueAgainstSchema_ArrayTypeMismatch(t *testing.T) {
	ok, reason := validateValueAgainstSchema(map[string]any{}, &tool.Schema{Type: "array"}, nil, "$")
	assert.False(t, ok)
	assert.Contains(t, reason, "expected array")
}

func TestSplitToolResults_GroupsByIDs(t *testing.T) {
	toolResults := []model.Message{
		{Role: model.RoleTool, ToolID: ""},
		{Role: model.RoleTool, ToolID: "valid"},
		{Role: model.RoleTool, ToolID: "invalid"},
		{Role: model.RoleTool, ToolID: "unknown"},
	}
	validIDs := map[string]struct{}{"valid": {}}
	invalidIDs := map[string]struct{}{"invalid": {}}
	split := splitToolResults(toolResults, validIDs, invalidIDs)
	assert.Len(t, split.kept, 1)
	assert.Len(t, split.invalidByID["invalid"], 1)
	assert.Len(t, split.orphan, 2)
}

func TestIsEmptyAssistantMessage(t *testing.T) {
	assert.True(t, isEmptyAssistantMessage(model.Message{Role: model.RoleAssistant}))
	assert.False(t, isEmptyAssistantMessage(model.Message{Role: model.RoleUser}))
	assert.False(t, isEmptyAssistantMessage(model.Message{Role: model.RoleAssistant, Content: "x"}))
	assert.False(t, isEmptyAssistantMessage(model.Message{Role: model.RoleAssistant, ReasoningContent: "x"}))
	assert.False(t, isEmptyAssistantMessage(model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call_1"}}}))
}
