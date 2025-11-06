//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

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
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

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

// Minimal callable tool used by tests above
type mockCallableTool struct {
	declaration *tool.Declaration
	callFn      func(ctx context.Context, args []byte) (any, error)
}

func (m *mockCallableTool) Declaration() *tool.Declaration { return m.declaration }
func (m *mockCallableTool) Call(ctx context.Context, args []byte) (any, error) {
	return m.callFn(ctx, args)
}

func TestExecuteToolCall_MapsSubAgentToTransfer(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

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

	choice, _, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, choice)

	// The tool name should have been mapped to transfer_to_agent by the time execution happens.
	// Returned Tool choice stores content only; we verify the captured args passed to our mock tool.
	var got transfer.Request
	require.NoError(t, json.Unmarshal(capturedArgs, &got))
	assert.Equal(t, "weather-agent", got.AgentName)
	assert.Equal(t, "What's the weather like in Tokyo?", got.Message)
}

func TestExecuteToolCall(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	// Prepare invocation with a parent agent that has a sub-agent named weather-agent.
	inv := &agent.Invocation{
		AgentName: "weather-agent",
	}

	// Prepare tools: only transfer tool is exposed, no weather-agent tool.
	tools := map[string]tool.Tool{
		"weather-agent": &mockCallableTool{
			declaration: &tool.Declaration{Name: "weather-agent", Description: "get weather"},
			callFn: func(_ context.Context, args []byte) (any, error) {
				return "Tokyo'weather is good", nil
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

	choice, _, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, nil)
	res, _ := json.Marshal("Tokyo'weather is good")
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, string(res), choice.Message.Content)
}

func TestExecuteToolCallsInParallel(t *testing.T) {
	tools := map[string]tool.Tool{
		"func-1": &mockCallableTool{
			declaration: &tool.Declaration{Name: "func-1", Description: "func-1"},
			callFn: func(_ context.Context, args []byte) (any, error) {
				return "func-1 result", nil
			},
		},

		"func-2": &mockCallableTool{
			declaration: &tool.Declaration{Name: "func-2", Description: "func-2"},
			callFn: func(_ context.Context, args []byte) (any, error) {
				return "func-2 result", nil
			},
		},
	}
	inv := &agent.Invocation{}
	toolCalls := []model.ToolCall{
		{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "func-1",
				Arguments: []byte(`{}`),
			},
		},
		{
			ID: "call-2",
			Function: model.FunctionDefinitionParam{
				Name:      "func-2",
				Arguments: []byte(`{}`),
			},
		},
	}
	response := &model.Response{
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					ToolCalls: toolCalls,
				},
			},
		},
	}
	ctx := context.Background()
	evt, err := NewFunctionCallResponseProcessor(true, nil).executeToolCallsInParallel(ctx, inv, response,
		toolCalls, tools, nil)
	require.NoError(t, err)
	require.NotNil(t, evt.Choices)
	assert.Equal(t, 2, len(evt.Choices))
}

