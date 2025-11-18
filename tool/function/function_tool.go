//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package function provides function-based tool implementations for the agent system.
package function

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// FunctionTool implements the CallableTool interface for executing functions with arguments.
// It provides a generic way to wrap any function as a tool that can be called
// with JSON arguments and returns results.
type FunctionTool[I, O any] struct {
	name         string
	description  string
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
	fn           func(context.Context, I) (O, error)
	longRunning  bool
	unmarshaler  unmarshaler
	// skipSummarization indicates whether the outer flow should skip
	// the post-tool summarization step after this tool returns.
	skipSummarization bool
}

// Option is a function that configures a FunctionTool.
type Option func(*functionToolOptions)

// functionToolOptions holds the configuration options for FunctionTool.
type functionToolOptions struct {
	name              string
	description       string
	unmarshaler       unmarshaler
	longRunning       bool
	skipSummarization bool
	inputSchema       *tool.Schema
	outputSchema      *tool.Schema
}

// WithName sets the name of the function tool.
//
// Note: Tool names must comply with LLM API requirements for compatibility.
// Some APIs (e.g., Kimi, DeepSeek) enforce strict naming patterns:
// - Must match pattern: ^[a-zA-Z0-9_-]+$
// - Cannot contain Chinese characters, parentheses, or special symbols
// - Use only English letters, numbers, underscores, and hyphens
//
// Best practice: Use ^[a-zA-Z0-9_-]+ only to ensure maximum compatibility.
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

// WithLongRunning sets whether the function tool is long-running.
// A long-running function tool indicates that it may take a significant amount of time to complete.
func WithLongRunning(longRunning bool) Option {
	return func(opts *functionToolOptions) {
		opts.longRunning = longRunning
	}
}

// WithSkipSummarization sets whether the outer flow should skip the
// summarization step after this tool returns a result. When true, the
// tool.response event will be annotated and the current turn ends.
func WithSkipSummarization(skip bool) Option {
	return func(opts *functionToolOptions) {
		opts.skipSummarization = skip
	}
}

// WithInputSchema sets a custom input schema for the function tool.
// When provided, the automatic schema generation will be skipped.
func WithInputSchema(schema *tool.Schema) Option {
	return func(opts *functionToolOptions) {
		opts.inputSchema = schema
	}
}

// WithOutputSchema sets a custom output schema for the function tool.
// When provided, the automatic schema generation will be skipped.
func WithOutputSchema(schema *tool.Schema) Option {
	return func(opts *functionToolOptions) {
		opts.outputSchema = schema
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
func NewFunctionTool[I, O any](fn func(context.Context, I) (O, error), opts ...Option) *FunctionTool[I, O] {
	// Set default options
	options := &functionToolOptions{
		unmarshaler: &jsonUnmarshaler{},
	}

	// Apply provided options
	for _, opt := range opts {
		opt(options)
	}
	if options.name == "" {
		log.Warnf("FunctionTool: name is empty")
	}
	if options.description == "" {
		log.Warnf("FunctionTool: description is empty")
	}

	var (
		emptyI I
		emptyO O
	)

	var iSchema *tool.Schema
	if options.inputSchema != nil {
		iSchema = options.inputSchema
	} else {
		iSchema = itool.GenerateJSONSchema(reflect.TypeOf(emptyI))
	}

	var oSchema *tool.Schema
	if options.outputSchema != nil {
		oSchema = options.outputSchema
	} else {
		oSchema = itool.GenerateJSONSchema(reflect.TypeOf(emptyO))
	}

	return &FunctionTool[I, O]{
		name:              options.name,
		description:       options.description,
		longRunning:       options.longRunning,
		fn:                fn,
		unmarshaler:       options.unmarshaler,
		inputSchema:       iSchema,
		outputSchema:      oSchema,
		skipSummarization: options.skipSummarization,
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
	return ft.fn(ctx, input)
}

// LongRunning indicates whether the function tool is expected to run for a long time.
func (ft *FunctionTool[I, O]) LongRunning() bool {
	return ft.longRunning
}

// SkipSummarization reports whether this tool prefers skipping the
// outer-agent summarization after tool.response.
func (ft *FunctionTool[I, O]) SkipSummarization() bool {
	return ft.skipSummarization
}

// Declaration returns the tool's declaration information.
// It provides metadata about the tool including its name, description,
// and JSON schema for the expected input arguments.
//
// Note: The tool name must comply with LLM API requirements.
// Some APIs (e.g., Kimi, DeepSeek) enforce strict naming patterns:
// - Must match pattern: ^[a-zA-Z0-9_-]+$
// - Cannot contain Chinese characters, parentheses, or special symbols
//
// Best practice: Use ^[a-zA-Z0-9_-]+ only to ensure maximum compatibility.
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

// StreamableFunctionTool implements the CallableTool interface for executing functions
// that return streaming results. It extends the basic FunctionTool to support
// streaming output through StreamReader.
type StreamableFunctionTool[I, O any] struct {
	name         string
	description  string
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
	fn           func(context.Context, I) (*tool.StreamReader, error)
	longRunning  bool
	unmarshaler  unmarshaler
	// skipSummarization has the same meaning as in FunctionTool.
	skipSummarization bool
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
func NewStreamableFunctionTool[I, O any](fn func(context.Context, I) (*tool.StreamReader, error), opts ...Option) *StreamableFunctionTool[I, O] {
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

	var iSchema *tool.Schema
	if options.inputSchema != nil {
		iSchema = options.inputSchema
	} else {
		iSchema = itool.GenerateJSONSchema(reflect.TypeOf(emptyI))
	}

	var oSchema *tool.Schema
	if options.outputSchema != nil {
		oSchema = options.outputSchema
	} else {
		oSchema = itool.GenerateJSONSchema(reflect.TypeOf(emptyO))
	}

	return &StreamableFunctionTool[I, O]{
		name:              options.name,
		description:       options.description,
		longRunning:       options.longRunning,
		fn:                fn,
		unmarshaler:       options.unmarshaler,
		inputSchema:       iSchema,
		outputSchema:      oSchema,
		skipSummarization: options.skipSummarization,
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
	return t.fn(ctx, input)
}

// Declaration returns the tool's declaration information.
// It provides metadata about the streamable tool including its name, description,
// and JSON schema for the expected input arguments.
//
// Note: The tool name must comply with LLM API requirements.
// Some APIs (e.g., Kimi, DeepSeek) enforce strict naming patterns:
// - Must match pattern: ^[a-zA-Z0-9_-]+$
// - Cannot contain Chinese characters, parentheses, or special symbols
//
// Best practice: Use ^[a-zA-Z0-9_-]+ only to ensure maximum compatibility.
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

// LongRunning indicates whether the streamable function tool is expected to run for a long time.
func (t *StreamableFunctionTool[I, O]) LongRunning() bool {
	return t.longRunning
}

// SkipSummarization reports whether this tool prefers skipping the
// outer-agent summarization after tool.response.
func (t *StreamableFunctionTool[I, O]) SkipSummarization() bool {
	return t.skipSummarization
}

type unmarshaler interface {
	Unmarshal([]byte, any) error
}

type jsonUnmarshaler struct{}

// Unmarshal unmarshals JSON data into the provided interface.
func (j *jsonUnmarshaler) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
