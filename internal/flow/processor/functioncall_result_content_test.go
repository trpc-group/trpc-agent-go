//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

func imageToolResultContent() model.ToolResultContent {
	text := "image <one>"
	return model.ToolResultContent{
		{Type: model.ContentTypeText, Text: &text},
		{Type: model.ContentTypeImage, Image: &model.Image{
			Data: []byte{1, 2, 3}, Format: "png",
		}},
	}
}

func TestExecutionTraceToolResultContent(t *testing.T) {
	empty := ""
	for _, tt := range []struct {
		name    string
		content string
		parts   []model.ContentPart
		want    string
	}{
		{
			name:  "mixed content",
			parts: []model.ContentPart(imageToolResultContent()),
			want:  `[{"type":"text","text":"image <one>"},{"type":"image","image":{"url":"","data":"AQID","format":"png"}}]`,
		},
		{
			name: "externalized content",
			parts: []model.ContentPart{{Type: model.ContentTypeImage, ContentRef: &model.ContentRef{
				ArtifactRef: "artifact://image.png@0", MimeType: "image/png",
			}}},
			want: `[{"type":"image","content_ref":{"artifact_ref":"artifact://image.png@0","artifact_version":0,"mime_type":"image/png"}}]`,
		},
		{
			name:  "empty receipt",
			parts: []model.ContentPart{{Type: model.ContentTypeText, Text: &empty}},
			want:  `[{"type":"text","text":""}]`,
		},
		{
			name: "legacy content takes precedence", content: "legacy",
			parts: []model.ContentPart(imageToolResultContent()), want: `"legacy"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inv, ctx := newExecutionTraceProcessorInvocation(t)
			recordExecutionTraceToolResult(ctx, inv, toolResult{
				event: &event.Event{Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleTool, ToolID: "call-read", ToolName: "read",
						Content: tt.content, ContentParts: tt.parts,
					},
				}}}},
				toolArgs: []byte(`{}`),
			})
			executionTrace := agent.BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
			require.NotNil(t, executionTrace)
			require.Len(t, executionTrace.Steps, 1)
			require.Len(t, executionTrace.Steps[0].Tools, 1)
			recorded := executionTrace.Steps[0].Tools[0]
			require.Equal(t, "call-read", recorded.ID)
			require.Equal(t, "read", recorded.Name)
			encoded, err := json.Marshal(recorded.Result)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(encoded))
		})
	}
}

func TestExecuteToolCall_ResultContentKeepsLegacyResults(t *testing.T) {
	content := imageToolResultContent()
	for _, tt := range []struct {
		name   string
		result any
	}{
		{name: "untyped nil"},
		{name: "string", result: "<plain>"},
		{name: "bytes", result: []byte{1, 2, 3}},
		{name: "map", result: map[string]string{"output": "<plain>"}},
		{name: "bare content parts", result: []model.ContentPart(content)},
		{name: "pointer to content", result: &content},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ft := function.NewFunctionTool(
				func(context.Context, struct{}) (any, error) { return tt.result, nil },
				function.WithName("read"),
				function.WithDisableOutputSchemaGen(),
			)
			choices := requireResultFormatToolCall(t,
				NewFunctionCallResponseProcessor(false, nil), "read", ft)
			require.Len(t, choices, 1)
			want, err := marshalJSONNoHTMLEscape(tt.result)
			require.NoError(t, err)
			assert.Equal(t, string(want), choices[0].Message.Content)
			assert.Empty(t, choices[0].Message.ContentParts)
			assert.Equal(t, "call-1", choices[0].Message.ToolID)
		})
	}
}

func TestExecuteToolCall_ResultContentUsesAfterToolReplacement(t *testing.T) {
	result := imageToolResultContent()
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(context.Context, *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: result}, nil
	})
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (string, error) { return "original", nil },
		function.WithName("read"),
	)
	choices := requireResultFormatToolCall(t,
		NewFunctionCallResponseProcessor(false, callbacks), "read", ft)
	require.Len(t, choices, 1)
	msg := choices[0].Message
	assert.Equal(t, model.RoleTool, msg.Role)
	assert.Equal(t, "call-1", msg.ToolID)
	assert.Equal(t, "read", msg.ToolName)
	assert.Empty(t, msg.Content)
	assert.Equal(t, []model.ContentPart(result), msg.ContentParts)
	result[1].Image.Data[0] = 9
	assert.Equal(t, byte(1), msg.ContentParts[1].Image.Data[0])
}

func TestExecuteToolCall_ResultContentFormatterAndHookPrecedence(t *testing.T) {
	for _, formatted := range []bool{false, true} {
		name := "structured"
		if formatted {
			name = "formatted"
		}
		t.Run(name, func(t *testing.T) {
			result := imageToolResultContent()
			var formatterCalls int
			opts := []function.Option{function.WithName("read")}
			if formatted {
				opts = append(opts, function.WithResultFormatter(
					resultformat.FormatterFunc[model.ToolResultContent](func(
						_ context.Context, got model.ToolResultContent,
					) (string, error) {
						formatterCalls++
						assert.Equal(t, result, got)
						return "formatted", nil
					}),
				))
			}
			ft := function.NewFunctionTool(
				func(context.Context, struct{}) (model.ToolResultContent, error) { return result, nil },
				opts...,
			)
			callbacks := tool.NewCallbacks()
			callbacks.RegisterToolResultMessages(func(_ context.Context, input *tool.ToolResultMessagesInput) (any, error) {
				assert.Equal(t, result, input.Result)
				msg := input.DefaultToolMessage.(model.Message)
				if formatted {
					assert.Equal(t, "formatted", msg.Content)
					assert.Empty(t, msg.ContentParts)
				} else {
					assert.Empty(t, msg.Content)
					assert.Equal(t, []model.ContentPart(result), msg.ContentParts)
				}
				return model.NewToolMessage(input.ToolCallID, input.ToolName, "overridden"), nil
			})
			choices := requireResultFormatToolCall(t,
				NewFunctionCallResponseProcessor(false, callbacks), "read", ft)
			require.Len(t, choices, 1)
			assert.Equal(t, "overridden", choices[0].Message.Content)
			assert.Empty(t, choices[0].Message.ContentParts)
			if formatted {
				assert.Equal(t, 1, formatterCalls)
			} else {
				assert.Zero(t, formatterCalls)
			}
		})
	}
}

func TestExecuteToolCall_ResultContentStateDelta(t *testing.T) {
	for _, tt := range []struct {
		name      string
		formatted bool
		callback  resultFormatCallbackMode
	}{
		{name: "structured"},
		{name: "keep default", callback: callbackModeKeepDefault},
		{name: "rewrite", callback: callbackModeRewrite},
		{name: "formatted", formatted: true},
		{name: "formatted keep default", formatted: true, callback: callbackModeKeepDefault},
		{name: "formatted rewrite", formatted: true, callback: callbackModeRewrite},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := imageToolResultContent()
			wantRaw, err := json.Marshal(result)
			require.NoError(t, err)
			opts := []function.Option{function.WithName("read")}
			if tt.formatted {
				opts = append(opts, function.WithResultFormatter(
					resultformat.FormatterFunc[model.ToolResultContent](func(context.Context, model.ToolResultContent) (string, error) {
						return "formatted", nil
					}),
				))
			}
			stateful := &statefulFunctionTool[struct{}, model.ToolResultContent]{
				FunctionTool: function.NewFunctionTool(
					func(context.Context, struct{}) (model.ToolResultContent, error) { return result, nil },
					opts...,
				),
			}
			var seenDefault string
			callbacks := newResultFormatCallbacks(tt.callback, &seenDefault)
			evt, err := NewFunctionCallResponseProcessor(false, callbacks).executeSingleToolCallSequential(
				context.Background(),
				&agent.Invocation{AgentName: "agent", Model: &mockModel{}, Session: &session.Session{}},
				&model.Response{},
				map[string]tool.Tool{"read": stateful},
				make(chan *event.Event, 32),
				0,
				model.ToolCall{ID: "call-1", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}},
			)
			require.NoError(t, err)
			require.NotNil(t, evt)
			if tt.callback == callbackModeRewrite {
				assert.Equal(t, statefulRewritten, string(stateful.stateContent))
			} else {
				assert.JSONEq(t, string(wantRaw), string(stateful.stateContent))
			}
			assert.Equal(t, []byte("updated"), evt.StateDelta["state"])
		})
	}
}

func TestAfterToolMessagesHook_ObservesEmptyResultContent(t *testing.T) {
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (model.ToolResultContent, error) { return nil, nil },
		function.WithName("read"),
	)
	p := NewFunctionCallResponseProcessor(false, nil)
	choices := requireResultFormatToolCall(t, p, "read", ft)
	require.Len(t, choices, 1)
	assert.Empty(t, choices[0].Message.Content)
	toolEvent := event.NewResponseEvent("inv", "agent", &model.Response{
		Object: model.ObjectTypeToolResponse, Choices: choices,
	})
	var called bool
	mgr := plugin.MustNewManager(afterToolMessagesTestPlugin{
		hook: func(_ context.Context, args *plugin.AfterToolMessagesArgs) (*plugin.AfterToolMessagesResult, error) {
			called = true
			require.Len(t, args.ToolResultMessages, 1)
			assert.Equal(t, "call-1", args.ToolResultMessages[0].ToolID)
			assert.Empty(t, args.ToolResultMessages[0].Content)
			return &plugin.AfterToolMessagesResult{ToolResultMessages: []model.Message{
				model.NewToolMessage("call-1", "read", "acknowledged"),
			}}, nil
		},
	})
	err := p.applyAfterToolMessagesHooks(context.Background(),
		&agent.Invocation{InvocationID: "inv", AgentName: "agent", Plugins: mgr},
		&model.Request{}, nil, toolEvent)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "acknowledged", toolEvent.Choices[0].Message.Content)
}
