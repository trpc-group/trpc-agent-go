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
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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
}

// NewFunctionCallResponseProcessor creates a new transfer response processor.
func NewFunctionCallResponseProcessor(enableParallelTools bool, toolCallbacks *tool.Callbacks) *FunctionCallResponseProcessor {
	return &FunctionCallResponseProcessor{
		enableParallelTools: enableParallelTools,
		toolCallbacks:       toolCallbacks,
	}
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

	functioncallResponseEvent, err := p.handleFunctionCallsAndSendEvent(ctx, invocation, rsp, req.Tools, ch)

	// Option one: set invocation.EndInvocation is true, and stop next step.
	// Option two: emit error event, maybe the LLM can correct this error and also need to wait for notice completion.
	// maybe the Option two is better.
	// Allow users to intervene in error handling through callbacks.
	if _, ok := agent.AsStopError(err); ok {
		invocation.EndInvocation = true
		return
	}

	if err != nil || functioncallResponseEvent == nil {
		return
	}

	// If the tool indicates skipping outer summarization, mark the invocation to end
	// after this tool response so the flow does not perform an extra LLM call.
	if functioncallResponseEvent.Actions != nil && functioncallResponseEvent.Actions.SkipSummarization {
		invocation.EndInvocation = true
		return
	}
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
	agent.EmitEvent(ctx, invocation, eventChan, functionResponseEvent)
	return functionResponseEvent, nil
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
	_, span := trace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName(toolCall.Function.Name))
	defer span.End()
	startTime := time.Now()
	ctx, choices, modifiedArgs, shouldIgnoreError, err := p.executeToolCall(
		ctx, invocation, toolCall, tools, index, eventChan,
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
	if len(choices) == 0 {
		return nil, nil
	}
	// Annotate only tool messages with ToolName for observability.
	for i := range choices {
		if choices[i].Message.Role == model.RoleTool && choices[i].Message.ToolName == "" {
			choices[i].Message.ToolName = toolCall.Function.Name
		}
	}
	toolEvent := newToolCallResponseEvent(
		invocation, llmResponse, choices,
	)
	if toolCall.Function.Name == transfer.TransferToolName {
		toolEvent.Tag = event.TransferTag
	}
	if tl, ok := tools[toolCall.Function.Name]; ok {
		p.annotateSkipSummarization(toolEvent, tl)
	}
	decl := p.lookupDeclaration(tools, toolCall.Function.Name)

	var (
		sess      = &session.Session{}
		modelName string
		agentName string
	)
	// Attach state delta if the tool provides it.
	if tl, ok := tools[toolCall.Function.Name]; ok {
		// Use the first choice as the canonical tool result for state delta.
		p.attachStateDelta(tl, modifiedArgs, &choices[0], toolEvent)
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

	itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolEvent, err)
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
	_, span := trace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName(tc.Function.Name))
	defer span.End()
	startTime := time.Now()
	// Execute the tool (streamable or callable) with callbacks.
	ctx, choices, modifiedArgs, shouldIgnoreError, err := p.executeToolCall(
		ctx, invocation, tc, tools, index, eventChan,
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
		if tc.Function.Name == transfer.TransferToolName {
			errorEvent.Tag = event.TransferTag
		}
		// Only propagate the error if it's not ignorable (e.g., stop errors)
		var returnErr error
		if !shouldIgnoreError {
			returnErr = err
		}
		p.sendToolResult(ctx, resultChan, toolResult{index: index, event: errorEvent, err: returnErr})
		return
	}

	// No error and at least one choice means we have tool result messages.
	if len(choices) == 0 {
		p.sendToolResult(ctx, resultChan, toolResult{index: index})
		return
	}

	// Annotate only tool messages with ToolName for observability.
	for i := range choices {
		if choices[i].Message.Role == model.RoleTool && choices[i].Message.ToolName == "" {
			choices[i].Message.ToolName = tc.Function.Name
		}
	}

	toolCallResponseEvent := newToolCallResponseEvent(
		invocation, llmResponse, choices,
	)
	if tc.Function.Name == transfer.TransferToolName {
		toolCallResponseEvent.Tag = event.TransferTag
	}
	// Respect tool preference to skip outer summarization when present.
	if tl, ok := tools[tc.Function.Name]; ok {
		p.annotateSkipSummarization(toolCallResponseEvent, tl)
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
	if tl, ok := tools[tc.Function.Name]; ok {
		// Use the first choice as the canonical tool result for state delta.
		p.attachStateDelta(tl, modifiedArgs, &choices[0], toolCallResponseEvent)
	}
	itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolCallResponseEvent, err)
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

