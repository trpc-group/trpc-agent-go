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
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	callbackPanicErrFmt = "%s: %v"
	callbackPanicLogFmt = log.PanicPrefix + " %s (tool_call_id: %s, tool: %s): " +
		"%v\n%s"

	beforeToolCallbackPanic         = "before tool callback panic"
	afterToolCallbackPanic          = "after tool callback panic"
	afterToolResultFinalizerPanic   = "after tool result finalizer panic"
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
	// ModifiedArguments if not nil, will use these modified arguments.
	ModifiedArguments []byte
}

// BeforeToolCallbackStructured is called before a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be returned and tool execution will be skipped.
//   - Context: if not nil, will be used by the framework for subsequent operations.
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
	CustomResult any
	// SkipSummarization requests ending the turn after the tool response.
	SkipSummarization bool
}

// AfterToolResultFinalizer transforms the final CustomResult returned by one
// after-tool callback chain.
type AfterToolResultFinalizer func(
	ctx context.Context,
	args *AfterToolArgs,
	result *AfterToolResult,
) (*AfterToolResult, error)

const (
	maxAfterToolResultFinalizers     = 1024
	maxAfterToolResultFinalizerCalls = 1024
	afterToolResultFinalizerCallTTL  = time.Minute
)

type afterToolResultFinalizerEntry struct {
	token     uint64
	finalizer AfterToolResultFinalizer
}

type afterToolResultFinalizerCallKey struct {
	toolCallID  string
	declaration *Declaration
}

type afterToolResultFinalizerCallBinding struct {
	entry      afterToolResultFinalizerEntry
	generation uint64
	timer      *time.Timer
}

var afterToolResultFinalizers = struct {
	sync.Mutex
	byDeclaration map[*Declaration]afterToolResultFinalizerEntry
	byCall        map[afterToolResultFinalizerCallKey][]afterToolResultFinalizerCallBinding
	callsByToken  map[uint64]map[afterToolResultFinalizerCallKey]struct{}
	next          uint64
	nextCall      uint64
	callCount     int
}{
	byDeclaration: make(map[*Declaration]afterToolResultFinalizerEntry),
	byCall:        make(map[afterToolResultFinalizerCallKey][]afterToolResultFinalizerCallBinding),
	callsByToken:  make(map[uint64]map[afterToolResultFinalizerCallKey]struct{}),
}

// RegisterAfterToolResultFinalizer associates a finalizer with one tool
// declaration by pointer identity. Declarations with equal fields or names are
// distinct keys. The process-global registration set is concurrency-safe and
// bounded.
//
// Registration fails when declaration already has a finalizer or the registry
// is full. The returned cleanup removes the declaration and every pending call
// binding created through BindAfterToolResultFinalizer.
func RegisterAfterToolResultFinalizer(
	declaration *Declaration,
	finalizer AfterToolResultFinalizer,
) (cleanup func(), err error) {
	if declaration == nil || finalizer == nil {
		return nil, errors.New(
			"after-tool result finalizer declaration and function are required",
		)
	}
	afterToolResultFinalizers.Lock()
	defer afterToolResultFinalizers.Unlock()
	if _, exists :=
		afterToolResultFinalizers.byDeclaration[declaration]; exists {
		return nil, errors.New(
			"after-tool result finalizer is already registered for declaration",
		)
	}
	if len(afterToolResultFinalizers.byDeclaration) >=
		maxAfterToolResultFinalizers {
		return nil, errors.New(
			"after-tool result finalizer registry is full",
		)
	}
	afterToolResultFinalizers.next++
	entry := afterToolResultFinalizerEntry{
		token:     afterToolResultFinalizers.next,
		finalizer: finalizer,
	}
	afterToolResultFinalizers.byDeclaration[declaration] = entry
	afterToolResultFinalizers.callsByToken[entry.token] =
		make(map[afterToolResultFinalizerCallKey]struct{})
	return func() {
		afterToolResultFinalizers.Lock()
		defer afterToolResultFinalizers.Unlock()
		if current, exists :=
			afterToolResultFinalizers.byDeclaration[declaration]; exists && current.token == entry.token {
			delete(
				afterToolResultFinalizers.byDeclaration,
				declaration,
			)
		}
		for key := range afterToolResultFinalizers.callsByToken[entry.token] {
			removeAfterToolResultFinalizerCallLocked(
				key,
				entry.token,
			)
		}
		delete(afterToolResultFinalizers.callsByToken, entry.token)
	}, nil
}

type afterToolResultFinalizerContextKey struct{}

type afterToolResultFinalizerContextValue struct {
	toolCallID  string
	declaration *Declaration
	finalizer   AfterToolResultFinalizer
}

