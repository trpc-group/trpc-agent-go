//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolretry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

const (
	// ErrorToolNotFound is the error message for tool not found.
	ErrorToolNotFound = "Error: tool not found"
	// ErrorCallableToolExecution is the error message for callable tool execution failed.
	ErrorCallableToolExecution = "Error: callable tool execution failed"
	// ErrorStreamableToolExecution is the error message for streamable tool execution failed.
	ErrorStreamableToolExecution = "Error: streamable tool execution failed"
	// ErrorMarshalResult is the error message for failed to marshal result.
	ErrorMarshalResult = "Error: failed to marshal result"
)

// funcRespCompletionTimeout is the default wait duration for ensuring a
// tool.response event has been processed by the session persistence layer.
const funcRespCompletionTimeout = 5 * time.Second

// summarizationSkipper is implemented by tools that can indicate whether
// the flow should skip a post-tool summarization step. This allows tools
// like AgentTool to mark their tool.response as final for the turn.
type summarizationSkipper interface {
	SkipSummarization() bool
}

// streamInnerPreference is implemented by tools that want to control whether
// the flow should treat them as streamable (forwarding inner deltas) or fall
// back to the callable path. When this returns false, the flow will not use
// the StreamableTool path even if the tool implements it.
type streamInnerPreference interface {
	StreamInner() bool
}

// toolResult holds the result of a single tool execution.
type toolResult struct {
	index int
	event *event.Event
	err   error
}

// Default message used when transferring to a sub-agent without an explicit message.
// Users can override or disable it via SetDefaultTransferMessage.
var defaultTransferMessage = "Task delegated from coordinator"

// SetDefaultTransferMessage configures the message to inject when a sub-agent is
// called without an explicit message (model directly calls the sub-agent name).
func SetDefaultTransferMessage(message string) {
	defaultTransferMessage = message
}

// subAgentCall defines the input format for direct sub-agent tool calls.
// This handles cases where models call sub-agent names directly instead of using transfer_to_agent.
type subAgentCall struct {
	Message string `json:"message,omitempty"`
}

// FunctionCallResponseProcessor handles agent transfer operations after LLM responses.
type FunctionCallResponseProcessor struct {
	enableParallelTools bool
	toolCallbacks       *tool.Callbacks
	toolRetryPolicy     *tool.RetryPolicy
}

// FunctionCallResponseProcessorOption configures a function-call response processor.
type FunctionCallResponseProcessorOption func(*FunctionCallResponseProcessor)

// WithToolCallRetryPolicy sets the retry policy used for single callable tool invocations.
func WithToolCallRetryPolicy(policy *tool.RetryPolicy) FunctionCallResponseProcessorOption {
	return func(p *FunctionCallResponseProcessor) {
		if policy == nil {
			p.toolRetryPolicy = nil
			return
		}
		p.toolRetryPolicy = policy
	}
}

// NewFunctionCallResponseProcessor creates a new transfer response processor.
func NewFunctionCallResponseProcessor(
	enableParallelTools bool,
	toolCallbacks *tool.Callbacks,
	opts ...FunctionCallResponseProcessorOption,
) *FunctionCallResponseProcessor {
	processor := &FunctionCallResponseProcessor{
		enableParallelTools: enableParallelTools,
		toolCallbacks:       toolCallbacks,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(processor)
		}
	}
	return processor
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It checks for transfer requests and handles agent handoffs by actually calling
// the target agent's Run method.
func (p *FunctionCallResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || rsp.IsPartial || !rsp.IsToolCallResponse() {
		return
	}

	// Enforce optional per-invocation tool iteration limit. A "tool iteration"
	// is defined as an assistant response that contains tool calls and reaches
	// this processor. When the limit is not configured (<= 0), this check is a
	// no-op and preserves existing behavior.
	if invocation.IncToolIteration() {
		// Mark the invocation as ended so the flow will not issue another LLM call.
		invocation.EndInvocation = true

		// Emit an error response event describing the limit breach instead of
		// executing any tools. This makes the termination visible to callers
		// while avoiding additional model or tool invocations.
		resp := &model.Response{
			Object: model.ObjectTypeError,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: fmt.Sprintf("max tool iterations (%d) exceeded", invocation.MaxToolIterations),
			},
			Done: true,
		}
		agent.EmitEvent(ctx, invocation, ch, event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			resp,
		))
		return
	}

	deferred, executable, unknown := p.toolExecutionDecision(
		ctx,
		invocation,
		req,
		rsp,
	)
	if deferred && !executable && !unknown {
		invocation.EndInvocation = true
		return
	}

	functioncallResponseEvent, err := p.handleFunctionCallsAndSendEvent(ctx, invocation, rsp, req.Tools, ch)

	// Option one: set invocation.EndInvocation is true, and stop next step.
	// Option two: emit error event, maybe the LLM can correct this error and also need to wait for notice completion.
	// maybe the Option two is better.
	// Allow users to intervene in error handling through callbacks.
	if _, ok := agent.AsStopError(err); ok {
		invocation.EndInvocation = true
		return
	}

	if deferred && !unknown {
		invocation.EndInvocation = true
	}

	if err != nil || functioncallResponseEvent == nil {
		return
	}

	if invocation.EndInvocation {
		return
	}

	// If the tool indicates skipping outer summarization, mark the invocation to end
	// after this tool response so the flow does not perform an extra LLM call.
	if functioncallResponseEvent.Actions != nil && functioncallResponseEvent.Actions.SkipSummarization {
		invocation.EndInvocation = true
		return
	}
}

func (p *FunctionCallResponseProcessor) toolExecutionDecision(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
) (deferred bool, executable bool, unknown bool) {
	if invocation == nil {
		return false, true, false
	}
	filter := invocation.RunOptions.ToolExecutionFilter
	if filter == nil {
		return false, true, false
	}
	if req == nil || req.Tools == nil || rsp == nil {
		return false, true, false
	}
	if len(rsp.Choices) == 0 {
		return false, true, false
	}
	for _, tc := range rsp.Choices[0].Message.ToolCalls {
		tl, ok := req.Tools[tc.Function.Name]
		if !ok {
			unknown = true
			continue
		}
		if filter(ctx, tl) {
			executable = true
			continue
		}
		deferred = true
	}
	return deferred, executable, unknown
}

func (p *FunctionCallResponseProcessor) handleFunctionCallsAndSendEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	functionResponseEvent, err := p.handleFunctionCalls(
		ctx,
		invocation,
		llmResponse,
		tools,
		eventChan,
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Function call handling failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return nil, err
	}

	if functionResponseEvent == nil {
		return nil, nil
	}

	functionResponseEvent.RequiresCompletion = true
	agent.EmitEvent(ctx, invocation, eventChan, functionResponseEvent)

	if !appender.IsAttached(invocation) {
		return functionResponseEvent, nil
	}

	completionID :=
		agent.GetAppendEventNoticeKey(functionResponseEvent.ID)
	timeout := funcRespWaitTimeout(ctx)
	err = invocation.AddNoticeChannelAndWait(ctx, completionID, timeout)
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if err != nil {
		log.WarnfContext(
			ctx,
			"Wait for tool response persistence failed: %v",
			err,
		)
	}
	return functionResponseEvent, nil
}

func funcRespWaitTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return funcRespCompletionTimeout
}

