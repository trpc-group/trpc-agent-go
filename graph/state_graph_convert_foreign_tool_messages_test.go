package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLLMRunner_convertForeignToolMessages_NoTools_ConvertsAll(t *testing.T) {
	runner := &llmRunner{}
	messages := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "c1",
					Function: model.FunctionDefinitionParam{
						Name:      "weather",
						Arguments: []byte(`{"city":"SF"}`),
					},
				},
			},
		},
		model.NewToolMessage("c1", "weather", `{"temp":20}`),
		model.NewAssistantMessage("done"),
	}

	out := runner.convertForeignToolMessages(messages, nil)
	require.Len(t, out, 3)

	require.Equal(t, model.RoleUser, out[0].Role)
	require.Contains(t, out[0].Content, "For context:")
	require.Contains(t, out[0].Content, "Tool `weather` called with parameters: {\"city\":\"SF\"}")
	require.Empty(t, out[0].ToolCalls)

	require.Equal(t, model.RoleUser, out[1].Role)
	require.Contains(t, out[1].Content, "`c1` tool returned result: {\"temp\":20}")
	require.Empty(t, out[1].ToolCalls)

	require.Equal(t, model.RoleAssistant, out[2].Role)
	require.Equal(t, "done", out[2].Content)
	require.Empty(t, out[2].ToolCalls)
}

func TestLLMRunner_convertForeignToolMessages_ToolSubset_ConvertsOnlyForeign(t *testing.T) {
	runner := &llmRunner{}
	messages := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "c1",
					Function: model.FunctionDefinitionParam{
						Name:      "weather",
						Arguments: []byte(`{"city":"SF"}`),
					},
				},
				{
					ID: "c2",
					Function: model.FunctionDefinitionParam{
						Name:      "math",
						Arguments: []byte(`{"x":1}`),
					},
				},
			},
		},
		model.NewToolMessage("c1", "weather", `{"temp":20}`),
		model.NewToolMessage("c2", "math", `{"y":2}`),
	}
	tools := map[string]tool.Tool{
		"weather": nil,
	}

	out := runner.convertForeignToolMessages(messages, tools)
	require.Len(t, out, 4)

	require.Equal(t, model.RoleAssistant, out[0].Role)
	require.Len(t, out[0].ToolCalls, 1)
	require.Equal(t, "weather", out[0].ToolCalls[0].Function.Name)

	require.Equal(t, model.RoleUser, out[1].Role)
	require.Contains(t, out[1].Content, "For context:")
	require.Contains(t, out[1].Content, "Tool `math` called with parameters: {\"x\":1}")

	require.Equal(t, model.RoleTool, out[2].Role)
	require.Equal(t, "c1", out[2].ToolID)
	require.Equal(t, "weather", out[2].ToolName)

	require.Equal(t, model.RoleUser, out[3].Role)
	require.Contains(t, out[3].Content, "For context:")
	require.Contains(t, out[3].Content, "`c2` tool returned result: {\"y\":2}")
}

func TestLLMRunner_convertForeignToolMessages_MixedContent_PreservesAssistantContentAndLocalCalls(t *testing.T) {
	runner := &llmRunner{}
	messages := []model.Message{
		model.NewUserMessage("Please check the weather and also run the local tool."),
		{
			Role:    model.RoleAssistant,
			Content: "Sure, I'll do both.",
			ToolCalls: []model.ToolCall{
				{
					ID: "local-1",
					Function: model.FunctionDefinitionParam{
						Name:      "local_tool",
						Arguments: []byte(`{"foo":"bar"}`),
					},
				},
				{
					ID: "foreign-1",
					Function: model.FunctionDefinitionParam{
						Name:      "foreign_tool",
						Arguments: []byte(`{"foo":"baz"}`),
					},
				},
			},
		},
	}
	tools := map[string]tool.Tool{
		"local_tool": nil,
	}

	out := runner.convertForeignToolMessages(messages, tools)
	require.Len(t, out, 3)

	require.Equal(t, model.RoleUser, out[0].Role)
	require.Equal(t, messages[0].Content, out[0].Content)

	require.Equal(t, model.RoleAssistant, out[1].Role)
	require.Equal(t, "Sure, I'll do both.", out[1].Content)
	require.Len(t, out[1].ToolCalls, 1)
	require.Equal(t, "local_tool", out[1].ToolCalls[0].Function.Name)
	require.Equal(t, "local-1", out[1].ToolCalls[0].ID)

	require.Equal(t, model.RoleUser, out[2].Role)
	require.Contains(t, out[2].Content, "For context:")
	require.Contains(t, out[2].Content, "Tool `foreign_tool` called with parameters: {\"foo\":\"baz\"}")
}

func TestLLMRunner_convertForeignToolMessages_AllToolsPresent_NoOp(t *testing.T) {
	runner := &llmRunner{}
	messages := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID: "c1",
					Function: model.FunctionDefinitionParam{
						Name:      "weather",
						Arguments: []byte(`{}`),
					},
				},
			},
		},
		model.NewToolMessage("c1", "weather", `{}`),
	}
	tools := map[string]tool.Tool{
		"weather": nil,
	}

	out := runner.convertForeignToolMessages(messages, tools)
	require.Equal(t, messages, out)
}
