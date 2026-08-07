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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonutils"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
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
	// resultFormatter optionally formats the final tool result as model-visible
	// message content. When nil, the framework keeps its default JSON behavior.
	resultFormatter resultformat.Formatter
	// concurrencySafe reports whether this tool may share a turn with others on
	// the parallel tool path. It defaults to true, which is how a tool that
	// publishes nothing at all is already read.
	concurrencySafe bool
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
	concurrencySafe   bool
	inputSchema       *tool.Schema
	outputSchema      *tool.Schema
	resultFormatter   resultformat.Formatter
	disableOutputGen  bool
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

// WithConcurrencySafe sets whether the function tool tolerates running at the
// same time as the other tool calls in a turn. It defaults to true.
//
// Set it to false for a tool whose calls contend for something the caller cannot
// see — a shared working directory, an external process, a session the tool
// reads back after writing. The value is published through tool.ConcurrencyAware
// so schedulers and host policies can honor it; a tool that says nothing is read
// as safe, which is why the default here is true.
func WithConcurrencySafe(safe bool) Option {
	return func(opts *functionToolOptions) {
		opts.concurrencySafe = safe
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

// WithDisableOutputSchemaGen disables automatic output schema generation. A
// custom schema provided with WithOutputSchema always takes precedence.
func WithDisableOutputSchemaGen() Option {
	return func(opts *functionToolOptions) {
		opts.disableOutputGen = true
	}
}

// WithResultFormatter sets the formatter for the function tool's final result.
// It is currently supported by LLMAgent's default tool-call flow. Graph
// ToolsNode, ToolPipe, wrappers that replace tool instances, and direct
// Tool.Call consumers do not currently apply it. The formatter changes only
// the default model-visible tool message content; the framework continues to
// manage the message role, tool name, tool call ID, ordering, and session
// persistence. When formatter is nil, the framework uses its default JSON
// representation. A formatter runs only when the tool declared a result for
// the call: when a before-tool callback or plugin short-circuits the call with
// its own result, the tool never runs and the framework keeps its default
// JSON. An after-tool callback replacing the result of a tool that did run is
// a different case: the replacement is formatted, so it has to be a value the
// formatter accepts. A streamable tool must declare its final result with
// tool.FinalResultChunk to be formatted; see
// StreamableFunctionTool.ResultFormatter. Repeated configuration is
// last-writer-wins.
func WithResultFormatter(formatter resultformat.Formatter) Option {
	return func(opts *functionToolOptions) {
		opts.resultFormatter = formatter
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
		unmarshaler:     &jsonUnmarshaler{},
		concurrencySafe: true,
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
	} else if !options.disableOutputGen {
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
		resultFormatter:   options.resultFormatter,
		concurrencySafe:   options.concurrencySafe,
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
	jsonArgs = normalizeJSONArgs(jsonArgs, ft.inputSchema)
	var input I
	if err := unmarshalToolArgs(ctx, jsonArgs, &input); err != nil {
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

// ResultFormatter returns the formatter configured by WithResultFormatter.
// It is used by LLMAgent's default tool-call flow; configure formatting with
// WithResultFormatter rather than calling this method directly.
func (ft *FunctionTool[I, O]) ResultFormatter() resultformat.Formatter {
	return ft.resultFormatter
}

// IsConcurrencySafe reports whether this tool may run at the same time as the
// other tool calls in a turn, implementing tool.ConcurrencyAware. It is true
// unless WithConcurrencySafe(false) was given.
func (ft *FunctionTool[I, O]) IsConcurrencySafe() bool {
	return ft.concurrencySafe
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
	// resultFormatter optionally formats the final tool result as model-visible
	// message content. Intermediate stream events are unaffected.
	resultFormatter resultformat.Formatter
	// concurrencySafe has the same meaning as in FunctionTool.
	concurrencySafe bool
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
		unmarshaler:     &jsonUnmarshaler{},
		concurrencySafe: true,
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
	} else if !options.disableOutputGen {
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
		resultFormatter:   options.resultFormatter,
		concurrencySafe:   options.concurrencySafe,
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
	jsonArgs = normalizeJSONArgs(jsonArgs, t.inputSchema)
	var input I
	if err := unmarshalToolArgs(ctx, jsonArgs, &input); err != nil {
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

// ResultFormatter returns the formatter configured by WithResultFormatter.
// In LLMAgent's default tool-call flow, only the final streamable result is
// formatted; intermediate events are not. The tool must declare that result
// with tool.FinalResultChunk. When a stream ends without one, the final result
// is the stream content merged by the framework rather than O, so the
// framework keeps its default JSON representation instead of formatting it,
// and an after-tool callback replacing that content does not make the call
// eligible for formatting again.
func (t *StreamableFunctionTool[I, O]) ResultFormatter() resultformat.Formatter {
	return t.resultFormatter
}

// IsConcurrencySafe reports whether this tool may run at the same time as the
// other tool calls in a turn, implementing tool.ConcurrencyAware. It is true
// unless WithConcurrencySafe(false) was given.
func (t *StreamableFunctionTool[I, O]) IsConcurrencySafe() bool {
	return t.concurrencySafe
}

type unmarshaler interface {
	Unmarshal([]byte, any) error
}

type jsonUnmarshaler struct{}

// normalizeJSONArgs coerces nil or empty argument payloads to "{}" only for
// zero-parameter tools (input schema with no properties and no required fields).
// Tools with required or optional properties keep empty args so unmarshal fails.
func normalizeJSONArgs(jsonArgs []byte, inputSchema *tool.Schema) []byte {
	if len(jsonArgs) > 0 {
		return jsonArgs
	}
	if schemaAcceptsEmptyObject(inputSchema) {
		return []byte("{}")
	}
	return jsonArgs
}

// schemaAcceptsEmptyObject reports whether an omitted argument object is valid.
func schemaAcceptsEmptyObject(inputSchema *tool.Schema) bool {
	if inputSchema == nil {
		return false
	}
	return len(inputSchema.Required) == 0 && len(inputSchema.Properties) == 0
}

// unmarshalToolArgs decodes tool arguments using strict JSON by default. When
// ToolCallArgumentsJSONRepairEnabled is set on the invocation in ctx, malformed
// JSON is repaired via internal/jsonutils before unmarshaling.
func unmarshalToolArgs(ctx context.Context, data []byte, v any) error {
	if inv, ok := agent.InvocationFromContext(ctx); ok &&
		jsonrepair.IsToolCallArgumentsJSONRepairEnabled(inv) {
		return jsonutils.DecodeLeadingJSON(string(data), v)
	}
	return json.Unmarshal(data, v)
}

// Unmarshal unmarshals JSON data into the provided interface.
func (j *jsonUnmarshaler) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
