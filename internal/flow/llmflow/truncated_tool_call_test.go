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
	"testing"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func truncatedToolCallResponse(reason string) *model.Response {
	return &model.Response{
		Done:  true,
		Usage: &model.Usage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33},
		Choices: []model.Choice{{
			FinishReason: &reason,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "file_write",
						Arguments: []byte(`{"content":"truncated`),
					},
				}},
			},
		}},
	}
}

func processTruncatedResponse(
	t *testing.T,
	inv *agent.Invocation,
	req *model.Request,
	response *model.Response,
) (*event.Event, error, chan *event.Event) {
	t.Helper()
	f := New(nil, nil, Options{})
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(context.Background(), "s")
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		inv,
		nil,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	return lastEvent, err, eventChan
}

func TestProcessStreamingResponses_RejectsTruncatedToolCall(t *testing.T) {
	inv := agent.NewInvocation()
	lastEvent, err, eventChan := processTruncatedResponse(
		t,
		inv,
		&model.Request{},
		truncatedToolCallResponse(model.FinishReasonLength),
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.NotNil(t, lastEvent.Response.Error)
	require.Contains(t, lastEvent.Response.Error.Message, "truncated by the length limit")
	// The rejected call still reports the usage of the model response.
	require.NotNil(t, lastEvent.Response.Usage)
	require.Equal(t, 33, lastEvent.Response.Usage.TotalTokens)
	require.True(t, inv.EndInvocation)
	require.Len(t, eventChan, 1)
}

func TestProcessStreamingResponses_RejectsAnthropicOutputLimit(t *testing.T) {
	inv := agent.NewInvocation()
	lastEvent, err, _ := processTruncatedResponse(
		t,
		inv,
		&model.Request{},
		truncatedToolCallResponse("max_tokens"),
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.NotNil(t, lastEvent.Response.Error)
}

func TestProcessStreamingResponses_RejectsTextRepairedTruncatedToolCall(t *testing.T) {
	enabled := true
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		ToolCallTextRepairEnabled: &enabled,
	}))
	req := &model.Request{Tools: map[string]tool.Tool{
		"file_write": textRepairTool{name: "file_write"},
	}}
	reason := model.FinishReasonLength
	response := &model.Response{
		Done:  true,
		Usage: &model.Usage{TotalTokens: 7},
		Choices: []model.Choice{{
			FinishReason: &reason,
			Message: model.Message{
				Role: model.RoleAssistant,
				Content: "<tool_call>file_write" +
					"<arg_key>file_path</arg_key><arg_value>/tmp/x</arg_value>" +
					"</tool_call>",
			},
		}},
	}
	lastEvent, err, _ := processTruncatedResponse(t, inv, req, response)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.NotNil(t, lastEvent.Response.Error)
	require.Contains(t, lastEvent.Response.Error.Message, "truncated by the length limit")
	require.True(t, inv.EndInvocation)
}

func TestProcessStreamingResponses_IgnorePolicyKeepsTruncatedToolCall(t *testing.T) {
	inv := ignoreTruncatedToolCallsInvocation()
	lastEvent, err, eventChan := processTruncatedResponse(
		t,
		inv,
		&model.Request{},
		truncatedToolCallResponse(model.FinishReasonLength),
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, lastEvent.Response.Error)
	require.True(t, lastEvent.Response.IsToolCallResponse())
	require.False(t, inv.EndInvocation)
	require.Len(t, eventChan, 1)
}

// TestProcessStreamingResponses_KeepsValidToolCallWhenAnotherChoiceTruncated
// covers multi-choice responses: only the first choice is repaired and
// executed, so a truncated later choice must not reject a valid first one.
func TestProcessStreamingResponses_KeepsValidToolCallWhenAnotherChoiceTruncated(t *testing.T) {
	stopReason := "stop"
	truncatedReason := model.FinishReasonLength
	response := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				FinishReason: &stopReason,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "file_write",
							Arguments: []byte(`{"file_path":"/tmp/x"}`),
						},
					}},
				},
			},
			{
				FinishReason: &truncatedReason,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "truncated text",
				},
			},
		},
	}
	inv := agent.NewInvocation()
	lastEvent, err, _ := processTruncatedResponse(t, inv, &model.Request{}, response)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, lastEvent.Response.Error)
	require.True(t, lastEvent.Response.IsToolCallResponse())
	require.False(t, inv.EndInvocation)
}

func ignoreTruncatedToolCallsInvocation() *agent.Invocation {
	inv := agent.NewInvocation()
	inv.TruncatedToolCallPolicy = agent.TruncatedToolCallPolicyIgnore
	return inv
}

func TestTruncatedToolCallRejector(t *testing.T) {
	stopReason := "stop"
	partial := truncatedToolCallResponse(model.FinishReasonLength)
	partial.IsPartial = true
	notToolCall := truncatedToolCallResponse(model.FinishReasonLength)
	notToolCall.Choices[0].Message.ToolCalls = nil

	tests := []struct {
		name       string
		invocation *agent.Invocation
		response   *model.Response
		wantReject bool
	}{
		{
			name:       "nil response",
			invocation: agent.NewInvocation(),
			response:   nil,
			wantReject: false,
		},
		{
			name:       "no invocation",
			invocation: nil,
			response:   truncatedToolCallResponse(model.FinishReasonLength),
			wantReject: false,
		},
		{
			name:       "partial response",
			invocation: agent.NewInvocation(),
			response:   partial,
			wantReject: false,
		},
		{
			name:       "output limit without tool calls",
			invocation: agent.NewInvocation(),
			response:   notToolCall,
			wantReject: false,
		},
		{
			name:       "tool calls with stop reason",
			invocation: agent.NewInvocation(),
			response:   truncatedToolCallResponse(stopReason),
			wantReject: false,
		},
		{
			name:       "ignore policy",
			invocation: ignoreTruncatedToolCallsInvocation(),
			response:   truncatedToolCallResponse(model.FinishReasonLength),
			wantReject: false,
		},
		{
			name:       "default policy",
			invocation: agent.NewInvocation(),
			response:   truncatedToolCallResponse(model.FinishReasonLength),
			wantReject: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &streamingResponseProcessor{
				ctx:               context.Background(),
				currentInvocation: tt.invocation,
			}
			got := p.truncatedToolCallRejector(tt.response)
			if tt.wantReject {
				require.NotNil(t, got)
				return
			}
			require.Nil(t, got)
		})
	}
}
