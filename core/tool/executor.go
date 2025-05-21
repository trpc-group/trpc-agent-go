package tool

import (
	"context"
)

// Executor defines an interface for executing functions with arguments.
type Executor interface {
	// ExecuteFunction executes a function with the provided arguments.
	ExecuteFunction(ctx context.Context, args map[string]interface{}) (interface{}, error)
}

// FunctionExecutor is a basic implementation of Executor.
type FunctionExecutor struct {
	fn func(context.Context, map[string]interface{}) (interface{}, error)
}

// NewFunctionExecutor creates a new function executor.
func NewFunctionExecutor(fn func(context.Context, map[string]interface{}) (interface{}, error)) *FunctionExecutor {
	return &FunctionExecutor{fn: fn}
}

// ExecuteFunction executes the function with the provided arguments.
func (e *FunctionExecutor) ExecuteFunction(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	return e.fn(ctx, args)
} 