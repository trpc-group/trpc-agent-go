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
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	beforeToolCallbackPanic         = "before tool callback panic"
	afterToolCallbackPanic          = "after tool callback panic"
	toolResultMessagesCallbackPanic = "tool result messages callback panic"
)

func requireFinalizerBinding(
	t *testing.T,
	source *tool.Declaration,
	target *tool.Declaration,
	toolCallID string,
) {
	t.Helper()
	complete, err := tool.BindAfterToolResultFinalizer(
		source,
		target,
		toolCallID,
	)
	require.NoError(t, err)
	complete()
}

// ToolError represents an error that occurred during tool execution.
type ToolError struct {
	Message string
}

type callbackCompatibleResult struct {
	callbackResult any
	meta           map[string]any
}

func (c *callbackCompatibleResult) GetCallbackResult() any {
	return c.callbackResult
}

func (c *callbackCompatibleResult) GetMeta() map[string]any {
	return c.meta
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

func TestToolCallbacks_Clone_PreservesOptionsAndDoesNotShareSlices(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnError(true),
		tool.WithContinueOnResponse(true),
	)
	expectedErr := errors.New("first")
	var trail []string
	callbacks.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		trail = append(trail, "orig-error")
		return nil, expectedErr
	})
	callbacks.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		trail = append(trail, "orig-response")
		return &tool.BeforeToolResult{CustomResult: "orig"}, nil
	})
	callbacks.RegisterToolResultMessages(func(
		_ context.Context,
		_ *tool.ToolResultMessagesInput,
	) (any, error) {
		return "converter", nil
	})

	cloned := callbacks.Clone()
	cloned.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		trail = append(trail, "clone-response")
		return &tool.BeforeToolResult{CustomResult: "clone"}, nil
	})

	result, err := cloned.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{ToolName: "x"},
	)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, "clone", result.CustomResult)
	require.Equal(t,
		[]string{"orig-error", "orig-response", "clone-response"},
		trail,
		"clone must preserve ContinueOnError/ContinueOnResponse options",
	)
	got, err := cloned.RunToolResultMessages(
		context.Background(),
		&tool.ToolResultMessagesInput{ToolName: "x"},
	)
	require.NoError(t, err)
	require.Equal(t, "converter", got,
		"clone must preserve ToolResultMessages")

	trail = nil
	result, err = callbacks.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{ToolName: "x"},
	)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, "orig", result.CustomResult)
	require.Equal(t, []string{"orig-error", "orig-response"}, trail,
		"adding callbacks to the clone must not mutate the original")
}

func TestRunToolResultMessages_PanicRecovery(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterToolResultMessages(func(
		_ context.Context,
		_ *tool.ToolResultMessagesInput,
	) (any, error) {
		panic("boom")
	})

	in := &tool.ToolResultMessagesInput{
		ToolCallID: "call-1",
		ToolName:   "test-tool",
	}
	var err error
	require.NotPanics(t, func() {
		_, err = callbacks.RunToolResultMessages(context.Background(), in)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), toolResultMessagesCallbackPanic)
}

