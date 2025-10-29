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
