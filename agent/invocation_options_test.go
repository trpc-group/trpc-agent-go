//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWithInvocationBranch(t *testing.T) {
	inv := NewInvocation(
		WithInvocationBranch("test-branch"),
	)
	require.NotNil(t, inv)
	assert.Equal(t, "test-branch", inv.Branch)
}

func TestWithInvocationEndInvocation(t *testing.T) {
	tests := []struct {
		name          string
		endInvocation bool
	}{
		{
			name:          "set to true",
			endInvocation: true,
		},
		{
			name:          "set to false",
			endInvocation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := NewInvocation(
				WithInvocationEndInvocation(tt.endInvocation),
			)
			require.NotNil(t, inv)
			assert.Equal(t, tt.endInvocation, inv.EndInvocation)
		})
	}
}

func TestWithInvocationSession(t *testing.T) {
	sess := &session.Session{
		ID: "test-session-123",
	}

	inv := NewInvocation(
		WithInvocationSession(sess),
	)
	require.NotNil(t, inv)
	assert.Equal(t, sess, inv.Session)
	assert.Equal(t, "test-session-123", inv.Session.ID)
}

func TestWithInvocationModel(t *testing.T) {
	mockModel := &mockModel{name: "test-model"}

	inv := NewInvocation(
		WithInvocationModel(mockModel),
	)
	require.NotNil(t, inv)
	assert.Equal(t, mockModel, inv.Model)
}

func TestWithInvocationRunOptions(t *testing.T) {
	runOpts := RunOptions{
		RuntimeState: map[string]any{
			"key1": "value1",
		},
		KnowledgeFilter: map[string]any{
			"filter1": "value1",
		},
		RequestID: "test-request-123",
	}

	inv := NewInvocation(
		WithInvocationRunOptions(runOpts),
	)
	require.NotNil(t, inv)
	assert.Equal(t, runOpts, inv.RunOptions)
	assert.Equal(t, "test-request-123", inv.RunOptions.RequestID)
	assert.Equal(t, "value1", inv.RunOptions.RuntimeState["key1"])
}

func TestWithInvocationTransferInfo(t *testing.T) {
	transferInfo := &TransferInfo{
		TargetAgentName: "target-agent",
	}

	inv := NewInvocation(
		WithInvocationTransferInfo(transferInfo),
	)
	require.NotNil(t, inv)
	assert.Equal(t, transferInfo, inv.TransferInfo)
	assert.Equal(t, "target-agent", inv.TransferInfo.TargetAgentName)
}

func TestWithInvocationStructuredOutput(t *testing.T) {
	structuredOutput := &model.StructuredOutput{
		Type: "object",
	}

	inv := NewInvocation(
		WithInvocationStructuredOutput(structuredOutput),
	)
	require.NotNil(t, inv)
	assert.Equal(t, structuredOutput, inv.StructuredOutput)
}

func TestWithInvocationStructuredOutputType(t *testing.T) {
	type TestStruct struct {
		Field1 string
		Field2 int
	}

	outputType := reflect.TypeOf(TestStruct{})

	inv := NewInvocation(
		WithInvocationStructuredOutputType(outputType),
	)
	require.NotNil(t, inv)
	assert.Equal(t, outputType, inv.StructuredOutputType)
	assert.Equal(t, "TestStruct", inv.StructuredOutputType.Name())
}

func TestWithInvocationMemoryService(t *testing.T) {
	mockMemoryService := &mockMemoryService{}

	inv := NewInvocation(
		WithInvocationMemoryService(mockMemoryService),
	)
	require.NotNil(t, inv)
	assert.Equal(t, mockMemoryService, inv.MemoryService)
}

func TestWithInvocationArtifactService(t *testing.T) {
	mockArtifactService := &mockArtifactService{}

	inv := NewInvocation(
		WithInvocationArtifactService(mockArtifactService),
	)
	require.NotNil(t, inv)
	assert.Equal(t, mockArtifactService, inv.ArtifactService)
}

func TestWithInvocationEventFilterKey(t *testing.T) {
	inv := NewInvocation(
		WithInvocationEventFilterKey("test-filter-key"),
	)
	require.NotNil(t, inv)
	assert.Equal(t, "test-filter-key", inv.GetEventFilterKey())
}

func TestMultipleInvocationOptions(t *testing.T) {
	sess := &session.Session{ID: "multi-test-session"}
	transferInfo := &TransferInfo{TargetAgentName: "multi-target"}

	inv := NewInvocation(
		WithInvocationID("multi-test-id"),
		WithInvocationBranch("multi-branch"),
		WithInvocationSession(sess),
		WithInvocationEndInvocation(true),
		WithInvocationTransferInfo(transferInfo),
		WithInvocationEventFilterKey("multi-filter"),
	)

	require.NotNil(t, inv)
	assert.Equal(t, "multi-test-id", inv.InvocationID)
	assert.Equal(t, "multi-branch", inv.Branch)
	assert.Equal(t, sess, inv.Session)
	assert.Equal(t, true, inv.EndInvocation)
	assert.Equal(t, transferInfo, inv.TransferInfo)
	assert.Equal(t, "multi-filter", inv.GetEventFilterKey())
}