func TestRunToolResultMessages_UsesCallbackFacingResult(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterToolResultMessages(func(
		_ context.Context,
		in *tool.ToolResultMessagesInput,
	) (any, error) {
		return in.Result, nil
	})
	carrier := &callbackCompatibleResult{
		callbackResult: map[string]any{"safe": true},
	}
	in := &tool.ToolResultMessagesInput{Result: carrier}
	result, err := callbacks.RunToolResultMessages(
		context.Background(),
		in,
	)
	require.NoError(t, err)
	require.Equal(t, carrier.callbackResult, result)
	require.Same(t, carrier, in.Result)
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

func TestRegisterBeforeTool_UnsupportedTypePanics(t *testing.T) {
	callbacks := tool.NewCallbacks()
	require.Panics(t, func() {
		callbacks.RegisterBeforeTool(123)
	})
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

func TestRegisterAfterTool_UnsupportedTypePanics(t *testing.T) {
	callbacks := tool.NewCallbacks()
	require.Panics(t, func() {
		callbacks.RegisterAfterTool(123)
	})
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

func TestRunBeforeTool_PanicRecovery(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(
		_ context.Context,
		_ string,
		_ *tool.Declaration,
		_ *[]byte,
	) (any, error) {
		panic("boom")
	})

	args := &tool.BeforeToolArgs{
		ToolCallID:  "call-1",
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	var err error
	require.NotPanics(t, func() {
		_, err = callbacks.RunBeforeTool(context.Background(), args)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), beforeToolCallbackPanic)
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

func TestRunBeforeTool_CustomResultWithError(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "interrupt"
	expectedResult := map[string]string{"result": "custom"}

	callbacks.RegisterBeforeTool(func(
		_ context.Context,
		_ string,
		_ *tool.Declaration,
		_ *[]byte,
	) (any, error) {
		return expectedResult, NewError(expectedErr)
	})

	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.NotNil(t, result)
	require.Equal(t, expectedResult, result.CustomResult)
}

func TestToolCallbacks_Structured_Before_CustomResultWithError(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "interrupt"
	expectedResult := map[string]string{"result": "custom"}

	callbacks.RegisterBeforeTool(func(
		_ context.Context,
		_ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		return &tool.BeforeToolResult{CustomResult: expectedResult}, NewError(expectedErr)
	})

	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
	}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.NotNil(t, result)
	require.Equal(t, expectedResult, result.CustomResult)
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

func TestRunAfterTool_PanicRecovery(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ string,
		_ *tool.Declaration,
		_ []byte,
		_ any,
		_ error,
	) (any, error) {
		panic("boom")
	})

	args := &tool.AfterToolArgs{
		ToolCallID:  "call-1",
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      map[string]any{"ok": true},
	}
	var err error
	require.NotPanics(t, func() {
		_, err = callbacks.RunAfterTool(context.Background(), args)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), afterToolCallbackPanic)
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

func TestRunAfterTool_FinalizerRunsAfterShortCircuit(t *testing.T) {
	callbacks := tool.NewCallbacks()
	finalizerCalls := 0
	declaration := &tool.Declaration{Name: "finalizer"}
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		declaration,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls++
			out := *result
			out.CustomResult = "safe"
			return &out, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{
			CustomResult: "unsafe",
		}, nil
	})
	result, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "call-finalizer",
			Declaration: declaration,
			Result:      "original",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe", result.CustomResult)
	require.Equal(t, 1, finalizerCalls)
}

func TestRunAfterTool_FinalizerPanicSuppressesCustomResult(t *testing.T) {
	callbacks := tool.NewCallbacks()
	declaration := &tool.Declaration{Name: "finalizer-panic"}
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		declaration,
		func(
			context.Context,
			*tool.AfterToolArgs,
			*tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			panic("boom")
		},
	)
	require.NoError(t, err)
	defer cleanup()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{
			CustomResult: "unsafe",
		}, nil
	})
	result, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "call-finalizer-panic",
			Declaration: declaration,
			Result:      "original",
		},
	)
	require.ErrorContains(t, err, "after tool result finalizer panic")
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
}

func TestRunAfterTool_FinalizerScopedByDeclaration(t *testing.T) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: "unsafe"}, nil
	})
	declarationA := &tool.Declaration{
		Name:        "same-name",
		InputSchema: &tool.Schema{Type: "object"},
	}
	declarationB := &tool.Declaration{
		Name:        "same-name",
		InputSchema: &tool.Schema{Type: "object"},
	}
	register := func(
		declaration *tool.Declaration,
		final string,
	) func() {
		cleanup, err := tool.RegisterAfterToolResultFinalizer(
			declaration,
			func(
				_ context.Context,
				_ *tool.AfterToolArgs,
				result *tool.AfterToolResult,
			) (*tool.AfterToolResult, error) {
				out := *result
				out.CustomResult = final
				return &out, nil
			},
		)
		require.NoError(t, err)
		return cleanup
	}
	cleanupA := register(declarationA, "safe-a")
	defer cleanupA()
	cleanupB := register(declarationB, "safe-b")
	defer cleanupB()

	result, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			Declaration: declarationA,
			ToolCallID:  "same-id",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-a", result.CustomResult)
	result, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			Declaration: declarationB,
			ToolCallID:  "same-id",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-b", result.CustomResult)

	schemaCopy := *declarationA.InputSchema
	wrappedDeclaration := &tool.Declaration{
		Name:        "prefix_same-name",
		InputSchema: &schemaCopy,
	}
	requireFinalizerBinding(
		t,
		declarationA,
		wrappedDeclaration,
		"wrapped",
	)
	result, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			Declaration: wrappedDeclaration,
			ToolCallID:  "wrapped",
			ToolName:    wrappedDeclaration.Name,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-a", result.CustomResult)

	overlayDeclaration := &tool.Declaration{
		Name:        "overlay",
		InputSchema: &tool.Schema{Type: "object"},
	}
	requireFinalizerBinding(
		t,
		declarationA,
		overlayDeclaration,
		"overlay",
	)
	result, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			Declaration: overlayDeclaration,
			ToolCallID:  "overlay",
			ToolName:    overlayDeclaration.Name,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-a", result.CustomResult)

	_, err = tool.RegisterAfterToolResultFinalizer(
		declarationA,
		func(
			context.Context,
			*tool.AfterToolArgs,
			*tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			return nil, nil
		},
	)
	require.ErrorContains(t, err, "already registered")
}

