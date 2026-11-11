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
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
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
	opts Options,
	inv *agent.Invocation,
	req *model.Request,
	response *model.Response,
) (*event.Event, error, chan *event.Event) {
	t.Helper()
	f := New(nil, nil, opts)
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
		Options{},
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

func TestProcessStreamingResponses_TraceRejectedTruncatedToolCall(t *testing.T) {
	tests := []struct {
		name, reason                             string
		fromText, rewriteReason, noUsage, parent bool
	}{
		{name: "native call", reason: model.FinishReasonLength},
		{name: "text repair", reason: "max_tokens", fromText: true},
		{name: "callback rewrites reason", reason: "MAX_TOKENS", rewriteReason: true},
		{name: "usage unavailable", reason: model.FinishReasonLength, noUsage: true},
		{name: "borrowed parent span", reason: model.FinishReasonLength, parent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			ctx, span := provider.Tracer("truncated-call-test").Start(context.Background(), "chat")
			req := &model.Request{}
			inv := agent.NewInvocation()
			response := truncatedToolCallResponse(tt.reason)
			response.Usage.PromptTokensDetails.CachedTokens = 3
			if tt.noUsage {
				response.Usage = nil
			}
			if tt.fromText {
				enabled := true
				inv = agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
					ToolCallTextRepairEnabled: &enabled,
				}))
				req.Tools = map[string]tool.Tool{"file_write": textRepairTool{name: "file_write"}}
				response.Choices[0].Message = model.NewAssistantMessage(
					"<tool_call>file_write<arg_key>file_path</arg_key><arg_value>/tmp/x</arg_value></tool_call>",
				)
			}
			opts := Options{}
			if tt.rewriteReason {
				opts.ModelCallbacks = model.NewCallbacks().RegisterAfterModel(
					func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
						*args.Response.Choices[0].FinishReason = "stop"
						return nil, nil
					},
				)
			}
			events := make(chan *event.Event, 10)
			seq := func(yield func(*model.Response) bool) { yield(response) }
			lastEvent, err := New(nil, nil, opts).processStreamingResponses(ctx, inv, nil, req, seq, events, span, !tt.parent)
			span.End()
			require.NoError(t, err)
			require.NotNil(t, lastEvent.Response.Error)
			require.Equal(t, model.ErrorTypeFlowError, lastEvent.Response.Error.Type)
			require.Equal(t, response.Usage, lastEvent.Response.Usage)
			require.True(t, inv.EndInvocation)
			require.Len(t, events, 1)
			require.False(t, (<-events).Response.IsToolCallResponse())

			var want []attribute.KeyValue
			if !tt.parent {
				want = append(want, attribute.StringSlice(semconvtrace.KeyGenAIResponseFinishReasons, []string{tt.reason}))
				if !tt.noUsage {
					want = append(want,
						attribute.Int(semconvtrace.KeyGenAIUsageInputTokens, 11),
						attribute.Int(semconvtrace.KeyGenAIUsageOutputTokens, 22),
						attribute.Int(semconvtrace.KeyGenAIUsageTotalTokens, 33),
						attribute.Int(semconvtrace.KeyGenAIUsageInputTokensCached, 3),
					)
				}
			}
			ended := recorder.Ended()
			require.Len(t, ended, 1)
			// The rejection adds metadata without tracing the tool payload.
			require.ElementsMatch(t, want, ended[0].Attributes())
		})
	}
}

func TestProcessStreamingResponses_RejectsAnthropicOutputLimit(t *testing.T) {
	inv := agent.NewInvocation()
	lastEvent, err, _ := processTruncatedResponse(
		t,
		Options{},
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
	lastEvent, err, _ := processTruncatedResponse(t, Options{}, inv, req, response)
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
		Options{},
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

func TestProcessStreamingResponses_TruncatedCallIdentityAfterCallback(t *testing.T) {
	const originalArgs = `{"content":"before"}`
	const changedArgs = `{"content":"edited"}`
	tests := []struct {
		name          string
		originalID    string
		callbackID    string
		callbackArgs  string
		wantRejection bool
	}{
		{"same ID with edited arguments", "call-1", "call-1", changedArgs, true},
		{"new ID with identical arguments", "call-1", "call-2", originalArgs, false},
		{"removed ID with identical arguments", "call-1", "", originalArgs, true},
		{"ID-less replacement edits arguments in place", "", "", changedArgs, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := truncatedToolCallResponse(model.FinishReasonLength)
			call := &response.Choices[0].Message.ToolCalls[0]
			call.ID = tt.originalID
			call.Function.Arguments = []byte(originalArgs)
			opts := Options{ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
				func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					choice := &args.Response.Choices[0]
					choice.FinishReason = nil
					call := &choice.Message.ToolCalls[0]
					call.ID = tt.callbackID
					// Mutate the original buffer to exercise the pre-callback snapshot.
					copy(call.Function.Arguments, tt.callbackArgs)
					return nil, nil
				},
			)}
			inv := agent.NewInvocation()
			lastEvent, err, events := processTruncatedResponse(t, opts, inv, &model.Request{}, response)
			require.NoError(t, err)
			require.NotNil(t, lastEvent)
			require.Len(t, events, 1)
			if tt.wantRejection {
				require.NotNil(t, lastEvent.Response.Error)
				require.Contains(t, lastEvent.Response.Error.Message, "truncated by the length limit")
				require.False(t, lastEvent.Response.IsToolCallResponse())
				require.True(t, inv.EndInvocation)
				return
			}
			require.Nil(t, lastEvent.Response.Error)
			require.False(t, inv.EndInvocation)
			require.True(t, lastEvent.Response.IsToolCallResponse())
			calls := lastEvent.Response.Choices[0].Message.ToolCalls
			require.Len(t, calls, 1)
			require.Equal(t, tt.callbackID, calls[0].ID)
			require.JSONEq(t, tt.callbackArgs, string(calls[0].Function.Arguments))
		})
	}
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
	lastEvent, err, _ := processTruncatedResponse(t, Options{}, inv, &model.Request{}, response)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, lastEvent.Response.Error)
	require.True(t, lastEvent.Response.IsToolCallResponse())
	require.Len(t, lastEvent.Response.Choices, 2)
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
				eventChan:         make(chan *event.Event, 1),
			}
			require.Equal(t, !tt.wantReject, p.prepareToolCalls(tt.invocation, tt.response, truncatedOutputSnapshot{}))
		})
	}
}