// handleFunctionCalls executes tool calls and returns a merged response event.
func (p *FunctionCallResponseProcessor) handleFunctionCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	toolCalls := llmResponse.Choices[0].Message.ToolCalls

	// If parallel tools are enabled AND multiple tool calls, execute concurrently
	if p.enableParallelTools && len(toolCalls) > 1 {
		return p.executeToolCallsInParallel(ctx, invocation, llmResponse, toolCalls, tools, eventChan)
	}

	var toolCallResponsesEvents []*event.Event
	for i, tc := range toolCalls {
		toolEvent, err := p.executeSingleToolCallSequential(
			ctx, invocation, llmResponse, tools, eventChan, i, tc,
		)
		if err != nil {
			return nil, err
		}
		if toolEvent != nil {
			toolCallResponsesEvents = append(toolCallResponsesEvents, toolEvent)
		}
	}

	if len(toolCallResponsesEvents) == 0 &&
		invocation != nil &&
		invocation.RunOptions.ToolExecutionFilter != nil {
		filter := invocation.RunOptions.ToolExecutionFilter
		for _, tc := range toolCalls {
			tl, ok := tools[tc.Function.Name]
			if ok && !filter(ctx, tl) {
				return nil, nil
			}
		}
	}

	mergedEvent := p.buildMergedParallelEvent(
		ctx, invocation, llmResponse, tools, toolCalls, toolCallResponsesEvents,
	)
	return mergedEvent, nil
}

// executeSingleToolCallSequential runs one tool call and returns its event.
func (p *FunctionCallResponseProcessor) executeSingleToolCallSequential(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
	index int,
	toolCall model.ToolCall,
) (*event.Event, error) {
	ctx, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewExecuteToolSpanName(toolCall.Function.Name))
	if startedSpan {
		defer span.End()
	}
	startTime := time.Now()
	ctx, choices, modifiedArgs, shouldIgnoreError, skipSummarization, err :=
		p.executeToolCall(
			ctx,
			invocation,
			toolCall,
			tools,
			index,
			eventChan,
		)
	if err != nil {
		if shouldIgnoreError {
			// Create error choice for ignorable errors
			choice := p.createErrorChoice(index, toolCall.ID, err.Error())
			choices = []model.Choice{*choice}
		} else {
			// Return critical errors (e.g., stop errors) immediately
			return nil, err
		}
	}
	toolEvent := p.buildToolCallResponseEvent(
		invocation,
		llmResponse,
		choices,
		tools,
		toolCall,
		index,
		skipSummarization,
	)
	if toolEvent == nil {
		return nil, nil
	}
	decl := p.lookupDeclaration(tools, toolCall.Function.Name)

	var (
		sess      = &session.Session{}
		modelName string
		agentName string
	)
	// Attach state delta if the tool provides it.
	if err == nil && len(choices) > 0 &&
		!hasSyntheticStateOnlyToolChoice(ctx) {
		if tl, ok := tools[toolCall.Function.Name]; ok {
			// Use the first choice as the canonical tool result for state
			// delta.
			p.attachStateDelta(
				invocation,
				tl,
				modifiedArgs,
				&choices[0],
				toolEvent,
			)
		}
	}

	if invocation != nil {
		if invocation.Session != nil {
			sess = invocation.Session
		}
		if invocation.Model != nil {
			modelName = invocation.Model.Info().Name
		}
		if invocation.AgentName != "" {
			agentName = invocation.AgentName
		}
	}

	if startedSpan {
		itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolEvent, err)
	}
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: modelName,
		ToolName:         toolCall.Function.Name,
		AppName:          sess.AppName,
		UserID:           sess.UserID,
		SessionID:        sess.ID,
		AgentName:        agentName,
		Error:            err,
	}, time.Since(startTime))
	return toolEvent, nil
}

// executeToolCallsInParallel runs multiple tool calls concurrently and merges
// their results into a single event.
func (p *FunctionCallResponseProcessor) executeToolCallsInParallel(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	toolCalls []model.ToolCall,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	resultChan := make(chan toolResult, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Add(1)
		runCtx := agent.CloneContext(ctx)
		go p.runParallelToolCall(
			runCtx, &wg, invocation, llmResponse, tools, eventChan, resultChan, i, tc,
		)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultChan)
		close(done)
	}()

	toolCallResponsesEvents, err := p.collectParallelToolResults(
		ctx, resultChan, len(toolCalls),
	)
	if len(toolCallResponsesEvents) == 0 &&
		invocation != nil &&
		invocation.RunOptions.ToolExecutionFilter != nil {
		filter := invocation.RunOptions.ToolExecutionFilter
		for _, tc := range toolCalls {
			tl, ok := tools[tc.Function.Name]
			if ok && !filter(ctx, tl) {
				return nil, nil
			}
		}
	}
	mergedEvent := p.buildMergedParallelEvent(
		ctx, invocation, llmResponse, tools, toolCalls, toolCallResponsesEvents,
	)
	return mergedEvent, err
}

// runParallelToolCall executes one tool call and reports the result.
func (p *FunctionCallResponseProcessor) runParallelToolCall(
	ctx context.Context,
	wg *sync.WaitGroup,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
	resultChan chan<- toolResult,
	index int,
	tc model.ToolCall,
) {
	defer wg.Done()
	// Recover from panics to avoid breaking sibling goroutines.
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				ctx,
				"Tool execution panic for %s (index: %d, ID: %s, agent: %s): %v",
				tc.Function.Name,
				index,
				tc.ID,
				invocation.AgentName,
				r,
			)
			errorChoice := p.createErrorChoice(
				index, tc.ID, fmt.Sprintf("tool execution panic: %v", r),
			)
			errorChoice.Message.ToolName = tc.Function.Name
			errorEvent := newToolCallResponseEvent(
				invocation, llmResponse, []model.Choice{*errorChoice},
			)
			if tc.Function.Name == transfer.TransferToolName {
				errorEvent.Tag = event.TransferTag
			}
			p.sendToolResult(ctx, resultChan, toolResult{index: index, event: errorEvent})
		}
	}()
	// Trace the tool execution for observability.
	ctx, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewExecuteToolSpanName(tc.Function.Name))
	if startedSpan {
		defer span.End()
	}
	startTime := time.Now()
	// Execute the tool (streamable or callable) with callbacks.
	ctx, choices, modifiedArgs, shouldIgnoreError, skipSummarization, err :=
		p.executeToolCall(
			ctx,
			invocation,
			tc,
			tools,
			index,
			eventChan,
		)
	// Handle errors based on whether they are ignorable or critical.
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
			tc.Function.Name,
			index,
			tc.ID,
			invocation.AgentName,
			err,
		)
		errorChoice := p.createErrorChoice(
			index, tc.ID, fmt.Sprintf("tool execution error: %v", err),
		)
		errorChoice.Message.ToolName = tc.Function.Name
		errorEvent := newToolCallResponseEvent(
			invocation, llmResponse, []model.Choice{*errorChoice},
		)
		errorEvent = p.decorateToolCallResponseEvent(
			errorEvent,
			tools,
			tc,
			skipSummarization,
		)
		// Only propagate the error if it's not ignorable (e.g., stop errors)
		var returnErr error
		if !shouldIgnoreError {
			returnErr = err
		}
		p.sendToolResult(ctx, resultChan, toolResult{index: index, event: errorEvent, err: returnErr})
		return
	}

	// No error and at least one choice means we have tool result messages.
	toolCallResponseEvent := p.buildToolCallResponseEvent(
		invocation,
		llmResponse,
		choices,
		tools,
		tc,
		index,
		skipSummarization,
	)
	if toolCallResponseEvent == nil {
		p.sendToolResult(ctx, resultChan, toolResult{index: index})
		return
	}
	// Include declaration for telemetry even when tool is missing.
	decl := p.lookupDeclaration(tools, tc.Function.Name)

	var (
		sess      = &session.Session{}
		modelName string
		agentName string
	)
	if invocation != nil {
		if invocation.Session != nil {
			sess = invocation.Session
		}
		if invocation.Model != nil {
			modelName = invocation.Model.Info().Name
		}
		if invocation.AgentName != "" {
			agentName = invocation.AgentName
		}
	}
	// Attach state delta if the tool provides it.
	if len(choices) > 0 && !hasSyntheticStateOnlyToolChoice(ctx) {
		if tl, ok := tools[tc.Function.Name]; ok {
			// Use the first choice as the canonical tool result for state delta.
			p.attachStateDelta(
				invocation,
				tl,
				modifiedArgs,
				&choices[0],
				toolCallResponseEvent,
			)
		}
	}
	if startedSpan {
		itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolCallResponseEvent, err)
	}
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: modelName,
		ToolName:         tc.Function.Name,
		AppName:          sess.AppName,
		UserID:           sess.UserID,
		SessionID:        sess.ID,
		AgentName:        agentName,
		Error:            err,
	}, time.Since(startTime))
	// Send result back to aggregator.
	p.sendToolResult(
		ctx, resultChan, toolResult{index: index, event: toolCallResponseEvent},
	)
}