func TestRunAfterTool_BoundFinalizerCarriesAcrossCallbackChains(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	source := &tool.Declaration{Name: "source"}
	finalizerCalls := 0
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls++
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()

	overlay := &tool.Declaration{Name: "overlay"}
	requireFinalizerBinding(
		t,
		source,
		overlay,
		"shared-call",
	)
	first, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "shared-call",
			ToolName:    overlay.Name,
			Declaration: overlay,
			Result:      "first",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.Context)

	pending := make([]*tool.Declaration, 0, 1024)
	for i := 0; i < 1024; i++ {
		target := &tool.Declaration{Name: "pending"}
		pending = append(pending, target)
		requireFinalizerBinding(
			t,
			source,
			target,
			fmt.Sprintf("pending-%d", i),
		)
	}
	_, err = tool.BindAfterToolResultFinalizer(
		source,
		&tool.Declaration{Name: "overflow"},
		"overflow",
	)
	require.ErrorContains(t, err, "call registry is full")

	_, err = callbacks.RunAfterTool(
		first.Context,
		&tool.AfterToolArgs{
			ToolCallID:  "shared-call",
			ToolName:    overlay.Name,
			Declaration: overlay,
			Result:      "second",
		},
	)
	require.NoError(t, err)
	require.Equal(t, 2, finalizerCalls)
	require.Len(t, pending, 1024)
}

func TestRunAfterTool_BoundFinalizerSupportsConcurrentCalls(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	source := &tool.Declaration{Name: "source"}
	var finalizerCalls atomic.Int64
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls.Add(1)
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()

	overlay := &tool.Declaration{Name: "shared-overlay"}
	requireFinalizerBinding(
		t,
		source,
		overlay,
		"call-a",
	)
	requireFinalizerBinding(
		t,
		source,
		overlay,
		"call-b",
	)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, callID := range []string{"call-a", "call-b"} {
		callID := callID
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, runErr := callbacks.RunAfterTool(
				context.Background(),
				&tool.AfterToolArgs{
					ToolCallID:  callID,
					ToolName:    overlay.Name,
					Declaration: overlay,
					Result:      "ok",
				},
			)
			errs <- runErr
		}()
	}
	wg.Wait()
	close(errs)
	for runErr := range errs {
		require.NoError(t, runErr)
	}
	require.Equal(t, int64(2), finalizerCalls.Load())

	_, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "unleased",
			ToolName:    overlay.Name,
			Declaration: overlay,
			Result:      "ok",
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), finalizerCalls.Load())
}

func TestRunAfterTool_BoundFinalizerSupportsDuplicateActiveKey(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	source := &tool.Declaration{Name: "source"}
	target := &tool.Declaration{Name: "target"}
	var finalizerCalls atomic.Int64
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls.Add(1)
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()
	requireFinalizerBinding(t, source, target, "auto_call_0")
	requireFinalizerBinding(t, source, target, "auto_call_0")

	for i := 0; i < 2; i++ {
		_, err := callbacks.RunAfterTool(
			context.Background(),
			&tool.AfterToolArgs{
				ToolCallID:  "auto_call_0",
				ToolName:    target.Name,
				Declaration: target,
				Result:      "ok",
			},
		)
		require.NoError(t, err)
	}
	require.Equal(t, int64(2), finalizerCalls.Load())
}

