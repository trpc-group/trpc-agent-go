// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"
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

// BeforeToolCallback is called before a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be returned and tool execution will be skipped.
// - error: if not nil, tool execution will be stopped with this error.
type BeforeToolCallback func(ctx context.Context, toolName string, toolDeclaration *Declaration, jsonArgs []byte) (any, error)

// AfterToolCallback is called after a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be used instead of the actual tool result.
// - error: if not nil, this error will be returned.
type AfterToolCallback func(ctx context.Context, toolName string, toolDeclaration *Declaration, jsonArgs []byte, result any, runErr error) (any, error)

// ToolCallbacks holds callbacks for tool operations.
type ToolCallbacks struct {
	BeforeTool []BeforeToolCallback
	AfterTool  []AfterToolCallback
}

// NewToolCallbacks creates a new ToolCallbacks instance.
func NewToolCallbacks() *ToolCallbacks {
	return &ToolCallbacks{}
}

// RegisterBeforeTool registers a before tool callback.
func (c *ToolCallbacks) RegisterBeforeTool(cb BeforeToolCallback) {
	c.BeforeTool = append(c.BeforeTool, cb)
}

// RegisterAfterTool registers an after tool callback.
func (c *ToolCallbacks) RegisterAfterTool(cb AfterToolCallback) {
	c.AfterTool = append(c.AfterTool, cb)
}

// RunBeforeTool runs all before tool callbacks in order.
// Returns (customResult, error).
// If any callback returns a custom result, stop and return.
func (c *ToolCallbacks) RunBeforeTool(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
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
func (c *ToolCallbacks) RunAfterTool(
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