func TestFlow_EnableParallelTools_ForcesSerialExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
										Name:      tool1.Declaration().Name,
										Arguments: []byte(`{}`),
									},
								},
								{
									Index: func() *int { i := 1; return &i }(),
									ID:    "call-2",
									Type:  "function",
									Function: model.FunctionDefinitionParam{
										Name:      tool2.Declaration().Name,
										Arguments: []byte(`{}`),
									},
								},
								{
									Index: func() *int { i := 2; return &i }(),
									ID:    "call-3",
									Type:  "function",
									Function: model.FunctionDefinitionParam{
										Name:      tool3.Declaration().Name,
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
	toolMap := map[string]tool.Tool{
		tool1.Declaration().Name: tool1,
		tool2.Declaration().Name: tool2,
		tool3.Declaration().Name: tool3,
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
		agent.WithInvocationAgent(testAgent),
		agent.WithInvocationModel(mockModel),
	)

	// Test with EnableParallelTools = false (default)
	startTime := time.Now()
	eventChan := make(chan *event.Event, 100)
	p := NewFunctionCallResponseProcessor(false, nil)
	req := &model.Request{
		Tools: toolMap,
	}
	rsp := mockModel.responses[0]
	go func() {
		p.ProcessResponse(ctx, invocation, req, rsp, eventChan)
	}()

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

	eventChan := make(chan *event.Event, 10)

	mockModel := createMockModelWithTools(tc.tools)
	testAgent := &mockAgentWithTools{
		name:  "test-agent",
		tools: tc.tools,
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationID(fmt.Sprintf("test-%s", strings.ReplaceAll(tc.name, " ", "-"))),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
		agent.WithInvocationAgent(testAgent),
		agent.WithInvocationModel(mockModel),
	)

	// Run test with specified parallel setting
	toolMap := map[string]tool.Tool{}
	for _, tool := range tc.tools {
		toolMap[tool.Declaration().Name] = tool
	}
	startTime := time.Now()
	p := NewFunctionCallResponseProcessor(!tc.disableParallel, nil)
	req := &model.Request{
		Tools: toolMap,
	}
	rsp := mockModel.responses[0]
	go func() {
		p.ProcessResponse(ctx, invocation, req, rsp, eventChan)
	}()

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

func TestExecuteToolCall_ToolNotFound_ReturnsErrorChoice(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	// Invocation without matching sub-agent and with a mock model to satisfy logging.
	inv := &agent.Invocation{
		Model: &mockModel{},
	}

	tools := map[string]tool.Tool{} // No tools available.

	pc2 := model.ToolCall{
		ID: "call-404",
		Function: model.FunctionDefinitionParam{
			Name:      "non-existent-tool",
			Arguments: []byte(`{}`),
		},
	}

	choice, _, shouldIgnoreError, err := p.executeToolCall(ctx, inv, pc2, tools, 0, nil)
	require.True(t, shouldIgnoreError)
	require.Contains(t, err.Error(), ErrorToolNotFound)
	require.Nil(t, choice)
}

func TestFindCompatibleTool(t *testing.T) {
	tests := []struct {
		name           string
		requested      string
		tools          map[string]tool.Tool
		invocation     *agent.Invocation
		expectedResult tool.Tool
		description    string
	}{
		{
			name:      "should find compatible tool when sub-agent exists",
			requested: "weather-agent",
			tools: map[string]tool.Tool{
				transfer.TransferToolName: &mockTransferTool{name: transfer.TransferToolName},
			},
			invocation: &agent.Invocation{
				Agent: &mockTransferAgent{
					subAgents: []agent.Agent{
						&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "weather-agent"}},
						&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "math-agent"}},
					},
				},
			},
			expectedResult: &mockTransferTool{name: transfer.TransferToolName},
			description:    "should return transfer tool when weather-agent is requested",
		},
		{
			name:      "should return nil when transfer tool not available",
			requested: "weather-agent",
			tools:     map[string]tool.Tool{},
			invocation: &agent.Invocation{
				Agent: &mockTransferAgent{
					subAgents: []agent.Agent{
						&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "weather-agent"}},
					},
				},
			},
			expectedResult: nil,
			description:    "should return nil when transfer_to_agent tool is not available",
		},
		{
			name:      "should return nil when invocation is nil",
			requested: "weather-agent",
			tools: map[string]tool.Tool{
				transfer.TransferToolName: &mockTransferTool{name: transfer.TransferToolName},
			},
			invocation:     nil,
			expectedResult: nil,
			description:    "should return nil when invocation is nil",
		},
		{
			name:      "should return nil when agent is nil",
			requested: "weather-agent",
			tools: map[string]tool.Tool{
				transfer.TransferToolName: &mockTransferTool{name: transfer.TransferToolName},
			},
			invocation: &agent.Invocation{
				Agent: nil,
			},
			expectedResult: nil,
			description:    "should return nil when agent is nil",
		},
		{
			name:      "should return nil when sub-agent not found",
			requested: "unknown-agent",
			tools: map[string]tool.Tool{
				transfer.TransferToolName: &mockTransferTool{name: transfer.TransferToolName},
			},
			invocation: &agent.Invocation{
				Agent: &mockTransferAgent{
					subAgents: []agent.Agent{
						&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "weather-agent"}},
						&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "math-agent"}},
					},
				},
			},
			expectedResult: nil,
			description:    "should return nil when requested agent is not in sub-agents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findCompatibleTool(tt.requested, tt.tools, tt.invocation)
			assert.Equal(t, tt.expectedResult, result, tt.description)
		})
	}
}

// Ensure executeSingleToolCallSequential uses fallback declaration when missing tool
func TestExecuteSingleToolCallSequential_MissingTool_UsesFallbackDecl(t *testing.T) {
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	rsp := &model.Response{Choices: []model.Choice{{}}}
	tc := model.ToolCall{ID: "id1", Function: model.FunctionDefinitionParam{Name: "missing"}}
	ch := make(chan *event.Event, 4)
	ev, err := p.executeSingleToolCallSequential(context.Background(), inv, rsp, map[string]tool.Tool{}, ch, 0, tc)
	require.NoError(t, err)
	require.NotNil(t, ev)
}

