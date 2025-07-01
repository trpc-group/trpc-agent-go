package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func TestCreateAgentCallbacks(t *testing.T) {
	callbacks := createAgentCallbacks()
	require.NotNil(t, callbacks)
	require.Greater(t, len(callbacks.BeforeAgent), 0)
	require.Greater(t, len(callbacks.AfterAgent), 0)
}

func TestCreateModelCallbacks(t *testing.T) {
	callbacks := createModelCallbacks()
	require.NotNil(t, callbacks)
	require.Greater(t, len(callbacks.BeforeModel), 0)
	require.Greater(t, len(callbacks.AfterModel), 0)
}

func TestCreateToolCallbacks(t *testing.T) {
	callbacks := createToolCallbacks()
	require.NotNil(t, callbacks)
	require.Greater(t, len(callbacks.BeforeTool), 0)
	require.Greater(t, len(callbacks.AfterTool), 0)
}

func TestAgentCallbacks_SkipExecution(t *testing.T) {
	callbacks := createAgentCallbacks()

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation",
		Message:      model.NewUserMessage("skip"),
	}

	customResponse, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.NotNil(t, customResponse)
	require.Greater(t, len(customResponse.Choices), 0)
}

func TestAgentCallbacks_CustomResponse(t *testing.T) {
	callbacks := createAgentCallbacks()

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation",
		Message:      model.NewUserMessage("custom"),
	}

	customResponse, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.NotNil(t, customResponse)
	require.Greater(t, len(customResponse.Choices), 0)
}

func TestModelCallbacks_SkipExecution(t *testing.T) {
	callbacks := createModelCallbacks()

	request := &model.Request{
		Messages: []model.Message{},
	}

	customResponse, err := callbacks.RunBeforeModel(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, customResponse)
	require.Greater(t, len(customResponse.Choices), 0)
}

func TestToolCallbacks_SkipExecution(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "skip-tool",
		Description: "A tool to skip",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "skip-tool", declaration, args)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "true", result["skipped"])
}

func TestToolCallbacks_CustomResult(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "calculator",
		Description: "A calculator tool",
	}

	args := []byte(`{"a":0,"b":0}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "calculator", declaration, args)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(CalculatorOutput)
	require.True(t, ok)
	require.Equal(t, 42, result.Result)
}

func TestToolCallbacks_OverrideResult(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "calculator",
		Description: "A calculator tool",
	}

	args := []byte(`{"a":5,"b":3}`)
	result := CalculatorOutput{Result: 8}

	customResult, err := callbacks.RunAfterTool(context.Background(), "calculator", declaration, args, result, nil)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	formattedResult, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "The answer is 8", formattedResult["formatted_result"])
}