func (p *FunctionCallResponseProcessor) buildToolCallResponseEvent(
	invocation *agent.Invocation,
	llmResponse *model.Response,
	choices []model.Choice,
	tools map[string]tool.Tool,
	toolCall model.ToolCall,
	index int,
	skipSummarization bool,
) *event.Event {
	if len(choices) == 0 {
		if !skipSummarization {
			return nil
		}
		return p.decorateToolCallResponseEvent(
			newMinimalToolCallResponseEvent(
				invocation,
				llmResponse,
				toolCall,
				index,
			),
			tools,
			toolCall,
			skipSummarization,
		)
	}
	annotateToolChoicesWithName(choices, toolCall.Function.Name)
	return p.decorateToolCallResponseEvent(
		newToolCallResponseEvent(invocation, llmResponse, choices),
		tools,
		toolCall,
		skipSummarization,
	)
}

func annotateToolChoicesWithName(choices []model.Choice, toolName string) {
	for i := range choices {
		if choices[i].Message.Role == model.RoleTool &&
			choices[i].Message.ToolName == "" {
			choices[i].Message.ToolName = toolName
		}
	}
}

func (p *FunctionCallResponseProcessor) decorateToolCallResponseEvent(
	ev *event.Event,
	tools map[string]tool.Tool,
	toolCall model.ToolCall,
	skipSummarization bool,
) *event.Event {
	if ev == nil {
		return nil
	}
	if toolCall.Function.Name == transfer.TransferToolName {
		ev.Tag = event.TransferTag
	}
	if tl, ok := tools[toolCall.Function.Name]; ok {
		p.annotateSkipSummarization(ev, tl, skipSummarization)
	} else if skipSummarization {
		markSkipSummarization(ev)
	}
	return ev
}

// attachStateDelta copies tool-provided state delta to the event.
func (p *FunctionCallResponseProcessor) attachStateDelta(
	inv *agent.Invocation,
	tl tool.Tool,
	args []byte,
	choice *model.Choice,
	ev *event.Event,
) {
	if tl == nil || choice == nil || ev == nil {
		return
	}
	original := tl
	if nameTool, ok := tl.(*itool.NamedTool); ok {
		original = nameTool.Original()
	}
	b := []byte(choice.Message.Content)
	toolCallID := choice.Message.ToolID

	type stateDeltaProvider interface {
		StateDelta(string, []byte, []byte) map[string][]byte
	}
	type invocationStateDeltaProvider interface {
		StateDeltaForInvocation(
			*agent.Invocation,
			string,
			[]byte,
			[]byte,
		) map[string][]byte
	}

	var delta map[string][]byte
	if isdp, ok := original.(invocationStateDeltaProvider); ok {
		delta = isdp.StateDeltaForInvocation(inv, toolCallID, args, b)
	} else if sdp, ok := original.(stateDeltaProvider); ok {
		delta = sdp.StateDelta(toolCallID, args, b)
	}
	if len(delta) == 0 {
		return
	}
	if ev.StateDelta == nil {
		ev.StateDelta = map[string][]byte{}
	}
	for k, v := range delta {
		ev.StateDelta[k] = v
	}
}

// annotateSkipSummarization marks an event to skip outer summarization.
func (p *FunctionCallResponseProcessor) annotateSkipSummarization(
	ev *event.Event,
	tl tool.Tool,
	dynamic bool,
) {
	if dynamic || toolPrefersSkipSummarization(tl) {
		markSkipSummarization(ev)
	}
}

// lookupDeclaration returns a declaration or a safe placeholder.
func (p *FunctionCallResponseProcessor) lookupDeclaration(
	tools map[string]tool.Tool, name string,
) *tool.Declaration {
	if tl, ok := tools[name]; ok {
		return tl.Declaration()
	}
	return &tool.Declaration{Name: "<not found>", Description: "<not found>"}
}

// sendToolResult sends without blocking when the context is cancelled.
func (p *FunctionCallResponseProcessor) sendToolResult(
	ctx context.Context, ch chan<- toolResult, res toolResult,
) {
	select {
	case ch <- res:
	case <-ctx.Done():
	}
}

// buildMergedParallelEvent merges child tool events or builds minimal choices.
func (p *FunctionCallResponseProcessor) buildMergedParallelEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	toolCalls []model.ToolCall,
	toolCallEvents []*event.Event,
) *event.Event {
	var mergedEvent *event.Event
	if len(toolCallEvents) == 0 {
		minimal := make([]model.Choice, 0, len(toolCalls))
		for i, tc := range toolCalls {
			minimal = append(minimal, newMinimalToolChoice(tc, i))
		}
		mergedEvent = newToolCallResponseEvent(invocation, llmResponse, minimal)
		for _, tc := range toolCalls {
			if tl, ok := tools[tc.Function.Name]; ok &&
				toolPrefersSkipSummarization(tl) {
				markSkipSummarization(mergedEvent)
				break
			}
		}
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallEvents)
	}
	if len(toolCallEvents) > 1 {
		_, span, startedSpan := itrace.StartSpan(
			ctx,
			invocation,
			itelemetry.NewExecuteToolSpanName(itelemetry.ToolNameMergedTools),
		)
		if startedSpan {
			itelemetry.TraceMergedToolCalls(span, mergedEvent)
			span.End()
		}
	}
	return mergedEvent
}

