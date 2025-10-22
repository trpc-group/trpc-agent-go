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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Additional unit tests for long-running tool tracking and preprocess

// mockLongRunnerTool implements tool.Tool and a LongRunning() flag.
type mockLongRunnerTool struct {
	name string
	long bool
}

func (m *mockLongRunnerTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }
func (m *mockLongRunnerTool) LongRunning() bool              { return m.long }

func TestCollectLongRunningToolIDs(t *testing.T) {
	calls := []model.ToolCall{
		{ID: "1", Function: model.FunctionDefinitionParam{Name: "fast"}},
		{ID: "2", Function: model.FunctionDefinitionParam{Name: "slow"}},
		{ID: "3", Function: model.FunctionDefinitionParam{Name: "unknown"}},
		{ID: "4", Function: model.FunctionDefinitionParam{Name: "nolong"}},
	}
	tools := map[string]tool.Tool{
		"fast":   &mockLongRunnerTool{name: "fast", long: false},
		"slow":   &mockLongRunnerTool{name: "slow", long: true},
		"nolong": &mockLongRunnerTool{name: "nolong", long: false},
		// unknown not present
	}
	got := collectLongRunningToolIDs(calls, tools)
	if _, ok := got["2"]; !ok {
		t.Fatalf("expected long-running id '2' present, got %#v", got)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 id, got %d", len(got))
	}
}

// minimalAgent exposes tools for preprocess test.
type minimalAgent struct{ tools []tool.Tool }

func (m *minimalAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
func (m *minimalAgent) Tools() []tool.Tool              { return m.tools }
func (m *minimalAgent) Info() agent.Info                { return agent.Info{Name: "a"} }
func (m *minimalAgent) SubAgents() []agent.Agent        { return nil }
func (m *minimalAgent) FindSubAgent(string) agent.Agent { return nil }

func TestPreprocess_AddsAgentToolsWhenPresent(t *testing.T) {
	f := New(nil, nil, Options{})
	req := &model.Request{Tools: map[string]tool.Tool{}}
	inv := agent.NewInvocation()
	inv.Agent = &minimalAgent{tools: []tool.Tool{&mockLongRunnerTool{name: "t1"}}}
	ch := make(chan *event.Event, 4)
	f.preprocess(context.Background(), inv, req, ch)
	if _, ok := req.Tools["t1"]; !ok {
		t.Fatalf("expected tool 't1' added to request")
	}
}

func TestCreateLLMResponseEvent_LongRunningIDs(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation()
	req := &model.Request{Tools: map[string]tool.Tool{
		"slow": &mockLongRunnerTool{name: "slow", long: true},
	}}
	rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "x", Function: model.FunctionDefinitionParam{Name: "slow"}}}}}}}
	evt := f.createLLMResponseEvent(inv, rsp, req)
	if _, ok := evt.LongRunningToolIDs["x"]; !ok {
		t.Fatalf("expected long-running tool id tracked")
	}
}

// mockAgent implements agent.Agent for testing
type mockAgent struct {
	name  string
	tools []tool.CallableTool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Simple mock implementation
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.CallableTool {
	return m.tools
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// mockAgentWithTools implements agent.Agent with tool.Tool support
type mockAgentWithTools struct {
	name  string
	tools []tool.Tool
}

func (m *mockAgentWithTools) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgentWithTools) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgentWithTools) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent with tools for testing",
	}
}

func (m *mockAgentWithTools) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgentWithTools) FindSubAgent(name string) agent.Agent {
	return nil
}

// mockModel implements model.Model for testing
type mockModel struct {
	ShouldError bool
	responses   []*model.Response
	currentIdx  int
}

func (m *mockModel) Info() model.Info {
	return model.Info{
		Name: "mock",
	}
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.ShouldError {
		return nil, errors.New("mock model error")
	}

	respChan := make(chan *model.Response, len(m.responses))

	go func() {
		defer close(respChan)
		for _, resp := range m.responses {
			select {
			case respChan <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()

	return respChan, nil
}

// mockRequestProcessor implements flow.RequestProcessor
type mockRequestProcessor struct{}

func (m *mockRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "preprocessing"
	select {
	case ch <- evt:
	default:
	}
}

// mockResponseProcessor implements flow.ResponseProcessor
type mockResponseProcessor struct{}

func (m *mockResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "postprocessing"
	select {
	case ch <- evt:
	default:
	}
}

func TestFlow_Interface(t *testing.T) {
	llmFlow := New(nil, nil, Options{})
	var f flow.Flow = llmFlow

	// Test that the flow implements the interface
	log.Debugf("Flow interface test: %v", f)

	// Simple compile test
	var _ flow.Flow = f
}

func TestModelCallbacks_BeforeSkip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "skip-response"}, nil // Return custom response to skip model call
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "skip-response", events[1].Response.ID)
}

func TestModelCBs_BeforeCustom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "custom-before"}, nil
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "custom-before", events[1].Response.ID)
}

func TestModelCallbacks_BeforeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, errors.New("before error")
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "before error" {
			cancel()
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "before error", events[1].Error.Message)
}

func TestModelCBs_AfterOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
			return &model.Response{Object: "after-override"}, nil
		},
	)

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "original"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	t.Log(events[0])
	require.Equal(t, "after-override", events[1].Response.Object)
}

func TestModelCallbacks_AfterError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
			return nil, errors.New("after error")
		},
	)

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "original"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "after error" {
			cancel()
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "after error", events[1].Error.Message)
}

// noResponseModel returns a closed channel without emitting any responses.
type noResponseModel struct{}

func (m *noResponseModel) Info() model.Info { return model.Info{Name: "noresp"} }
func (m *noResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

// Ensures Flow.Run does not panic when a step produces no events (lastEvent == nil).
// We use a short-lived context so the loop exits via ctx.Done() without hanging.
func TestRun_NoPanicWhenModelReturnsNoResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&noResponseModel{}),
	)

	ch, err := f.Run(ctx, inv)
	require.NoError(t, err)

	// Collect all events until channel closes. Expect none and, importantly, no panic.
	var count int
	for evt := range ch {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			inv.NotifyCompletion(ctx, key)
		}
		count++
	}
	require.Equal(t, 1, count)
}
