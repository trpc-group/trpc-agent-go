package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubTool struct{ name string }

func (s stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name, Description: "stub"}
}

// Test that handler is not invoked when there are no tool calls.
func TestFunctionCallProcessor_NoToolCalls_NoInvoke(t *testing.T) {
	called := false
	p := NewFunctionCallResponseProcessor(
		func(ctx context.Context, inv *agent.Invocation, llmEvt *event.Event, tools map[string]tool.Tool, ch chan<- *event.Event) (*event.Event, error) {
			called = true
			return nil, nil
		},
		nil,
	)

	inv := &agent.Invocation{InvocationID: "inv1", AgentName: "agent1", Branch: "b1"}
	req := &model.Request{}
	rsp := &model.Response{Choices: []model.Choice{{}}} // no ToolCalls
	ch := make(chan *event.Event, 1)

	p.ProcessResponse(context.Background(), inv, req, rsp, ch)

	require.False(t, called, "handler should not be called without tool calls")
}

// Test that handler receives expected tools and llm event metadata, and wait is invoked.
func TestFunctionCallProcessor_WithToolCalls_CallsHandlerAndWait(t *testing.T) {
	var gotInvocation *agent.Invocation
	var gotLLMEvent *event.Event
	var gotTools map[string]tool.Tool
	waitCalled := false
	var waitEvent *event.Event

	p := NewFunctionCallResponseProcessor(
		func(
			ctx context.Context,
			inv *agent.Invocation,
			llmEvt *event.Event,
			tools map[string]tool.Tool,
			ch chan<- *event.Event,
		) (*event.Event, error) {
			gotInvocation = inv
			gotLLMEvent = llmEvt
			gotTools = tools
			// Return a dummy event to trigger wait
			return event.New(inv.InvocationID, inv.AgentName), nil
		},
		func(
			ctx context.Context,
			inv *agent.Invocation,
			last *event.Event,
		) error {
			waitCalled = true
			waitEvent = last
			require.Equal(t, inv, gotInvocation)
			return nil
		},
	)

	toolsMap := map[string]tool.Tool{
		"tool1": stubTool{name: "tool1"},
		"tool2": stubTool{name: "tool2"},
	}

	inv := &agent.Invocation{InvocationID: "inv2", AgentName: "agent2", Branch: "branch-x"}
	req := &model.Request{Tools: toolsMap}
	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{Type: "function", Function: model.FunctionDefinitionParam{Name: "tool1"}, ID: "id1"},
					},
				},
			},
		},
	}
	ch := make(chan *event.Event, 1)

	p.ProcessResponse(context.Background(), inv, req, rsp, ch)

	// Handler received metadata
	require.Equal(t, inv, gotInvocation)
	require.NotNil(t, gotLLMEvent)
	require.Equal(t, inv.InvocationID, gotLLMEvent.InvocationID)
	require.Equal(t, inv.AgentName, gotLLMEvent.Author)
	require.Equal(t, inv.Branch, gotLLMEvent.Branch)
	require.Equal(t, rsp, gotLLMEvent.Response)

	// Tools propagated from request
	require.Len(t, gotTools, len(toolsMap))
	require.Contains(t, gotTools, "tool1")
	require.Contains(t, gotTools, "tool2")

	// Wait was called with the returned event
	require.True(t, waitCalled)
	require.NotNil(t, waitEvent)
}

// Test that wait is not called when handler returns nil event.
func TestFunctionCallProcessor_HandlerReturnsNil_NoWait(t *testing.T) {
	waitCalled := false
	p := NewFunctionCallResponseProcessor(
		func(
			ctx context.Context,
			inv *agent.Invocation,
			llmEvt *event.Event,
			tools map[string]tool.Tool,
			ch chan<- *event.Event,
		) (*event.Event, error) {
			return nil, nil
		},
		func(
			ctx context.Context,
			inv *agent.Invocation,
			last *event.Event,
		) error {
			waitCalled = true
			return nil
		},
	)

	inv := &agent.Invocation{InvocationID: "inv3", AgentName: "agent3"}
	req := &model.Request{Tools: map[string]tool.Tool{"tool1": stubTool{name: "tool1"}}}
	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{Type: "function", Function: model.FunctionDefinitionParam{Name: "tool1"}},
					},
				},
			},
		},
	}
	ch := make(chan *event.Event, 1)

	p.ProcessResponse(context.Background(), inv, req, rsp, ch)

	require.False(t, waitCalled, "wait should not be called when handler returns nil event")
}

// Test that errors from handler prevent wait from being called and are swallowed.
func TestFunctionCallProcessor_HandlerError_SwallowsAndNoWait(t *testing.T) {
	waitCalled := false
	p := NewFunctionCallResponseProcessor(
		func(
			ctx context.Context,
			inv *agent.Invocation,
			llmEvt *event.Event,
			tools map[string]tool.Tool,
			ch chan<- *event.Event,
		) (*event.Event, error) {
			return nil, errors.New("boom")
		},
		func(
			ctx context.Context,
			inv *agent.Invocation,
			last *event.Event,
		) error {
			waitCalled = true
			return nil
		},
	)

	inv := &agent.Invocation{InvocationID: "inv4", AgentName: "agent4"}
	req := &model.Request{Tools: map[string]tool.Tool{"tool1": stubTool{name: "tool1"}}}
	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{Type: "function", Function: model.FunctionDefinitionParam{Name: "tool1"}},
					},
				},
			},
		},
	}
	ch := make(chan *event.Event, 1)

	// Should not panic; error is swallowed inside processor.
	p.ProcessResponse(context.Background(), inv, req, rsp, ch)
	require.False(t, waitCalled, "wait should not be called when handler errors")
}

// Test that tools map is empty when request or request.Tools are nil.
func TestFunctionCallProcessor_NilRequest_EmptyToolsPassed(t *testing.T) {
	var gotTools map[string]tool.Tool
	p := NewFunctionCallResponseProcessor(
		func(
			ctx context.Context,
			inv *agent.Invocation,
			llmEvt *event.Event,
			tools map[string]tool.Tool,
			ch chan<- *event.Event,
		) (*event.Event, error) {
			gotTools = tools
			return nil, nil
		},
		nil,
	)

	inv := &agent.Invocation{InvocationID: "inv5", AgentName: "agent5"}
	rsp := &model.Response{Choices: []model.Choice{
		{Message: model.Message{ToolCalls: []model.ToolCall{{
			Type: "function", Function: model.FunctionDefinitionParam{Name: "toolx"}}}}}},
	}
	ch := make(chan *event.Event, 1)

	// req is nil
	p.ProcessResponse(context.Background(), inv, nil, rsp, ch)
	require.NotNil(t, gotTools)
	require.Len(t, gotTools, 0)
}