// executeToolCall executes a single tool call and returns the choice.
// Parameters:
//   - ctx: context for cancellation and tracing
//   - invocation: agent invocation context containing agent name, model info, etc.
//   - toolCall: the tool call to execute, including function name and arguments
//   - tools: map of available tools by name
//   - index: index of this tool call in the batch (for error reporting)
//   - eventChan: channel for emitting events during execution
//
// Returns:
//   - context.Context: updated context from callbacks (if any)
//   - []model.Choice: tool response choices (nil if no response is emitted)
//   - []byte: the modified arguments after before-tool callbacks (for telemetry)
//   - bool: shouldIgnoreError - true if the error is ignorable (e.g., tool not found, marshal error), false for critical errors (e.g., stop errors)
//   - bool: skipSummarization - true if callbacks requested ending the turn
//     after the tool response
//   - error: any error that occurred during execution (no longer swallowed)
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
	eventChan chan<- *event.Event,
) (context.Context, []model.Choice, []byte, bool, bool, error) {
	toolCall, tl, shouldIgnoreError, err := p.resolveToolCallTarget(ctx, invocation, toolCall, tools)
	if err != nil || tl == nil {
		return ctx, nil, toolCall.Function.Arguments, shouldIgnoreError,
			false, err
	}

	log.DebugfContext(
		ctx,
		"Executing tool %s with args: %s",
		toolCall.Function.Name,
		string(toolCall.Function.Arguments),
	)

	// Execute the tool with callbacks.
	ctx, result, modifiedArgs, suppressDefaultToolMessage,
		skipSummarization, err := p.executeToolWithCallbacks(
		ctx,
		invocation,
		toolCall,
		tl,
		eventChan,
	)
	// Only return error when it's a stop error
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return ctx, nil, modifiedArgs, false, skipSummarization, err
		}
		return ctx, nil, modifiedArgs, true, skipSummarization, err
	}
	//  allow to return nil not provide function response.
	if r, ok := tl.(function.LongRunner); ok && r.LongRunning() {
		if result == nil {
			return ctx, nil, modifiedArgs, true, skipSummarization, nil
		}
	}
	if suppressDefaultToolMessage {
		defaultMsg, err := buildDefaultToolMessage(toolCall.ID, result)
		if err != nil {
			log.WarnfContext(
				ctx,
				"Failed to marshal tool result for %s: %v",
				toolCall.Function.Name,
				err,
			)
			return ctx, nil, modifiedArgs, true, skipSummarization,
				fmt.Errorf("%s: %w", ErrorMarshalResult, err)
		}
		defaultChoices := []model.Choice{
			{Index: index, Message: defaultMsg},
		}
		ctx = markSyntheticStateOnlyToolChoice(ctx)
		if p.toolCallbacks == nil || p.toolCallbacks.ToolResultMessages == nil {
			return ctx, defaultChoices, modifiedArgs, true,
				skipSummarization, nil
		}
		customChoices, overridden, cbErr := p.applyToolResultMessagesCallback(
			ctx,
			toolCall,
			tl,
			result,
			modifiedArgs,
			index,
			defaultMsg,
		)
		if cbErr != nil {
			return ctx, nil, modifiedArgs, true, skipSummarization, cbErr
		}
		if overridden {
			return ctx, customChoices, modifiedArgs, true,
				skipSummarization, nil
		}
		return ctx, defaultChoices, modifiedArgs, true,
			skipSummarization, nil
	}

	defaultMsg, err := buildDefaultToolMessage(toolCall.ID, result)
	if err != nil {
		// Marshal failures (for example, NaN in floats) do not
		// affect the overall flow. Downgrade to warning to avoid
		// noisy alerts while still surfacing the issue.
		log.WarnfContext(
			ctx,
			"Failed to marshal tool result for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, nil, modifiedArgs, true, skipSummarization,
			fmt.Errorf("%s: %w", ErrorMarshalResult, err)
	}

	choices := []model.Choice{
		{Index: index, Message: defaultMsg},
	}

	if p.toolCallbacks != nil &&
		p.toolCallbacks.ToolResultMessages != nil {
		customChoices, overridden, cbErr :=
			p.applyToolResultMessagesCallback(
				ctx,
				toolCall,
				tl,
				result,
				modifiedArgs,
				index,
				defaultMsg,
			)
		if cbErr != nil {
			return ctx, nil, modifiedArgs, true, skipSummarization, cbErr
		}
		if overridden {
			choices = customChoices
		}
	}

	log.DebugfContext(
		ctx,
		"CallableTool %s executed successfully, result: %s",
		toolCall.Function.Name,
		defaultMsg.Content,
	)

	return ctx, choices, modifiedArgs, true, skipSummarization, nil
}

// resolveToolCallTarget resolves the callable tool, applies compatibility remapping,
// and evaluates the optional tool execution filter.
func (p *FunctionCallResponseProcessor) resolveToolCallTarget(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
) (model.ToolCall, tool.Tool, bool, error) {
	tl, exists := tools[toolCall.Function.Name]
	if !exists {
		// Compatibility: map sub-agent name calls to transfer_to_agent if present.
		if mapped := findCompatibleTool(toolCall.Function.Name, tools, invocation); mapped != nil {
			tl = mapped
			if newArgs := convertToolArguments(
				toolCall.Function.Name, toolCall.Function.Arguments,
				mapped.Declaration().Name,
			); newArgs != nil {
				toolCall.Function.Name = mapped.Declaration().Name
				toolCall.Function.Arguments = newArgs
			}
		} else {
			log.ErrorfContext(
				ctx,
				"CallableTool %s not found (agent=%s, model=%s)",
				toolCall.Function.Name,
				invocation.AgentName,
				invocation.Model.Info().Name,
			)
			return toolCall, nil, true, fmt.Errorf("executeToolCall: %s", ErrorToolNotFound)
		}
	}
	if invocation != nil && invocation.RunOptions.ToolExecutionFilter != nil {
		if !invocation.RunOptions.ToolExecutionFilter(ctx, tl) {
			return toolCall, nil, true, nil
		}
	}
	return toolCall, tl, false, nil
}

// applyToolResultMessagesCallback invokes the optional ToolResultMessages callback and
// converts its return value into choices. It returns:
//   - customChoices: the choices derived from callback output
//   - overridden: whether the default tool message should be replaced
//   - err: non-nil when the callback itself fails
func (p *FunctionCallResponseProcessor) applyToolResultMessagesCallback(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.Tool,
	result any,
	modifiedArgs []byte,
	index int,
	defaultMsg model.Message,
) ([]model.Choice, bool, error) {
	raw, cbErr := p.toolCallbacks.RunToolResultMessages(
		ctx,
		&tool.ToolResultMessagesInput{
			ToolName:           toolCall.Function.Name,
			Declaration:        tl.Declaration(),
			Arguments:          modifiedArgs,
			Result:             result,
			ToolCallID:         toolCall.ID,
			DefaultToolMessage: defaultMsg,
		},
	)
	if cbErr != nil {
		log.Errorf("ToolResultMessages callback failed for %s: %v", toolCall.Function.Name, cbErr)
		return nil, false, fmt.Errorf("tool callback error: %w", cbErr)
	}

	var msgs []model.Message
	switch v := raw.(type) {
	case nil:
		// No override.
	case model.Message:
		msgs = []model.Message{v}
	case []model.Message:
		msgs = v
	default:
		log.Warnf("ToolResultMessages callback for %s returned unsupported type %T; expected model.Message or []model.Message", toolCall.Function.Name, v)
	}

	if len(msgs) == 0 {
		return nil, false, nil
	}

	customChoices := make([]model.Choice, 0, len(msgs))
	for _, msg := range msgs {
		customChoices = append(customChoices, model.Choice{
			Index:   index,
			Message: msg,
		})
	}
	// When a callback is provided and returns non-empty messages,
	// the framework defers entirely to the callback for correctness.
	return customChoices, true, nil
}

// createErrorChoice creates an error choice for tool execution failures.
func (p *FunctionCallResponseProcessor) createErrorChoice(index int, toolID string,
	errorMsg string) *model.Choice {
	return &model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: errorMsg,
			ToolID:  toolID,
		},
	}
}

