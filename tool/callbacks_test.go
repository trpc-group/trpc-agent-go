//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewToolCallbacks(t *testing.T) {
	callbacks := tool.NewToolCallbacks()
	require.NotNil(t, callbacks)
	require.Empty(t, callbacks.BeforeTool)
	require.Empty(t, callbacks.AfterTool)
}

func TestRegisterBeforeTool(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		return nil, nil
	}

	callbacks.RegisterBeforeTool(callback)

	require.Equal(t, 1, len(callbacks.BeforeTool))
}

func TestRegisterAfterTool(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
		result any,
		runErr error,
	) (any, error) {
		return nil, nil
	}

	callbacks.RegisterAfterTool(callback)

	require.Equal(t, 1, len(callbacks.AfterTool))
}

func TestRunBeforeTool_Empty(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	require.NoError(t, err)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_Skip(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		return nil, nil
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	require.NoError(t, err)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_CustomResult(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedResult := map[string]string{"result": "custom"}

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		return expectedResult, nil
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "custom", result["result"])

}

func TestRunBeforeTool_Error(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedErr := "callback error"

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		return nil, tool.NewError(expectedErr)
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_Multiple(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callCount := 0

	callback1 := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		callCount++
		return nil, nil
	}

	callback2 := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		callCount++
		return map[string]string{"result": "from-second"}, nil
	}

	callbacks.RegisterBeforeTool(callback1)
	callbacks.RegisterBeforeTool(callback2)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "from-second", result["result"])

}

func TestRunAfterTool_Empty(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	result := map[string]string{"original": "result"}

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, result, nil)

	require.NoError(t, err)
	require.Nil(t, customResult)
}

func TestRunAfterTool_Override(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedResult := map[string]string{"result": "overridden"}

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		return expectedResult, nil
	}

	callbacks.RegisterAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "overridden", result["result"])
}

func TestRunAfterTool_WithError(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		if runErr != nil {
			return map[string]string{"error": "handled"}, nil
		}
		return nil, nil
	}

	callbacks.RegisterAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}
	runErr := tool.NewError("tool execution error")

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, runErr)

	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "handled", result["error"])
}

func TestRunAfterTool_Error(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedErr := "callback error"

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		return nil, tool.NewError(expectedErr)
	}

	callbacks.RegisterAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	require.Error(t, err)
	require.Equal(t, expectedErr, err.Error())
	require.Nil(t, customResult)
}

func TestRunAfterTool_Multiple(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callCount := 0

	callback1 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		callCount++
		return nil, nil
	}

	callback2 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		callCount++
		return map[string]string{"result": "from-second"}, nil
	}

	callbacks.RegisterAfterTool(callback1)
	callbacks.RegisterAfterTool(callback2)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "from-second", result["result"])
}

// Mock tool for testing
type MockTool struct {
	name        string
	description string
}

func (m *MockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

func TestToolCallbacks_Integration(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	// Add before callback that logs and modifies args.
	callbacks.RegisterBeforeTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		if toolName == "skip-tool" {
			return map[string]string{"skipped": "true"}, nil
		}

		// Modify args for certain tools.
		if toolName == "modify-args" {
			var args map[string]any
			if err := json.Unmarshal(jsonArgs, &args); err != nil {
				return nil, err
			}
			args["modified"] = true
			return args, nil
		}

		return nil, nil
	})

	// Add after callback that logs and modifies results.
	callbacks.RegisterAfterTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
		result any,
		runErr error,
	) (any, error) {
		if runErr != nil {
			return map[string]string{"error": "handled"}, nil
		}

		if toolName == "override-result" {
			return map[string]string{"overridden": "true"}, nil
		}

		return nil, nil
	})

	// Test skip functionality.
	declaration := &tool.Declaration{Name: "skip-tool", Description: "A tool to skip"}
	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "skip-tool", declaration, args)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	// Test error handling.
	declaration = &tool.Declaration{Name: "error-tool", Description: "A tool with error"}
	args = []byte(`{"test": "value"}`)
	runErr := tool.NewError("execution error")

	customResult, err = callbacks.RunAfterTool(context.Background(), "error-tool", declaration, args, nil, runErr)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	// Test override functionality.
	declaration = &tool.Declaration{Name: "override-result", Description: "A tool to override"}
	args = []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, err = callbacks.RunAfterTool(context.Background(), "override-result", declaration, args, originalResult, nil)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "true", result["overridden"])
}

func TestToolCallbacks_EdgeCases(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	// Test with nil declaration.
	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", nil, args)
	require.NoError(t, err)
	require.Nil(t, customResult)

	// Test with nil args.
	declaration := &tool.Declaration{Name: "test-tool", Description: "A test tool"}

	customResult, err = callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, nil)
	require.NoError(t, err)
	require.Nil(t, customResult)

	// Test with empty tool name.
	customResult, err = callbacks.RunBeforeTool(context.Background(), "", declaration, args)
	require.NoError(t, err)
	require.Nil(t, customResult)
}
