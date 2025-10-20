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
	if invocation == nil || !rsp.IsToolCallResponse() {
		return
	}

	functioncallResponseEvent, err := p.handleFunctionCallsAndSendEvent(ctx, invocation, rsp, req.Tools, ch)

	// Option one: set invocation.EndInvocation is true, and stop next step.
	// Option two: emit error event, maybe the LLM can correct this error and also need to wait for notice completion.
	// maybe the Option two is better.
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
		log.Errorf("Function call handling failed for agent %s: %v", invocation.AgentName, err)
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
	choice, modifiedArgs, err := p.executeToolCall(
		ctx, invocation, toolCall, tools, index, eventChan,
	)
	if err != nil {
		return nil, err
	}
	if choice == nil {
		return nil, nil
	}
	choice.Message.ToolName = toolCall.Function.Name
	toolEvent := newToolCallResponseEvent(
		invocation, llmResponse, []model.Choice{*choice},
	)
	if tl, ok := tools[toolCall.Function.Name]; ok {
		p.annotateSkipSummarization(toolEvent, tl)
	}
	decl := p.lookupDeclaration(tools, toolCall.Function.Name)
	itelemetry.TraceToolCall(
		span, decl, modifiedArgs, toolEvent,
	)
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
		go p.runParallelToolCall(
			ctx, &wg, invocation, llmResponse, tools, eventChan, resultChan, i, tc,
		)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultChan)
		close(done)
	}()

	toolCallResponsesEvents := p.collectParallelToolResults(
		ctx, resultChan, len(toolCalls),
	)
	mergedEvent := p.buildMergedParallelEvent(
		ctx, invocation, llmResponse, tools, toolCalls, toolCallResponsesEvents,
	)
	return mergedEvent, nil
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
			log.Errorf(
				"Tool execution panic for %s (index: %d, ID: %s, agent: %s): %v",
				tc.Function.Name, index, tc.ID, invocation.AgentName, r,
			)
			errorChoice := p.createErrorChoice(
				index, tc.ID, fmt.Sprintf("tool execution panic: %v", r),
			)
			errorChoice.Message.ToolName = tc.Function.Name
			errorEvent := newToolCallResponseEvent(
				invocation, llmResponse, []model.Choice{*errorChoice},
			)
			p.sendToolResult(ctx, resultChan, toolResult{index: index, event: errorEvent})
		}
	}()

	// Trace the tool execution for observability.
	_, span := trace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName(tc.Function.Name))
	defer span.End()

	// Execute the tool (streamable or callable) with callbacks.
	choice, modifiedArgs, err := p.executeToolCall(
		ctx, invocation, tc, tools, index, eventChan,
	)
	if err != nil {
		log.Errorf(
			"Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
			tc.Function.Name, index, tc.ID, invocation.AgentName, err,
		)
		errorChoice := p.createErrorChoice(
			index, tc.ID, fmt.Sprintf("tool execution error: %v", err),
		)
		errorChoice.Message.ToolName = tc.Function.Name
		errorEvent := newToolCallResponseEvent(
			invocation, llmResponse, []model.Choice{*errorChoice},
		)
		p.sendToolResult(ctx, resultChan, toolResult{index: index, event: errorEvent})
		return
	}
	// Long-running tools may return nil to indicate no immediate event.
	if choice == nil {
		p.sendToolResult(ctx, resultChan, toolResult{index: index, event: nil})
		return
	}

	choice.Message.ToolName = tc.Function.Name
	toolCallResponseEvent := newToolCallResponseEvent(
		invocation, llmResponse, []model.Choice{*choice},
	)
	// Respect tool preference to skip outer summarization when present.
	if tl, ok := tools[tc.Function.Name]; ok {
		p.annotateSkipSummarization(toolCallResponseEvent, tl)
	}
	// Include declaration for telemetry even when tool is missing.
	decl := p.lookupDeclaration(tools, tc.Function.Name)
	itelemetry.TraceToolCall(span, decl, modifiedArgs, toolCallResponseEvent)
	// Send result back to aggregator.
	p.sendToolResult(
		ctx, resultChan, toolResult{index: index, event: toolCallResponseEvent},
	)
}