// collectParallelToolResults drains resultChan and preserves order by index.
// It returns only non-nil events.
func (p *FunctionCallResponseProcessor) collectParallelToolResults(
	ctx context.Context,
	resultChan <-chan toolResult,
	toolCallsCount int,
) ([]*event.Event, error) {
	results := make([]*event.Event, toolCallsCount)
	var err error
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				// Channel closed, all results received.
				return p.filterNilEvents(results), err
			}
			if result.index >= 0 && result.index < len(results) {
				results[result.index] = result.event
				if err == nil && result.err != nil {
					err = result.err
				}
			} else {
				log.ErrorfContext(
					ctx,
					"Tool result index %d out of range [0, %d)",
					result.index,
					len(results),
				)
			}
		case <-ctx.Done():
			// Context cancelled, return what we have.
			log.WarnfContext(
				ctx,
				"Context cancelled while waiting for tool results",
			)
			return p.filterNilEvents(results), nil
		}
	}
}

func (p *FunctionCallResponseProcessor) runBeforeToolPluginCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
) (context.Context, model.ToolCall, any, error) {
	if invocation == nil || invocation.Plugins == nil {
		return ctx, toolCall, nil, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, toolCall, nil, nil
	}

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
	}
	result, err := callbacks.RunBeforeTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Before tool plugin failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolCall, nil,
			fmt.Errorf("tool callback error: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResult != nil {
		return ctx, toolCall, result.CustomResult, nil
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	return ctx, toolCall, nil, nil
}

func (p *FunctionCallResponseProcessor) runBeforeToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
) (context.Context, model.ToolCall, any, error) {
	if p.toolCallbacks == nil {
		return ctx, toolCall, nil, nil
	}

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
	}
	result, err := p.toolCallbacks.RunBeforeTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Before tool callback failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolCall, nil,
			fmt.Errorf("tool callback error: %w", err)
	}

	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResult != nil {
		return ctx, toolCall, result.CustomResult, nil
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	return ctx, toolCall, nil, nil
}

func (p *FunctionCallResponseProcessor) runAfterToolPluginCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
	toolResult any,
	toolErr error,
) (context.Context, any, bool, bool, error) {
	if invocation == nil || invocation.Plugins == nil {
		return ctx, toolResult, false, false, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, toolResult, false, false, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
		Result:      toolResult,
		Error:       toolErr,
		Meta:        extractMetaFromResult(toolResult),
	}
	afterResult, err := callbacks.RunAfterTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"After tool plugin failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolResult, false, false,
			fmt.Errorf("tool callback error: %w", err)
	}
	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	skipSummarization := afterResult != nil &&
		afterResult.SkipSummarization
	if afterResult != nil && afterResult.CustomResult != nil {
		return ctx, afterResult.CustomResult, true, skipSummarization, nil
	}
	return ctx, toolResult, false, skipSummarization, nil
}

func (p *FunctionCallResponseProcessor) runAfterToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
	toolResult any,
	toolErr error,
) (context.Context, any, bool, error) {
	if p.toolCallbacks == nil {
		return ctx, toolResult, false, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
		Result:      toolResult,
		Error:       toolErr,
		Meta:        extractMetaFromResult(toolResult),
	}
	afterResult, err := p.toolCallbacks.RunAfterTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"After tool callback failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolResult, false,
			fmt.Errorf("tool callback error: %w", err)
	}

	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	skipSummarization := afterResult != nil &&
		afterResult.SkipSummarization
	if afterResult != nil && afterResult.CustomResult != nil {
		toolResult = afterResult.CustomResult
	}
	return ctx, toolResult, skipSummarization, nil
}

// extractMetaFromResult extracts metadata from tool result.
// For MCP mcpToolResult, returns the Meta field.
func extractMetaFromResult(result any) map[string]any {
	if result == nil {
		return nil
	}
	// Check for our wrapped mcpToolResult type (from tool/mcp package)
	type metaGetter interface {
		GetMeta() map[string]any
	}
	if mg, ok := result.(metaGetter); ok {
		return mg.GetMeta()
	}
	return nil
}

// executeToolWithCallbacks executes a tool with before/after callbacks.
// Returns (context, result, modifiedArguments, suppressDefaultToolMessage,
// skipSummarization, error).
func (p *FunctionCallResponseProcessor) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (context.Context, any, []byte, bool, bool, error) {
	// Inject tool call ID into context for callbacks to use.
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, toolCall.ID)
	// Repair tool call arguments in place when needed.
	if jsonrepair.IsToolCallArgumentsJSONRepairEnabled(invocation) {
		jsonrepair.RepairToolCallArgumentsInPlace(ctx, &toolCall)
	}
	toolDeclaration := tl.Declaration()
	ctx, toolCall, customResult, err := p.runBeforeToolPluginCallbacks(
		ctx,
		invocation,
		toolCall,
		toolDeclaration,
	)
	if err != nil {
		return ctx, nil, toolCall.Function.Arguments, false, false, err
	}
	if customResult != nil {
		return ctx, customResult, toolCall.Function.Arguments, false,
			false, nil
	}
	ctx, toolCall, customResult, err = p.runBeforeToolCallbacks(
		ctx,
		toolCall,
		toolDeclaration,
	)
	if err != nil {
		return ctx, nil, toolCall.Function.Arguments, false, false, err
	}
	if customResult != nil {
		return ctx, customResult, toolCall.Function.Arguments, false,
			false, nil
	}
	// Execute the actual tool.
	ctx, toolResult, suppressDefaultToolMessage, toolErr := p.executeTool(
		ctx,
		invocation,
		toolCall,
		tl,
		eventChan,
	)
	if toolErr != nil {
		log.WarnfContext(
			ctx,
			"tool execute failed, function name: %v, arguments: %s, "+
				"result: %v, err: %v",
			toolCall.Function.Name,
			string(toolCall.Function.Arguments),
			toolResult,
			toolErr,
		)
	}
	ctx, toolResult, pluginOverride, skipSummarization, err :=
		p.runAfterToolPluginCallbacks(
			ctx,
			invocation,
			toolCall,
			toolDeclaration,
			toolResult,
			toolErr,
		)
	if err != nil {
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization, err
	}
	if pluginOverride {
		if toolResult != nil {
			suppressDefaultToolMessage = false
		}
		pluginErr := toolErr
		if afterCallbackReplacedResult(toolErr, toolResult) {
			pluginErr = nil
		}
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization, pluginErr
	}
	ctx, toolResult, localSkip, err := p.runAfterToolCallbacks(
		ctx,
		toolCall,
		toolDeclaration,
		toolResult,
		toolErr,
	)
	if err != nil {
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization || localSkip, err
	}
	if toolResult != nil {
		suppressDefaultToolMessage = false
	}
	// When the after-tool callback replaced the result with a CustomResult,
	// the original tool execution error should be cleared so that the
	// caller uses the replacement result as the tool response message
	// instead of discarding it due to a non-nil error.
	if afterCallbackReplacedResult(toolErr, toolResult) {
		toolErr = nil
	}
	return ctx, toolResult, toolCall.Function.Arguments,
		suppressDefaultToolMessage, skipSummarization || localSkip, toolErr
}

