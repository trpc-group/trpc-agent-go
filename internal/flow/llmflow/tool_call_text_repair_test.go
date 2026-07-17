//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	gooteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type textRepairTool struct {
	name string
}

func (t textRepairTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func TestToolCallTextRepairEnabled(t *testing.T) {
	t.Parallel()

	require.False(t, isToolCallTextRepairEnabled(nil))

	inv := &agent.Invocation{}
	require.False(t, isToolCallTextRepairEnabled(inv))

	inv.RunOptions = agent.NewRunOptions(
		agent.WithToolCallTextRepairEnabled(true),
	)
	require.True(t, isToolCallTextRepairEnabled(inv))

	inv.RunOptions = agent.NewRunOptions(
		agent.WithToolCallTextRepairEnabled(false),
	)
	require.False(t, isToolCallTextRepairEnabled(inv))
}

func TestRepairResponseToolCallTextInPlace(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "Let me inspect it." +
					"<tool_call>exec_command" +
					"<arg_key>command</arg_key>" +
					"<arg_value>python3 -c \"print(1)\"</arg_value>" +
					"<arg_key>timeout_sec</arg_key>" +
					"<arg_value>15</arg_value>" +
					"</tool_call>",
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.True(t, resp.IsToolCallResponse())
	require.Equal(t, "Let me inspect it.", resp.Choices[0].Message.Content)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	call := resp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "auto_text_call_0", call.ID)
	require.Equal(t, "exec_command", call.Function.Name)
	require.JSONEq(
		t,
		`{"command":"python3 -c \"print(1)\"","timeout_sec":15}`,
		string(call.Function.Arguments),
	)
	require.NotNil(t, call.Index)
	require.Equal(t, 0, *call.Index)
	require.NotNil(t, resp.Choices[0].FinishReason)
	require.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
}

func TestRepairResponseToolCallTextInPlace_PreservesIntegerPrecision(
	t *testing.T,
) {
	t.Parallel()

	const maxInt64 = "9223372036854775807"
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"lookup": textRepairTool{name: "lookup"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>lookup" +
					"<arg_key>id</arg_key>" +
					"<arg_value>" + maxInt64 + "</arg_value>" +
					"<arg_key>nested</arg_key>" +
					"<arg_value>{\"ids\":[" + maxInt64 +
					",1]}</arg_value></tool_call>",
			},
		}},
	}

	repaired := repairResponseToolCallTextInPlace(
		context.Background(),
		req,
		resp,
	)

	require.True(t, repaired)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	var args map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(
		resp.Choices[0].Message.ToolCalls[0].Function.Arguments,
		&args,
	))
	require.Equal(t, maxInt64, string(args["id"]))
	require.Equal(
		t,
		`{"ids":[9223372036854775807,1]}`,
		string(args["nested"]),
	)
}

func TestToolCallTextRepair_BuffersPartialResponses(t *testing.T) {
	t.Parallel()

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-text-repair-stream"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithToolCallTextRepairEnabled(true),
		)),
	)
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	partial := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(
				"<tool_call>exec_command<arg_key>",
			),
		}},
	}
	terminal := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(
				"<tool_call>exec_command" +
					"<arg_key>command</arg_key>" +
					"<arg_value>echo ok</arg_value>" +
					"</tool_call>",
			),
		}},
	}
	seq := func(yield func(*model.Response) bool) {
		if yield(partial) {
			yield(terminal)
		}
	}
	eventChan := make(chan *event.Event, 2)
	tracer := gooteltrace.NewNoopTracerProvider().Tracer("test")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), inv),
		"stream",
	)
	defer span.End()

	lastEvent, err := New(nil, nil, Options{}).
		processStreamingResponses(
			ctx,
			inv,
			nil,
			req,
			seq,
			eventChan,
			span,
			true,
		)

	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Len(t, eventChan, 1)
	require.True(t, lastEvent.Response.IsToolCallResponse())
	require.NotContains(
		t,
		lastEvent.Response.Choices[0].Message.Content,
		textToolCallOpenTag,
	)
}

