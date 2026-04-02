//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcallid

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestNew(t *testing.T) {
	t.Parallel()
	p := New()
	require.Equal(t, defaultPluginName, p.Name())
}

func TestPlugin_NameWithNilReceiverReturnsEmptyString(t *testing.T) {
	t.Parallel()
	var p *plugin
	require.Empty(t, p.Name())
}

func TestPlugin_RegisterHandlesNilReceiverAndRegistry(t *testing.T) {
	t.Parallel()
	var p *plugin
	require.NotPanics(t, func() {
		p.Register(nil)
	})
	plugin := newPlugin()
	require.NotPanics(t, func() {
		plugin.Register(nil)
	})
	registry := &pluginbase.Registry{}
	require.NotPanics(t, func() {
		plugin.Register(registry)
	})
}

func TestPlugin_AfterModelNoOpsOnNilInputs(t *testing.T) {
	t.Parallel()
	plugin := newPlugin()
	result, err := plugin.afterModel(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, result)
	result, err = plugin.afterModel(context.Background(), &model.AfterModelArgs{})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPlugin_AfterModelBestEffortWithoutInvocation(t *testing.T) {
	t.Parallel()
	plugin := newPlugin()
	rsp := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
		}},
	}
	_, err := plugin.afterModel(context.Background(), &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.Equal(t, canonicalToolCallID("", "rsp-1", "call-1", 0, 0), rsp.Choices[0].Message.ToolCalls[0].ID)
}

func TestPlugin_AfterModelCanonicalizesWithInvocationFromContext(t *testing.T) {
	t.Parallel()
	plugin := newPlugin()
	rsp := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 2,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-9",
					Type: "function",
				}},
			},
		}},
	}
	ctx := agent.NewInvocationContext(context.Background(), agent.NewInvocation(agent.WithInvocationID("inv-ctx")))
	_, err := plugin.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.Equal(t, canonicalToolCallID("inv-ctx", "rsp-1", "call-9", 2, 0), rsp.Choices[0].Message.ToolCalls[0].ID)
}