// afterCallbackReplacedResult returns true when the after-tool callback has
// replaced the original (failed) tool result with a non-nil custom result.
// In that case the original toolErr should be cleared so that the framework
// sends the replacement result as the tool response message to the model.
// StopError is excluded because it carries a control-flow signal that must
// not be silently swallowed by a callback result replacement.
func afterCallbackReplacedResult(toolErr error, toolResult any) bool {
	if toolErr == nil || toolResult == nil {
		return false
	}
	if _, ok := agent.AsStopError(toolErr); ok {
		return false
	}
	return true
}

// isStreamable returns true if the tool supports streaming and its stream
// preference is enabled.
func isStreamable(t tool.Tool) bool {
	// Check if the tool has a stream preference and if it is enabled.
	if pref, ok := t.(streamInnerPreference); ok && !pref.StreamInner() {
		return false
	}
	_, ok := t.(tool.StreamableTool)
	return ok
}

// executeTool executes the tool based on its capabilities.
func (f *FunctionCallResponseProcessor) executeTool(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (context.Context, any, bool, error) {
	// originalTool refers to the actual underlying tool used to determine
	// whether streaming is supported. If tl is a NamedTool, use its
	// inner original tool instead of the wrapper itself.
	originalTool := tl
	if nameTool, ok := tl.(*itool.NamedTool); ok {
		originalTool = nameTool.Original()
	}
	// Prefer streaming execution if the tool supports it.
	if isStreamable(originalTool) {
		// Safe to cast since isStreamable checks for StreamableTool.
		return f.executeStreamableTool(
			ctx, invocation, toolCall, tl.(tool.StreamableTool), eventChan,
		)
	}
	// Fallback to callable tool execution if supported.
	if callable, ok := tl.(tool.CallableTool); ok {
		ctx, result, err := f.executeCallableTool(ctx, toolCall, callable)
		return ctx, result, false, err
	}
	return ctx, nil, false, fmt.Errorf("unsupported tool type: %T", tl)
}

// executeCallableTool executes a callable tool.
func (p *FunctionCallResponseProcessor) executeCallableTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.CallableTool,
) (context.Context, any, error) {
	if p.toolRetryPolicy == nil {
		result, err := tl.Call(ctx, toolCall.Function.Arguments)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"CallableTool execution failed for %s: %v",
				toolCall.Function.Name,
				err,
			)
			return ctx, nil, fmt.Errorf("%s: %w", ErrorCallableToolExecution, err)
		}
		return ctx, result, nil
	}
	runResult := toolretry.Execute(ctx, toolretry.ExecuteInput{
		ToolName:   toolCall.Function.Name,
		ToolCallID: toolCall.ID,
		Arguments:  toolCall.Function.Arguments,
		Policy:     p.toolRetryPolicy,
		Call:       tl.Call,
		ResultError: func(result any) bool {
			return extractResultError(result)
		},
		IsTerminalError: func(err error) bool {
			_, ok := agent.AsStopError(err)
			return ok
		},
	})
	if runResult.Error != nil {
		log.ErrorfContext(
			ctx,
			"CallableTool execution failed for %s: %v",
			toolCall.Function.Name,
			runResult.Error,
		)
		return ctx, nil, fmt.Errorf("%s: %w", ErrorCallableToolExecution, runResult.Error)
	}
	return ctx, runResult.Result, nil
}

func extractResultError(result any) bool {
	if result == nil {
		return false
	}
	type resultErrorGetter interface {
		RetryResultError() bool
	}
	rg, ok := result.(resultErrorGetter)
	if !ok {
		return false
	}
	return rg.RetryResultError()
}

func buildDefaultToolMessage(
	toolCallID string,
	result any,
) (model.Message, error) {
	// Preserve legacy tool message serialization for default fallback content.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return model.Message{}, err
	}
	return model.Message{
		Role:    model.RoleTool,
		Content: string(resultBytes),
		ToolID:  toolCallID,
	}, nil
}

type structuredStreamErrorOptIn interface {
	TRPCAgentGoStructuredStreamErrorsOptIn() bool
}

func streamableToolCallContext(
	ctx context.Context,
	tl tool.StreamableTool,
) context.Context {
	ctx = tool.WithFinalResultChunks(ctx)
	if !shouldRequestStructuredStreamErrors(tl) {
		return ctx
	}
	return tool.WithStructuredStreamErrors(ctx)
}

func shouldRequestStructuredStreamErrors(tl tool.StreamableTool) bool {
	if tl == nil {
		return false
	}
	candidate := any(tl)
	if namedTool, ok := tl.(*itool.NamedTool); ok {
		candidate = namedTool.Original()
	}
	pref, ok := candidate.(structuredStreamErrorOptIn)
	return ok && pref.TRPCAgentGoStructuredStreamErrorsOptIn()
}

// executeStreamableTool executes a streamable tool.
func (f *FunctionCallResponseProcessor) executeStreamableTool(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.StreamableTool,
	eventChan chan<- *event.Event,
) (context.Context, any, bool, error) {
	reader, err := tl.StreamableCall(
		streamableToolCallContext(ctx, tl),
		toolCall.Function.Arguments,
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"StreamableTool execution failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, nil, false, fmt.Errorf("%s: %w", ErrorStreamableToolExecution, err)
	}
	defer reader.Close()

	// Process stream chunks, handling:
	// Case 1: Raw sub-agent event passthrough.
	// Case 2: Plain text-like chunk. Emit partial tool.response event.
	contents, finalResult, err := f.consumeStream(
		ctx,
		invocation,
		toolCall,
		reader,
		eventChan,
		shouldRequestStructuredStreamErrors(tl),
	)
	if err != nil {
		return ctx, nil, false, err
	}
	if finalResult.seen {
		return ctx, finalResult.value, finalResult.value == nil, nil
	}
	// If we forwarded inner events, still return the merged content as the tool
	// result so it can be recorded in the tool response message for the next LLM
	// turn (to satisfy providers that require tool messages). The UI example
	// suppresses printing these aggregated strings to avoid duplication; they are
	// primarily for model consumption.
	return ctx, tool.Merge(contents), false, nil
}

type streamFinalResult struct {
	seen  bool
	value any
}

type streamInnerEventState struct {
	pendingGraphToolErrorEvent *event.Event
}

type normalizedFinalResultChunk struct {
	result     any
	stateDelta map[string][]byte
}

type syntheticStateOnlyToolChoiceKey struct{}

func markSyntheticStateOnlyToolChoice(ctx context.Context) context.Context {
	return context.WithValue(ctx, syntheticStateOnlyToolChoiceKey{}, true)
}

func hasSyntheticStateOnlyToolChoice(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	synthetic, _ := ctx.Value(syntheticStateOnlyToolChoiceKey{}).(bool)
	return synthetic
}

// consumeStream reads all chunks from the reader and processes them.
func (f *FunctionCallResponseProcessor) consumeStream(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	reader *tool.StreamReader,
	eventChan chan<- *event.Event,
	structuredErrors bool,
) ([]any, streamFinalResult, error) {
	var contents []any
	var finalResult streamFinalResult
	var innerEventState streamInnerEventState
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.ErrorfContext(
				ctx,
				"StreamableTool execution failed for %s: receive chunk "+
					"from stream reader failed: %v, may merge "+
					"incomplete data",
				toolCall.Function.Name,
				err,
			)
			break
		}

		if err := f.processStreamChunk(
			ctx,
			invocation,
			toolCall,
			chunk,
			eventChan,
			&contents,
			&finalResult,
			&innerEventState,
			structuredErrors,
		); err != nil {
			return contents, finalResult, err
		}
	}
	if structuredErrors && innerEventState.pendingGraphToolErrorEvent != nil {
		return contents, finalResult, streamToolEventError(
			innerEventState.pendingGraphToolErrorEvent,
		)
	}
	return contents, finalResult, nil
}

