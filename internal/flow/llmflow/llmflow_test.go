//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

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

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "skip-response", events[0].Response.ID)
}

func TestModelCBs_BeforeCustom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "custom-before"}, nil
	})

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "custom-before", events[0].Response.ID)
}

func TestModelCallbacks_BeforeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, errors.New("before error")
	})

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "before error" {
			cancel()
			break
		}
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "before error", events[0].Error.Message)
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

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "original"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	t.Log(events[0])
	require.Equal(t, "after-override", events[0].Response.Object)
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

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "original"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "after error" {
			cancel()
			break
		}
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "after error", events[0].Error.Message)
}

// mockTool implements tool.Tool for testing parallel tool execution
type mockTool struct {
	name        string
	shouldError bool
	shouldPanic bool
	delay       time.Duration
	result      any
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: "Mock tool for testing",
	}
}

func (m *mockTool) Call(ctx context.Context, args []byte) (any, error) {
	// Add delay to simulate processing time
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if m.shouldPanic {
		panic("mock tool panic")
	}

	if m.shouldError {
		return nil, errors.New("mock tool error")
	}

	return m.result, nil
}

// mockLongRunningTool implements both tool.Tool and function.LongRunner
type mockLongRunningTool struct {
	*mockTool
	isLongRunning bool
}

func (m *mockLongRunningTool) LongRunning() bool {
	return m.isLongRunning
}

// parallelTestCase defines a test case for parallel tool execution
type parallelTestCase struct {
	name                string
	tools               []tool.Tool
	disableParallel     bool
	expectedMinDuration time.Duration
	expectedMaxDuration time.Duration
	expectError         bool
	testTimeout         time.Duration
}

// createMockModelWithTools creates a mock model that returns tool calls for the given tools
func createMockModelWithTools(tools []tool.Tool) *mockModel {
	toolCalls := make([]model.ToolCall, len(tools))
	for i, tool := range tools {
		toolCalls[i] = model.ToolCall{
			Index: func(idx int) *int { return &idx }(i),
			ID:    fmt.Sprintf("call-%d", i+1),
			Type:  "function",
			Function: model.FunctionDefinitionParam{
				Name:      tool.Declaration().Name,
				Arguments: []byte(`{}`),
			},
		}
	}

	return &mockModel{
		responses: []*model.Response{
			{
				ID:      "test-response",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:      model.RoleAssistant,
							ToolCalls: toolCalls,
						},
					},
				},
				Done: false,
			},
		},
	}
}

// runParallelToolTest runs a parallel tool execution test with the given test case
func runParallelToolTest(t *testing.T, tc parallelTestCase) {
	ctx, cancel := context.WithTimeout(context.Background(), tc.testTimeout)
	defer cancel()

	mockModel := createMockModelWithTools(tc.tools)
	testAgent := &mockAgentWithTools{
		name:  "test-agent",
		tools: tc.tools,
	}

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: fmt.Sprintf("test-%s", strings.ReplaceAll(tc.name, " ", "-")),
		Model:        mockModel,
		Agent:        testAgent,
		Session:      &session.Session{ID: "test-session"},
	}

	// Run test with specified parallel setting
	startTime := time.Now()
	eventChan, err := New(nil, nil, Options{EnableParallelTools: !tc.disableParallel}).Run(ctx, invocation)

	if tc.expectError {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)

	// Collect tool call response event
	var toolCallEvent *event.Event
	for evt := range eventChan {
		if evt.Object == model.ObjectTypeToolResponse {
			toolCallEvent = evt
			break
		}
	}

	executionTime := time.Since(startTime)
	t.Logf("%s execution time: %v", tc.name, executionTime)

	// Verify tool call event
	require.NotNil(t, toolCallEvent, "Should have tool call response event")
	// Note: In some test scenarios (context cancellation, errors), we may not get all responses
	// This is expected behavior, so we just verify we got at least one response
	require.Greater(t, len(toolCallEvent.Response.Choices), 0,
		"Should have at least one tool call response")

	// Verify execution time if specified
	if tc.expectedMinDuration > 0 {
		require.GreaterOrEqual(t, executionTime, tc.expectedMinDuration,
			"Execution time should be at least %v for %s. Actual: %v",
			tc.expectedMinDuration, tc.name, executionTime)
	}
	if tc.expectedMaxDuration > 0 {
		require.LessOrEqual(t, executionTime, tc.expectedMaxDuration,
			"Execution time should be at most %v for %s. Actual: %v",
			tc.expectedMaxDuration, tc.name, executionTime)
	}

	t.Logf("✅ %s verified: %v", tc.name, executionTime)
}