func TestWithAllowedTools(t *testing.T) {
	tests := []struct {
		name      string
		toolNames []string
		expected  []string
	}{
		{
			name:      "with tools",
			toolNames: []string{"calculator", "time_tool", "text_tool"},
			expected:  []string{"calculator", "time_tool", "text_tool"},
		},
		{
			name:      "empty list",
			toolNames: []string{},
			expected:  []string{},
		},
		{
			name:      "nil list",
			toolNames: nil,
			expected:  nil,
		},
		{
			name:      "single tool",
			toolNames: []string{"calculator"},
			expected:  []string{"calculator"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runOpts := &RunOptions{}
			opt := WithAllowedTools(tt.toolNames)
			opt(runOpts)

			if tt.expected == nil {
				assert.Nil(t, runOpts.AllowedTools)
			} else {
				assert.Equal(t, tt.expected, runOpts.AllowedTools)
			}
		})
	}
}

func TestWithAllowedAgentTools(t *testing.T) {
	tests := []struct {
		name       string
		agentTools map[string][]string
		wantNil    bool
		checkCopy  bool
	}{
		{
			name: "with multiple agents",
			agentTools: map[string][]string{
				"agent1": {"tool_a", "tool_b"},
				"agent2": {"tool_c"},
				"agent3": {"tool_d", "tool_e", "tool_f"},
			},
			wantNil:   false,
			checkCopy: true,
		},
		{
			name:       "empty map",
			agentTools: map[string][]string{},
			wantNil:    false,
			checkCopy:  false,
		},
		{
			name:       "nil map",
			agentTools: nil,
			wantNil:    true,
			checkCopy:  false,
		},
		{
			name: "single agent",
			agentTools: map[string][]string{
				"agent1": {"tool_a"},
			},
			wantNil:   false,
			checkCopy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runOpts := &RunOptions{}
			opt := WithAllowedAgentTools(tt.agentTools)
			opt(runOpts)

			if tt.wantNil {
				assert.Nil(t, runOpts.AllowedAgentTools)
			} else {
				assert.NotNil(t, runOpts.AllowedAgentTools)
				assert.Equal(t, len(tt.agentTools), len(runOpts.AllowedAgentTools))

				// Verify content matches
				for agent, tools := range tt.agentTools {
					assert.Equal(t, tools, runOpts.AllowedAgentTools[agent])
				}

				// Verify it's a copy (different map instance)
				if tt.checkCopy && tt.agentTools != nil {
					// Modify original - should not affect the copy
					if len(tt.agentTools) > 0 {
						for k := range tt.agentTools {
							originalTools := tt.agentTools[k]
							if len(originalTools) > 0 {
								// Modify the original map
								tt.agentTools[k] = append(originalTools, "new_tool")
								// Verify the copy is not affected
								assert.NotEqual(t, tt.agentTools[k], runOpts.AllowedAgentTools[k])
							}
							break
						}
					}
				}
			}
		})
	}
}

func TestWithAllowedToolsAndAgentToolsCombined(t *testing.T) {
	// Test using both options together
	allowedTools := []string{"calculator", "time_tool"}
	agentTools := map[string][]string{
		"math-agent": {"calculator"},
		"time-agent": {"time_tool"},
	}

	runOpts := &RunOptions{}

	opt1 := WithAllowedTools(allowedTools)
	opt1(runOpts)

	opt2 := WithAllowedAgentTools(agentTools)
	opt2(runOpts)

	// Verify both are set
	assert.Equal(t, allowedTools, runOpts.AllowedTools)
	assert.Equal(t, len(agentTools), len(runOpts.AllowedAgentTools))
	assert.Equal(t, []string{"calculator"}, runOpts.AllowedAgentTools["math-agent"])
	assert.Equal(t, []string{"time_tool"}, runOpts.AllowedAgentTools["time-agent"])
}

// Mock implementations for testing

type mockModel struct {
	name string
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *mockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "mock response",
			},
		}},
	}
	close(ch)
	return ch, nil
}

type mockMemoryService struct{}

func (m *mockMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, mem string, topics []string) error {
	return nil
}

func (m *mockMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, mem string, topics []string) error {
	return nil
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return nil
}

func (m *mockMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return nil
}

func (m *mockMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) Tools() []tool.Tool {
	return nil
}

type mockArtifactService struct{}

func (m *mockArtifactService) SaveArtifact(ctx context.Context, info artifact.SessionInfo, filename string, artifact *artifact.Artifact) (int, error) {
	return 1, nil
}

func (m *mockArtifactService) LoadArtifact(ctx context.Context, info artifact.SessionInfo, filename string, version *int) (*artifact.Artifact, error) {
	return nil, nil
}

func (m *mockArtifactService) ListArtifactKeys(ctx context.Context, info artifact.SessionInfo) ([]string, error) {
	return nil, nil
}

func (m *mockArtifactService) DeleteArtifact(ctx context.Context, info artifact.SessionInfo, filename string) error {
	return nil
}

func (m *mockArtifactService) ListVersions(ctx context.Context, info artifact.SessionInfo, filename string) ([]int, error) {
	return nil, nil
}