func TestPlugin_Integration_CanonicalIDPropagatesAcrossCallbacksToolExecutionAndNextRequest(t *testing.T) {
	modelStub := &capturingToolLoopModel{}
	var localAfterModelSeenID string
	localModelCallbacks := model.NewCallbacks().RegisterAfterModel(func(
		ctx context.Context,
		args *model.AfterModelArgs,
	) (*model.AfterModelResult, error) {
		if args == nil || args.Response == nil {
			return nil, nil
		}
		for _, choice := range args.Response.Choices {
			if len(choice.Message.ToolCalls) == 0 {
				continue
			}
			localAfterModelSeenID = choice.Message.ToolCalls[0].ID
			break
		}
		return nil, nil
	})
	var toolContextCallID string
	echoTool := function.NewFunctionTool(
		func(ctx context.Context, input struct {
			Value string `json:"value"`
		}) (string, error) {
			toolContextCallID, _ = tool.ToolCallIDFromContext(ctx)
			return input.Value, nil
		},
		function.WithName("echo"),
		function.WithDescription("Echoes the input."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithModelCallbacks(localModelCallbacks),
		llmagent.WithTools([]tool.Tool{echoTool}),
	)
	run := runner.NewRunner("toolcallid-app", ag, runner.WithPlugins(New()))
	t.Cleanup(func() {
		require.NoError(t, run.Close())
	})
	eventCh, err := run.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	events := collectEvents(eventCh)
	require.NotEmpty(t, events)
	invocationID := firstInvocationID(events)
	require.NotEmpty(t, invocationID)
	expectedToolCallID := canonicalToolCallID(invocationID, "rsp-1", "call-1", 0, 0)
	require.Equal(t, expectedToolCallID, localAfterModelSeenID)
	require.Equal(t, expectedToolCallID, toolContextCallID)
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	assistantToolCall, ok := firstAssistantToolCall(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, assistantToolCall.ID)
	toolResultMessage, ok := firstToolResultMessage(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, toolResultMessage.ToolID)
}

func TestPlugin_Integration_StreamingFinalToolCallResponseCanonicalizesOnlyFinalResponse(t *testing.T) {
	modelStub := &scriptedResponseModel{
		name: "streaming-final-tool-call-model",
		calls: []scriptedResponseCall{
			{
				responses: []*model.Response{
					{
						ID:        "rsp-stream",
						Object:    model.ObjectTypeChatCompletionChunk,
						IsPartial: true,
						Choices: []model.Choice{{
							Index: 0,
							Delta: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{{
									ID:    "call-1",
									Type:  "function",
									Index: intPtr(0),
								}},
							},
						}},
					},
					{
						ID:        "rsp-stream",
						Object:    model.ObjectTypeChatCompletion,
						Done:      true,
						IsPartial: false,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{{
									ID:   "call-1",
									Type: "function",
									Function: model.FunctionDefinitionParam{
										Name:      "echo",
										Arguments: []byte(`{"value":"ok"}`),
									},
								}},
							},
						}},
					},
				},
			},
			{
				responses: []*model.Response{{
					ID:        "rsp-finish",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("done"),
					}},
				}},
			},
		},
	}
	var (
		mu             sync.Mutex
		seenResponses  []*model.Response
		toolContextIDs []string
	)
	localModelCallbacks := model.NewCallbacks().RegisterAfterModel(func(
		ctx context.Context,
		args *model.AfterModelArgs,
	) (*model.AfterModelResult, error) {
		if args == nil || args.Response == nil {
			return nil, nil
		}
		mu.Lock()
		seenResponses = append(seenResponses, cloneToolCallTestResponse(args.Response))
		mu.Unlock()
		return nil, nil
	})
	echoTool := function.NewFunctionTool(
		func(ctx context.Context, input struct {
			Value string `json:"value"`
		}) (string, error) {
			toolCallID, _ := tool.ToolCallIDFromContext(ctx)
			mu.Lock()
			toolContextIDs = append(toolContextIDs, toolCallID)
			mu.Unlock()
			return input.Value, nil
		},
		function.WithName("echo"),
		function.WithDescription("Echoes the input."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithModelCallbacks(localModelCallbacks),
		llmagent.WithTools([]tool.Tool{echoTool}),
	)
	run := runner.NewRunner("toolcallid-streaming-app", ag, runner.WithPlugins(New()))
	t.Cleanup(func() {
		require.NoError(t, run.Close())
	})
	eventCh, err := run.Run(
		context.Background(),
		"user-stream",
		"session-stream",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	events := collectEvents(eventCh)
	invocationID := firstInvocationID(events)
	require.NotEmpty(t, invocationID)
	expectedToolCallID := canonicalToolCallID(invocationID, "rsp-stream", "call-1", 0, 0)
	require.Len(t, seenResponses, 3)
	require.True(t, seenResponses[0].IsPartial)
	require.Len(t, seenResponses[0].Choices[0].Delta.ToolCalls, 1)
	require.Equal(t, "call-1", seenResponses[0].Choices[0].Delta.ToolCalls[0].ID)
	require.False(t, seenResponses[1].IsPartial)
	require.Len(t, seenResponses[1].Choices[0].Message.ToolCalls, 1)
	require.Equal(t, expectedToolCallID, seenResponses[1].Choices[0].Message.ToolCalls[0].ID)
	require.False(t, seenResponses[2].IsPartial)
	require.Empty(t, seenResponses[2].Choices[0].Message.ToolCalls)
	require.Equal(t, []string{expectedToolCallID}, toolContextIDs)
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	assistantToolCall, ok := firstAssistantToolCall(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, assistantToolCall.ID)
	toolResultMessage, ok := firstToolResultMessage(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, toolResultMessage.ToolID)
}

func TestPlugin_Integration_FinalResponseWithDeltaToolCallsCanonicalizesAndRunsTool(t *testing.T) {
	modelStub := &scriptedResponseModel{
		name: "final-delta-tool-call-model",
		calls: []scriptedResponseCall{
			{
				responses: []*model.Response{{
					ID:        "rsp-delta",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "echo",
									Arguments: []byte(`{"value":"ok"}`),
								},
							}},
						},
						Delta: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "echo",
									Arguments: []byte(`{"value":"ok"}`),
								},
							}},
						},
					}},
				}},
			},
			{
				responses: []*model.Response{{
					ID:        "rsp-finish",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("done"),
					}},
				}},
			},
		},
	}
	var (
		mu             sync.Mutex
		seenResponses  []*model.Response
		toolContextIDs []string
	)
	localModelCallbacks := model.NewCallbacks().RegisterAfterModel(func(
		ctx context.Context,
		args *model.AfterModelArgs,
	) (*model.AfterModelResult, error) {
		if args == nil || args.Response == nil {
			return nil, nil
		}
		mu.Lock()
		seenResponses = append(seenResponses, cloneToolCallTestResponse(args.Response))
		mu.Unlock()
		return nil, nil
	})
	echoTool := function.NewFunctionTool(
		func(ctx context.Context, input struct {
			Value string `json:"value"`
		}) (string, error) {
			toolCallID, _ := tool.ToolCallIDFromContext(ctx)
			mu.Lock()
			toolContextIDs = append(toolContextIDs, toolCallID)
			mu.Unlock()
			return input.Value, nil
		},
		function.WithName("echo"),
		function.WithDescription("Echoes the input."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithModelCallbacks(localModelCallbacks),
		llmagent.WithTools([]tool.Tool{echoTool}),
	)
	run := runner.NewRunner("toolcallid-final-delta-app", ag, runner.WithPlugins(New()))
	t.Cleanup(func() {
		require.NoError(t, run.Close())
	})
	eventCh, err := run.Run(
		context.Background(),
		"user-final-delta",
		"session-final-delta",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	events := collectEvents(eventCh)
	invocationID := firstInvocationID(events)
	require.NotEmpty(t, invocationID)
	expectedToolCallID := canonicalToolCallID(invocationID, "rsp-delta", "call-1", 0, 0)
	require.Len(t, seenResponses, 2)
	require.False(t, seenResponses[0].IsPartial)
	require.Equal(t, expectedToolCallID, seenResponses[0].Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, expectedToolCallID, seenResponses[0].Choices[0].Delta.ToolCalls[0].ID)
	require.Equal(t, []string{expectedToolCallID}, toolContextIDs)
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	assistantToolCall, ok := firstAssistantToolCall(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, assistantToolCall.ID)
	toolResultMessage, ok := firstToolResultMessage(requests[1].messages)
	require.True(t, ok)
	require.Equal(t, expectedToolCallID, toolResultMessage.ToolID)
}

func TestPlugin_Integration_RepeatedRawToolCallIDAcrossLLMCallsProducesDistinctCanonicalIDs(t *testing.T) {
	modelStub := &scriptedResponseModel{
		name: "repeated-tool-call-id-model",
		calls: []scriptedResponseCall{
			{
				responses: []*model.Response{{
					ID:        "rsp-1",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "echo",
									Arguments: []byte(`{"value":"first"}`),
								},
							}},
						},
					}},
				}},
			},
			{
				responses: []*model.Response{{
					ID:        "rsp-2",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "echo",
									Arguments: []byte(`{"value":"second"}`),
								},
							}},
						},
					}},
				}},
			},
			{
				responses: []*model.Response{{
					ID:        "rsp-3",
					Object:    model.ObjectTypeChatCompletion,
					Done:      true,
					IsPartial: false,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("done"),
					}},
				}},
			},
		},
	}
	var (
		mu             sync.Mutex
		toolContextIDs []string
	)
	echoTool := function.NewFunctionTool(
		func(ctx context.Context, input struct {
			Value string `json:"value"`
		}) (string, error) {
			toolCallID, _ := tool.ToolCallIDFromContext(ctx)
			mu.Lock()
			toolContextIDs = append(toolContextIDs, toolCallID)
			mu.Unlock()
			return input.Value, nil
		},
		function.WithName("echo"),
		function.WithDescription("Echoes the input."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{echoTool}),
	)
	run := runner.NewRunner("toolcallid-repeat-app", ag, runner.WithPlugins(New()))
	t.Cleanup(func() {
		require.NoError(t, run.Close())
	})
	eventCh, err := run.Run(
		context.Background(),
		"user-repeat",
		"session-repeat",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	events := collectEvents(eventCh)
	invocationID := firstInvocationID(events)
	require.NotEmpty(t, invocationID)
	firstCanonicalID := canonicalToolCallID(invocationID, "rsp-1", "call-1", 0, 0)
	secondCanonicalID := canonicalToolCallID(invocationID, "rsp-2", "call-1", 0, 0)
	require.NotEqual(t, firstCanonicalID, secondCanonicalID)
	require.Equal(t, []string{firstCanonicalID, secondCanonicalID}, toolContextIDs)
	requests := modelStub.Requests()
	require.Len(t, requests, 3)
	require.Equal(t, []string{firstCanonicalID}, collectAssistantToolCallIDs(requests[1].messages))
	require.Equal(t, []string{firstCanonicalID}, collectToolResultIDs(requests[1].messages))
	require.Equal(
		t,
		[]string{firstCanonicalID, secondCanonicalID},
		collectAssistantToolCallIDs(requests[2].messages),
	)
	require.Equal(
		t,
		[]string{firstCanonicalID, secondCanonicalID},
		collectToolResultIDs(requests[2].messages),
	)
}