func TestConvertToolArguments(t *testing.T) {
	tests := []struct {
		name         string
		originalName string
		originalArgs []byte
		targetName   string
		expected     []byte
		description  string
	}{
		{
			name:         "should convert message field correctly",
			originalName: "weather-agent",
			originalArgs: []byte(`{"message": "What's the weather like in Tokyo?"}`),
			targetName:   transfer.TransferToolName,
			expected: func() []byte {
				req := &transfer.Request{
					AgentName: "weather-agent",
					Message:   "What's the weather like in Tokyo?",
				}
				b, _ := json.Marshal(req)
				return b
			}(),
			description: "should convert message field to transfer_to_agent format",
		},
		{
			name:         "should use default message when no message",
			originalName: "research-agent",
			originalArgs: []byte(`{}`),
			targetName:   transfer.TransferToolName,
			expected: func() []byte {
				req := &transfer.Request{
					AgentName: "research-agent",
					Message:   "Task delegated from coordinator",
				}
				b, _ := json.Marshal(req)
				return b
			}(),
			description: "should use default message when no message field",
		},
		{
			name:         "should return nil for non-transfer target",
			originalName: "weather-agent",
			originalArgs: []byte(`{"message": "test"}`),
			targetName:   "other-tool",
			expected:     nil,
			description:  "should return nil when target is not transfer_to_agent",
		},
		{
			name:         "should handle empty args",
			originalName: "weather-agent",
			originalArgs: []byte{},
			targetName:   transfer.TransferToolName,
			expected: func() []byte {
				req := &transfer.Request{
					AgentName: "weather-agent",
					Message:   "Task delegated from coordinator",
				}
				b, _ := json.Marshal(req)
				return b
			}(),
			description: "should handle empty arguments correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToolArguments(tt.originalName, tt.originalArgs, tt.targetName)

			if tt.expected == nil {
				assert.Nil(t, result, tt.description)
				return
			}

			require.NotNil(t, result, tt.description)

			// Parse both results to compare
			var expectedReq, actualReq transfer.Request
			err1 := json.Unmarshal(tt.expected, &expectedReq)
			err2 := json.Unmarshal(result, &actualReq)

			require.NoError(t, err1, "should unmarshal expected result")
			require.NoError(t, err2, "should unmarshal actual result")

			assert.Equal(t, expectedReq.AgentName, actualReq.AgentName, "agent_name should match")
			assert.Equal(t, expectedReq.Message, actualReq.Message, "message should match")
		})
	}
}

func TestSubAgentCall(t *testing.T) {
	t.Run("should unmarshal message field correctly", func(t *testing.T) {
		input := subAgentCall{}
		data := []byte(`{"message": "test message"}`)

		err := json.Unmarshal(data, &input)
		require.NoError(t, err)
		assert.Equal(t, "test message", input.Message)
	})

	t.Run("should handle empty json", func(t *testing.T) {
		input := subAgentCall{}
		data := []byte(`{}`)

		err := json.Unmarshal(data, &input)
		require.NoError(t, err)
		assert.Equal(t, "", input.Message)
	})
}

// mockStreamTool implements tool.StreamableTool for testing partial tool responses.
type mockStreamTool struct {
	name   string
	chunks []any
}

func (m *mockStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: m.name, Description: "mock stream tool"}
}

func (m *mockStreamTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	stream := tool.NewStream(8)
	go func() {
		defer stream.Writer.Close()
		for _, c := range m.chunks {
			if stream.Writer.Send(tool.StreamChunk{Content: c}, nil) {
				return
			}
		}
	}()
	return stream.Reader, nil
}

// Test that newToolCallResponseEvent constructs events via helpers and fills metadata correctly.
func TestNewToolCallResponseEvent_Constructor(t *testing.T) {
	inv := &agent.Invocation{InvocationID: "inv-1", AgentName: "tester", Branch: "main"}
	base := &model.Response{Model: "unit-model"}
	choices := []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleTool, ToolID: "call-1", Content: "ok"}}}

	evt := newToolCallResponseEvent(inv, base, choices)

	require.NotNil(t, evt)
	require.NotNil(t, evt.Response)
	require.NotEmpty(t, evt.ID)
	require.Equal(t, inv.InvocationID, evt.InvocationID)
	require.Equal(t, inv.AgentName, evt.Author)
	require.Equal(t, inv.Branch, evt.Branch)
	require.Equal(t, model.ObjectTypeToolResponse, evt.Object)
	require.Equal(t, "unit-model", evt.Model)
	require.Len(t, evt.Choices, 1)
	require.Equal(t, "call-1", evt.Choices[0].Message.ToolID)
}

// Test that executeStreamableTool emits partial tool.response events to the channel.
func TestExecuteStreamableTool_EmitsPartialEvents(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-stream", AgentName: "tester", Branch: "b1", Model: &mockModel{}}

	toolCall := model.ToolCall{
		ID:       "call-xyz",
		Function: model.FunctionDefinitionParam{Name: "streamer"},
	}

	st := &mockStreamTool{name: "streamer", chunks: []any{"hello", " world"}}
	ch := make(chan *event.Event, 4)

	// Call and collect
	res, err := f.executeStreamableTool(ctx, inv, toolCall, st, ch)
	require.NoError(t, err)
	require.NotNil(t, res)
	// merged content should equal concatenation
	require.Equal(t, "hello world", res.(string))

	// Expect two partial events
	var evts []*event.Event
	for i := 0; i < 2; i++ {
		select {
		case e := <-ch:
			evts = append(evts, e)
		default:
			// drain synchronously; function sends before return
			e := <-ch
			evts = append(evts, e)
		}
	}

	require.Len(t, evts, 2)
	for i, e := range evts {
		require.NotNil(t, e)
		require.NotNil(t, e.Response)
		require.Equal(t, model.ObjectTypeToolResponse, e.Object)
		require.True(t, e.IsPartial, "event %d should be partial", i)
		require.False(t, e.Done)
		require.Equal(t, inv.InvocationID, e.InvocationID)
		require.Equal(t, inv.AgentName, e.Author)
		require.Equal(t, inv.Branch, e.Branch)
		require.Len(t, e.Choices, 1)
		require.Equal(t, "call-xyz", e.Choices[0].Message.ToolID)
	}
}

