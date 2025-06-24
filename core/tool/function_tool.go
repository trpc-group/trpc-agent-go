// Package tool provides tool implementations for the agent system.
package tool

import (
	"context"
	"encoding/json"
	"reflect"
)

// FunctionTool implements the Tool interface for executing functions with arguments.
type FunctionTool[I, O any] struct {
	name        string
	description string
	inputSchema *Schema
	fn          func(I) O
	unmarshaler unmarshaler
}

// FunctionToolConfig contains configuration options for a FunctionTool.
type FunctionToolConfig struct {
	// Name is the name of the tool. If empty, the function name will be used.
	Name string

	// Description describes what the tool does.
	Description string
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
func NewFunctionTool[I, O any](fn func(I) O, cfg FunctionToolConfig) *FunctionTool[I, O] {
	var empty I
	schema := generateJSONSchema(reflect.TypeOf(empty))

	return &FunctionTool[I, O]{name: cfg.Name, description: cfg.Description, fn: fn, unmarshaler: &jsonUnmarshaler{}, inputSchema: schema}
}

// Call calls the function tool with the provided arguments.
// It unmarshals the given arguments into the tool's arguments placeholder,
// then calls the underlying function with these arguments.
// Returns the result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var input I
	if err := ft.unmarshaler.Unmarshal(jsonArgs, &input); err != nil {
		return nil, err
	}
	return ft.fn(input), nil
}

// Declaration returns a pointer to a Declaration struct that describes the FunctionTool,
// including its name, description, and expected arguments.
func (ft *FunctionTool[I, O]) Declaration() *Declaration {
	return &Declaration{
		Name:        ft.name,
		Description: ft.description,
		InputSchema: ft.inputSchema,
	}
}

type unmarshaler interface {
	Unmarshal([]byte, any) error
}

type jsonUnmarshaler struct{}

// Unmarshal unmarshals JSON data into the provided interface.
func (j *jsonUnmarshaler) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