type capturingToolLoopModel struct {
	mu       sync.Mutex
	requests []*capturedRequest
}

func (m *capturingToolLoopModel) Info() model.Info {
	return model.Info{Name: "capturing-tool-loop-model"}
}

func (m *capturingToolLoopModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneCapturedRequest(req))
	callIndex := len(m.requests) - 1
	m.mu.Unlock()
	var rsp *model.Response
	if callIndex == 0 {
		rsp = &model.Response{
			ID:        "rsp-1",
			Done:      true,
			IsPartial: false,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "echo",
							Arguments: []byte(`{"value":"ok"}`),
						},
					}},
				},
			}},
		}
	} else {
		rsp = &model.Response{
			ID:        "rsp-2",
			Done:      true,
			IsPartial: false,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("done"),
			}},
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- rsp
	close(ch)
	return ch, nil
}

func (m *capturingToolLoopModel) Requests() []*capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*capturedRequest, len(m.requests))
	for i, req := range m.requests {
		out[i] = cloneCapturedRequestValue(req)
	}
	return out
}

type scriptedResponseCall struct {
	responses []*model.Response
}

type scriptedResponseModel struct {
	name     string
	mu       sync.Mutex
	calls    []scriptedResponseCall
	requests []*capturedRequest
}

func (m *scriptedResponseModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *scriptedResponseModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneCapturedRequest(req))
	callIndex := len(m.requests) - 1
	var responses []*model.Response
	if callIndex < len(m.calls) {
		responses = m.calls[callIndex].responses
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, len(responses))
	for _, rsp := range responses {
		ch <- cloneToolCallTestResponse(rsp)
	}
	close(ch)
	return ch, nil
}