// Tool that requests skipping summarization.
type skipSummCallableTool struct {
	declaration *tool.Declaration
	result      any
	skip        bool
	longRun     bool
}

func (m *skipSummCallableTool) Declaration() *tool.Declaration { return m.declaration }
func (m *skipSummCallableTool) Call(ctx context.Context, args []byte) (any, error) {
	return m.result, nil
}

// Mark that outer summarization should be skipped.
func (m *skipSummCallableTool) SkipSummarization() bool { return m.skip }

// Implement LongRunner to allow returning nil and skipping child event creation when longRun is true.
func (m *skipSummCallableTool) LongRunning() bool { return m.longRun }

// Verify SkipSummarization propagation and EndInvocation flag in the sequential path.
func TestHandleFunctionCalls_SkipSummarizationSequential_SetsEndInvocation(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	t1 := &skipSummCallableTool{
		declaration: &tool.Declaration{Name: "t1"},
		result:      map[string]any{"ok": true},
		skip:        true,
	}
	tools := map[string]tool.Tool{"t1": t1}
	inv := &agent.Invocation{InvocationID: "inv-s", AgentName: "agent"}

	req := &model.Request{Tools: tools}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{{
			ID: "c1", Function: model.FunctionDefinitionParam{Name: "t1", Arguments: []byte(`{}`)},
		}}},
	}}}
	evenChan := make(chan *event.Event, 10)
	defer close(evenChan)
	p.ProcessResponse(ctx, inv, req, rsp, evenChan)
	var evt *event.Event
	for e := range evenChan {
		evt = e
		break
	}
	require.NotNil(t, evt)
	require.NotNil(t, evt.Actions)
	require.True(t, evt.Actions.SkipSummarization)
	require.True(t, inv.EndInvocation, "invocation should be marked to end when skipping summarization")
}

// Verify SkipSummarization propagation in the no-child-events path (e.g., long-running returns nil).
func TestHandleFunctionCalls_SkipSummarization_NoChildEvents_SetsEndInvocation(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	t1 := &skipSummCallableTool{
		declaration: &tool.Declaration{Name: "t1"},
		// Return nil and mark as LongRunning so executeToolCall yields no choice.
		result:  nil,
		longRun: true,
		skip:    true,
	}
	tools := map[string]tool.Tool{"t1": t1}
	inv := &agent.Invocation{InvocationID: "inv-nc", AgentName: "agent"}

	req := &model.Request{Tools: tools}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{{
			ID: "c1", Function: model.FunctionDefinitionParam{Name: "t1", Arguments: []byte(`{}`)},
		}}},
	}}}

	evenChan := make(chan *event.Event, 10)
	defer close(evenChan)
	p.ProcessResponse(ctx, inv, req, rsp, evenChan)
	var evt *event.Event
	for e := range evenChan {
		evt = e
		break
	}
	require.NotNil(t, evt.Actions)
	require.True(t, evt.Actions.SkipSummarization, "merged event should propagate SkipSummarization when no child events")
	require.True(t, inv.EndInvocation, "invocation should end when skipping summarization")
}

// Verify SkipSummarization propagation and EndInvocation in parallel execution.
func TestHandleFunctionCalls_SkipSummarization_Parallel_PropagatesFlag(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(true, nil)

	tSkip := &skipSummCallableTool{
		declaration: &tool.Declaration{Name: "ts"},
		result:      map[string]any{"v": 1},
		skip:        true,
	}
	tOther := &mockCallableTool{declaration: &tool.Declaration{Name: "to"}, callFn: func(_ context.Context, _ []byte) (any, error) { return "ok", nil }}
	tools := map[string]tool.Tool{"ts": tSkip, "to": tOther}
	inv := &agent.Invocation{InvocationID: "inv-p", AgentName: "agent"}

	toolCalls := []model.ToolCall{
		{ID: "c1", Function: model.FunctionDefinitionParam{Name: "ts", Arguments: []byte(`{}`)}},
		{ID: "c2", Function: model.FunctionDefinitionParam{Name: "to", Arguments: []byte(`{}`)}},
	}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{Message: model.Message{ToolCalls: toolCalls}}}}

	evt, err := p.handleFunctionCalls(ctx, inv, rsp, tools, nil)
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.NotNil(t, evt.Actions)
	require.True(t, evt.Actions.SkipSummarization)
	// Parallel path returns early from handleFunctionCalls (via executeToolCallsInParallel),
	// so EndInvocation is not toggled here. We only verify flag propagation.
}

// stream tool forwarding a single final assistant message (no deltas).
type finalOnlyInnerEventStreamTool struct{ name string }

