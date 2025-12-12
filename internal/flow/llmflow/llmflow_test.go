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
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
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

// TestRunAfterModelCallbacks_ErrorPassing tests that modelErr is correctly passed to callbacks
// when response.Error is not nil.
func TestRunAfterModelCallbacks_ErrorPassing(t *testing.T) {
	tests := []struct {
		name       string
		response   *model.Response
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "response with error",
			response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeAPIError,
					Message: "rate limit exceeded",
				},
			},
			wantErr:    true,
			wantErrMsg: "api_error: rate limit exceeded",
		},
		{
			name: "response without error",
			response: &model.Response{
				Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}},
			},
			wantErr:    false,
			wantErrMsg: "",
		},
		{
			name:       "nil response",
			response:   nil,
			wantErr:    false,
			wantErrMsg: "",
		},
		{
			name: "response with nil error field",
			response: &model.Response{
				Error: nil,
			},
			wantErr:    false,
			wantErrMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedErr error
			callbacks := model.NewCallbacks().RegisterAfterModel(
				func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
					receivedErr = modelErr
					return nil, nil
				},
			)

			flow := &Flow{
				modelCallbacks: callbacks,
			}

			_, _, err := flow.runAfterModelCallbacks(context.Background(), &model.Request{}, tt.response)
			require.NoError(t, err)

			if tt.wantErr {
				require.NotNil(t, receivedErr, "expected callback to receive error, but got nil")
				require.Equal(t, tt.wantErrMsg, receivedErr.Error(), "error message mismatch")
			} else {
				require.Nil(t, receivedErr, "expected callback to receive nil error, but got: %v", receivedErr)
			}
		})
	}
}

// blockingModel emits one response then waits for ctx cancellation.
type blockingModel struct{}

func (m *blockingModel) Info() model.Info {
	return model.Info{Name: "blocking"}
}

func (m *blockingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("hi"),
			},
		},
	}
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func TestFlow_Run_ContextCanceledIsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&minimalAgent{}),
		agent.WithInvocationModel(&blockingModel{}),
	)

	ch, err := f.Run(ctx, inv)
	require.NoError(t, err)

	var sawLLMEvent bool
	for evt := range ch {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			_ = inv.NotifyCompletion(ctx, key)
		}
		if evt.Response != nil {
			sawLLMEvent = true
			cancel()
		}
	}
	require.True(t, sawLLMEvent)
}

func TestFlow_callLLM_NoModel(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation()
	req := &model.Request{}

	ch, err := f.callLLM(context.Background(), inv, req)
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestFlow_callLLM_ModelError(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{ShouldError: true}),
	)
	req := &model.Request{}

	ch, err := f.callLLM(context.Background(), inv, req)
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestFlow_Postprocess_WithProcessor(t *testing.T) {
	respProcessor := &mockResponseProcessor{}
	f := New(nil, []flow.ResponseProcessor{respProcessor}, Options{})

	ctx := context.Background()
	inv := agent.NewInvocation()
	req := &model.Request{}
	resp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("ok"),
			},
		},
	}
	eventCh := make(chan *event.Event, 2)

	f.postprocess(ctx, inv, req, resp, eventCh)

	var count int
	for {
		select {
		case <-eventCh:
			count++
		default:
			goto done
		}
	}

done:
	require.Equal(t, 1, count)
}

// Test that when RunOptions.Resume is enabled and the latest session event
// is an assistant tool_call response, the flow executes the pending tool
// before issuing a new LLM request.
func TestRun_WithResumeExecutesPendingToolCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Record invocations of the test tool.
	var toolCalls []string
	testTool := function.NewFunctionTool(
		func(_ context.Context, req *struct {
			Value string `json:"value"`
		}) (*struct {
			Value string `json:"value"`
		}, error) {
			toolCalls = append(toolCalls, req.Value)
			return &struct {
				Value string `json:"value"`
			}{Value: "ok:" + req.Value}, nil
		},
		function.WithName("resume_tool"),
		function.WithDescription("resume test tool"),
	)

	// Session contains a single assistant tool_call response.
	sess := &session.Session{}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							ID: "call-1",
							Function: model.FunctionDefinitionParam{
								Name:      "resume_tool",
								Arguments: []byte(`{"value":"resume"}`),
							},
						},
					},
				},
			},
		},
	}
	toolCallEvent := event.NewResponseEvent("inv-1", "agent-resume", resp)
	sess.Events = append(sess.Events, *toolCallEvent)

	// Agent with the test tool and a model that returns no responses.
	agentWithTool := &mockAgentWithTools{
		name:  "agent-resume",
		tools: []tool.Tool{testTool},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationAgent(agentWithTool),
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(&noResponseModel{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			Resume: true,
		}),
	)

	llmFlow := New(
		nil,
		[]flow.ResponseProcessor{
			processor.NewFunctionCallResponseProcessor(false, nil),
		},
		Options{},
	)

	eventCh, err := llmFlow.Run(ctx, inv)
	require.NoError(t, err)

	var sawToolResult bool
	for evt := range eventCh {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			_ = inv.NotifyCompletion(ctx, key)
		}
		if evt.Response != nil && evt.Response.IsToolResultResponse() {
			sawToolResult = true
		}
	}

	require.True(t, sawToolResult, "expected tool result event when resuming")
	require.Len(t, toolCalls, 1)
	require.Equal(t, "resume", toolCalls[0])
}

func TestWaitEventTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	timeout := WaitEventTimeout(ctx)
	require.InDelta(t, time.Second.Seconds(), timeout.Seconds(), 0.1)
}

func TestWaitEventTimeout_NoDeadline(t *testing.T) {
	ctx := context.Background()
	timeout := WaitEventTimeout(ctx)
	require.Equal(t, 5*time.Second, timeout)
}
