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
	"fmt"
	"runtime/debug"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	callbackPanicErrFmt = "%s: %v"
	callbackPanicLogFmt = log.PanicPrefix + " %s (tool_call_id: %s, tool: %s): " +
		"%v\n%s"

	beforeToolCallbackPanic         = "before tool callback panic"
	afterToolCallbackPanic          = "after tool callback panic"
	toolResultMessagesCallbackPanic = "tool result messages callback panic"
)

// BeforeToolCallback is called before a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be returned and tool execution will be skipped.
// - error: if not nil, tool execution will be stopped with this error.
// Deprecated: Use BeforeToolCallbackStructured instead for better type safety and context passing.
type BeforeToolCallback = func(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs *[]byte,
) (any, error)

// AfterToolCallback is called after a tool is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be used instead of the actual tool result.
// - error: if not nil, this error will be returned.
// Deprecated: Use AfterToolCallbackStructured instead for better type safety and context passing.
type AfterToolCallback = func(
	ctx context.Context,
	toolName string,
	toolDeclaration *Declaration,
	jsonArgs []byte,
	result any,
	runErr error,
) (any, error)

// BeforeToolArgs contains all parameters for before tool callback.
type BeforeToolArgs struct {
	// ToolCallID is the ID of the tool call issued by the model.
	ToolCallID string
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the tool arguments in JSON bytes (can be modified).
	Arguments []byte
	// ResumeValue is the value of the resume.
	ResumeValue any
	// ResumeMap is the map of resume values.
	ResumeMap map[string]any
}

// BeforeToolResult contains the return value for before tool callback.
type BeforeToolResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResult if not nil, will skip tool execution and return this result.
	CustomResult any
	// SkipStateDelta requests skipping the tool's state-delta provider for
	// CustomResult. It is ignored when CustomResult is nil.
	SkipStateDelta bool
	// ModifiedArguments if not nil, will use these modified arguments.
	ModifiedArguments []byte
}

// BeforeToolCallbackStructured is called before a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be returned and tool execution will be skipped.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//   - SkipStateDelta: if true and CustomResult is non-nil, the framework will
//     not invoke the tool's state-delta provider for that result.
//   - ModifiedArguments: if not nil, will use these modified arguments for tool execution.
//
// - error: if not nil, tool execution will be stopped with this error.
type BeforeToolCallbackStructured = func(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error)

// AfterToolArgs contains all parameters for after tool callback.
type AfterToolArgs struct {
	// ToolCallID is the ID of the tool call issued by the model.
	ToolCallID string
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the tool arguments in JSON bytes.
	Arguments []byte
	// Result is the tool execution result (may be nil).
	Result any
	// Error is the error occurred during tool execution (may be nil).
	Error error
	// Meta contains optional metadata from the tool result.
	// For MCP tools, this includes the _meta field from CallToolResult.
	Meta map[string]any
}

// AfterToolResult contains the return value for after tool callback.
type AfterToolResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResult if not nil, will replace the original result.
	// Empty maps, slices, and strings are still replacements.
	CustomResult any
	// SkipResultFormatter requests default result conversion for CustomResult
	// instead of applying the tool's configured result formatter. It is ignored
	// when CustomResult is nil. model.ToolResultContent is preserved; other
	// results use JSON serialization.
	SkipResultFormatter bool
	// SkipStateDelta requests skipping the tool's state-delta provider for
	// CustomResult. It is ignored when CustomResult is nil.
	SkipStateDelta bool
	// SkipSummarization requests ending the turn after the tool response.
	SkipSummarization bool
}

// AfterToolCallbackStructured is called after a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be used instead of the actual tool result.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//   - SkipResultFormatter: if true and CustomResult is non-nil, the framework
//     uses default result conversion instead of the tool's configured result
//     formatter, preserving model.ToolResultContent and using JSON for other values.
//   - SkipStateDelta: if true and CustomResult is non-nil, the framework will
//     not invoke the tool's state-delta provider for that result.
//   - SkipSummarization: if true, the framework will skip the extra
//     post-tool LLM summarization step.
//
// - error: if not nil, this error will be returned.
type AfterToolCallbackStructured = func(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error)

// ToolResultMessagesInput contains all parameters for generating messages from a tool result.
type ToolResultMessagesInput struct {
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the final tool arguments in JSON bytes (after before-tool callbacks).
	Arguments []byte
	// Result is the final tool execution result (after after-tool callbacks).
	Result any
	// ToolCallID is the ID of the tool call issued by the model.
	ToolCallID string
	// DefaultToolMessage is the default tool response message that the framework
	// would send if no custom messages are provided by the callback.
	// The concrete type is framework-specific (typically model.Message).
	DefaultToolMessage any
}

