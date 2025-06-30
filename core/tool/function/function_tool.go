// Package tool provides tool implementations for the agent system.
package function

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
)

// FunctionTool implements the UnaryTool interface for executing functions with arguments.
// It provides a generic way to wrap any function as a tool that can be called
// with JSON arguments and returns results.
type FunctionTool[I, O any] struct {
	name         string
	description  string
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
	fn           func(I) O
	unmarshaler  unmarshaler
}

// Option is a function that configures a FunctionTool.
type Option func(*functionToolOptions)

// functionToolOptions holds the configuration options for FunctionTool.
type functionToolOptions struct {
	name        string
	description string
	unmarshaler unmarshaler
}

// WithName sets the name of the function tool.
func WithName(name string) Option {
	return func(opts *functionToolOptions) {
		opts.name = name
	}
}

// WithDescription sets the description of the function tool.
func WithDescription(description string) Option {
	return func(opts *functionToolOptions) {
		opts.description = description
	}
}

// NewFunctionTool creates and returns a new instance of FunctionTool with the specified
// function implementation and optional configuration.
// Parameters:
//   - fn: the function implementation conforming to FuncType.
//   - opts: optional configuration functions.
//
// Returns:
//   - A pointer to the newly created FunctionTool.
func NewFunctionTool[I, O any](fn func(I) O, opts ...Option) *FunctionTool[I, O] {
	// Set default options
	options := &functionToolOptions{
		unmarshaler: &jsonUnmarshaler{},
	}

	// Apply provided options
	for _, opt := range opts {
		opt(options)
	}

	var (
		emptyI I
		emptyO O
	)
	iSchema := itool.GenerateJSONSchema(reflect.TypeOf(emptyI))
	oSchema := itool.GenerateJSONSchema(reflect.TypeOf(emptyO))

	return &FunctionTool[I, O]{
		name:         options.name,
		description:  options.description,
		fn:           fn,
		unmarshaler:  options.unmarshaler,
		inputSchema:  iSchema,
		outputSchema: oSchema,
	}
}

// UnaryCall executes the function tool with the provided JSON arguments.
// It unmarshals the given arguments into the tool's input type,
// then calls the underlying function with these arguments.
//
// Parameters:
//   - ctx: the context for the function call
//   - jsonArgs: JSON-encoded arguments for the function
//
// Returns:
//   - The result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) UnaryCall(ctx context.Context, jsonArgs []byte) (any, error) {
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
func (ft *FunctionTool[I, O]) Declaration() *tool.Declaration {
	return &tool.Declaration{
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
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
	fn           func(I) *tool.StreamReader
	unmarshaler  unmarshaler
}

// NewStreamableFunctionTool creates a new StreamableFunctionTool instance.
// It wraps a function that returns a StreamReader to provide streaming capabilities.
//
// Parameters:
//   - fn: the function that takes input I and returns a StreamReader[O]
//   - opts: optional configuration functions
//
// Returns:
//   - A pointer to the newly created StreamableFunctionTool.
func NewStreamableFunctionTool[I, O any](fn func(I) *tool.StreamReader, opts ...Option) *StreamableFunctionTool[I, O] {
	// Set default options
	options := &functionToolOptions{
		unmarshaler: &jsonUnmarshaler{},
	}

	// Apply provided options
	for _, opt := range opts {
		opt(options)
	}

	var (
		emptyI I
		emptyO O
	)
	iSchema := itool.GenerateJSONSchema(reflect.TypeOf(emptyI))
	oSchema := itool.GenerateJSONSchema(reflect.TypeOf(emptyO))

	return &StreamableFunctionTool[I, O]{
		name:         options.name,
		description:  options.description,
		fn:           fn,
		unmarshaler:  options.unmarshaler,
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
func (t *StreamableFunctionTool[I, O]) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
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
func (t *StreamableFunctionTool[I, O]) Declaration() *tool.Declaration {
	return &tool.Declaration{
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