func TestRepairResponseToolCallTextInPlace_MultipleCalls(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
			"current_time": textRepairTool{name: "current_time"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "Let me inspect it.\n" +
					"<tool_call>exec_command" +
					"<arg_key>command</arg_key>" +
					"<arg_value>echo ok</arg_value>" +
					"</tool_call>\n\n" +
					"<tool_call>current_time</tool_call>\n",
			},
		}},
	}

	repaired := repairResponseToolCallTextInPlace(
		context.Background(),
		req,
		resp,
	)

	require.True(t, repaired)
	require.True(t, resp.IsToolCallResponse())
	require.Equal(t, "Let me inspect it.", resp.Choices[0].Message.Content)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 2)

	first := resp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "auto_text_call_0", first.ID)
	require.Equal(t, "exec_command", first.Function.Name)
	require.JSONEq(t, `{"command":"echo ok"}`, string(first.Function.Arguments))
	require.NotNil(t, first.Index)
	require.Equal(t, 0, *first.Index)

	second := resp.Choices[0].Message.ToolCalls[1]
	require.Equal(t, "auto_text_call_1", second.ID)
	require.Equal(t, "current_time", second.Function.Name)
	require.JSONEq(t, `{}`, string(second.Function.Arguments))
	require.NotNil(t, second.Index)
	require.Equal(t, 1, *second.Index)

	require.NotNil(t, resp.Choices[0].FinishReason)
	require.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
}

func TestRepairResponseToolCallTextInPlace_NoArgs(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"current_time": textRepairTool{name: "current_time"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "<tool_call>current_time</tool_call>",
			},
		}},
	}

	repaired := repairResponseToolCallTextInPlace(
		context.Background(),
		req,
		resp,
	)

	require.True(t, repaired)
	require.True(t, resp.IsToolCallResponse())
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	call := resp.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "current_time", call.Function.Name)
	require.JSONEq(t, `{}`, string(call.Function.Arguments))
}

func TestRepairResponseToolCallTextInPlace_GuardsPreserveText(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	toolText := "<tool_call>exec_command" +
		"<arg_key>command</arg_key><arg_value>echo x</arg_value>" +
		"</tool_call>"
	tests := []struct {
		name string
		resp *model.Response
	}{
		{
			name: "partial",
			resp: &model.Response{
				IsPartial: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: toolText,
					},
				}},
			},
		},
		{
			name: "tool result",
			resp: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call_1",
						Content: toolText,
					},
				}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repaired := repairResponseToolCallTextInPlace(
				context.Background(),
				req,
				tt.resp,
			)

			require.False(t, repaired)
			require.False(t, tt.resp.IsToolCallResponse())
			require.Contains(t, tt.resp.Choices[0].Message.Content, toolText)
			require.Empty(t, tt.resp.Choices[0].Message.ToolCalls)
			require.Nil(t, tt.resp.Choices[0].FinishReason)
		})
	}
}

func TestRepairToolCallTextDisabledPaths(t *testing.T) {
	t.Parallel()

	resp := &model.Response{}
	processor := &streamingResponseProcessor{ctx: context.Background()}

	require.False(t, processor.repairToolCallText(resp))

	processor.currentInvocation = &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolCallTextRepairEnabled(false),
		),
	}
	require.False(t, processor.repairToolCallText(resp))
}

func TestRepairToolCallTextAndStatsCountsRepairedToolResponse(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>exec_command" +
					"<arg_key>command</arg_key>" +
					"<arg_value>echo ok</arg_value>" +
					"</tool_call>",
			},
		}},
	}
	processor := &streamingResponseProcessor{
		ctx: context.Background(),
		currentInvocation: &agent.Invocation{
			RunOptions: agent.NewRunOptions(
				agent.WithToolCallTextRepairEnabled(true),
			),
		},
		llmRequest: req,
	}

	processor.recordResponseStats(resp)
	processor.repairToolCallTextAndStats(resp)

	require.True(t, resp.IsToolCallResponse())
	require.Equal(t, 1, processor.responseCount)
	require.Equal(t, 1, processor.toolResponseCount)
}