// appendInnerEventContent extracts textual content from an inner event and appends it.
func (f *FunctionCallResponseProcessor) appendInnerEventContent(
	ev *event.Event,
	contents *[]any,
) {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		ch := ev.Response.Choices[0]
		if ch.Delta.Content != "" {
			*contents = append(*contents, ch.Delta.Content)
		} else if ch.Message.Role == model.RoleAssistant &&
			ch.Message.Content != "" {
			*contents = append(*contents, ch.Message.Content)
		}
	}
}

// buildPartialToolResponseEvent constructs a partial tool.response event.
func (f *FunctionCallResponseProcessor) buildPartialToolResponseEvent(
	inv *agent.Invocation,
	toolCall model.ToolCall,
	text string,
) *event.Event {
	resp := &model.Response{
		ID:      uuid.New().String(),
		Object:  model.ObjectTypeToolResponse,
		Created: time.Now().Unix(),
		Model:   inv.Model.Info().Name,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleTool, ToolID: toolCall.ID},
			Delta:   model.Message{Role: model.RoleTool, Content: text, ToolID: toolCall.ID},
		}},
		Timestamp: time.Now(),
		Done:      false,
		IsPartial: true,
	}
	return event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithResponse(resp),
	)
}

func (f *FunctionCallResponseProcessor) buildStateDeltaToolResponseEvent(
	inv *agent.Invocation,
	toolCall model.ToolCall,
	stateDelta map[string][]byte,
) *event.Event {
	evt := f.buildPartialToolResponseEvent(inv, toolCall, "")
	if evt != nil && len(stateDelta) > 0 {
		evt.StateDelta = cloneEventStateDelta(stateDelta)
	}
	return evt
}

func cloneEventStateDelta(stateDelta map[string][]byte) map[string][]byte {
	if len(stateDelta) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(stateDelta))
	for key, value := range stateDelta {
		if value == nil {
			cloned[key] = nil
			continue
		}
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}

func streamToolEventError(ev *event.Event) error {
	if ev == nil || !ev.IsError() {
		return nil
	}
	if ev.Error != nil && ev.Error.Type == agent.ErrorTypeStopAgentError {
		return agent.NewStopError(ev.Error.Message)
	}
	if isRetryingGraphNodeErrorEvent(ev) {
		return nil
	}
	if ev.Error != nil {
		return fmt.Errorf(
			"%s: %s: %s",
			ErrorStreamableToolExecution,
			ev.Error.Type,
			ev.Error.Message,
		)
	}
	return fmt.Errorf(ErrorStreamableToolExecution)
}

func isGraphToolExecutionErrorEvent(ev *event.Event) bool {
	if ev == nil || ev.Response == nil || ev.StateDelta == nil {
		return false
	}
	if ev.Response.Object != model.ObjectTypeToolResponse {
		return false
	}
	_, ok := ev.StateDelta[graph.MetadataKeyTool]
	return ok
}

func isRetryingGraphNodeErrorEvent(ev *event.Event) bool {
	if ev == nil ||
		ev.Error == nil ||
		ev.StateDelta == nil {
		return false
	}
	rawMetadata := ev.StateDelta[graph.MetadataKeyNode]
	if len(rawMetadata) == 0 {
		return false
	}
	var metadata graph.NodeExecutionMetadata
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		return false
	}
	return metadata.Phase == graph.ExecutionPhaseError && metadata.Retrying
}

// marshalChunkToText converts a chunk content into a string representation.
func marshalChunkToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	default:
		if bts, e := json.Marshal(v); e == nil {
			return string(bts)
		}
		return fmt.Sprintf("%v", v)
	}
}