// attachStateDelta copies tool-provided state delta to the event.
func (p *FunctionCallResponseProcessor) attachStateDelta(
	tl tool.Tool, args []byte, choice *model.Choice, ev *event.Event,
) {
	if tl == nil || choice == nil || ev == nil {
		return
	}
	original := tl
	if nameTool, ok := tl.(*itool.NamedTool); ok {
		original = nameTool.Original()
	}
	type stateDeltaProvider interface {
		StateDelta([]byte, []byte) map[string][]byte
	}
	sdp, ok := original.(stateDeltaProvider)
	if !ok {
		return
	}
	b := []byte(choice.Message.Content)
	delta := sdp.StateDelta(args, b)
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
	ev *event.Event, tl tool.Tool,
) {
	// Unwrap NamedTool to inspect the original for preferences.
	original := tl
	if nameTool, ok := tl.(*itool.NamedTool); ok {
		original = nameTool.Original()
	}
	if skipper, ok := original.(summarizationSkipper); ok &&
		skipper.SkipSummarization() {
		if ev.Actions == nil {
			ev.Actions = &event.EventActions{}
		}
		ev.Actions.SkipSummarization = true
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
		for _, tc := range toolCalls {
			minimal = append(minimal, model.Choice{
				Index:   0,
				Message: model.Message{Role: model.RoleTool, ToolID: tc.ID},
			})
		}
		mergedEvent = newToolCallResponseEvent(invocation, llmResponse, minimal)
		for _, tc := range toolCalls {
			if tl, ok := tools[tc.Function.Name]; ok {
				// Unwrap NamedTool then check for preference.
				original := tl
				if nameTool, ok2 := tl.(*itool.NamedTool); ok2 {
					original = nameTool.Original()
				}
				if skipper, ok2 := original.(summarizationSkipper); ok2 &&
					skipper.SkipSummarization() {
					if mergedEvent.Actions == nil {
						mergedEvent.Actions = &event.EventActions{}
					}
					mergedEvent.Actions.SkipSummarization = true
					break
				}
			}
		}
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallEvents)
	}
	if len(toolCallEvents) > 1 {
		_, span := trace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName(itelemetry.ToolNameMergedTools))
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
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
//   - *model.Choice: the result choice containing tool response (nil if error occurred)
//   - []byte: the modified arguments after before-tool callbacks (for telemetry)
//   - bool: shouldIgnoreError - true if the error is ignorable (e.g., tool not found, marshal error), false for critical errors (e.g., stop errors)
//   - error: any error that occurred during execution (no longer swallowed)
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
	eventChan chan<- *event.Event,
) (context.Context, []model.Choice, []byte, bool, error) {
	// Check if tool exists.
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
			return ctx, nil, toolCall.Function.Arguments, true,
				fmt.Errorf("executeToolCall: %s", ErrorToolNotFound)
		}
	}

	log.DebugfContext(
		ctx,
		"Executing tool %s with args: %s",
		toolCall.Function.Name,
		string(toolCall.Function.Arguments),
	)

	// Execute the tool with callbacks.
	ctx, result, modifiedArgs, err := p.executeToolWithCallbacks(ctx, invocation, toolCall, tl, eventChan)
	// Only return error when it's a stop error
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return ctx, nil, modifiedArgs, false, err
		}
		return ctx, nil, modifiedArgs, true, err
	}
	//  allow to return nil not provide function response.
	if r, ok := tl.(function.LongRunner); ok && r.LongRunning() {
		if result == nil {
			return ctx, nil, modifiedArgs, true, nil
		}
	}

	// Marshal the result to JSON for the default tool message.
	resultBytes, err := json.Marshal(result)
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
		return ctx, nil, modifiedArgs, true,
			fmt.Errorf("%s: %w", ErrorMarshalResult, err)
	}

	defaultMsg := model.Message{
		Role:    model.RoleTool,
		Content: string(resultBytes),
		ToolID:  toolCall.ID,
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
			return ctx, nil, modifiedArgs, true, cbErr
		}
		if overridden {
			choices = customChoices
		}
	}

	log.DebugfContext(
		ctx,
		"CallableTool %s executed successfully, result: %s",
		toolCall.Function.Name,
		string(resultBytes),
	)

	return ctx, choices, modifiedArgs, true, nil
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
	raw, cbErr := p.toolCallbacks.ToolResultMessages(ctx, &tool.ToolResultMessagesInput{
		ToolName:           toolCall.Function.Name,
		Declaration:        tl.Declaration(),
		Arguments:          modifiedArgs,
		Result:             result,
		ToolCallID:         toolCall.ID,
		DefaultToolMessage: defaultMsg,
	})
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

