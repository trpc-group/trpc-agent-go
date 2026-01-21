package processor

import (
	"context"
	"encoding/json"
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