func (s *finalOnlyInnerEventStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}
func (s *finalOnlyInnerEventStreamTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	st := tool.NewStream(1)
	go func() {
		defer st.Writer.Close()
		// Final full assistant message only, no deltas prior.
		inner := event.New("inv-final", "child", event.WithResponse(&model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "final"},
		}}}))
		inner.Branch = "br"
		st.Writer.Send(tool.StreamChunk{Content: inner}, nil)
	}()
	return st.Reader, nil
}

// Ensure the final full inner assistant message is forwarded when there were no prior deltas.
func TestExecuteStreamableTool_ForwardsFinalOnlyInnerMessage(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-final", AgentName: "parent", Branch: "br", Model: &mockModel{}}
	tc := model.ToolCall{ID: "c1", Function: model.FunctionDefinitionParam{Name: "inner-final"}}
	st := &finalOnlyInnerEventStreamTool{name: "inner-final"}
	ch := make(chan *event.Event, 2)

	res, err := f.executeStreamableTool(ctx, inv, tc, st, ch)
	require.NoError(t, err)
	require.Equal(t, "final", res.(string))

	// Exactly one forwarded event (the final full assistant message)
	select {
	case e := <-ch:
		require.NotNil(t, e)
		require.Equal(t, inv.InvocationID, e.InvocationID)
		require.Equal(t, inv.Branch, e.Branch)
		require.NotNil(t, e.Response)
		require.False(t, e.Response.IsPartial)
		require.Equal(t, model.RoleAssistant, e.Choices[0].Message.Role)
		require.Equal(t, "final", e.Choices[0].Message.Content)
	default:
		t.Fatalf("expected the final inner assistant message to be forwarded")
	}

	// And no more events
	select {
	case <-ch:
		t.Fatalf("did not expect more than one forwarded event")
	default:
	}
}

// Minimal callable tool used by tests above
type mockTransferCallableTool struct {
	declaration *tool.Declaration
	callFn      func(ctx context.Context, args []byte) (any, error)
}

func (m *mockTransferCallableTool) Declaration() *tool.Declaration { return m.declaration }
func (m *mockTransferCallableTool) Call(ctx context.Context, args []byte) (any, error) {
	return m.callFn(ctx, args)
}

// prefTool implements both StreamableTool and CallableTool, with a stream
// preference toggle to validate isStreamable logic.
type prefTool struct {
	name        string
	preferInner bool
}

func (p *prefTool) Declaration() *tool.Declaration                  { return &tool.Declaration{Name: p.name} }
func (p *prefTool) StreamInner() bool                               { return p.preferInner }
func (p *prefTool) Call(ctx context.Context, _ []byte) (any, error) { return "called:" + p.name, nil }
func (p *prefTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	s := tool.NewStream(2)
	go func() {
		defer s.Writer.Close()
		s.Writer.Send(tool.StreamChunk{Content: "streamed:" + p.name}, nil)
	}()
	return s.Reader, nil
}

// Ensure executeTool respects streamInnerPreference: when false, fallback to callable path.
func TestExecuteTool_RespectsStreamInnerPreference(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-pref", AgentName: "tester", Model: &mockModel{}}
	toolCall := model.ToolCall{ID: "call-1", Function: model.FunctionDefinitionParam{Name: "pref"}}
	ch := make(chan *event.Event, 2)

	// preferInner=false => should call callable path
	pt := &prefTool{name: "pref", preferInner: false}
	res, err := f.executeTool(ctx, inv, toolCall, pt, ch)
	require.NoError(t, err)
	str, _ := res.(string)
	require.Equal(t, "called:pref", str)
	require.Equal(t, 0, len(ch), "should not emit streaming events when inner disabled")

	// preferInner=true => should stream
	pt.preferInner = true
	res2, err := f.executeTool(ctx, inv, toolCall, pt, ch)
	require.NoError(t, err)
	str2, _ := res2.(string)
	require.Equal(t, "streamed:pref", str2)
	// Should have at least one partial tool.response
	select {
	case e := <-ch:
		require.NotNil(t, e)
		require.Equal(t, model.ObjectTypeToolResponse, e.Object)
		require.True(t, e.IsPartial)
	default:
		t.Fatalf("expected a partial tool.response event when streaming")
	}
}

func TestMergeParallelToolCallResponseEvents_PropagatesSkipSummarization(t *testing.T) {
	e1 := event.New("inv", "a", event.WithResponse(&model.Response{Model: "m1"}))
	e2 := event.New("inv", "a", event.WithResponse(&model.Response{Model: "m1"}))
	e2.Actions = &event.EventActions{SkipSummarization: true}

	merged := mergeParallelToolCallResponseEvents([]*event.Event{e1, e2})
	require.NotNil(t, merged)
	require.NotNil(t, merged.Actions)
	require.True(t, merged.Actions.SkipSummarization)
}