// executeToolWithCallbacks executes a tool with before/after callbacks.
// Returns (context, result, modifiedArguments, error).
func (p *FunctionCallResponseProcessor) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (context.Context, any, []byte, error) {
	// Inject tool call ID into context for callbacks to use.
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, toolCall.ID)

	toolDeclaration := tl.Declaration()
	// Run before tool callbacks if they exist.
	if p.toolCallbacks != nil {
		result, callbackErr := p.toolCallbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
			ToolName:    toolCall.Function.Name,
			Declaration: toolDeclaration,
			Arguments:   toolCall.Function.Arguments,
		})
		if callbackErr != nil {
			log.ErrorfContext(
				ctx,
				"Before tool callback failed for %s: %v",
				toolCall.Function.Name,
				callbackErr,
			)
			return ctx, nil, toolCall.Function.Arguments, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		// Use the context from result if provided for subsequent operations.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResult != nil {
			// Use custom result from callback.
			return ctx, result.CustomResult, toolCall.Function.Arguments, nil
		}
		if result != nil && result.ModifiedArguments != nil {
			// Use modified arguments from callback.
			toolCall.Function.Arguments = result.ModifiedArguments
		}
	}

	// Execute the actual tool.
	toolResult, err := p.executeTool(ctx, invocation, toolCall, tl, eventChan)
	if err != nil {
		log.WarnfContext(
			ctx,
			"tool execute failed, function name: %v, arguments: %s, "+
				"result: %v, err: %v",
			toolCall.Function.Name,
			string(toolCall.Function.Arguments),
			toolResult,
			err,
		)
	}

	// Run after tool callbacks if they exist.
	// If the tool returns an error, the callback function will still execute to allow the user to handle the error.
	if p.toolCallbacks != nil {
		afterResult, callbackErr := p.toolCallbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
			ToolName:    toolCall.Function.Name,
			Declaration: toolDeclaration,
			Arguments:   toolCall.Function.Arguments,
			Result:      toolResult,
			Error:       err,
		})
		if callbackErr != nil {
			log.ErrorfContext(
				ctx,
				"After tool callback failed for %s: %v",
				toolCall.Function.Name,
				callbackErr,
			)
			return ctx, toolResult, toolCall.Function.Arguments, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		// Use the context from result if provided for subsequent operations.
		if afterResult != nil && afterResult.Context != nil {
			ctx = afterResult.Context
		}
		if afterResult != nil && afterResult.CustomResult != nil {
			toolResult = afterResult.CustomResult
		}
	}
	return ctx, toolResult, toolCall.Function.Arguments, err
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
) (any, error) {
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
		return f.executeCallableTool(ctx, toolCall, callable)
	}
	return nil, fmt.Errorf("unsupported tool type: %T", tl)
}

// executeCallableTool executes a callable tool.
func (p *FunctionCallResponseProcessor) executeCallableTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.CallableTool,
) (any, error) {
	result, err := tl.Call(ctx, toolCall.Function.Arguments)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"CallableTool execution failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return nil, fmt.Errorf("%s: %w", ErrorCallableToolExecution, err)
	}
	return result, nil
}

// executeStreamableTool executes a streamable tool.
func (f *FunctionCallResponseProcessor) executeStreamableTool(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.StreamableTool,
	eventChan chan<- *event.Event,
) (any, error) {
	reader, err := tl.StreamableCall(ctx, toolCall.Function.Arguments)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"StreamableTool execution failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return nil, fmt.Errorf("%s: %w", ErrorStreamableToolExecution, err)
	}
	defer reader.Close()

	// Process stream chunks, handling:
	// Case 1: Raw sub-agent event passthrough.
	// Case 2: Plain text-like chunk. Emit partial tool.response event.
	contents, err := f.consumeStream(ctx, invocation, toolCall, reader, eventChan)
	if err != nil {
		return nil, err
	}
	// If we forwarded inner events, still return the merged content as the tool
	// result so it can be recorded in the tool response message for the next LLM
	// turn (to satisfy providers that require tool messages). The UI example
	// suppresses printing these aggregated strings to avoid duplication; they are
	// primarily for model consumption.
	return tool.Merge(contents), nil
}

// consumeStream reads all chunks from the reader and processes them.
func (f *FunctionCallResponseProcessor) consumeStream(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	reader *tool.StreamReader,
	eventChan chan<- *event.Event,
) ([]any, error) {
	var contents []any
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

		if err := f.processStreamChunk(ctx, invocation, toolCall, chunk, eventChan, &contents); err != nil {
			return contents, err
		}
	}
	return contents, nil
}

// appendInnerEventContent extracts textual content from an inner event and appends it.
func (f *FunctionCallResponseProcessor) appendInnerEventContent(ev *event.Event, contents *[]any) {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		ch := ev.Response.Choices[0]
		if ch.Delta.Content != "" {
			*contents = append(*contents, ch.Delta.Content)
		} else if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
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
			Delta:   model.Message{Content: text},
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
		if mergedEvent.Actions == nil {
			mergedEvent.Actions = &event.EventActions{}
		}
		mergedEvent.Actions.SkipSummarization = true
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
) error {
	// Case 1: Raw sub-agent event passthrough.
	if ev, ok := chunk.Content.(*event.Event); ok {
		// With random FilterKey isolation, we can safely forward all inner events
		// since they are properly isolated and won't pollute the parent session.
		if err := event.EmitEvent(ctx, eventChan, ev); err != nil {
			return err
		}
		f.appendInnerEventContent(ev, contents)
		return nil
	}

	// Case 2: Plain text-like chunk. Emit partial tool.response event.
	text := marshalChunkToText(chunk.Content)
	if text == "" {
		return nil
	}
	*contents = append(*contents, text)
	if eventChan != nil {
		partial := f.buildPartialToolResponseEvent(invocation, toolCall, text)
		if err := agent.EmitEvent(ctx, invocation, eventChan, partial); err != nil {
			return err
		}
	}
	return nil
}