func TestRepairResponseToolCallTextInPlace_UnknownToolPreservesText(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"known": textRepairTool{name: "known"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>missing" +
					"<arg_key>query</arg_key><arg_value>x</arg_value>" +
					"</tool_call>",
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.False(t, resp.IsToolCallResponse())
	require.Contains(t, resp.Choices[0].Message.Content, "<tool_call>")
	require.Nil(t, resp.Choices[0].FinishReason)
}

func TestRepairResponseToolCallTextInPlace_NoVisibleToolsPreservesText(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{Tools: nil}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>exec_command" +
					"<arg_key>command</arg_key><arg_value>echo x</arg_value>" +
					"</tool_call>",
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.False(t, resp.IsToolCallResponse())
	require.Contains(t, resp.Choices[0].Message.Content, "<tool_call>")
}

func TestRepairResponseToolCallTextInPlace_MalformedPreservesText(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>exec_command" +
					"<arg_key>command</arg_key>" +
					"<arg_value>echo x</tool_call>",
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.False(t, resp.IsToolCallResponse())
	require.Contains(t, resp.Choices[0].Message.Content, "<tool_call>")
}

func TestRepairResponseToolCallTextInPlace_TrailingTextPreservesText(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "Example: <tool_call>exec_command" +
					"<arg_key>command</arg_key><arg_value>echo x</arg_value>" +
					"</tool_call> means run a command.",
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.False(t, resp.IsToolCallResponse())
	require.Contains(t, resp.Choices[0].Message.Content, "means run a command")
}

func TestRepairResponseToolCallTextInPlace_ExistingToolCallPreserved(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "<tool_call>exec_command</tool_call>",
				ToolCalls: []model.ToolCall{{
					ID: "structured",
					Function: model.FunctionDefinitionParam{
						Name: "exec_command",
					},
				}},
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "structured", resp.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(
		t,
		"<tool_call>exec_command</tool_call>",
		resp.Choices[0].Message.Content,
	)
}

func TestRepairResponseToolCallTextInPlace_TextContentParts(t *testing.T) {
	t.Parallel()

	text := "<tool_call>exec_command" +
		"<arg_key>command</arg_key><arg_value>echo ok</arg_value>" +
		"</tool_call>"
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: &text,
				}},
			},
		}},
	}

	repairResponseToolCallTextInPlace(context.Background(), req, resp)

	require.True(t, resp.IsToolCallResponse())
	require.Empty(t, resp.Choices[0].Message.ContentParts)
	require.Empty(t, resp.Choices[0].Message.Content)
}

func TestRepairableMessageTextSkipCases(t *testing.T) {
	t.Parallel()

	text := ""
	tests := []struct {
		name string
		msg  *model.Message
	}{
		{name: "nil"},
		{name: "blank", msg: &model.Message{Content: "   "}},
		{
			name: "non text content part",
			msg: &model.Message{
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeImage,
				}},
			},
		},
		{
			name: "nil text content part",
			msg: &model.Message{
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: &text,
				}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := repairableMessageText(tt.msg)

			require.False(t, ok)
			require.Empty(t, got)
		})
	}
}

func TestParseTextToolCallsRejectsInvalidBlocks(t *testing.T) {
	t.Parallel()

	tools := map[string]tool.Tool{
		"exec_command": textRepairTool{name: "exec_command"},
	}
	tests := []struct {
		name string
		text string
	}{
		{
			name: "inter block text",
			text: "<tool_call>exec_command</tool_call> then " +
				"<tool_call>exec_command</tool_call>",
		},
		{
			name: "missing close",
			text: "<tool_call>exec_command",
		},
		{
			name: "empty tool",
			text: "<tool_call></tool_call>",
		},
		{
			name: "invalid argument",
			text: "<tool_call>exec_command" +
				"<arg_key>command</arg_key>bad" +
				"<arg_value>echo x</arg_value></tool_call>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cleaned, calls, ok := parseTextToolCalls(tt.text, tools)

			require.False(t, ok)
			require.Equal(t, tt.text, cleaned)
			require.Nil(t, calls)
		})
	}
}

func TestParseTextToolCallsNoMarkup(t *testing.T) {
	t.Parallel()

	cleaned, calls, ok := parseTextToolCalls(
		"plain text",
		map[string]tool.Tool{
			"exec_command": textRepairTool{name: "exec_command"},
		},
	)

	require.False(t, ok)
	require.Equal(t, "plain text", cleaned)
	require.Nil(t, calls)
}

func TestParseTextToolCallArgsRejectsMalformed(t *testing.T) {
	t.Parallel()

	tests := []string{
		"garbage",
		"<arg_key></arg_key><arg_value>x</arg_value>",
		"<arg_key>command</arg_key>bad<arg_value>x</arg_value>",
		"<arg_key>command</arg_key><arg_value>x",
	}

	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			got, ok := parseTextToolCallArgs(input)

			require.False(t, ok)
			require.Nil(t, got)
		})
	}
}

func TestParseTextToolCallArgValueEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, parseTextToolCallArgValue("   "))
}
