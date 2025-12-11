//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"
)

// BeforeToolCallback is called before a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be returned and tool execution will be skipped.
// - error: if not nil, tool execution will be stopped with this error.
// Deprecated: Use BeforeToolCallbackStructured instead for better type safety and context passing.
type BeforeToolCallback = func(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs *[]byte,
) (any, error)

// AfterToolCallback is called after a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be used instead of the actual tool result.
// - error: if not nil, this error will be returned.
// Deprecated: Use AfterToolCallbackStructured instead for better type safety and context passing.
type AfterToolCallback = func(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
	result any,
	runErr error,
) (any, error)

// BeforeToolArgs contains all parameters for before tool callback.
type BeforeToolArgs struct {
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the tool arguments in JSON bytes (can be modified).
	Arguments []byte
}

// BeforeToolResult contains the return value for before tool callback.
type BeforeToolResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResult if not nil, will skip tool execution and return this result.
	CustomResult any
	// ModifiedArguments if not nil, will use these modified arguments.
	ModifiedArguments []byte
}

// BeforeToolCallbackStructured is called before a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be returned and tool execution will be skipped.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//   - ModifiedArguments: if not nil, will use these modified arguments for tool execution.
//
// - error: if not nil, tool execution will be stopped with this error.
type BeforeToolCallbackStructured = func(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error)

// AfterToolArgs contains all parameters for after tool callback.
type AfterToolArgs struct {
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the tool arguments in JSON bytes.
	Arguments []byte
	// Result is the tool execution result (may be nil).
	Result any
	// Error is the error occurred during tool execution (may be nil).
	Error error
}

// AfterToolResult contains the return value for after tool callback.
type AfterToolResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResult if not nil, will replace the original result.
	CustomResult any
}

// AfterToolCallbackStructured is called after a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be used instead of the actual tool result.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//
// - error: if not nil, this error will be returned.
type AfterToolCallbackStructured = func(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error)

// ToolResultMessagesInput contains all parameters for generating messages from a tool result.
type ToolResultMessagesInput struct {
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the final tool arguments in JSON bytes (after before-tool callbacks).
	Arguments []byte
	// Result is the final tool execution result (after after-tool callbacks).
	Result any
	// ToolCallID is the ID of the tool call issued by the model.
	ToolCallID string
	// DefaultToolMessage is the default tool response message that the framework
	// would send if no custom messages are provided by the callback.
	// The concrete type is framework-specific (typically model.Message).
	DefaultToolMessage any
}

// ToolResultMessagesFunc converts a tool execution result into one or more messages
// to be sent back to the model.
//
// Behavior contract:
//   - If the callback returns (nil, nil) or an empty slice, the framework will
//     fall back to DefaultToolMessage.
//   - If the callback returns non-empty messages, they will replace the default
//     tool message. Callers are expected to return a value that the framework
//     understands (typically []model.Message) and to include at least one
//     RoleTool message whose ToolID matches ToolCallID to remain
//     protocol-compatible.
//
// To avoid import cycles, the return type is any. When using llmagent with
// the built-in OpenAI/Anthropic adapters, the recommended return type is
// []model.Message (or a single model.Message), which will be type-asserted
// by the framework.
type ToolResultMessagesFunc = func(
	ctx context.Context,
	in *ToolResultMessagesInput,
) (any, error)

// Callbacks holds callbacks for tool operations.
// Internally stores the new structured callback types.
type Callbacks struct {
	// BeforeTool is a list of callbacks called before the tool is executed.
	BeforeTool []BeforeToolCallbackStructured
	// AfterTool is a list of callbacks called after the tool is executed.
	AfterTool []AfterToolCallbackStructured
	// ToolResultMessages is an optional callback that can convert a tool
	// execution result into one or more messages to be sent back to the model.
	// When set, it is invoked after the tool and AfterTool callbacks have run.
	ToolResultMessages ToolResultMessagesFunc
	// continueOnError controls whether to continue executing callbacks when an error occurs.
	// Default: false (stop on first error)
	continueOnError bool
	// continueOnResponse controls whether to continue executing callbacks when a CustomResult is returned.
	// Default: false (stop on first CustomResult)
	continueOnResponse bool
}

// CallbacksOption configures Callbacks behavior.
type CallbacksOption func(*Callbacks)

// WithContinueOnError sets whether to continue executing callbacks when an error occurs.
func WithContinueOnError(continueOnError bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnError = continueOnError
	}
}

// WithContinueOnResponse sets whether to continue executing callbacks when a CustomResult is returned.
func WithContinueOnResponse(continueOnResponse bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnResponse = continueOnResponse
	}
}

