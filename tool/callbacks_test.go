//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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

// ToolError represents an error that occurred during tool execution.
type ToolError struct {
	Message string
}

// Error returns the error message.
func (e *ToolError) Error() string {
	return e.Message
}

// NewError creates a new ToolError.
func NewError(message string) error {
	return &ToolError{Message: message}
}

func TestNewToolCallbacks(t *testing.T) {
	callbacks := tool.NewCallbacks()
	require.NotNil(t, callbacks)
	require.Empty(t, callbacks.BeforeTool)
	require.Empty(t, callbacks.AfterTool)
}

func TestRegisterBeforeTool(t *testing.T) {
	callbacks := tool.NewCallbacks()

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		return nil, nil
	}

	callbacks.RegisterBeforeTool(callback)

	require.Equal(t, 1, len(callbacks.BeforeTool))
}

func TestRunBeforeTool_ModifyArgsViaPointer(t *testing.T) {
	callbacks := tool.NewCallbacks()

	// Register a callback that modifies the args by reassigning through pointer.
	callbacks.RegisterBeforeTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		if jsonArgs != nil {
			// Change the content to verify propagation to caller.
			*jsonArgs = []byte(`{"x":2}`)
		}
		return nil, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"x":1}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.NoError(t, err)
	require.Nil(t, customResult)
	require.JSONEq(t, `{"x":2}`, string(args))
}

func TestRegisterAfterTool(t *testing.T) {
	callbacks := tool.NewCallbacks()

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
	callbacks := tool.NewCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.NoError(t, err)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_Skip(t *testing.T) {
	callbacks := tool.NewCallbacks()

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		return nil, nil
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.NoError(t, err)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_CustomResult(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedResult := map[string]string{"result": "custom"}

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		return expectedResult, nil
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "custom", result["result"])

}

func TestRunBeforeTool_Error(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "callback error"

	callback := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		return nil, NewError(expectedErr)
	}

	callbacks.RegisterBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.Nil(t, customResult)

}

func TestRunBeforeTool_Multiple(t *testing.T) {
	callbacks := tool.NewCallbacks()

	callCount := 0

	callback1 := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		callCount++
		return nil, nil
	}

	callback2 := func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
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

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, &args)

	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "from-second", result["result"])

}

func TestRunAfterTool_Empty(t *testing.T) {
	callbacks := tool.NewCallbacks()

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
	callbacks := tool.NewCallbacks()

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
	callbacks := tool.NewCallbacks()

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
	runErr := NewError("tool execution error")

	customResult, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, runErr)

	require.NoError(t, err)
	require.NotNil(t, customResult)

	result, ok := customResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "handled", result["error"])
}

func TestRunAfterTool_Error(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "callback error"

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		return nil, NewError(expectedErr)
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
	callbacks := tool.NewCallbacks()

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
	callbacks := tool.NewCallbacks()

	// Add before callback that logs and modifies args.
	callbacks.RegisterBeforeTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		if toolName == "skip-tool" {
			return map[string]string{"skipped": "true"}, nil
		}

		// Modify args for certain tools.
		if toolName == "modify-args" {
			var args map[string]any
			if jsonArgs == nil {
				return nil, nil
			}
			if err := json.Unmarshal(*jsonArgs, &args); err != nil {
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

	customResult, err := callbacks.RunBeforeTool(context.Background(), "skip-tool", declaration, &args)
	require.NoError(t, err)
	require.NotNil(t, customResult)

	// Test error handling.
	declaration = &tool.Declaration{Name: "error-tool", Description: "A tool with error"}
	args = []byte(`{"test": "value"}`)
	runErr := NewError("execution error")

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
	callbacks := tool.NewCallbacks()

	// Test with nil declaration.
	args := []byte(`{"test": "value"}`)

	customResult, err := callbacks.RunBeforeTool(context.Background(), "test-tool", nil, &args)
	require.NoError(t, err)
	require.Nil(t, customResult)

	// Test with nil args.
	declaration := &tool.Declaration{Name: "test-tool", Description: "A test tool"}

	customResult, err = callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, nil)
	require.NoError(t, err)
	require.Nil(t, customResult)

	// Test with empty tool name.
	customResult, err = callbacks.RunBeforeTool(context.Background(), "", declaration, &args)
	require.NoError(t, err)
	require.Nil(t, customResult)
}

func TestCallbacksChainRegistration(t *testing.T) {
	// Test chain registration.
	callbacks := tool.NewCallbacks().
		RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs *[]byte) (any, error) {
			return nil, nil
		}).
		RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
			return nil, nil
		})

	// Verify that both callbacks were registered.
	if len(callbacks.BeforeTool) != 1 {
		t.Errorf("Expected 1 before tool callback, got %d", len(callbacks.BeforeTool))
	}
	if len(callbacks.AfterTool) != 1 {
		t.Errorf("Expected 1 after tool callback, got %d", len(callbacks.AfterTool))
	}
}