func TestRunAfterTool_DoesNotConsumeActiveDuplicateLease(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	source := &tool.Declaration{Name: "source"}
	target := &tool.Declaration{Name: "target"}
	var finalizerCalls atomic.Int64
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls.Add(1)
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()
	completeFirst, err := tool.BindAfterToolResultFinalizer(
		source,
		target,
		"auto_call_0",
	)
	require.NoError(t, err)
	completeSecond, err := tool.BindAfterToolResultFinalizer(
		source,
		target,
		"auto_call_0",
	)
	require.NoError(t, err)

	completeFirst()
	_, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "auto_call_0",
			ToolName:    target.Name,
			Declaration: target,
			Result:      "first",
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), finalizerCalls.Load())

	completeSecond()
	_, err = callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "auto_call_0",
			ToolName:    target.Name,
			Declaration: target,
			Result:      "second",
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), finalizerCalls.Load())
}

func TestRunAfterTool_PendingBindingFallsBackToRegisteredDeclaration(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	source := &tool.Declaration{Name: "source"}
	target := &tool.Declaration{Name: "target"}
	register := func(
		declaration *tool.Declaration,
		value string,
	) func() {
		cleanup, err := tool.RegisterAfterToolResultFinalizer(
			declaration,
			func(
				_ context.Context,
				_ *tool.AfterToolArgs,
				result *tool.AfterToolResult,
			) (*tool.AfterToolResult, error) {
				out := *result
				out.CustomResult = value
				return &out, nil
			},
		)
		require.NoError(t, err)
		return cleanup
	}
	cleanupSource := register(source, "source-finalizer")
	defer cleanupSource()
	cleanupTarget := register(target, "target-finalizer")
	defer cleanupTarget()
	_, err := tool.BindAfterToolResultFinalizer(
		source,
		target,
		"call-1",
	)
	require.NoError(t, err)

	result, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "call-1",
			ToolName:    target.Name,
			Declaration: target,
			Result:      "original",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "target-finalizer", result.CustomResult)
}

func TestRunAfterTool_BoundFinalizersIsolateDuplicateCallIDs(
	t *testing.T,
) {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		context.Context,
		*tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: "unsafe"}, nil
	})
	sourceA := &tool.Declaration{Name: "same-name"}
	sourceB := &tool.Declaration{Name: "same-name"}
	targetA := &tool.Declaration{Name: "same-name"}
	targetB := &tool.Declaration{Name: "same-name"}
	register := func(
		source *tool.Declaration,
		value string,
	) func() {
		cleanup, err := tool.RegisterAfterToolResultFinalizer(
			source,
			func(
				_ context.Context,
				_ *tool.AfterToolArgs,
				result *tool.AfterToolResult,
			) (*tool.AfterToolResult, error) {
				out := *result
				out.CustomResult = value
				return &out, nil
			},
		)
		require.NoError(t, err)
		return cleanup
	}
	cleanupA := register(sourceA, "safe-a")
	defer cleanupA()
	cleanupB := register(sourceB, "safe-b")
	defer cleanupB()
	requireFinalizerBinding(
		t,
		sourceA,
		targetA,
		"auto_call_0",
	)
	requireFinalizerBinding(
		t,
		sourceB,
		targetB,
		"auto_call_0",
	)

	resultA, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "auto_call_0",
			ToolName:    "same-name",
			Declaration: targetA,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-a", resultA.CustomResult)
	resultB, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "auto_call_0",
			ToolName:    "same-name",
			Declaration: targetB,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "safe-b", resultB.CustomResult)
}