// ToolResultMessagesFunc converts a tool execution result into one or more messages
// to be sent back to the model.
//
// Behavior contract:
//   - If the callback returns (nil, nil) or an empty slice, the framework will
//     fall back to DefaultToolMessage.
//   - If the callback returns non-empty messages, they will replace the default
//     tool message. Callers are expected to return a value that the framework
//     understands (typically []model.Message) and to include at least one
//     RoleTool message whose ToolID matches ToolCallID to remain
//     protocol-compatible.
//
// To avoid import cycles, the return type is any. When using llmagent with
// the built-in OpenAI/Anthropic adapters, the recommended return type is
// []model.Message (or a single model.Message), which will be type-asserted
// by the framework.
type ToolResultMessagesFunc = func(
	ctx context.Context,
	in *ToolResultMessagesInput,
) (any, error)

// Callbacks holds callbacks for tool operations.
// Internally stores the new structured callback types.
type Callbacks struct {
	// BeforeTool is a list of callbacks called before the tool is executed.
	BeforeTool []BeforeToolCallbackStructured
	// AfterTool is a list of callbacks called after the tool is executed.
	AfterTool []AfterToolCallbackStructured
	// ToolResultMessages is an optional callback that can convert a tool
	// execution result into one or more messages to be sent back to the model.
	// When set, it is invoked after the tool and AfterTool callbacks have run.
	ToolResultMessages ToolResultMessagesFunc
	// continueOnError controls whether to continue executing callbacks when an error occurs.
	// Default: false (stop on first error)
	continueOnError bool
	// continueOnResponse controls whether to continue executing callbacks when a CustomResult is returned.
	// Default: false (stop on first CustomResult)
	continueOnResponse bool
}

// CallbacksOption configures Callbacks behavior.
type CallbacksOption func(*Callbacks)

// WithContinueOnError sets whether to continue executing callbacks when an error occurs.
func WithContinueOnError(continueOnError bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnError = continueOnError
	}
}

// WithContinueOnResponse sets whether to continue executing callbacks when a CustomResult is returned.
func WithContinueOnResponse(continueOnResponse bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnResponse = continueOnResponse
	}
}

