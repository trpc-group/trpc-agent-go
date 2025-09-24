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
type BeforeToolCallback func(ctx context.Context, toolName string, toolDeclaration *Declaration, jsonArgs *[]byte) (any, error)

// AfterToolCallback is called after a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be used instead of the actual tool result.
// - error: if not nil, this error will be returned.
type AfterToolCallback func(ctx context.Context, toolName string, toolDeclaration *Declaration, jsonArgs []byte, result any, runErr error) (any, error)

// Callbacks holds callbacks for tool operations.
type Callbacks struct {
	// BeforeTool is a list of callbacks that are called before the tool is executed.
	BeforeTool []BeforeToolCallback
	// AfterTool is a list of callbacks that are called after the tool is executed.
	AfterTool []AfterToolCallback
}

// NewCallbacks creates a new Callbacks instance for tool.
func NewCallbacks() *Callbacks {
	return &Callbacks{}
}

// RegisterBeforeTool registers a before tool callback.
func (c *Callbacks) RegisterBeforeTool(cb BeforeToolCallback) *Callbacks {
	c.BeforeTool = append(c.BeforeTool, cb)
	return c
}

// RegisterAfterTool registers an after tool callback.
func (c *Callbacks) RegisterAfterTool(cb AfterToolCallback) *Callbacks {
	c.AfterTool = append(c.AfterTool, cb)
	return c
}

// RunBeforeTool runs all before tool callbacks in order.
// Returns (customResult, error).
// If any callback returns a custom result, stop and return.
func (c *Callbacks) RunBeforeTool(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs *[]byte,
) (any, error) {
	for _, cb := range c.BeforeTool {
		customResult, err := cb(ctx, toolName, toolDeclaration, jsonArgs)
		if err != nil {
			return nil, err
		}
		if customResult != nil {
			return customResult, nil
		}
	}
	return nil, nil
}

// RunAfterTool runs all after tool callbacks in order.
// Returns (customResult, error).
// If any callback returns a custom result, stop and return.
func (c *Callbacks) RunAfterTool(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
	result any,
	runErr error,
) (any, error) {
	for _, cb := range c.AfterTool {
		customResult, err := cb(ctx, toolName, toolDeclaration, jsonArgs, result, runErr)
		if err != nil {
			return nil, err
		}
		if customResult != nil {
			return customResult, nil
		}
	}
	return nil, nil
}