func TestFlow_SingleToolExecution_UsesSerialPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	tool1 := &mockTool{name: "tool1", result: "single_result"}

	mockModel := &mockModel{
		responses: []*model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{
								{
									Index: func() *int { i := 0; return &i }(),
									ID:    "call-1",
									Function: model.FunctionDefinitionParam{
										Name:      "tool1",
										Arguments: []byte(`{}`),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	llmFlow := New(nil, nil, Options{})

	// Create a mock agent with the tools
	mockAgentWithToolsList := []tool.Tool{tool1}
	testAgent := &mockAgentWithTools{
		name:  "test-agent",
		tools: mockAgentWithToolsList,
	}

	invocation := &agent.Invocation{
		InvocationID: "test-single-tool",
		AgentName:    "test-agent",
		Model:        mockModel,
		Agent:        testAgent,
		Session: &session.Session{
			ID: "test-session",
		},
	}

	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		if evt.Done {
			break
		}
	}

	// Find tool call response event
	var toolCallEvent *event.Event
	for _, evt := range events {
		if evt.Object == model.ObjectTypeToolResponse {
			toolCallEvent = evt
			break
		}
	}

	require.NotNil(t, toolCallEvent)
	require.Equal(t, 1, len(toolCallEvent.Response.Choices))

	choice := toolCallEvent.Response.Choices[0]
	require.Equal(t, "call-1", choice.Message.ToolID)
	require.Contains(t, choice.Message.Content, "single_result")
}

func TestFlow_EnableParallelTools_ForcesSerialExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create mock tools with delays to test serial execution
	tool1 := &mockTool{name: "tool1", delay: 100 * time.Millisecond, result: "result1"}
	tool2 := &mockTool{name: "tool2", delay: 100 * time.Millisecond, result: "result2"}
	tool3 := &mockTool{name: "tool3", delay: 100 * time.Millisecond, result: "result3"}

	// Create a mock model that returns tool calls
	mockModel := &mockModel{
		responses: []*model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{
								{
									Index: func() *int { i := 0; return &i }(),
									ID:    "call-1",
									Type:  "function",
									Function: model.FunctionDefinitionParam{
										Name:      "tool1",
										Arguments: []byte(`{}`),
									},
								},
								{
									Index: func() *int { i := 1; return &i }(),
									ID:    "call-2",
									Type:  "function",
									Function: model.FunctionDefinitionParam{
										Name:      "tool2",
										Arguments: []byte(`{}`),
									},
								},
								{
									Index: func() *int { i := 2; return &i }(),
									ID:    "call-3",
									Type:  "function",
									Function: model.FunctionDefinitionParam{
										Name:      "tool3",
										Arguments: []byte(`{}`),
									},
								},
							},
						},
					},
				},
				Done: false,
			},
		},
	}

	testAgent := &mockAgentWithTools{name: "test-agent", tools: []tool.Tool{tool1, tool2, tool3}}
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-serial-execution",
		Model:        mockModel,
		Agent:        testAgent,
		Session:      &session.Session{ID: "test-session"},
	}

	// Test with EnableParallelTools = false (default)
	startTime := time.Now()
	eventChan, err := New(nil, nil, Options{EnableParallelTools: false}).Run(ctx, invocation)
	require.NoError(t, err)

	var toolCallEvent *event.Event
	for evt := range eventChan {
		if evt.Object == model.ObjectTypeToolResponse {
			toolCallEvent = evt
			break
		}
	}

	executionTime := time.Since(startTime)
	t.Logf("Serial execution time: %v", executionTime)

	require.NotNil(t, toolCallEvent, "Should have tool call response event")
	require.Equal(t, 3, len(toolCallEvent.Response.Choices), "Should have 3 tool call responses")

	// Serial execution should take around 300ms (100ms * 3 tools)
	require.Greater(t, executionTime, 250*time.Millisecond,
		"Serial execution should take at least 250ms (3 tools * 100ms each). Actual: %v", executionTime)
	require.Less(t, executionTime, 500*time.Millisecond,
		"Serial execution should take less than 500ms (allowing for overhead). Actual: %v", executionTime)

	// Verify all tools were executed
	resultContents := make([]string, len(toolCallEvent.Response.Choices))
	for i, choice := range toolCallEvent.Response.Choices {
		resultContents[i] = choice.Message.Content
	}
	require.Contains(t, strings.Join(resultContents, " "), "result1")
	require.Contains(t, strings.Join(resultContents, " "), "result2")
	require.Contains(t, strings.Join(resultContents, " "), "result3")

	t.Logf("✅ Serial execution verified: %v >= 250ms", executionTime)
}