func (m *scriptedResponseModel) Requests() []*capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*capturedRequest, len(m.requests))
	for i, req := range m.requests {
		out[i] = cloneCapturedRequestValue(req)
	}
	return out
}

type capturedRequest struct {
	messages []model.Message
}

func cloneCapturedRequest(req *model.Request) *capturedRequest {
	if req == nil {
		return nil
	}
	messages := make([]model.Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = cloneMessage(msg)
	}
	return &capturedRequest{messages: messages}
}

func cloneCapturedRequestValue(req *capturedRequest) *capturedRequest {
	if req == nil {
		return nil
	}
	messages := make([]model.Message, len(req.messages))
	for i, msg := range req.messages {
		messages[i] = cloneMessage(msg)
	}
	return &capturedRequest{messages: messages}
}

func cloneToolCallTestResponse(rsp *model.Response) *model.Response {
	if rsp == nil {
		return nil
	}
	cloned := rsp.Clone()
	choices := make([]model.Choice, len(rsp.Choices))
	for i, choice := range rsp.Choices {
		choices[i] = choice
		choices[i].Message = cloneMessage(choice.Message)
		choices[i].Delta = cloneMessage(choice.Delta)
	}
	cloned.Choices = choices
	return cloned
}

func cloneMessage(message model.Message) model.Message {
	cloned := message
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	}
	if len(message.ContentParts) > 0 {
		cloned.ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	}
	return cloned
}

func collectEvents(ch <-chan *event.Event) []*event.Event {
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func firstInvocationID(events []*event.Event) string {
	for _, evt := range events {
		if evt == nil || evt.InvocationID == "" {
			continue
		}
		return evt.InvocationID
	}
	return ""
}

func firstAssistantToolCall(messages []model.Message) (model.ToolCall, bool) {
	for _, msg := range messages {
		if msg.Role != model.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		return msg.ToolCalls[0], true
	}
	return model.ToolCall{}, false
}

func firstToolResultMessage(messages []model.Message) (model.Message, bool) {
	for _, msg := range messages {
		if msg.Role != model.RoleTool || msg.ToolID == "" {
			continue
		}
		return msg, true
	}
	return model.Message{}, false
}

func collectAssistantToolCallIDs(messages []model.Message) []string {
	var ids []string
	for _, msg := range messages {
		if msg.Role != model.RoleAssistant {
			continue
		}
		for _, toolCall := range msg.ToolCalls {
			ids = append(ids, toolCall.ID)
		}
	}
	return ids
}

func collectToolResultIDs(messages []model.Message) []string {
	var ids []string
	for _, msg := range messages {
		if msg.Role != model.RoleTool || msg.ToolID == "" {
			continue
		}
		ids = append(ids, msg.ToolID)
	}
	return ids
}

func intPtr(v int) *int {
	return &v
}
