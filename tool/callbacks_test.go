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
	"errors"
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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.NoError(t, err)
	require.Nil(t, result)
	require.JSONEq(t, `{"x":2}`, string(beforeArgs.Arguments))
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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.NoError(t, err)
	require.Nil(t, result)

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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.NoError(t, err)
	require.Nil(t, result)

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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.NoError(t, err)
	require.NotNil(t, result)

	customResult, ok := result.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "custom", customResult["result"])

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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.Nil(t, result)

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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, result)

	customResult, ok := result.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "from-second", customResult["result"])

}

func TestRunAfterTool_Empty(t *testing.T) {
	callbacks := tool.NewCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, originalResult, result.CustomResult)
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

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.NoError(t, err)
	require.NotNil(t, result)

	customResult, ok := result.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "overridden", customResult["result"])
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

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       runErr,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.NoError(t, err)
	require.NotNil(t, result)

	customResult, ok := result.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "handled", customResult["error"])
}

// =========================
// Structured Callback Tests
// =========================

func TestToolCallbacks_Structured_Before_Custom(t *testing.T) {
	callbacks := tool.NewCallbacks()
	customResult := map[string]string{"custom": "result"}
	ctxWithValue := context.WithValue(context.Background(), "user_id", "123")

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		require.Equal(t, "test-tool", args.ToolName)
		require.Equal(t, "A test tool", args.Declaration.Description)
		return &tool.BeforeToolResult{
			Context:      ctxWithValue,
			CustomResult: customResult,
		}, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResult, result.CustomResult)
}

func TestToolCallbacks_Structured_After_Custom(t *testing.T) {
	callbacks := tool.NewCallbacks()
	customResult := map[string]string{"custom": "after"}
	ctxWithValue := context.WithValue(context.Background(), "trace_id", "456")

	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		require.Equal(t, "test-tool", args.ToolName)
		require.Equal(t, "A test tool", args.Declaration.Description)
		return &tool.AfterToolResult{
			Context:      ctxWithValue,
			CustomResult: customResult,
		}, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResult, result.CustomResult)
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

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.Error(t, err)
	require.Equal(t, expectedErr, err.Error())
	require.Nil(t, result)
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

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, result)

	customResult, ok := result.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "from-second", customResult["result"])
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

	beforeArgs := &tool.BeforeToolArgs{ToolName: "skip-tool", Declaration: declaration, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Test error handling.
	declaration = &tool.Declaration{Name: "error-tool", Description: "A tool with error"}
	args = []byte(`{"test": "value"}`)
	runErr := NewError("execution error")

	errorAfterArgs := &tool.AfterToolArgs{
		ToolName:    "error-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      nil,
		Error:       runErr,
	}
	afterResult, err := callbacks.RunAfterTool(context.Background(), errorAfterArgs)
	require.NoError(t, err)
	require.NotNil(t, afterResult)

	// Test override functionality.
	declaration = &tool.Declaration{Name: "override-result", Description: "A tool to override"}
	args = []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	overrideAfterArgs := &tool.AfterToolArgs{
		ToolName:    "override-result",
		Declaration: declaration,
		Arguments:   args,
		Result:      originalResult,
		Error:       nil,
	}
	overrideResult, err := callbacks.RunAfterTool(context.Background(), overrideAfterArgs)
	require.NoError(t, err)
	require.NotNil(t, overrideResult)

	resultMap, ok := overrideResult.CustomResult.(map[string]string)
	require.True(t, ok)
	require.Equal(t, "true", resultMap["overridden"])
}

func TestToolCallbacks_EdgeCases(t *testing.T) {
	callbacks := tool.NewCallbacks()

	// Test with nil declaration.
	args := []byte(`{"test": "value"}`)

	beforeArgs := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: nil, Arguments: args}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.Nil(t, result)

	// Test with nil args.
	declaration := &tool.Declaration{Name: "test-tool", Description: "A test tool"}

	beforeArgsNil := &tool.BeforeToolArgs{ToolName: "test-tool", Declaration: declaration, Arguments: nil}
	nilResult, err := callbacks.RunBeforeTool(context.Background(), beforeArgsNil)
	require.NoError(t, err)
	require.Nil(t, nilResult)

	// Test with empty tool name.
	emptyNameArgs := &tool.BeforeToolArgs{ToolName: "", Declaration: declaration, Arguments: args}
	emptyResult, err := callbacks.RunBeforeTool(context.Background(), emptyNameArgs)
	require.NoError(t, err)
	require.Nil(t, emptyResult)
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

