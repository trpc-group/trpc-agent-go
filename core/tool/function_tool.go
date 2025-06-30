// Package tool provides tool implementations for the agent system.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// FunctionTool implements the UnaryTool interface for executing functions with arguments.
// It provides a generic way to wrap any function as a tool that can be called
// with JSON arguments and returns results.
type FunctionTool[I, O any] struct {
	name         string
	description  string
	inputSchema  *Schema
	outputSchema *Schema
	fn           func(I) O
	unmarshaler  unmarshaler
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
	var (
		emptyI I
		emptyO O
	)
	iSchema := generateJSONSchema(reflect.TypeOf(emptyI))
	oSchema := generateJSONSchema(reflect.TypeOf(emptyO))

	return &FunctionTool[I, O]{
		name:        cfg.Name,
		description: cfg.Description,
		fn:          fn, unmarshaler: &jsonUnmarshaler{},
		inputSchema:  iSchema,
		outputSchema: oSchema,
	}
}

// Call executes the function tool with the provided JSON arguments.
// It unmarshals the given arguments into the tool's input type,
// then calls the underlying function with these arguments.
//
// Parameters:
//   - ctx: the context for the function call
//   - jsonArgs: JSON-encoded arguments for the function
//
// Returns:
//   - The result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var input I
	if err := ft.unmarshaler.Unmarshal(jsonArgs, &input); err != nil {
		return nil, err
	}
	return ft.fn(input), nil
}

// Declaration returns the tool's declaration information.
// It provides metadata about the tool including its name, description,
// and JSON schema for the expected input arguments.
//
// Returns:
//   - A Declaration struct containing the tool's metadata.
func (ft *FunctionTool[I, O]) Declaration() *Declaration {
	return &Declaration{
		Name:         ft.name,
		Description:  ft.description,
		InputSchema:  ft.inputSchema,
		OutputSchema: ft.outputSchema,
	}
}

// StreamableFunctionTool implements the UnaryTool interface for executing functions
// that return streaming results. It extends the basic FunctionTool to support
// streaming output through StreamReader.
type StreamableFunctionTool[I, O any] struct {
	name         string
	description  string
	inputSchema  *Schema
	outputSchema *Schema
	fn           func(I) *StreamReader
	unmarshaler  unmarshaler
}

// NewStreamableFunctionTool creates a new StreamableFunctionTool instance.
// It wraps a function that returns a StreamReader to provide streaming capabilities.
//
// Parameters:
//   - fn: the function that takes input I and returns a StreamReader[O]
//   - cfg: configuration options for the tool
//
// Returns:
//   - A pointer to the newly created StreamableFunctionTool.
func NewStreamableFunctionTool[I, O any](fn func(I) *StreamReader, cfg FunctionToolConfig) *StreamableFunctionTool[I, O] {
	var (
		emptyI I
		emptyO O
	)
	iSchema := generateJSONSchema(reflect.TypeOf(emptyI))
	oSchema := generateJSONSchema(reflect.TypeOf(emptyO))

	return &StreamableFunctionTool[I, O]{
		name:         cfg.Name,
		description:  cfg.Description,
		fn:           fn,
		unmarshaler:  &jsonUnmarshaler{},
		inputSchema:  iSchema,
		outputSchema: oSchema,
	}
}

// StreamableCall executes the streamable function tool with JSON arguments.
// It unmarshals the arguments, calls the underlying function, and returns
// a StreamReader that converts the output to JSON strings.
//
// Parameters:
//   - ctx: the context for the function call
//   - jsonArgs: JSON-encoded arguments for the function
//
// Returns:
//   - A StreamReader[string] containing JSON-encoded results, or an error.
func (t *StreamableFunctionTool[I, O]) StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error) {
	// FunctionTool does not support streaming calls, so we return an error.
	var input I
	if err := t.unmarshaler.Unmarshal(jsonArgs, &input); err != nil {
		return nil, err
	}
	if t.fn == nil {
		return nil, fmt.Errorf("FunctionTool: %s does not support streaming calls", t.name)
	}
	return t.fn(input), nil
}

// Declaration returns the tool's declaration information.
// It provides metadata about the streamable tool including its name, description,
// and JSON schema for the expected input arguments.
//
// Returns:
//   - A Declaration struct containing the tool's metadata.
func (t *StreamableFunctionTool[I, O]) Declaration() *Declaration {
	return &Declaration{
		Name:         t.name,
		Description:  t.description,
		InputSchema:  t.inputSchema,
		OutputSchema: t.outputSchema,
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