func afterToolResultFinalizerFor(
	ctx context.Context,
	args *AfterToolArgs,
) (AfterToolResultFinalizer, bool) {
	if args == nil || args.Declaration == nil {
		return nil, false
	}
	if carried, ok := ctx.Value(
		afterToolResultFinalizerContextKey{},
	).(afterToolResultFinalizerContextValue); ok &&
		carried.toolCallID == args.ToolCallID &&
		carried.declaration == args.Declaration &&
		carried.finalizer != nil {
		return carried.finalizer, true
	}
	afterToolResultFinalizers.Lock()
	defer afterToolResultFinalizers.Unlock()
	key := afterToolResultFinalizerCallKey{
		toolCallID:  args.ToolCallID,
		declaration: args.Declaration,
	}
	if bindings := afterToolResultFinalizers.byCall[key]; len(bindings) > 0 {
		index := completedAfterToolResultFinalizerCallBinding(
			bindings,
		)
		if index < 0 {
			return nil, false
		}
		binding := bindings[index]
		removeAfterToolResultFinalizerCallBindingLocked(
			key,
			binding.entry.token,
			binding.generation,
		)
		return binding.entry.finalizer, true
	}
	entry, ok :=
		afterToolResultFinalizers.byDeclaration[args.Declaration]
	return entry.finalizer, ok
}

// BindAfterToolResultFinalizer reserves source's finalizer for one tool call
// after all permission checks have allowed execution. The returned completion
// function must be called after that execution attempt finishes; it starts the
// expiry window for paths that do not run any after-tool callback. A retry may
// bind the same source, target, and tool call id again, which refreshes the
// reservation.
//
// The first after-tool callback chain consumes the binding and carries the
// finalizer in its returned context, so later plugin or local callback chains
// for the same call remain protected. Pending call bindings are process-global,
// concurrency-safe, and bounded. Capacity exhaustion fails closed rather than
// evicting a call whose callback may not have run yet.
func BindAfterToolResultFinalizer(
	source *Declaration,
	target *Declaration,
	toolCallID string,
) (complete func(), err error) {
	if source == nil || target == nil || toolCallID == "" {
		return nil, errors.New(
			"after-tool result finalizer declarations and tool call id are required",
		)
	}
	afterToolResultFinalizers.Lock()
	defer afterToolResultFinalizers.Unlock()
	entry, ok := afterToolResultFinalizers.byDeclaration[source]
	if !ok {
		return nil, errors.New(
			"source declaration has no after-tool result finalizer",
		)
	}
	key := afterToolResultFinalizerCallKey{
		toolCallID:  toolCallID,
		declaration: target,
	}
	bindings := afterToolResultFinalizers.byCall[key]
	if len(bindings) > 0 {
		if bindings[0].entry.token != entry.token {
			return nil, errors.New(
				"tool call has a different after-tool result finalizer",
			)
		}
	}
	if afterToolResultFinalizers.callCount >=
		maxAfterToolResultFinalizerCalls {
		return nil, errors.New(
			"after-tool result finalizer call registry is full",
		)
	}
	afterToolResultFinalizers.nextCall++
	generation := afterToolResultFinalizers.nextCall
	afterToolResultFinalizers.byCall[key] = append(
		bindings,
		afterToolResultFinalizerCallBinding{
			entry:      entry,
			generation: generation,
		},
	)
	afterToolResultFinalizers.callCount++
	afterToolResultFinalizers.callsByToken[entry.token][key] =
		struct{}{}
	return afterToolResultFinalizerCompletion(
		key,
		entry.token,
		generation,
	), nil
}

func afterToolResultFinalizerCompletion(
	key afterToolResultFinalizerCallKey,
	token uint64,
	generation uint64,
) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			afterToolResultFinalizers.Lock()
			defer afterToolResultFinalizers.Unlock()
			index := afterToolResultFinalizerCallBindingIndex(
				afterToolResultFinalizers.byCall[key],
				token,
				generation,
			)
			if index < 0 {
				return
			}
			bindings := afterToolResultFinalizers.byCall[key]
			bindings[index].timer = time.AfterFunc(
				afterToolResultFinalizerCallTTL,
				func() {
					afterToolResultFinalizers.Lock()
					defer afterToolResultFinalizers.Unlock()
					removeAfterToolResultFinalizerCallBindingLocked(
						key,
						token,
						generation,
					)
				},
			)
			afterToolResultFinalizers.byCall[key] = bindings
		})
	}
}