// annotateSkipSummarization marks an event to skip outer summarization.
func (p *FunctionCallResponseProcessor) annotateSkipSummarization(
	ev *event.Event, tl tool.Tool,
) {
	if skipper, ok := tl.(summarizationSkipper); ok && skipper.SkipSummarization() {
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
				if skipper, ok2 := tl.(summarizationSkipper); ok2 && skipper.SkipSummarization() {
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
//   - *model.Choice: the result choice containing tool response
//   - []byte: the modified arguments after before-tool callbacks (for telemetry)
//   - error: any error that occurred during execution
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
	eventChan chan<- *event.Event,
) (*model.Choice, []byte, error) {
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
			log.Errorf("CallableTool %s not found (agent=%s, model=%s)",
				toolCall.Function.Name, invocation.AgentName, invocation.Model.Info().Name)
			return p.createErrorChoice(index, toolCall.ID, ErrorToolNotFound), toolCall.Function.Arguments, nil
		}
	}

	log.Debugf("Executing tool %s with args: %s", toolCall.Function.Name, string(toolCall.Function.Arguments))

	// Execute the tool with callbacks.
	result, modifiedArgs, err := p.executeToolWithCallbacks(ctx, invocation, toolCall, tl, eventChan)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, modifiedArgs, err
		}
		return p.createErrorChoice(index, toolCall.ID, err.Error()), modifiedArgs, nil
	}
	//  allow to return nil not provide function response.
	if r, ok := tl.(function.LongRunner); ok && r.LongRunning() {
		if result == nil {
			return nil, modifiedArgs, nil
		}
	}

	// Marshal the result to JSON.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Errorf("Failed to marshal tool result for %s: %v", toolCall.Function.Name, err)
		return p.createErrorChoice(index, toolCall.ID, ErrorMarshalResult), modifiedArgs, nil
	}

	log.Debugf("CallableTool %s executed successfully, result: %s", toolCall.Function.Name, string(resultBytes))

	return &model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: string(resultBytes),
			ToolID:  toolCall.ID,
		},
	}, modifiedArgs, nil
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
) []*event.Event {
	results := make([]*event.Event, toolCallsCount)
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				// Channel closed, all results received.
				return p.filterNilEvents(results)
			}
			if result.index >= 0 && result.index < len(results) {
				results[result.index] = result.event
			} else {
				log.Errorf("Tool result index %d out of range [0, %d)", result.index, len(results))
			}
		case <-ctx.Done():
			// Context cancelled, return what we have.
			log.Warnf("Context cancelled while waiting for tool results")
			return p.filterNilEvents(results)
		}
	}
}

// executeToolWithCallbacks executes a tool with before/after callbacks.
// Returns (result, modifiedArguments, error).
func (p *FunctionCallResponseProcessor) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (any, []byte, error) {
	toolDeclaration := tl.Declaration()
	// Run before tool callbacks if they exist.
	if p.toolCallbacks != nil {
		customResult, callbackErr := p.toolCallbacks.RunBeforeTool(
			ctx,
			toolCall.Function.Name,
			toolDeclaration,
			&toolCall.Function.Arguments,
		)
		if callbackErr != nil {
			log.Errorf("Before tool callback failed for %s: %v", toolCall.Function.Name, callbackErr)
			return nil, toolCall.Function.Arguments, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		if customResult != nil {
			// Use custom result from callback.
			return customResult, toolCall.Function.Arguments, nil
		}
	}

	// Execute the actual tool.
	result, err := p.executeTool(ctx, invocation, toolCall, tl, eventChan)
	if err != nil {
		return nil, toolCall.Function.Arguments, err
	}

	// Run after tool callbacks if they exist.
	if p.toolCallbacks != nil {
		customResult, callbackErr := p.toolCallbacks.RunAfterTool(
			ctx,
			toolCall.Function.Name,
			toolDeclaration,
			toolCall.Function.Arguments,
			result,
			err,
		)
		if callbackErr != nil {
			log.Errorf("After tool callback failed for %s: %v", toolCall.Function.Name, callbackErr)
			return nil, toolCall.Function.Arguments, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		if customResult != nil {
			result = customResult
		}
	}
	return result, toolCall.Function.Arguments, nil
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
		log.Errorf("CallableTool execution failed for %s: %v", toolCall.Function.Name, err)
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
		log.Errorf("StreamableTool execution failed for %s: %v", toolCall.Function.Name, err)
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
			log.Errorf("StreamableTool execution failed for %s: receive chunk from stream reader failed: %v, "+
				"may merge incomplete data", toolCall.Function.Name, err)
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
	if len(es) == 0 {
		return nil
	}
	if len(es) == 1 {
		return es[0]
	}

	// Pre-calculate capacity to avoid multiple slice reallocations
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
	eventID := uuid.New().String()

	// Find a valid base event for metadata.
	var baseEvent *event.Event
	for _, e := range es {
		if e != nil {
			baseEvent = e
			break
		}
	}

	// Build response payload with appropriate metadata.
	modelName := "unknown"
	if baseEvent != nil && baseEvent.Response != nil {
		modelName = baseEvent.Response.Model
	}

	resp := &model.Response{
		ID:        eventID,
		Object:    model.ObjectTypeToolResponse,
		Created:   time.Now().Unix(),
		Model:     modelName,
		Choices:   mergedChoices,
		Timestamp: time.Now(),
	}

	// If we have a base event, carry over invocation, author and branch.
	var merged *event.Event
	if baseEvent != nil {
		merged = event.New(
			baseEvent.InvocationID,
			baseEvent.Author,
			event.WithResponse(resp),
		)
	} else {
		// Fallback: construct without base metadata.
		merged = event.New("", "", event.WithResponse(resp))
	}
	// If any child event prefers skipping summarization, propagate it.
	for _, e := range es {
		if e != nil && e.Actions != nil && e.Actions.SkipSummarization {
			if merged.Actions == nil {
				merged.Actions = &event.EventActions{}
			}
			merged.Actions.SkipSummarization = true
			break
		}
	}
	return merged
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
		message = "Task delegated from coordinator"
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
