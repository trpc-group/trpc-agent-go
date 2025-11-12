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

// Callbacks holds callbacks for tool operations.
// Internally stores the new structured callback types.
type Callbacks struct {
	// BeforeTool is a list of callbacks called before the tool is executed.
	BeforeTool []BeforeToolCallbackStructured
	// AfterTool is a list of callbacks called after the tool is executed.
	AfterTool []AfterToolCallbackStructured
}

// NewCallbacks creates a new Callbacks instance for tool.
func NewCallbacks() *Callbacks {
	return &Callbacks{}
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

// RunBeforeTool runs all before tool callbacks in order.
// This method uses the new structured callback interface.
func (c *Callbacks) RunBeforeTool(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error) {
	for _, cb := range c.BeforeTool {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil && result.CustomResult != nil {
			return result, nil
		}
	}
	return nil, nil
}

// RunAfterTool runs all after tool callbacks in order.
// This method uses the new structured callback interface.
func (c *Callbacks) RunAfterTool(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	for _, cb := range c.AfterTool {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil && result.CustomResult != nil {
			return result, nil
		}
	}
	// If no callbacks or no custom result, return the original result
	if args.Result != nil {
		return &AfterToolResult{
			CustomResult: args.Result,
		}, nil
	}
	return &AfterToolResult{}, nil
}