// Ensure FunctionTool configured to skip summarization ends the turn.
func TestHandleFunctionCalls_FunctionTool_SkipSummarization_EndInvocation(
	t *testing.T,
) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	// Build a simple function tool returning a small map.
	fn := func(_ context.Context, _ struct{}) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	}
	ft := function.NewFunctionTool(fn,
		function.WithName("ft"),
		function.WithSkipSummarization(true),
	)
	tools := map[string]tool.Tool{"ft": ft}
	inv := &agent.Invocation{InvocationID: "inv-ft", AgentName: "a"}
	req := &model.Request{Tools: tools}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{{
			ID: "c1",
			Function: model.FunctionDefinitionParam{
				Name: "ft", Arguments: []byte(`{}`),
			},
		}}},
	}}}
	ch := make(chan *event.Event, 4)
	defer close(ch)
	p.ProcessResponse(ctx, inv, req, rsp, ch)
	var got *event.Event
	for e := range ch {
		got = e
		break
	}
	require.NotNil(t, got)
	require.NotNil(t, got.Actions)
	require.True(t, got.Actions.SkipSummarization)
	require.True(t, inv.EndInvocation)
}

// Ensure StreamableFunctionTool with skip flag ends the turn as well.
func TestHandleFunctionCalls_StreamableFunctionTool_SkipSummarization_EndInvocation(
	t *testing.T,
) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	// stream returns one chunk then closes
	sfn := func(_ context.Context, _ struct{}) (*tool.StreamReader, error) {
		s := tool.NewStream(1)
		go func() {
			defer s.Writer.Close()
			_ = s.Writer.Send(tool.StreamChunk{Content: "ok"}, nil)
		}()
		return s.Reader, nil
	}
	st := function.NewStreamableFunctionTool[struct{}, string](
		sfn,
		function.WithName("st"),
		function.WithSkipSummarization(true),
	)
	tools := map[string]tool.Tool{"st": st}
	inv := &agent.Invocation{InvocationID: "inv-st", AgentName: "a",
		Model: &mockModel{},
	}
	req := &model.Request{Tools: tools}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{{
			ID: "c1",
			Function: model.FunctionDefinitionParam{
				Name: "st", Arguments: []byte(`{}`),
			},
		}}},
	}}}
	ch := make(chan *event.Event, 8)
	defer close(ch)
	p.ProcessResponse(ctx, inv, req, rsp, ch)
	// Drain until we see the non-partial tool.response
	var final *event.Event
	for e := range ch {
		if e != nil && e.Response != nil && !e.IsPartial &&
			e.Object == model.ObjectTypeToolResponse {
			final = e
			break
		}
	}
	require.NotNil(t, final)
	require.NotNil(t, final.Actions)
	require.True(t, final.Actions.SkipSummarization)
	require.True(t, inv.EndInvocation)
}

// Ensure NamedTool wrapper still propagates the skip summarization flag.
func TestHandleFunctionCalls_NamedTool_PropagatesSkipSummarization(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	fn := func(_ context.Context, _ struct{}) (map[string]int, error) {
		return map[string]int{"v": 1}, nil
	}
	ft := function.NewFunctionTool(fn,
		function.WithName("raw"),
		function.WithSkipSummarization(true),
	)
	// Wrap into NamedToolSet to prefix the name.
	nts := itool.NewNamedToolSet(&mockToolSet{tools: []tool.Tool{ft}})
	ts := nts.Tools(ctx)
	require.Len(t, ts, 1)
	named := ts[0]
	name := named.Declaration().Name
	tools := map[string]tool.Tool{name: named}

	inv := &agent.Invocation{InvocationID: "inv-nt", AgentName: "ag"}
	req := &model.Request{Tools: tools}
	rsp := &model.Response{Model: "m", Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{{
			ID: "c1",
			Function: model.FunctionDefinitionParam{
				Name: name, Arguments: []byte(`{}`),
			},
		}}},
	}}}
	ch := make(chan *event.Event, 4)
	defer close(ch)
	p.ProcessResponse(ctx, inv, req, rsp, ch)
	var got *event.Event
	for e := range ch {
		got = e
		break
	}
	require.NotNil(t, got)
	require.NotNil(t, got.Actions)
	require.True(t, got.Actions.SkipSummarization)
	require.True(t, inv.EndInvocation)
}

// stream tool sending struct chunks to exercise JSON marshaling path
type structStreamTool struct{ name string }

func (s *structStreamTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }
func (s *structStreamTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	st := tool.NewStream(2)
	go func() {
		defer st.Writer.Close()
		st.Writer.Send(tool.StreamChunk{Content: struct {
			A int `json:"a"`
		}{A: 1}}, nil)
		st.Writer.Send(tool.StreamChunk{Content: struct {
			B string `json:"b"`
		}{B: "x"}}, nil)
	}()
	return st.Reader, nil
}

