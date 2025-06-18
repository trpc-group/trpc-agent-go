// Package tool provides tool implementations for the agent system.
package tool

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// FunctionTool implements the Tool interface for executing functions with arguments.
type FunctionTool[I, O any] struct {
	name        string
	description string
	fn          func(I) O
}

// NewFunctionTool creates and returns a new instance of FunctionTool with the specified
// name, description, function implementation, and argument placeholder.
// Parameters:
//   - name: the name of the function tool.
//   - description: a brief description of the function tool.
//   - fn: the function implementation conforming to FuncType.
//   - argumentsPlaceholder: a placeholder for the function's arguments of type ArgumentType.
//
// Returns:
//   - A pointer to the newly created FunctionTool.
func NewFunctionTool[I, O any](name, description string, fn func(I) O) *FunctionTool[I, O] {
	return &FunctionTool[I, O]{name: name, description: description, fn: fn}
}

// Run executes the function tool with the provided arguments.
// It unmarshals the given arguments into the tool's arguments placeholder,
// then calls the underlying function with these arguments.
// Returns the result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) Run(ctx context.Context, args Arguments) (any, error) {
	var argumentsPlaceholder I
	err := json.Unmarshal(args, &argumentsPlaceholder)
	if err != nil {
		return nil, err
	}
	result := ft.fn(argumentsPlaceholder)
	log.Info(result)
	return result, nil
}

// Declaration returns a pointer to a Declaration struct that describes the FunctionTool,
// including its name, description, and expected arguments.
func (ft *FunctionTool[I, O]) Declaration() *Declaration {
	return &Declaration{
		Name:        "FunctionTool",
		Description: "A tool that executes a function with provided arguments.",
		Arguments:   Arguments{},
	}
}

// Combine modifies the provided request to include the current FunctionTool.
// It returns the updated request, the tool itself, and an error if any occurred during the process.
// In this placeholder implementation, the request is returned unmodified along with the tool and a nil error.
func (ft *FunctionTool[I, O]) Combine(req *model.Request) (*model.Request, Tool, error) {
	// This is a placeholder implementation.
	// In a real implementation, you would modify the request to include this tool.
	return req, ft, nil
}