// NewCallbacks creates a new Callbacks instance for tool.
func NewCallbacks(opts ...CallbacksOption) *Callbacks {
	c := &Callbacks{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// RegisterToolResultMessages registers a ToolResultMessages callback.
// The callback will be invoked once per tool execution, after the tool has
// completed and after all AfterTool callbacks have run.
func (c *Callbacks) RegisterToolResultMessages(cb ToolResultMessagesFunc) *Callbacks {
	c.ToolResultMessages = cb
	return c
}

// RegisterBeforeTool registers a before tool callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterBeforeTool(cb any) *Callbacks {
	switch callback := cb.(type) {
	case BeforeToolCallbackStructured:
		c.BeforeTool = append(c.BeforeTool, callback)
	case BeforeToolCallback:
		wrapped := func(ctx context.Context, args *BeforeToolArgs) (*BeforeToolResult, error) {
			// Call old signature
			customResult, err := callback(ctx, args.ToolName, args.Declaration, &args.Arguments)
			if err != nil {
				return nil, err
			}
			if customResult != nil {
				return &BeforeToolResult{CustomResult: customResult}, nil
			}
			return &BeforeToolResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.BeforeTool = append(c.BeforeTool, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// RegisterAfterTool registers an after tool callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterAfterTool(cb any) *Callbacks {
	switch callback := cb.(type) {
	case AfterToolCallbackStructured:
		c.AfterTool = append(c.AfterTool, callback)
	case AfterToolCallback:
		wrapped := func(ctx context.Context, args *AfterToolArgs) (*AfterToolResult, error) {
			// Call old signature
			customResult, err := callback(ctx, args.ToolName, args.Declaration, args.Arguments, args.Result, args.Error)
			if err != nil {
				return nil, err
			}
			if customResult != nil {
				return &AfterToolResult{CustomResult: customResult}, nil
			}
			return &AfterToolResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.AfterTool = append(c.AfterTool, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// handleCallbackError processes callback error and returns whether to continue.
func (c *Callbacks) handleCallbackError(err error, firstErr *error) (shouldStop bool) {
	if err == nil {
		return false
	}
	if !c.continueOnError {
		return true
	}
	if *firstErr == nil {
		*firstErr = err
	}
	return false
}

// processBeforeToolResult processes before tool callback result and updates context/arguments.
// Returns whether to stop execution immediately.
func (c *Callbacks) processBeforeToolResult(
	result *BeforeToolResult,
	ctx *context.Context,
	args *BeforeToolArgs,
	lastResult **BeforeToolResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if result.ModifiedArguments != nil {
		args.Arguments = result.ModifiedArguments
	}
	if result.CustomResult != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeBeforeToolResult determines the final return value for before tool callbacks.
func (c *Callbacks) finalizeBeforeToolResult(
	lastResult *BeforeToolResult,
	firstErr error,
) (*BeforeToolResult, error) {
	if lastResult != nil && lastResult.CustomResult != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResult == nil && lastResult.ModifiedArguments == nil {
		return nil, nil
	}
	return lastResult, nil
}

// RunBeforeTool runs all before tool callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunBeforeTool(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error) {
	var lastResult *BeforeToolResult
	var firstErr error

	for _, cb := range c.BeforeTool {
		result, err := cb(ctx, args)

		if c.handleCallbackError(err, &firstErr) {
			return nil, err
		}

		if c.processBeforeToolResult(result, &ctx, args, &lastResult) {
			if c.continueOnError && firstErr != nil {
				return result, firstErr
			}
			return result, nil
		}
	}

	return c.finalizeBeforeToolResult(lastResult, firstErr)
}

// processAfterToolResult processes after tool callback result and updates context.
// Returns whether to stop execution immediately.
func (c *Callbacks) processAfterToolResult(
	result *AfterToolResult,
	ctx *context.Context,
	lastResult **AfterToolResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if result.CustomResult != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeAfterToolResult determines the final return value for after tool callbacks.
func (c *Callbacks) finalizeAfterToolResult(
	lastResult *AfterToolResult,
	firstErr error,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	if lastResult != nil && lastResult.CustomResult != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult == nil {
		if args.Result != nil {
			return &AfterToolResult{
				CustomResult: args.Result,
			}, nil
		}
		return &AfterToolResult{}, nil
	}
	return lastResult, nil
}

// RunAfterTool runs all after tool callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunAfterTool(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	var lastResult *AfterToolResult
	var firstErr error

	for _, cb := range c.AfterTool {
		result, err := cb(ctx, args)

		if c.handleCallbackError(err, &firstErr) {
			return nil, err
		}

		if c.processAfterToolResult(result, &ctx, &lastResult) {
			if c.continueOnError && firstErr != nil {
				return result, firstErr
			}
			return result, nil
		}
	}

	return c.finalizeAfterToolResult(lastResult, firstErr, args)
}
