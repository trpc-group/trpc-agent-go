// Package tool provides tool implementations for the agent system.
package tool

import (
	"context"
	"encoding/json"
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

// Call calls the function tool with the provided arguments.
// It unmarshals the given arguments into the tool's arguments placeholder,
// then calls the underlying function with these arguments.
// Returns the result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) Call(ctx context.Context, args json.RawMessage) (any, error) {
	var input I
	err := json.Unmarshal(args, &input)
	if err != nil {
		return nil, err
	}
	return ft.fn(input), nil
}

// Declaration returns a pointer to a Declaration struct that describes the FunctionTool,
// including its name, description, and expected arguments.
func (ft *FunctionTool[I, O]) Declaration() *Declaration {
	return &Declaration{
		Name:        "FunctionTool",
		Description: "A tool that executes a function with provided arguments.",
		Arguments:   json.RawMessage{},
	}
}