// TestToolCallbacks_ContextPropagation tests that context values set in before
// callbacks can be accessed in after callbacks.
func TestToolCallbacks_ContextPropagation(t *testing.T) {
	callbacks := tool.NewCallbacks()

	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"

	// Register before callback that sets a context value.
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		// Set a value in context.
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &tool.BeforeToolResult{
			Context: ctxWithValue,
		}, nil
	})

	// Register after callback that reads the context value.
	var capturedValue any
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		// Read the value from context.
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Execute before callback.
	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}
	args := []byte(`{"key": "value"}`)
	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
	}
	beforeResult, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.NotNil(t, beforeResult)
	require.NotNil(t, beforeResult.Context)

	// Use the context from before callback to run after callback.
	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		Result:      "test-result",
		Error:       nil,
	}
	_, err = callbacks.RunAfterTool(beforeResult.Context, afterArgs)
	require.NoError(t, err)

	// Verify that the value was captured in after callback.
	require.Equal(t, testValue, capturedValue)
}

// TestToolCallbacks_After_NoCallbacks_WithResult tests that when no callbacks
// are registered and args.Result is not nil, RunAfterTool returns the original result.
func TestToolCallbacks_After_NoCallbacks_WithResult(t *testing.T) {
	callbacks := tool.NewCallbacks()
	originalResult := map[string]string{"key": "value"}
	args := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      originalResult,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, originalResult, result.CustomResult)
}

// TestToolCallbacks_After_NoCallbacks_WithoutResult tests that when no callbacks
// are registered and args.Result is nil, RunAfterTool returns an empty result.
func TestToolCallbacks_After_NoCallbacks_WithoutResult(t *testing.T) {
	callbacks := tool.NewCallbacks()
	args := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      nil,
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
}

// TestToolCallbacks_After_NilResult tests that when a callback returns
// nil result, RunAfterTool continues to the next callback.
func TestToolCallbacks_After_NilResult(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		// Return nil result.
		return nil, nil
	})
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		// Second callback returns a custom result.
		return &tool.AfterToolResult{
			CustomResult: map[string]string{"second": "result"},
		}, nil
	})
	args := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      map[string]string{"original": "result"},
		Error:       nil,
	}
	result, err := callbacks.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, map[string]string{"second": "result"}, result.CustomResult)
}

// =========================
// ContinueOnError Tests
// =========================

func TestToolCallbacks_DefaultBehavior_StopOnError(t *testing.T) {
	callbacks := tool.NewCallbacks()
	executed := []int{}

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 2)
		return nil, nil
	})

	args := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	_, err := callbacks.RunBeforeTool(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1}, executed)
}

func TestToolCallbacks_ContinueOnError_ContinuesExecution(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnError(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 2)
		return nil, errors.New("error 2")
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	_, err := callbacks.RunBeforeTool(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1, 2, 3}, executed)
}

func TestToolCallbacks_ContinueOnResponse_ContinuesExecution(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 1)
		return &tool.BeforeToolResult{
			CustomResult: map[string]string{"result": "1"},
		}, nil
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 2)
		return &tool.BeforeToolResult{
			CustomResult: map[string]string{"result": "2"},
		}, nil
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	result, err := callbacks.RunBeforeTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, map[string]string{"result": "2"}, result.CustomResult)
	require.Equal(t, []int{1, 2, 3}, executed)
}

func TestToolCallbacks_BothOptions_ContinuesExecution(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnError(true),
		tool.WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 2)
		return &tool.BeforeToolResult{
			CustomResult: map[string]string{"result": "1"},
		}, nil
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		executed = append(executed, 3)
		return &tool.BeforeToolResult{
			CustomResult: map[string]string{"result": "2"},
		}, nil
	})

	args := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	result, err := callbacks.RunBeforeTool(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result)
	require.Equal(t, map[string]string{"result": "2"}, result.CustomResult)
	require.Equal(t, []int{1, 2, 3}, executed)
}