// filterNilEvents filters out nil events from a slice of events while preserving order.
// Pre-allocates capacity to avoid multiple memory allocations.
func (p *FunctionCallResponseProcessor) filterNilEvents(results []*event.Event) []*event.Event {
	// Pre-allocate with capacity to reduce allocations
	filtered := make([]*event.Event, 0, len(results))
	for _, event := range results {
		if event != nil {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func newToolCallResponseEvent(
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	functionResponses []model.Choice) *event.Event {
	// Create function response event.
	e := event.NewResponseEvent(
		invocation.InvocationID,
		invocation.AgentName,
		&model.Response{
			Object:    model.ObjectTypeToolResponse,
			Created:   time.Now().Unix(),
			Model:     functionCallResponse.Model,
			Choices:   functionResponses,
			Timestamp: time.Now(),
		},
	)
	agent.InjectIntoEvent(invocation, e)
	return e
}

func newMinimalToolCallResponseEvent(
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	toolCall model.ToolCall,
	index int,
) *event.Event {
	return newToolCallResponseEvent(
		invocation,
		functionCallResponse,
		[]model.Choice{newMinimalToolChoice(toolCall, index)},
	)
}

func newMinimalToolChoice(
	toolCall model.ToolCall,
	index int,
) model.Choice {
	return model.Choice{
		Index: index,
		Message: model.Message{
			Role:     model.RoleTool,
			ToolID:   toolCall.ID,
			ToolName: toolCall.Function.Name,
		},
	}
}

func mergeParallelToolCallResponseEvents(es []*event.Event) *event.Event {
	switch len(es) {
	case 0:
		return nil
	case 1:
		return es[0]
	default:
	}

	mergedChoices := collectMergedChoices(es)
	mergedDelta := collectStateDelta(es)
	baseEvent := findBaseEvent(es)
	resp := buildMergedToolResponse(baseEvent, mergedChoices)
	mergedEvent := buildMergedEvent(baseEvent, resp)

	if len(mergedDelta) > 0 {
		mergedEvent.StateDelta = mergedDelta
	}
	if shouldSkipSummarization(es) {
		markSkipSummarization(mergedEvent)
	}
	return mergedEvent
}

// collectMergedChoices collects the choices from all events.
func collectMergedChoices(es []*event.Event) []model.Choice {
	totalChoices := 0
	for _, e := range es {
		if e != nil && e.Response != nil {
			totalChoices += len(e.Response.Choices)
		}
	}

	mergedChoices := make([]model.Choice, 0, totalChoices)
	for _, e := range es {
		// Add nil checks to prevent panic
		if e != nil && e.Response != nil {
			mergedChoices = append(mergedChoices, e.Response.Choices...)
		}
	}
	return mergedChoices
}

// collectStateDelta collects the state delta from all events.
func collectStateDelta(es []*event.Event) map[string][]byte {
	mergedDelta := map[string][]byte{}
	for _, e := range es {
		if e == nil || len(e.StateDelta) == 0 {
			continue
		}
		for k, v := range e.StateDelta {
			mergedDelta[k] = v
		}
	}
	return mergedDelta
}

// findBaseEvent finds a valid base event for metadata.
func findBaseEvent(es []*event.Event) *event.Event {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}

// buildMergedToolResponse builds the merged tool response.
func buildMergedToolResponse(baseEvent *event.Event, mergedChoices []model.Choice) *model.Response {
	modelName := "unknown"
	if baseEvent != nil && baseEvent.Response != nil {
		modelName = baseEvent.Response.Model
	}
	return &model.Response{
		ID:        uuid.New().String(),
		Object:    model.ObjectTypeToolResponse,
		Created:   time.Now().Unix(),
		Model:     modelName,
		Choices:   mergedChoices,
		Timestamp: time.Now(),
	}
}

// buildMergedEvent builds the merged event.
func buildMergedEvent(baseEvent *event.Event, resp *model.Response) *event.Event {
	// If we have a base event, carry over invocation, author and branch.
	if baseEvent != nil {
		return event.New(baseEvent.InvocationID, baseEvent.Author, event.WithResponse(resp))
	}
	// Fallback: construct without base metadata.
	return event.New("", "", event.WithResponse(resp))
}

// shouldSkipSummarization checks if any event prefers skipping summarization.
func shouldSkipSummarization(es []*event.Event) bool {
	for _, e := range es {
		if e != nil && e.Actions != nil && e.Actions.SkipSummarization {
			return true
		}
	}
	return false
}

func markSkipSummarization(ev *event.Event) {
	if ev == nil {
		return
	}
	if ev.Actions == nil {
		ev.Actions = &event.EventActions{}
	}
	ev.Actions.SkipSummarization = true
}

func toolPrefersSkipSummarization(tl tool.Tool) bool {
	if tl == nil {
		return false
	}
	original := tl
	if nameTool, ok := tl.(*itool.NamedTool); ok {
		original = nameTool.Original()
	}
	if skipper, ok := original.(summarizationSkipper); ok {
		return skipper.SkipSummarization()
	}
	return false
}

// findCompatibleTool attempts to map a requested (missing) tool name to a compatible tool.
// For models that directly call sub-agent names, map to transfer_to_agent when available.
func findCompatibleTool(requested string, tools map[string]tool.Tool, invocation *agent.Invocation) tool.Tool {
	transfer, ok := tools[transfer.TransferToolName]
	if !ok || invocation == nil || invocation.Agent == nil {
		return nil
	}
	for _, a := range invocation.Agent.SubAgents() {
		if a.Info().Name == requested {
			return transfer
		}
	}
	return nil
}

// convertToolArguments converts original args to the mapped tool args when needed.
// When mapping sub-agent name -> transfer_to_agent, wrap message and set agent_name.
func convertToolArguments(originalName string, originalArgs []byte, targetName string) []byte {
	if targetName != transfer.TransferToolName {
		return nil
	}

	var input subAgentCall
	if len(originalArgs) > 0 {
		if err := json.Unmarshal(originalArgs, &input); err != nil {
			log.Warnf("Failed to unmarshal sub-agent call arguments for %s: %v", originalName, err)
			return nil
		}
	}

	message := input.Message
	if message == "" {
		message = defaultTransferMessage
	}

	req := &transfer.Request{
		AgentName: originalName,
		Message:   message,
	}

	b, err := json.Marshal(req)
	if err != nil {
		log.Warnf("Failed to marshal transfer request for %s: %v", originalName, err)
		return nil
	}
	return b
}

// processStreamChunk handles a single streamed chunk and updates contents and events.
func (f *FunctionCallResponseProcessor) processStreamChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	chunk tool.StreamChunk,
	eventChan chan<- *event.Event,
	contents *[]any,
	finalResult *streamFinalResult,
	innerEventState *streamInnerEventState,
	structuredErrors bool,
) error {
	if finalChunk, ok := normalizeFinalResultChunk(chunk.Content); ok {
		return f.handleFinalResultChunk(
			ctx,
			invocation,
			toolCall,
			eventChan,
			finalResult,
			innerEventState,
			finalChunk,
			structuredErrors,
		)
	}
	if ev, ok := chunk.Content.(*event.Event); ok {
		return f.handleStreamInnerEvent(
			ctx,
			eventChan,
			contents,
			innerEventState,
			ev,
			structuredErrors,
		)
	}
	return f.handlePlainStreamChunk(
		ctx,
		invocation,
		toolCall,
		eventChan,
		contents,
		innerEventState,
		chunk.Content,
		structuredErrors,
	)
}

func normalizeFinalResultChunk(content any) (*normalizedFinalResultChunk, bool) {
	switch v := content.(type) {
	case tool.FinalResultChunk:
		if v.Result == nil {
			return nil, true
		}
		return &normalizedFinalResultChunk{result: v.Result}, true
	case *tool.FinalResultChunk:
		if v == nil || v.Result == nil {
			return nil, true
		}
		return &normalizedFinalResultChunk{result: v.Result}, true
	case tool.FinalResultStateChunk:
		if v.Result == nil && len(v.StateDelta) == 0 {
			return nil, true
		}
		return &normalizedFinalResultChunk{
			result:     v.Result,
			stateDelta: cloneEventStateDelta(v.StateDelta),
		}, true
	case *tool.FinalResultStateChunk:
		if v == nil || (v.Result == nil && len(v.StateDelta) == 0) {
			return nil, true
		}
		return &normalizedFinalResultChunk{
			result:     v.Result,
			stateDelta: cloneEventStateDelta(v.StateDelta),
		}, true
	default:
		return nil, false
	}
}

func (f *FunctionCallResponseProcessor) handleFinalResultChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	eventChan chan<- *event.Event,
	finalResult *streamFinalResult,
	innerEventState *streamInnerEventState,
	finalChunk *normalizedFinalResultChunk,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, nil, structuredErrors); err != nil {
		return err
	}
	if finalChunk != nil && finalResult != nil {
		finalResult.seen = true
		finalResult.value = finalChunk.result
	}
	if finalChunk == nil || len(finalChunk.stateDelta) == 0 || eventChan == nil {
		return nil
	}
	deltaEvent := f.buildStateDeltaToolResponseEvent(invocation, toolCall, finalChunk.stateDelta)
	return agent.EmitEvent(ctx, invocation, eventChan, deltaEvent)
}

func (f *FunctionCallResponseProcessor) handleStreamInnerEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	contents *[]any,
	innerEventState *streamInnerEventState,
	ev *event.Event,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, ev, structuredErrors); err != nil {
		return err
	}
	if err := event.EmitEvent(ctx, eventChan, ev); err != nil {
		return err
	}
	f.appendInnerEventContent(ev, contents)
	if !structuredErrors {
		return nil
	}
	if isGraphToolExecutionErrorEvent(ev) {
		if innerEventState != nil {
			innerEventState.pendingGraphToolErrorEvent = ev
		}
		return nil
	}
	return streamToolEventError(ev)
}

func (f *FunctionCallResponseProcessor) handlePlainStreamChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	eventChan chan<- *event.Event,
	contents *[]any,
	innerEventState *streamInnerEventState,
	content any,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, nil, structuredErrors); err != nil {
		return err
	}
	text := marshalChunkToText(content)
	if text == "" {
		return nil
	}
	*contents = append(*contents, text)
	if eventChan == nil {
		return nil
	}
	partial := f.buildPartialToolResponseEvent(invocation, toolCall, text)
	return agent.EmitEvent(ctx, invocation, eventChan, partial)
}

func flushPendingGraphToolError(
	state *streamInnerEventState,
	nextEvent *event.Event,
	structuredErrors bool,
) error {
	if !structuredErrors {
		return nil
	}
	if state == nil || state.pendingGraphToolErrorEvent == nil {
		return nil
	}
	if nextEvent != nil {
		if isRetryingGraphNodeErrorEvent(nextEvent) {
			state.pendingGraphToolErrorEvent = nil
			return nil
		}
		if nextEvent.IsError() {
			state.pendingGraphToolErrorEvent = nil
			return nil
		}
	}
	err := streamToolEventError(state.pendingGraphToolErrorEvent)
	state.pendingGraphToolErrorEvent = nil
	return err
}