// NewCallbacks creates a new Callbacks instance for tool.
func NewCallbacks(opts ...CallbacksOption) *Callbacks {
	c := &Callbacks{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Clone returns an independent copy of c, including callback lists,
// ToolResultMessages and execution options.
func (c *Callbacks) Clone() *Callbacks {
	if c == nil {
		return nil
	}
	out := &Callbacks{
		BeforeTool:         append([]BeforeToolCallbackStructured(nil), c.BeforeTool...),
		AfterTool:          append([]AfterToolCallbackStructured(nil), c.AfterTool...),
		ToolResultMessages: c.ToolResultMessages,
		continueOnError:    c.continueOnError,
		continueOnResponse: c.continueOnResponse,
	}
	return out
}

// RegisterToolResultMessages registers a ToolResultMessages callback.
// The callback will be invoked once per tool execution, after the tool has
// completed and after all AfterTool callbacks have run.
func (c *Callbacks) RegisterToolResultMessages(cb ToolResultMessagesFunc) *Callbacks {
	c.ToolResultMessages = cb
	return c
}

// RunToolResultMessages runs the ToolResultMessages callback (if set) with panic
// recovery, returning an error when the callback panics.
func (c *Callbacks) RunToolResultMessages(
	ctx context.Context,
	in *ToolResultMessagesInput,
) (result any, err error) {
	if c == nil || c.ToolResultMessages == nil {
		return nil, nil
	}

	toolCallID := ""
	toolName := ""
	if in != nil {
		toolCallID = in.ToolCallID
		toolName = in.ToolName
	}
	defer recoverToolCallbackPanic(
		ctx,
		toolResultMessagesCallbackPanic,
		toolCallID,
		toolName,
		&err,
	)
	return c.ToolResultMessages(ctx, in)
}

// RegisterBeforeTool registers a before tool callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterBeforeTool(cb any) *Callbacks {
	switch callback := cb.(type) {
	case BeforeToolCallbackStructured:
		c.BeforeTool = append(c.BeforeTool, callback)
	case BeforeToolCallback:
		wrapped := func(ctx context.Context, args *BeforeToolArgs) (*BeforeToolResult, error) {
			// Call old signature
			customResult, err := callback(ctx, args.ToolName, args.Declaration, &args.Arguments)
			if err != nil {
				if customResult != nil {
					return &BeforeToolResult{CustomResult: customResult}, err
				}
				return nil, err
			}
			if customResult != nil {
				return &BeforeToolResult{CustomResult: customResult}, nil
			}
			return &BeforeToolResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.BeforeTool = append(c.BeforeTool, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// RegisterAfterTool registers an after tool callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterAfterTool(cb any) *Callbacks {
	switch callback := cb.(type) {
	case AfterToolCallbackStructured:
		c.AfterTool = append(c.AfterTool, callback)
	case AfterToolCallback:
		wrapped := func(ctx context.Context, args *AfterToolArgs) (*AfterToolResult, error) {
			// Call old signature
			customResult, err := callback(ctx, args.ToolName, args.Declaration, args.Arguments, args.Result, args.Error)
			if err != nil {
				if customResult != nil {
					return &AfterToolResult{CustomResult: customResult}, err
				}
				return nil, err
			}
			if customResult != nil {
				return &AfterToolResult{CustomResult: customResult}, nil
			}
			return &AfterToolResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.AfterTool = append(c.AfterTool, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// handleCallbackError processes callback error and returns whether to continue.
func (c *Callbacks) handleCallbackError(err error, firstErr *error) (shouldStop bool) {
	if err == nil {
		return false
	}
	if !c.continueOnError {
		return true
	}
	if *firstErr == nil {
		*firstErr = err
	}
	return false
}

// processBeforeToolResult merges a before tool callback result into acc and updates ctx/arguments.
// Returns whether to stop execution immediately.
func (c *Callbacks) processBeforeToolResult(
	result *BeforeToolResult,
	ctx *context.Context,
	args *BeforeToolArgs,
	acc *BeforeToolResult,
	hasResult *bool,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	*hasResult = true
	if result.Context != nil {
		*ctx = result.Context
		acc.Context = result.Context
	}
	if result.ModifiedArguments != nil {
		if args != nil {
			args.Arguments = result.ModifiedArguments
		}
		acc.ModifiedArguments = result.ModifiedArguments
	}
	if result.CustomResult != nil {
		acc.CustomResult = result.CustomResult
		acc.SkipStateDelta = result.SkipStateDelta
		if !c.continueOnResponse {
			return true
		}
	}
	return false
}

// finalizeBeforeToolResult determines the final return value for before tool callbacks.
func (c *Callbacks) finalizeBeforeToolResult(
	acc BeforeToolResult,
	hasResult bool,
	firstErr error,
) (*BeforeToolResult, error) {
	if c.continueOnError && firstErr != nil {
		if !hasResult {
			return nil, firstErr
		}
		out := acc
		return &out, firstErr
	}
	if !hasResult || (acc.Context == nil && acc.CustomResult == nil && acc.ModifiedArguments == nil) {
		return nil, nil
	}
	out := acc
	return &out, nil
}

func recoverToolCallbackPanic(
	ctx context.Context,
	stage string,
	toolCallID string,
	toolName string,
	errp *error,
) {
	recovered := recover()
	if recovered == nil {
		return
	}

	stack := debug.Stack()
	log.ErrorfContext(
		ctx,
		callbackPanicLogFmt,
		stage,
		toolCallID,
		toolName,
		recovered,
		string(stack),
	)
	*errp = fmt.Errorf(callbackPanicErrFmt, stage, recovered)
}

func (c *Callbacks) runBeforeToolCallback(
	ctx context.Context,
	cb BeforeToolCallbackStructured,
	args *BeforeToolArgs,
) (result *BeforeToolResult, err error) {
	toolCallID := ""
	toolName := ""
	if args != nil {
		toolCallID = args.ToolCallID
		toolName = args.ToolName
	}
	defer recoverToolCallbackPanic(
		ctx,
		beforeToolCallbackPanic,
		toolCallID,
		toolName,
		&err,
	)
	return cb(ctx, args)
}

// RunBeforeTool runs all before tool callbacks in order.
// This method uses the new structured callback interface.
// Unless execution stops on a callback error, non-nil Context,
// ModifiedArguments, and CustomResult (paired with SkipStateDelta) values are
// accumulated across callbacks so later empty, legacy, or partial results
// preserve earlier values. Callback-owned result structs are never mutated and
// their top-level fields are copied into the returned result; referenced
// payloads and argument buffers are not deep-copied.
func (c *Callbacks) RunBeforeTool(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error) {
	var acc BeforeToolResult
	var hasResult bool
	var firstErr error

	for _, cb := range c.BeforeTool {
		result, err := c.runBeforeToolCallback(ctx, cb, args)

		if c.handleCallbackError(err, &firstErr) {
			return result, err
		}

		if c.processBeforeToolResult(result, &ctx, args, &acc, &hasResult) {
			out := acc
			if c.continueOnError && firstErr != nil {
				return &out, firstErr
			}
			return &out, nil
		}
	}

	return c.finalizeBeforeToolResult(acc, hasResult, firstErr)
}

// processAfterToolResult merges an after tool callback result into acc and updates ctx.
// Returns whether to stop execution immediately.
func (c *Callbacks) processAfterToolResult(
	result *AfterToolResult,
	ctx *context.Context,
	acc *AfterToolResult,
	hasResult *bool,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	*hasResult = true
	if result.Context != nil {
		*ctx = result.Context
		acc.Context = result.Context
	}
	if result.SkipSummarization {
		acc.SkipSummarization = true
	}
	if result.CustomResult != nil {
		// Result-handling preferences belong to the retained custom result.
		acc.CustomResult = result.CustomResult
		acc.SkipResultFormatter = result.SkipResultFormatter
		acc.SkipStateDelta = result.SkipStateDelta
		if !c.continueOnResponse {
			return true
		}
	}
	return false
}

// finalizeAfterToolResult determines the final return value for after tool callbacks.
//
// When no AfterTool callbacks are registered, CustomResult is args.Result so
// direct callers keep the released no-callback contract. When callbacks ran
// but none produced a CustomResult, CustomResult stays nil so plugin
// dispatchers treat that as pass-through rather than an override.
func (c *Callbacks) finalizeAfterToolResult(
	acc AfterToolResult,
	hasResult bool,
	firstErr error,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	if c.continueOnError && firstErr != nil {
		if !hasResult {
			return nil, firstErr
		}
		out := acc
		return &out, firstErr
	}
	if !hasResult {
		if len(c.AfterTool) == 0 && args != nil && args.Result != nil {
			return &AfterToolResult{
				CustomResult: args.Result,
			}, nil
		}
		return &AfterToolResult{}, nil
	}
	out := acc
	return &out, nil
}

func (c *Callbacks) runAfterToolCallback(
	ctx context.Context,
	cb AfterToolCallbackStructured,
	args *AfterToolArgs,
) (result *AfterToolResult, err error) {
	toolCallID := ""
	toolName := ""
	if args != nil {
		toolCallID = args.ToolCallID
		toolName = args.ToolName
	}
	defer recoverToolCallbackPanic(
		ctx,
		afterToolCallbackPanic,
		toolCallID,
		toolName,
		&err,
	)
	restore := normalizeAfterToolArgsResult(args)
	defer restore()
	return cb(ctx, args)
}

// normalizeAfterToolArgsResult temporarily rewrites args.Result to the
// callback-facing result shape and returns a restore function.
func normalizeAfterToolArgsResult(args *AfterToolArgs) func() {
	if args == nil {
		return func() {}
	}
	type callbackResultGetter interface {
		GetCallbackResult() any
	}
	rg, ok := args.Result.(callbackResultGetter)
	if !ok {
		return func() {}
	}
	original := args.Result
	args.Result = rg.GetCallbackResult()
	return func() {
		args.Result = original
	}
}

// RunAfterTool runs all after tool callbacks in order.
// This method uses the new structured callback interface.
// Unless execution stops on a callback error, non-nil Context,
// SkipSummarization (logical OR), and CustomResult (paired with
// SkipResultFormatter and SkipStateDelta) values are accumulated across
// callbacks, including on early response stops. Callback-owned result structs
// are never mutated and their top-level fields are copied into the returned
// result; referenced payloads are not deep-copied.
// If no AfterTool callbacks are registered, CustomResult is args.Result.
// If callbacks ran but none returned a CustomResult, CustomResult is nil and
// callers should keep using the original tool result.
func (c *Callbacks) RunAfterTool(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	var acc AfterToolResult
	var hasResult bool
	var firstErr error

	for _, cb := range c.AfterTool {
		result, err := c.runAfterToolCallback(ctx, cb, args)

		if c.handleCallbackError(err, &firstErr) {
			return result, err
		}

		if c.processAfterToolResult(result, &ctx, &acc, &hasResult) {
			out := acc
			if c.continueOnError && firstErr != nil {
				return &out, firstErr
			}
			return &out, nil
		}
	}

	return c.finalizeAfterToolResult(acc, hasResult, firstErr, args)
}
