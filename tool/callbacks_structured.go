//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
)

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
	// CustomResult if not nil, will skip tool execution and return this result.
	CustomResult any
	// ModifiedArguments if not nil, will use these modified arguments.
	ModifiedArguments []byte
}

// BeforeToolCallbackStructured is the before tool callback (structured version).
// Parameters:
// - ctx: context.Context (use agent.InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
// - result: if not nil and CustomResult is set, tool execution will be skipped.
// - error: if not nil, tool execution will be stopped with this error.
type BeforeToolCallbackStructured func(
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
	// CustomResult if not nil, will replace the original result.
	CustomResult any
}

// AfterToolCallbackStructured is the after tool callback (structured version).
// Parameters:
// - ctx: context.Context (use agent.InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
// - result: if not nil and CustomResult is set, this result will be used.
// - error: if not nil, this error will be returned.
type AfterToolCallbackStructured func(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error)

// CallbacksStructured holds structured callbacks for tool operations.
type CallbacksStructured struct {
	// BeforeTool is a list of callbacks called before the tool is executed.
	BeforeTool []BeforeToolCallbackStructured
	// AfterTool is a list of callbacks called after the tool is executed.
	AfterTool []AfterToolCallbackStructured
}

// NewCallbacksStructured creates a new CallbacksStructured instance for tool.
func NewCallbacksStructured() *CallbacksStructured {
	return &CallbacksStructured{}
}

// RegisterBeforeTool registers a before tool callback.
func (c *CallbacksStructured) RegisterBeforeTool(
	cb BeforeToolCallbackStructured,
) *CallbacksStructured {
	c.BeforeTool = append(c.BeforeTool, cb)
	return c
}

// RegisterAfterTool registers an after tool callback.
func (c *CallbacksStructured) RegisterAfterTool(
	cb AfterToolCallbackStructured,
) *CallbacksStructured {
	c.AfterTool = append(c.AfterTool, cb)
	return c
}

// RunBeforeTool runs all before tool callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResult or ModifiedArguments,
// stop and return.
func (c *CallbacksStructured) RunBeforeTool(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
) (*BeforeToolResult, error) {
	args := &BeforeToolArgs{
		ToolName:    toolName,
		Declaration: toolDeclaration,
		Arguments:   jsonArgs,
	}
	for _, cb := range c.BeforeTool {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil && (result.CustomResult != nil ||
			result.ModifiedArguments != nil) {
			return result, nil
		}
	}
	return nil, nil
}

// RunAfterTool runs all after tool callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResult, stop and return.
func (c *CallbacksStructured) RunAfterTool(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
	result any,
	runErr error,
) (*AfterToolResult, error) {
	args := &AfterToolArgs{
		ToolName:    toolName,
		Declaration: toolDeclaration,
		Arguments:   jsonArgs,
		Result:      result,
		Error:       runErr,
	}
	for _, cb := range c.AfterTool {
		callResult, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if callResult != nil && callResult.CustomResult != nil {
			return callResult, nil
		}
	}
	return nil, nil
}