func removeAfterToolResultFinalizerCallLocked(
	key afterToolResultFinalizerCallKey,
	token uint64,
) {
	bindings := afterToolResultFinalizers.byCall[key]
	kept := bindings[:0]
	for _, binding := range bindings {
		if binding.entry.token != token {
			kept = append(kept, binding)
			continue
		}
		if binding.timer != nil {
			binding.timer.Stop()
		}
		afterToolResultFinalizers.callCount--
	}
	if len(kept) == 0 {
		delete(afterToolResultFinalizers.byCall, key)
		delete(afterToolResultFinalizers.callsByToken[token], key)
		return
	}
	afterToolResultFinalizers.byCall[key] = kept
}

func removeAfterToolResultFinalizerCallBindingLocked(
	key afterToolResultFinalizerCallKey,
	token uint64,
	generation uint64,
) {
	bindings := afterToolResultFinalizers.byCall[key]
	index := afterToolResultFinalizerCallBindingIndex(
		bindings,
		token,
		generation,
	)
	if index < 0 {
		return
	}
	if bindings[index].timer != nil {
		bindings[index].timer.Stop()
	}
	bindings = append(bindings[:index], bindings[index+1:]...)
	afterToolResultFinalizers.callCount--
	if len(bindings) == 0 {
		delete(afterToolResultFinalizers.byCall, key)
		delete(afterToolResultFinalizers.callsByToken[token], key)
		return
	}
	afterToolResultFinalizers.byCall[key] = bindings
}

func afterToolResultFinalizerCallBindingIndex(
	bindings []afterToolResultFinalizerCallBinding,
	token uint64,
	generation uint64,
) int {
	for i, binding := range bindings {
		if binding.entry.token == token &&
			binding.generation == generation {
			return i
		}
	}
	return -1
}

func completedAfterToolResultFinalizerCallBinding(
	bindings []afterToolResultFinalizerCallBinding,
) int {
	for i := len(bindings) - 1; i >= 0; i-- {
		if bindings[i].timer != nil {
			return i
		}
	}
	return -1
}

// AfterToolCallbackStructured is called after a tool is executed.
// Returns (result, error).
// - result: contains optional custom result and context for subsequent operations.
//   - CustomResult: if not nil, this result will be used instead of the actual tool result.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//   - SkipSummarization: if true, the framework will skip the extra
//     post-tool LLM summarization step.
//
// - error: if not nil, this error will be returned.
type AfterToolCallbackStructured = func(
	ctx context.Context,
	args *AfterToolArgs,
) (*AfterToolResult, error)

// ToolResultMessagesInput contains all parameters for generating messages from
// a tool result. An input is single-use and must not be shared across
// goroutines.
type ToolResultMessagesInput struct {
	// ToolName is the name of the tool.
	ToolName string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the final tool arguments in JSON bytes (after before-tool callbacks).
	Arguments []byte
	// Result is the final tool execution result after after-tool callbacks.
	// When the result implements GetCallbackResult() any, ToolResultMessages
	// receives that callback-facing projection and should type-assert against
	// the projected type rather than the raw result type.
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

// RunToolResultMessages runs the ToolResultMessages callback, if set, with
// panic recovery. When Result implements GetCallbackResult() any, the callback
// observes that projection. The function temporarily mutates and restores in,
// so the input is single-use and must not be shared across goroutines.
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
	restore := normalizeToolResultMessagesInput(in)
	defer restore()
	return c.ToolResultMessages(ctx, in)
}