func TestRegisterAfterToolResultFinalizer_ReleasesBoundCalls(
	t *testing.T,
) {
	source := &tool.Declaration{Name: "source"}
	finalizerCalls := 0
	cleanup, err := tool.RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *tool.AfterToolArgs,
			result *tool.AfterToolResult,
		) (*tool.AfterToolResult, error) {
			finalizerCalls++
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()
	callbacks := tool.NewCallbacks()
	overlay := &tool.Declaration{Name: "target"}
	for i := 0; i < 2048; i++ {
		callID := fmt.Sprintf("call-%d", i)
		requireFinalizerBinding(
			t,
			source,
			overlay,
			callID,
		)
		_, err := callbacks.RunAfterTool(
			context.Background(),
			&tool.AfterToolArgs{
				ToolCallID:  callID,
				ToolName:    overlay.Name,
				Declaration: overlay,
				Result:      "ok",
			},
		)
		require.NoError(t, err)
	}
	require.Equal(t, 2048, finalizerCalls)

	cleanup()
	_, err = tool.BindAfterToolResultFinalizer(
		source,
		overlay,
		"after-cleanup",
	)
	require.ErrorContains(t, err, "no after-tool result finalizer")
	replacementCleanup, err :=
		tool.RegisterAfterToolResultFinalizer(
			overlay,
			func(
				_ context.Context,
				_ *tool.AfterToolArgs,
				result *tool.AfterToolResult,
			) (*tool.AfterToolResult, error) {
				return result, nil
			},
		)
	require.NoError(t, err)
	replacementCleanup()
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

func TestRunAfterTool_CustomResultWithError(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "interrupt"
	expectedResult := map[string]string{"result": "custom"}

	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ string,
		_ *tool.Declaration,
		_ []byte,
		_ any,
		_ error,
	) (any, error) {
		return expectedResult, NewError(expectedErr)
	})

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      map[string]string{"original": "result"},
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.NotNil(t, result)
	require.Equal(t, expectedResult, result.CustomResult)
}

func TestToolCallbacks_Structured_After_CustomResultWithError(t *testing.T) {
	callbacks := tool.NewCallbacks()

	expectedErr := "interrupt"
	expectedResult := map[string]string{"result": "custom"}

	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: expectedResult}, NewError(expectedErr)
	})

	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      map[string]string{"original": "result"},
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)

	require.Error(t, err)
	require.EqualError(t, err, expectedErr)
	require.NotNil(t, result)
	require.Equal(t, expectedResult, result.CustomResult)
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

func TestRunAfterTool_NormalizesResultOnlyForCallbackInvocation(t *testing.T) {
	callbacks := tool.NewCallbacks()

	rawResult := &callbackCompatibleResult{
		callbackResult: map[string]string{"callback": "result"},
		meta:           map[string]any{"source": "mcp"},
	}
	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      rawResult,
		Meta:        rawResult.meta,
	}

	var seenResult any
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		seenResult = args.Result
		require.Equal(t, rawResult.meta, args.Meta)
		return &tool.AfterToolResult{}, nil
	})

	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)
	require.NoError(t, err)
	require.Equal(t, rawResult.callbackResult, seenResult)
	require.Same(t, rawResult, afterArgs.Result)
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
}

func TestRunAfterTool_PreservesSkipSummarizationAcrossCallbacks(t *testing.T) {
	callbacks := tool.NewCallbacks(tool.WithContinueOnResponse(true))
	marker := map[string]string{"result": "callback"}
	secondCalled := false

	callbacks.RegisterAfterTool(func(
		ctx context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: marker}, nil
	})
	callbacks.RegisterAfterTool(func(
		ctx context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		secondCalled = true
		return &tool.AfterToolResult{SkipSummarization: true}, nil
	})

	result, err := callbacks.RunAfterTool(context.Background(),
		&tool.AfterToolArgs{
			ToolName:    "test-tool",
			Declaration: &tool.Declaration{Name: "test-tool"},
			Arguments:   []byte("{}"),
			Result:      map[string]any{"ok": true},
		},
	)
	require.NoError(t, err)
	require.True(t, secondCalled)
	require.NotNil(t, result)
	require.True(t, result.SkipSummarization)
	require.Equal(t, marker, result.CustomResult)
}

func TestRunAfterTool_NoCallbacksPreservesOriginalResultShape(t *testing.T) {
	callbacks := tool.NewCallbacks()

	rawResult := &callbackCompatibleResult{
		callbackResult: map[string]string{"callback": "result"},
		meta:           map[string]any{"source": "mcp"},
	}
	afterArgs := &tool.AfterToolArgs{
		ToolName:    "test-tool",
		Declaration: &tool.Declaration{Name: "test-tool"},
		Arguments:   []byte(`{}`),
		Result:      rawResult,
		Meta:        rawResult.meta,
	}

	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Same(t, rawResult, result.CustomResult)
	require.Same(t, rawResult, afterArgs.Result)
}

