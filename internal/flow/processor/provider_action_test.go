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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const providerActionProtocol = "test.native"

type providerActionModel struct {
	results []model.Message
	err     error
	calls   int
	request *model.Request
}

func (m *providerActionModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, errors.New("not used")
}

func (m *providerActionModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: providerActionProtocol}
}

func (m *providerActionModel) CompleteProviderActions(
	_ context.Context,
	request *model.Request,
	_ *model.Response,
) ([]model.Message, error) {
	m.calls++
	m.request = request
	return m.results, m.err
}

type plainNativeModel struct{}

func (plainNativeModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, errors.New("not used")
}

func (plainNativeModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: providerActionProtocol}
}

func providerActionData(payload string) *model.ProviderData {
	return &model.ProviderData{Protocol: providerActionProtocol, Model: "native-model", Payload: json.RawMessage(payload)}
}

func providerActionResult(id string) model.Message {
	return model.Message{Role: model.RoleTool, ToolID: id, ToolName: "search", Content: id,
		ProviderData: providerActionData(`[{"type":"result","id":"` + id + `"}]`)}
}

func pendingActionResponse() *model.Response {
	message := model.NewAssistantMessage("")
	message.ProviderData = providerActionData(`[{"type":"action","id":"a1"}]`)
	return &model.Response{Model: "native-model", Choices: []model.Choice{{Message: message}}}
}

func runProviderActions(
	ctx context.Context,
	m model.Model,
	rsp *model.Response,
) (*agent.Invocation, []*event.Event) {
	invocation := agent.NewInvocation(agent.WithInvocationModel(m))
	invocation.AgentName = "assistant"
	ch := make(chan *event.Event, 8)
	NewProviderActionResponseProcessor().ProcessResponse(ctx, invocation, &model.Request{}, rsp, ch)
	close(ch)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	return invocation, events
}

func TestProviderActionProcessorEmitsResultsInOrder(t *testing.T) {
	m := &providerActionModel{results: []model.Message{providerActionResult("a1"), providerActionResult("a2")}}
	invocation, events := runProviderActions(context.Background(), m, pendingActionResponse())
	require.Equal(t, 1, m.calls)
	require.NotNil(t, m.request)
	require.False(t, invocation.EndInvocation)
	require.Len(t, events, 2)
	for i, id := range []string{"a1", "a2"} {
		evt := events[i]
		require.Equal(t, model.ObjectTypeToolResponse, evt.Object)
		require.True(t, evt.RequiresCompletion, "results are persisted before the next model step")
		require.Equal(t, "assistant", evt.Author)
		require.Equal(t, id, evt.Choices[0].Message.ToolID)
		require.Equal(t, m.results[i].ProviderData, evt.Choices[0].Message.ProviderData)
	}
}

func TestProviderActionProcessorIgnoresOtherResponses(t *testing.T) {
	m := &providerActionModel{results: []model.Message{providerActionResult("a1")}}
	done := pendingActionResponse()
	done.Done = true
	partial := pendingActionResponse()
	partial.IsPartial = true
	failed := pendingActionResponse()
	failed.Error = &model.ResponseError{Message: "boom"}
	ordinary := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("hi")}}}
	for _, rsp := range []*model.Response{done, partial, failed, ordinary, nil} {
		invocation, events := runProviderActions(context.Background(), m, rsp)
		require.Empty(t, events)
		require.False(t, invocation.EndInvocation)
	}
	require.Zero(t, m.calls)
}

func TestProviderActionProcessorRejectsModelsWithoutCompleter(t *testing.T) {
	invocation, events := runProviderActions(context.Background(), plainNativeModel{}, pendingActionResponse())
	require.True(t, invocation.EndInvocation)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Error)
	require.Contains(t, events[0].Error.Message, `model "native-model" does not complete provider actions`)

	ordinary := &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("hi")}}}
	invocation, events = runProviderActions(context.Background(), plainNativeModel{}, ordinary)
	require.Empty(t, events, "ordinary responses need no completer")
	require.False(t, invocation.EndInvocation)
}

func TestProviderActionProcessorStopsOnFailure(t *testing.T) {
	invalid := providerActionResult("a1")
	invalid.ProviderData.Protocol = "other"
	assistant := providerActionResult("a1")
	assistant.Role = model.RoleAssistant
	for _, tc := range []struct {
		name string
		m    *providerActionModel
		want string
	}{
		{name: "error", m: &providerActionModel{err: errors.New("search backend down")}, want: "search backend down"},
		{name: "foreign record", m: &providerActionModel{results: []model.Message{invalid}}, want: "belongs to protocol"},
		{name: "not a tool message", m: &providerActionModel{results: []model.Message{assistant}}, want: "must be a tool message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invocation, events := runProviderActions(context.Background(), tc.m, pendingActionResponse())
			require.True(t, invocation.EndInvocation)
			require.Len(t, events, 1)
			require.NotNil(t, events[0].Error)
			require.Equal(t, model.ErrorTypeFlowError, events[0].Error.Type)
			require.Contains(t, events[0].Error.Message, "complete provider actions")
			require.Contains(t, events[0].Error.Message, tc.want)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &providerActionModel{err: context.Canceled}
	invocation, events := runProviderActions(ctx, m, pendingActionResponse())
	require.True(t, invocation.EndInvocation)
	require.Empty(t, events, "cancellation is not reported as a flow error")
}