func TestExecuteStreamableTool_ChunkStructJSON(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-json", AgentName: "tester", Branch: "br", Model: &mockModel{}}
	tc := model.ToolCall{ID: "c1", Function: model.FunctionDefinitionParam{Name: "s"}}
	st := &structStreamTool{name: "s"}
	ch := make(chan *event.Event, 4)
	res, err := f.executeStreamableTool(ctx, inv, tc, st, ch)
	require.NoError(t, err)
	// merged should be concatenation of marshaled chunks
	require.Equal(t, `{"a":1}{"b":"x"}`, res.(string))
}

// stream tool forwarding inner *event.Event
type innerEventStreamTool struct{ name string }

func (s *innerEventStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}
func (s *innerEventStreamTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	st := tool.NewStream(4)
	go func() {
		defer st.Writer.Close()
		// delta chunk
		ev1 := event.New("inv-fwd", "child", event.WithResponse(&model.Response{Choices: []model.Choice{{Delta: model.Message{Content: "abc"}}}}))
		ev1.Branch = "b"
		st.Writer.Send(tool.StreamChunk{Content: ev1}, nil)
		// final full assistant message
		ev2 := event.New("inv-fwd", "child", event.WithResponse(&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "def"}}}}))
		ev2.Branch = "b"
		st.Writer.Send(tool.StreamChunk{Content: ev2}, nil)
	}()
	return st.Reader, nil
}

func TestExecuteStreamableTool_ForwardsInnerEvents(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-fwd", AgentName: "parent", Branch: "b", Model: &mockModel{}}
	tc := model.ToolCall{ID: "c1", Function: model.FunctionDefinitionParam{Name: "inner"}}
	st := &innerEventStreamTool{name: "inner"}
	ch := make(chan *event.Event, 4)
	res, err := f.executeStreamableTool(ctx, inv, tc, st, ch)
	require.NoError(t, err)
	require.Equal(t, "abcdef", res.(string))
	// At least one forwarded event (delta). Final full message may be suppressed.
	n := len(ch)
	require.GreaterOrEqual(t, n, 1)
	e1 := <-ch
	require.Equal(t, inv.InvocationID, e1.InvocationID)
	require.Equal(t, inv.Branch, e1.Branch)
	if n > 1 {
		e2 := <-ch
		require.Equal(t, inv.InvocationID, e2.InvocationID)
		require.Equal(t, inv.Branch, e2.Branch)
	}
}

// Mock tool for transfer testing
type mockTransferTool struct {
	name        string
	description string
}

func (m *mockTransferTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

// Mock agent info for transfer testing
type mockTransferAgentInfo struct {
	name        string
	description string
}

func (m *mockTransferAgentInfo) Name() string        { return m.name }
func (m *mockTransferAgentInfo) Description() string { return m.description }

// Mock sub-agent for transfer testing
type mockTransferSubAgent struct {
	info *mockTransferAgentInfo
}

func (m *mockTransferSubAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}

func (m *mockTransferSubAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockTransferSubAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.info.name,
		Description: m.info.description,
	}
}

func (m *mockTransferSubAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockTransferSubAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// Mock agent for transfer testing
type mockTransferAgent struct {
	subAgents []agent.Agent
}

func (m *mockTransferAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}

func (m *mockTransferAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockTransferAgent) Info() agent.Info {
	return agent.Info{}
}

func (m *mockTransferAgent) SubAgents() []agent.Agent {
	return m.subAgents
}

func (m *mockTransferAgent) FindSubAgent(name string) agent.Agent {
	for _, a := range m.subAgents {
		if a.Info().Name == name {
			return a
		}
	}
	return nil
}

func TestHandleFunctionCallsAndSendEvent_StopErrorEmitsErrorEvent(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)

	inv := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "inv-err",
	}

	// Tool returns StopError so executeToolCall propagates error.
	errTool := &mockCallableTool{
		declaration: &tool.Declaration{Name: "err"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			return nil, agent.NewStopError("stop")
		},
	}
	tools := map[string]tool.Tool{"err": errTool}

	rsp := &model.Response{
		Model: "m",
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{{
					ID: "c1",
					Function: model.FunctionDefinitionParam{
						Name:      "err",
						Arguments: []byte(`{}`),
					},
				}},
			},
		}},
	}
	evtCh := make(chan *event.Event, 1)
	_, err := p.handleFunctionCallsAndSendEvent(ctx, inv, rsp, tools, evtCh)
	require.Error(t, err)
	select {
	case e := <-evtCh:
		require.NotNil(t, e)
		require.Equal(t, model.ObjectTypeError, e.Object)
	default:
		t.Fatalf("expected error event to be sent")
	}
}