func TestRunAfterTool_NilArgsWithStructuredCallback(t *testing.T) {
	callbacks := tool.NewCallbacks()

	called := false
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		called = true
		require.Nil(t, args)
		return &tool.AfterToolResult{}, nil
	})

	result, err := callbacks.RunAfterTool(context.Background(), nil)
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
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

func TestToolCallbacks_ContinueOnError_StopOnResponseReturnsFirstError(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnError(true),
	)

	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		return &tool.BeforeToolResult{
			CustomResult: map[string]string{"result": "ok"},
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
	require.Equal(t, map[string]string{"result": "ok"}, result.CustomResult)
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

func TestToolCallbacks_After_ContinueOnError_StopOnResponseReturnsFirstError(t *testing.T) {
	callbacks := tool.NewCallbacks(
		tool.WithContinueOnError(true),
	)

	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		return nil, errors.New("error 1")
	})
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{
			CustomResult: map[string]string{"result": "ok"},
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
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result)
	require.Equal(t, map[string]string{"result": "ok"}, result.CustomResult)
}

func TestToolCallbacks_Before_ToolCallID(t *testing.T) {
	callbacks := tool.NewCallbacks()
	expectedToolCallID := "call_abc123"

	var capturedToolCallID string
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		capturedToolCallID = args.ToolCallID
		return nil, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}
	args := []byte(`{"test": "value"}`)
	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		ToolCallID:  expectedToolCallID,
	}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.Equal(t, expectedToolCallID, capturedToolCallID)
	require.Nil(t, result)
}

func TestToolCallbacks_After_ToolCallID(t *testing.T) {
	callbacks := tool.NewCallbacks()
	expectedToolCallID := "call_xyz789"

	var capturedToolCallID string
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		capturedToolCallID = args.ToolCallID
		return nil, nil
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
		ToolCallID:  expectedToolCallID,
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)
	require.NoError(t, err)
	require.Equal(t, expectedToolCallID, capturedToolCallID)
	require.NotNil(t, result)
}

func TestToolCallbacks_Before_ToolCallID_Empty(t *testing.T) {
	callbacks := tool.NewCallbacks()
	var capturedToolCallID string
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		capturedToolCallID = args.ToolCallID
		return nil, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}
	args := []byte(`{"test": "value"}`)
	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		ToolCallID:  "",
	}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.Equal(t, "", capturedToolCallID)
	require.Nil(t, result)
}

func TestToolCallbacks_After_ToolCallID_Empty(t *testing.T) {
	callbacks := tool.NewCallbacks()
	var capturedToolCallID string
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		capturedToolCallID = args.ToolCallID
		return nil, nil
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
		ToolCallID:  "",
	}
	result, err := callbacks.RunAfterTool(context.Background(), afterArgs)
	require.NoError(t, err)
	require.Equal(t, "", capturedToolCallID)
	require.NotNil(t, result)
}

func TestToolCallbacks_Before_ToolCallID_Multiple(t *testing.T) {
	callbacks := tool.NewCallbacks()
	expectedToolCallID1 := "call_111"
	expectedToolCallID2 := "call_222"

	var capturedToolCallIDs []string
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		capturedToolCallIDs = append(capturedToolCallIDs, args.ToolCallID)
		return nil, nil
	})
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		capturedToolCallIDs = append(capturedToolCallIDs, args.ToolCallID)
		return nil, nil
	})

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}
	args := []byte(`{"test": "value"}`)
	beforeArgs := &tool.BeforeToolArgs{
		ToolName:    "test-tool",
		Declaration: declaration,
		Arguments:   args,
		ToolCallID:  expectedToolCallID1,
	}
	result, err := callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.Equal(t, []string{expectedToolCallID1, expectedToolCallID1}, capturedToolCallIDs)
	require.Nil(t, result)

	capturedToolCallIDs = []string{}
	beforeArgs.ToolCallID = expectedToolCallID2
	result, err = callbacks.RunBeforeTool(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.Equal(t, []string{expectedToolCallID2, expectedToolCallID2}, capturedToolCallIDs)
	require.Nil(t, result)
}