func normalizeToolResultMessagesInput(
	in *ToolResultMessagesInput,
) func() {
	if in == nil {
		return func() {}
	}
	type callbackResultGetter interface {
		GetCallbackResult() any
	}
	getter, ok := in.Result.(callbackResultGetter)
	if !ok {
		return func() {}
	}
	original := in.Result
	in.Result = getter.GetCallbackResult()
	return func() {
		in.Result = original
	}
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

// processBeforeToolResult processes before tool callback result and updates context/arguments.
// Returns whether to stop execution immediately.
func (c *Callbacks) processBeforeToolResult(
	result *BeforeToolResult,
	ctx *context.Context,
	args *BeforeToolArgs,
	lastResult **BeforeToolResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if result.ModifiedArguments != nil {
		args.Arguments = result.ModifiedArguments
	}
	if result.CustomResult != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeBeforeToolResult determines the final return value for before tool callbacks.
func (c *Callbacks) finalizeBeforeToolResult(
	lastResult *BeforeToolResult,
	firstErr error,
) (*BeforeToolResult, error) {
	if lastResult != nil && lastResult.CustomResult != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResult == nil && lastResult.ModifiedArguments == nil {
		return nil, nil
	}
	return lastResult, nil
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
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunBeforeTool(
	ctx context.Context,
	args *BeforeToolArgs,
) (*BeforeToolResult, error) {
	var lastResult *BeforeToolResult
	var firstErr error

	for _, cb := range c.BeforeTool {
		result, err := c.runBeforeToolCallback(ctx, cb, args)

		if c.handleCallbackError(err, &firstErr) {
			return result, err
		}

		if c.processBeforeToolResult(result, &ctx, args, &lastResult) {
			if c.continueOnError && firstErr != nil {
				return result, firstErr
			}
			return result, nil
		}
	}

	return c.finalizeBeforeToolResult(lastResult, firstErr)
}

// processAfterToolResult processes after tool callback result and updates context.
// Returns whether to stop execution immediately.
func (c *Callbacks) processAfterToolResult(
	result *AfterToolResult,
	ctx *context.Context,
	lastResult **AfterToolResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if *lastResult != nil {
		merged := *result
		if merged.Context == nil {
			merged.Context = (*lastResult).Context
		}
		if merged.CustomResult == nil {
			merged.CustomResult = (*lastResult).CustomResult
		}
		merged.SkipSummarization = merged.SkipSummarization ||
			(*lastResult).SkipSummarization
		result = &merged
	}
	if result.CustomResult != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeAfterToolResult determines the final return value for after tool callbacks.
func (c *Callbacks) finalizeAfterToolResult(
	lastResult *AfterToolResult,
	firstErr error,
	args *AfterToolArgs,
) (*AfterToolResult, error) {
	if lastResult != nil && lastResult.CustomResult != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult == nil {
		if args.Result != nil {
			return &AfterToolResult{
				CustomResult: args.Result,
			}, nil
		}
		return &AfterToolResult{}, nil
	}
	return lastResult, nil
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

func (c *Callbacks) runAfterToolResultFinalizer(
	ctx context.Context,
	finalizer AfterToolResultFinalizer,
	args *AfterToolArgs,
	result *AfterToolResult,
) (finalized *AfterToolResult, err error) {
	toolCallID := ""
	toolName := ""
	if args != nil {
		toolCallID = args.ToolCallID
		toolName = args.ToolName
	}
	defer recoverToolCallbackPanic(
		ctx,
		afterToolResultFinalizerPanic,
		toolCallID,
		toolName,
		&err,
	)
	return finalizer(ctx, args, result)
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
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunAfterTool(
	ctx context.Context,
	args *AfterToolArgs,
) (result *AfterToolResult, err error) {
	var finalizer AfterToolResultFinalizer
	var hasFinalizer bool
	if args != nil {
		finalizer, hasFinalizer =
			afterToolResultFinalizerFor(ctx, args)
	}
	if hasFinalizer {
		defer func() {
			finalized, finalizerErr :=
				c.runAfterToolResultFinalizer(
					ctx,
					finalizer,
					args,
					result,
				)
			if finalized != nil {
				result = finalized
			} else if finalizerErr != nil && result != nil {
				safeResult := *result
				safeResult.CustomResult = nil
				result = &safeResult
			}
			if finalizerErr == nil {
				result = carryAfterToolResultFinalizer(
					ctx,
					args,
					result,
					finalizer,
				)
			}
			err = errors.Join(err, finalizerErr)
		}()
	}
	var lastResult *AfterToolResult
	var firstErr error

	for _, cb := range c.AfterTool {
		callbackResult, callbackErr :=
			c.runAfterToolCallback(ctx, cb, args)

		if c.handleCallbackError(callbackErr, &firstErr) {
			return callbackResult, callbackErr
		}

		if c.processAfterToolResult(
			callbackResult,
			&ctx,
			&lastResult,
		) {
			if c.continueOnError && firstErr != nil {
				return callbackResult, firstErr
			}
			return callbackResult, nil
		}
	}

	return c.finalizeAfterToolResult(lastResult, firstErr, args)
}

func carryAfterToolResultFinalizer(
	ctx context.Context,
	args *AfterToolArgs,
	result *AfterToolResult,
	finalizer AfterToolResultFinalizer,
) *AfterToolResult {
	if args == nil || args.Declaration == nil || finalizer == nil {
		return result
	}
	out := AfterToolResult{}
	if result != nil {
		out = *result
	}
	base := ctx
	if out.Context != nil {
		base = out.Context
	}
	out.Context = context.WithValue(
		base,
		afterToolResultFinalizerContextKey{},
		afterToolResultFinalizerContextValue{
			toolCallID:  args.ToolCallID,
			declaration: args.Declaration,
			finalizer:   finalizer,
		},
	)
	return &out
}