func TestCollectParallelToolResults_ContextCancelled(t *testing.T) {
	p := NewFunctionCallResponseProcessor(true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := p.collectParallelToolResults(ctx, make(chan toolResult), 2)
	require.NotNil(t, res)
	require.NoError(t, err)
}

func TestExecuteToolWithCallbacks_BeforeCustomResult(t *testing.T) {
	cb := tool.NewCallbacks()
	cb.RegisterBeforeTool(func(_ context.Context, _ string,
		_ *tool.Declaration, _ *[]byte) (any, error) {
		return map[string]any{"v": 1}, nil
	})
	p := NewFunctionCallResponseProcessor(false, cb)
	ctx := context.Background()
	inv := &agent.Invocation{}
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "t"},
		callFn: func(_ context.Context, _ []byte) (any, error) { return "x", nil }}
	res, _, err := p.executeToolWithCallbacks(ctx, inv, model.ToolCall{Function: model.FunctionDefinitionParam{Name: "t"}}, tl, nil)
	require.NoError(t, err)
	b, _ := json.Marshal(map[string]any{"v": 1})
	require.JSONEq(t, string(b), string(mustJSON(res)))
}

func TestExecuteToolWithCallbacks_BeforeError(t *testing.T) {
	cb := tool.NewCallbacks()
	cb.RegisterBeforeTool(func(_ context.Context, _ string,
		_ *tool.Declaration, _ *[]byte) (any, error) {
		return nil, fmt.Errorf("fail")
	})
	p := NewFunctionCallResponseProcessor(false, cb)
	ctx := context.Background()
	inv := &agent.Invocation{}
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "t"},
		callFn: func(_ context.Context, _ []byte) (any, error) { return "x", nil }}
	_, _, err := p.executeToolWithCallbacks(ctx, inv, model.ToolCall{Function: model.FunctionDefinitionParam{Name: "t"}}, tl, nil)
	require.Error(t, err)
}

func TestExecuteToolWithCallbacks_AfterOverrideAndError(t *testing.T) {
	cb := tool.NewCallbacks()
	cb.RegisterAfterTool(func(_ context.Context, _ string,
		_ *tool.Declaration, _ []byte, _ any, _ error) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	p := NewFunctionCallResponseProcessor(false, cb)
	ctx := context.Background()
	inv := &agent.Invocation{}
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "t"},
		callFn: func(_ context.Context, _ []byte) (any, error) { return "x", nil }}
	res, _, err := p.executeToolWithCallbacks(ctx, inv, model.ToolCall{Function: model.FunctionDefinitionParam{Name: "t"}}, tl, nil)
	require.NoError(t, err)
	b, _ := json.Marshal(map[string]any{"ok": true})
	require.JSONEq(t, string(b), string(mustJSON(res)))

	// AfterError branch.
	cb = tool.NewCallbacks()
	cb.RegisterAfterTool(func(_ context.Context, _ string,
		_ *tool.Declaration, _ []byte, _ any, _ error) (any, error) {
		return nil, fmt.Errorf("bad")
	})
	inv2 := &agent.Invocation{}
	p.toolCallbacks = cb
	_, _, err = p.executeToolWithCallbacks(ctx, inv2, model.ToolCall{Function: model.FunctionDefinitionParam{Name: "t"}}, tl, nil)
	require.Error(t, err)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// stream tool that returns error on StreamableCall.
type errStreamTool struct{ name string }

func (e *errStreamTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: e.name} }
func (e *errStreamTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	return nil, fmt.Errorf("stream call error")
}

func TestExecuteStreamableTool_StreamableCallError(t *testing.T) {
	f := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "inv-s", AgentName: "tester", Branch: "b", Model: &mockModel{}}
	tc := model.ToolCall{ID: "x", Function: model.FunctionDefinitionParam{Name: "s"}}
	st := &errStreamTool{name: "s"}
	ch := make(chan *event.Event, 1)
	res, err := f.executeStreamableTool(ctx, inv, tc, st, ch)
	require.Error(t, err)
	require.Nil(t, res)
}

func TestMarshalChunkToText_MarshalError(t *testing.T) {
	// Passing a function is not JSON-serializable, forcing fmt.Sprintf path.
	text := marshalChunkToText(func() {})
	require.NotEmpty(t, text)
}

func TestExecuteTool_NamedTool(t *testing.T) {
	p := NewFunctionCallResponseProcessor(false, nil)
	ctx := context.Background()
	inv := &agent.Invocation{}
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "t"},
		callFn: func(_ context.Context, _ []byte) (any, error) { return map[string]string{"k": "v"}, nil }}
	toolSet := &mockToolSet{tools: []tool.Tool{tl}}
	namedToolSet := itool.NewNamedToolSet(toolSet)
	namedTools := namedToolSet.Tools(ctx)
	require.Len(t, namedTools, 1)
	nameTool := namedTools[0]
	require.IsType(t, &itool.NamedTool{}, nameTool)
	res, _, err := p.executeToolWithCallbacks(ctx, inv, model.ToolCall{Function: model.FunctionDefinitionParam{Name: "t"}}, nameTool, nil)
	require.NoError(t, err)
	b, _ := json.Marshal(map[string]any{"k": "v"})
	require.JSONEq(t, string(b), string(mustJSON(res)))
}

type mockToolSet struct {
	tools []tool.Tool
}

func (s *mockToolSet) Tools(ctx context.Context) []tool.Tool {
	return s.tools
}

func (s *mockToolSet) Close() error {
	return nil
}

func (s *mockToolSet) Name() string {
	return "mock"
}
