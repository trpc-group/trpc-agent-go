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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

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