// TestFlow_ParallelToolExecution_Unified replaces multiple individual parallel tests
// This unified test covers all the scenarios in a more maintainable way
func TestFlow_ParallelToolExecution_Unified(t *testing.T) {
	testCases := []parallelTestCase{
		{
			name: "basic parallel success",
			tools: []tool.Tool{
				&mockTool{name: "tool1", result: "result1"},
				&mockTool{name: "tool2", result: "result2"},
			},
			disableParallel: false,
			testTimeout:     5 * time.Second,
		},
		{
			name: "parallel performance validation",
			tools: []tool.Tool{
				&mockTool{name: "tool1", delay: 50 * time.Millisecond, result: "result1"},
				&mockTool{name: "tool2", delay: 50 * time.Millisecond, result: "result2"},
				&mockTool{name: "tool3", delay: 50 * time.Millisecond, result: "result3"},
			},
			disableParallel:     false,
			expectedMaxDuration: 150 * time.Millisecond, // Should be parallel (~50ms)
			testTimeout:         5 * time.Second,
		},
		{
			name: "serial execution with disable flag",
			tools: []tool.Tool{
				&mockTool{name: "tool1", delay: 100 * time.Millisecond, result: "result1"},
				&mockTool{name: "tool2", delay: 100 * time.Millisecond, result: "result2"},
				&mockTool{name: "tool3", delay: 100 * time.Millisecond, result: "result3"},
			},
			disableParallel:     true,
			expectedMinDuration: 250 * time.Millisecond, // Should be serial (~300ms)
			expectedMaxDuration: 500 * time.Millisecond,
			testTimeout:         3 * time.Second,
		},
		{
			name: "error handling in parallel",
			tools: []tool.Tool{
				&mockTool{name: "tool1", result: "success"},
				&mockTool{name: "tool2", shouldError: true},
				&mockTool{name: "tool3", shouldPanic: true},
			},
			disableParallel: false,
			testTimeout:     1 * time.Second,
		},
		{
			name: "long running tools handling",
			tools: []tool.Tool{
				&mockLongRunningTool{
					mockTool:      &mockTool{name: "tool1", delay: 50 * time.Millisecond, result: "result1"},
					isLongRunning: true,
				},
				&mockTool{name: "tool2", delay: 50 * time.Millisecond, result: "result2"},
			},
			disableParallel: false,
			testTimeout:     2 * time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runParallelToolTest(t, tc)
		})
	}
}

func TestExecuteToolCall_MapsSubAgentToTransfer(t *testing.T) {
	ctx := context.Background()
	f := New(nil, nil, Options{})

	// Prepare invocation with a parent agent that has a sub-agent named weather-agent.
	inv := &agent.Invocation{
		AgentName: "coordinator",
		Agent: &mockTransferAgent{
			subAgents: []agent.Agent{
				&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "weather-agent"}},
			},
		},
	}

	// Prepare tools: only transfer tool is exposed, no weather-agent tool.
	capturedArgs := make([]byte, 0)
	tools := map[string]tool.Tool{
		transfer.TransferToolName: &mockTransferCallableTool{
			declaration: &tool.Declaration{Name: transfer.TransferToolName, Description: "transfer"},
			callFn: func(_ context.Context, args []byte) (any, error) {
				capturedArgs = append(capturedArgs[:0], args...)
				return map[string]any{"ok": true}, nil
			},
		},
	}

	// Original tool call uses sub-agent name directly.
	originalArgs := []byte(`{"message":"What's the weather like in Tokyo?"}`)
	pc := model.ToolCall{
		ID: "call-1",
		Function: model.FunctionDefinitionParam{
			Name:      "weather-agent",
			Arguments: originalArgs,
		},
	}

	choice, err := f.executeToolCall(ctx, inv, pc, tools, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)

	// The tool name should have been mapped to transfer_to_agent by the time execution happens.
	// Returned Tool choice stores content only; we verify the captured args passed to our mock tool.
	var got transfer.Request
	require.NoError(t, json.Unmarshal(capturedArgs, &got))
	assert.Equal(t, "weather-agent", got.AgentName)
	assert.Equal(t, "What's the weather like in Tokyo?", got.Message)
	assert.Equal(t, false, got.EndInvocation)
}

func TestExecuteToolCall_ToolNotFound_ReturnsErrorChoice(t *testing.T) {
	ctx := context.Background()
	f := New(nil, nil, Options{})

	// Invocation without matching sub-agent and with a mock model to satisfy logging.
	inv := &agent.Invocation{
		AgentName: "coordinator",
		Agent:     &mockTransferAgent{subAgents: nil},
		Model:     &mockModel{},
	}

	tools := map[string]tool.Tool{} // No tools available.

	pc2 := model.ToolCall{
		ID: "call-404",
		Function: model.FunctionDefinitionParam{
			Name:      "non-existent-tool",
			Arguments: []byte(`{}`),
		},
	}

	choice, err := f.executeToolCall(ctx, inv, pc2, tools, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, ErrorToolNotFound, choice.Message.Content)
	assert.Equal(t, "call-404", choice.Message.ToolID)
}

// --- Test helpers used above ---

// Minimal callable tool used by tests above
type mockTransferCallableTool struct {
	declaration *tool.Declaration
	callFn      func(ctx context.Context, args []byte) (any, error)
}

func (m *mockTransferCallableTool) Declaration() *tool.Declaration { return m.declaration }
func (m *mockTransferCallableTool) Call(ctx context.Context, args []byte) (any, error) {
	return m.callFn(ctx, args)
}
