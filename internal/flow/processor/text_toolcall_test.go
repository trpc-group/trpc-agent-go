//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubTool struct {
	name string
}

func (t stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func TestTextToolCall_ToFunctions_WithJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "note\n" +
						"to=functions.web_search\n" +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
	require.NotContains(t, rsp.Choices[0].Message.Content, "to=functions")
}

func TestTextToolCall_ToFunctions_WithNumericJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "to=functions.web_search\n" +
						"{\"query\":\"hi\",\"max_results\":5}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
	require.Equal(t, float64(5), got["max_results"])
}

func TestTextToolCall_ToFunctions_NoJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_fetch": stubTool{name: "web_fetch"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "prefix\n" +
						"/*ACTION to=functions.web_fetch X */\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_fetch", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Empty(t, got)
	require.NotContains(t, rsp.Choices[0].Message.Content, "to=functions")
}

func TestTextToolCall_FunctionsCall_WithParensJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: strings.Join(
						[]string{
							"note",
							"functions.web_search({\"query\":\"hi\"," +
								"\"max_results\":5})",
						},
						"\n",
					),
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
	require.Equal(t, float64(5), got["max_results"])
	require.NotContains(t, rsp.Choices[0].Message.Content, "functions.")
}

func TestTextToolCall_FunctionsCall_WithSpacedJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "functions.web_search " +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
	require.NotContains(t, rsp.Choices[0].Message.Content, "functions.")
}

func TestTextToolCall_FunctionsCall_NoJSONArgs(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_fetch": stubTool{name: "web_fetch"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "functions.web_fetch\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_fetch", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Empty(t, got)
}

func TestTextToolCall_FunctionsCall_UnknownTool(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "functions.unknown_tool " +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_FunctionsCall_InvalidJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "functions.web_search " +
						"{\"query\":\"hi\"\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Empty(t, got)
}

func TestTextToolCall_ToolObject(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "/*ACTION*/\n" +
						"{\"tool\":\"functions.web_search\"," +
						"\"parameters\":{\"query\":\"hi\"}}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
}

func TestTextToolCall_ToolObject_WithNumericParameters(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "{\"tool\":\"functions.web_search\"," +
						"\"parameters\":" +
						"{\"query\":\"hi\",\"max_results\":5}}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)

	var got map[string]any
	require.NoError(t, json.Unmarshal(tc.Function.Arguments, &got))
	require.Equal(t, "hi", got["query"])
	require.Equal(t, float64(5), got["max_results"])
}

func TestTextToolCall_SkipsWhenFinalAnswerTagPresent(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "/*FINAL_ANSWER*/\n" +
						"to=functions.web_search\n" +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_SkipsWhenFinalAnswerPrefixPresent(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: strings.Join(
						[]string{
							"FINAL ANSWER: 3",
							"functions.web_search({\"query\":\"hi\"})",
						},
						"\n",
					),
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_SkipsWhenPartialResponse(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		IsPartial: true,
		Done:      true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "to=functions.web_search\n" +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_SkipsWhenToolCallsAlreadyPresent(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Function: model.FunctionDefinitionParam{
								Name: "web_search",
							},
						},
					},
					Content: "to=functions.web_search\n" +
						"{\"query\":\"hi\"}\n",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	require.Equal(
		t,
		"web_search",
		rsp.Choices[0].Message.ToolCalls[0].Function.Name,
	)
}

func TestTextToolCall_ToolObject_ScansUntilJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "Note: \"tool\" in plain text.\n" +
						"{\"tool\":\"functions.web_search\"," +
						"\"parameters\":{\"query\":\"hi\"}}",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Len(t, rsp.Choices[0].Message.ToolCalls, 1)
	tc := rsp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "web_search", tc.Function.Name)
}

func TestTextToolCall_ToolObject_InvalidParameters(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "{\"tool\":\"functions.web_search\"," +
						"\"parameters\":\"hi\"}",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_ToolObject_MissingParameters(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "{\"tool\":\"functions.web_search\"}",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_ToolObject_UnknownTool(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: "{\"tool\":\"functions.nope\"," +
						"\"parameters\":{\"query\":\"hi\"}}",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_ToolObject_InvalidJSON(t *testing.T) {
	p := NewTextToolCallResponseProcessor()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"web_search": stubTool{name: "web_search"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "{\"tool\":\"functions.web_search\",",
				},
			},
		},
	}

	p.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		req,
		rsp,
		nil,
	)

	require.Empty(t, rsp.Choices[0].Message.ToolCalls)
}

func TestTextToolCall_IsToolNameChar(t *testing.T) {
	require.True(t, isToolNameChar('_'))
	require.True(t, isToolNameChar('-'))
	require.True(t, isToolNameChar('a'))
	require.True(t, isToolNameChar('Z'))
	require.True(t, isToolNameChar('0'))
	require.False(t, isToolNameChar('.'))
}

func TestTextToolCall_DropHelpers(t *testing.T) {
	cleaned := dropLineFrom("to=functions.web_search", 0)
	require.Equal(t, "", cleaned)

	content := "line1\nline2\nline3"
	unchanged := dropRangeFromLine(content, 7, 5)
	require.Equal(t, content, unchanged)
}
