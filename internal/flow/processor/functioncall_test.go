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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
	p := NewFunctionCallResponseProcessor()

	// Prepare invocation with a parent agent that has a sub-agent named weather-agent.
	inv := &agent.Invocation{
		AgentName: "weather-agent",
	}

	// Prepare tools: only transfer tool is exposed, no weather-agent tool.
	tools := map[string]tool.Tool{
		"weather-agent": &mockCallableTool{
			declaration: &tool.Declaration{Name: "weather-agent", Description: "transfer"},
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

	choice, err := p.executeToolCall(ctx, inv, pc, tools, 0)
	res, _ := json.Marshal("Tokyo'weather is good")
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, string(res), choice.Message.Content)
}

func TestExecuteToolCall_ToolNotFound_ReturnsErrorChoice(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor()

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

	choice, err := p.executeToolCall(ctx, inv, pc2, tools, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, ErrorToolNotFound, choice.Message.Content)
	assert.Equal(t, "call-404", choice.Message.ToolID)
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
					AgentName:     "weather-agent",
					Message:       "What's the weather like in Tokyo?",
					EndInvocation: false,
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
					AgentName:     "research-agent",
					Message:       "Task delegated from coordinator",
					EndInvocation: false,
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
					AgentName:     "weather-agent",
					Message:       "Task delegated from coordinator",
					EndInvocation: false,
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
			assert.Equal(t, expectedReq.EndInvocation, actualReq.EndInvocation, "end_invocation should match")
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

// Mock invocation for transfer testing
type mockTransferInvocation struct {
	agent *mockTransferAgent
}

func (m *mockTransferInvocation) Agent() agent.Agent { return m.agent }
