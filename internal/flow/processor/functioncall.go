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

	// Timeout for event completion signaling.
	eventCompletionTimeout = 5 * time.Second
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
}

// NewFunctionCallResponseProcessor creates a new transfer response processor.
func NewFunctionCallResponseProcessor(enableParallelTools bool) *FunctionCallResponseProcessor {
	return &FunctionCallResponseProcessor{
		enableParallelTools: enableParallelTools,
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
	if !rsp.HasToolCalls() {
		return
	}

	functioncallResponseEvent, err := p.handleFunctionCallsAndSendEvent(ctx, invocation, rsp, req.Tools, ch)
	if err != nil || functioncallResponseEvent == nil {
		return
	}

	// Wait for completion if required.
	if err := p.waitForCompletion(ctx, invocation, functioncallResponseEvent); err != nil {
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		)
		select {
		case ch <- errorEvent:
		case <-ctx.Done():
		}
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
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		)
		select {
		case eventChan <- errorEvent:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, err
	} else if functionResponseEvent != nil {
		select {
		case eventChan <- functionResponseEvent:
		case <-ctx.Done():
			return functionResponseEvent, ctx.Err()
		}
	}
	return functionResponseEvent, nil
}

// handleFunctionCalls executes function calls and creates function response events.
func (p *FunctionCallResponseProcessor) handleFunctionCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	if llmResponse == nil || len(llmResponse.Choices) == 0 {
		return nil, nil
	}

	var toolCallResponsesEvents []*event.Event
	toolCalls := llmResponse.Choices[0].Message.ToolCalls

	// If parallel tools are enabled AND multiple tool calls, execute concurrently
	if p.enableParallelTools && len(toolCalls) > 1 {
		return p.executeToolCallsInParallel(ctx, invocation, llmResponse, toolCalls, tools, eventChan)
	}

	// Execute each tool call.
	for i, toolCall := range toolCalls {
		if err := func(index int, toolCall model.ToolCall) error {
			ctxWithInvocation, span := trace.Tracer.Start(ctx,
				fmt.Sprintf("%s %s", itelemetry.SpanNamePrefixExecuteTool, toolCall.Function.Name))
			defer span.End()
			choice, err := p.executeToolCall(ctxWithInvocation, invocation, toolCall, tools, i, eventChan)
			if err != nil {
				return err
			}
			if choice == nil {
				return nil
			}
			choice.Message.ToolName = toolCall.Function.Name
			toolCallResponseEvent := newToolCallResponseEvent(invocation, llmResponse,
				[]model.Choice{*choice})

			if tl, ok := tools[toolCall.Function.Name]; ok {
				if skipper, ok2 := tl.(summarizationSkipper); ok2 && skipper.SkipSummarization() {
					if toolCallResponseEvent.Actions == nil {
						toolCallResponseEvent.Actions = &event.EventActions{}
					}
					toolCallResponseEvent.Actions.SkipSummarization = true
				}
			}
			toolCallResponsesEvents = append(toolCallResponsesEvents, toolCallResponseEvent)
			tl, ok := tools[toolCall.Function.Name]
			var declaration *tool.Declaration
			if !ok {
				declaration = &tool.Declaration{
					Name:        "<not found>",
					Description: "<not found>",
				}
			} else {
				declaration = tl.Declaration()
			}
			itelemetry.TraceToolCall(span, declaration, toolCall.Function.Arguments, toolCallResponseEvent)
			return nil
		}(i, toolCall); err != nil {
			return nil, err
		}
	}

	var mergedEvent *event.Event
	if len(toolCallResponsesEvents) == 0 {
		// No explicit tool result events (likely forwarded inner events).
		// Create minimal tool response messages so the next LLM call has
		// required tool messages following tool_calls.
		minimalChoices := make([]model.Choice, 0, len(toolCalls))
		for _, tc := range toolCalls {
			minimalChoices = append(minimalChoices, model.Choice{
				Index: 0,
				Message: model.Message{
					Role:   model.RoleTool,
					ToolID: tc.ID,
					// Keep Content empty to avoid UI duplication; presence is enough for API.
				},
			})
		}
		mergedEvent = newToolCallResponseEvent(invocation, llmResponse, minimalChoices)
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallResponsesEvents)
	}

	// Signal that this event needs to be completed before proceeding.
	mergedEvent.RequiresCompletion = true
	if len(toolCallResponsesEvents) > 1 {
		_, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s (merged)", itelemetry.SpanNamePrefixExecuteTool))
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
	}

	return mergedEvent, nil
}

// executeToolCallsInParallel executes multiple tool calls concurrently using goroutines.
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

	// Start goroutines for concurrent execution.
	for i, toolCall := range toolCalls {
		wg.Add(1)
		go func(index int, tc model.ToolCall) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("Tool execution panic for %s (index: %d, ID: %s, agent: %s): %v",
						tc.Function.Name, index, tc.ID, invocation.AgentName, r)
					// Send error result to channel.
					errorChoice := p.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution panic: %v", r))
					errorChoice.Message.ToolName = tc.Function.Name
					errorEvent := newToolCallResponseEvent(invocation, llmResponse, []model.Choice{*errorChoice})
					select {
					case resultChan <- toolResult{index: index, event: errorEvent}:
					case <-ctx.Done():
						// Context cancelled, don't block.
					}
				}
			}()

			ctxWithInvocation, span := trace.Tracer.Start(ctx,
				fmt.Sprintf("%s %s", itelemetry.SpanNamePrefixExecuteTool, tc.Function.Name))
			defer span.End()

			choice, err := p.executeToolCall(ctxWithInvocation, invocation, tc, tools, index, eventChan)
			if err != nil {
				log.Errorf("Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
					tc.Function.Name, index, tc.ID, invocation.AgentName, err)
				// Send error result to channel.
				errorChoice := p.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution error: %v", err))
				errorChoice.Message.ToolName = tc.Function.Name
				errorEvent := newToolCallResponseEvent(invocation, llmResponse,
					[]model.Choice{*errorChoice})
				select {
				case resultChan <- toolResult{index: index, event: errorEvent}:
				case <-ctx.Done():
					// Context cancelled, don't block.
				}
				return
			}
			if choice == nil {
				// For LongRunning tools that return nil, we still need to send a placeholder.
				select {
				case resultChan <- toolResult{index: index, event: nil}:
				case <-ctx.Done():
					// Context cancelled, don't block.
				}
				return
			}

			choice.Message.ToolName = tc.Function.Name
			toolCallResponseEvent := newToolCallResponseEvent(invocation, llmResponse,
				[]model.Choice{*choice})

			tl, ok := tools[tc.Function.Name]
			var declaration *tool.Declaration
			if !ok {
				declaration = &tool.Declaration{
					Name:        "<not found>",
					Description: "<not found>",
				}
			} else {
				declaration = tl.Declaration()
			}
			itelemetry.TraceToolCall(span, declaration, tc.Function.Arguments, toolCallResponseEvent)

			// Send result to channel with context cancellation support.
			select {
			case resultChan <- toolResult{
				index: index,
				event: toolCallResponseEvent,
			}:
			case <-ctx.Done():
				// Context cancelled, don't block on channel send.
			}
		}(i, toolCall)
	}

	// Wait for all goroutines to complete with context cancellation support.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultChan)
		close(done)
	}()

	// Collect results and maintain original order.
	toolCallResponsesEvents := p.collectParallelToolResults(ctx, resultChan, done, len(toolCalls))

	var mergedEvent *event.Event
	if len(toolCallResponsesEvents) == 0 {
		// No explicit tool result events (likely forwarded inner events).
		// Create minimal tool response messages so the next LLM call has
		// required tool messages following tool_calls.
		minimalChoices := make([]model.Choice, 0, len(toolCalls))
		for _, tc := range toolCalls {
			minimalChoices = append(minimalChoices, model.Choice{
				Index: 0,
				Message: model.Message{
					Role:   model.RoleTool,
					ToolID: tc.ID,
				},
			})
		}
		mergedEvent = newToolCallResponseEvent(invocation, llmResponse, minimalChoices)
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallResponsesEvents)
	}

	// Signal that this event needs to be completed before proceeding.
	mergedEvent.RequiresCompletion = true
	if len(toolCallResponsesEvents) > 1 {
		_, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s (merged)", itelemetry.SpanNamePrefixExecuteTool))
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
	}

	return mergedEvent, nil
}

// executeToolCall executes a single tool call and returns the choice.
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
	eventChan chan<- *event.Event,
) (*model.Choice, error) {
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
			return p.createErrorChoice(index, toolCall.ID, ErrorToolNotFound), nil
		}
	}

	log.Debugf("Executing tool %s with args: %s", toolCall.Function.Name, string(toolCall.Function.Arguments))

	// Execute the tool with callbacks.
	result, err := p.executeToolWithCallbacks(ctx, invocation, toolCall, tl, eventChan)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, err
		}
		return p.createErrorChoice(index, toolCall.ID, err.Error()), nil
	}
	//  allow to return nil not provide function response.
	if r, ok := tl.(function.LongRunner); ok && r.LongRunning() {
		if result == nil {
			return nil, nil
		}
	}

	// Marshal the result to JSON.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Errorf("Failed to marshal tool result for %s: %v", toolCall.Function.Name, err)
		return p.createErrorChoice(index, toolCall.ID, ErrorMarshalResult), nil
	}

	log.Debugf("CallableTool %s executed successfully, result: %s", toolCall.Function.Name, string(resultBytes))

	return &model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: string(resultBytes),
			ToolID:  toolCall.ID,
		},
	}, nil
}

// waitForCompletion waits for event completion if required.
func (p *FunctionCallResponseProcessor) waitForCompletion(ctx context.Context, invocation *agent.Invocation, lastEvent *event.Event) error {
	if !lastEvent.RequiresCompletion {
		return nil
	}

	completionID := agent.AppendEventNoticeKeyPrefix + lastEvent.ID
	select {
	case <-invocation.AddNoticeChannel(ctx, completionID):
	case <-time.After(eventCompletionTimeout):
		log.Warnf("Timeout waiting for completion of event %s", lastEvent.ID)
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
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

// collectParallelToolResults collects results from the result channel and filters out nil events.
func (p *FunctionCallResponseProcessor) collectParallelToolResults(
	ctx context.Context,
	resultChan <-chan toolResult,
	done <-chan struct{},
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
			// Add bounds checking to prevent index out of range
			if result.index >= 0 && result.index < len(results) {
				results[result.index] = result.event
			} else {
				log.Errorf("Tool result index %d out of range [0, %d)", result.index, len(results))
			}
		case <-ctx.Done():
			// Context cancelled, stop waiting for more results.
			log.Warnf("Context cancelled while waiting for tool results")
			return p.filterNilEvents(results)
		case <-done:
			// All goroutines completed.
			return p.filterNilEvents(results)
		}
	}
}

// executeToolWithCallbacks executes a tool with before/after callbacks.
func (p *FunctionCallResponseProcessor) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (any, error) {
	toolDeclaration := tl.Declaration()
	// Run before tool callbacks if they exist.
	if invocation.ToolCallbacks != nil {
		customResult, callbackErr := invocation.ToolCallbacks.RunBeforeTool(
			ctx,
			toolCall.Function.Name,
			toolDeclaration,
			toolCall.Function.Arguments,
		)
		if callbackErr != nil {
			log.Errorf("Before tool callback failed for %s: %v", toolCall.Function.Name, callbackErr)
			return nil, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		if customResult != nil {
			// Use custom result from callback.
			return customResult, nil
		}
	}

	// Execute the actual tool.
	result, err := p.executeTool(ctx, invocation, toolCall, tl, eventChan)
	if err != nil {
		return nil, err
	}

	// Run after tool callbacks if they exist.
	if invocation.ToolCallbacks != nil {
		customResult, callbackErr := invocation.ToolCallbacks.RunAfterTool(
			ctx,
			toolCall.Function.Name,
			toolDeclaration,
			toolCall.Function.Arguments,
			result,
			err,
		)
		if callbackErr != nil {
			log.Errorf("After tool callback failed for %s: %v", toolCall.Function.Name, callbackErr)
			return nil, fmt.Errorf("tool callback error: %w", callbackErr)
		}
		if customResult != nil {
			result = customResult
		}
	}
	return result, nil
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
	// Prefer streaming execution if the tool supports it.
	if isStreamable(tl) {
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

// normalizeInnerEvent ensures invocation ID and branch are set for inner events.
func (f *FunctionCallResponseProcessor) normalizeInnerEvent(ev *event.Event, inv *agent.Invocation) {
	if ev.InvocationID == "" {
		ev.InvocationID = inv.InvocationID
	}
	if ev.Branch == "" {
		ev.Branch = inv.Branch
	}
}

// shouldForwardInnerEvent suppresses forwarding of the inner agent's final full
// content to avoid duplicate large blocks in the parent transcript. We still
// aggregate its text from deltas for the final tool.response content.
func (f *FunctionCallResponseProcessor) shouldForwardInnerEvent(ev *event.Event) bool {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		ch := ev.Response.Choices[0]
		if ch.Delta.Content == "" && ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" && !ev.Response.IsPartial {
			return false
		}
	}
	return true
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
		event.WithBranch(inv.Branch),
	)
}

// trySendEvent attempts to send an event to the channel without blocking.
func (f *FunctionCallResponseProcessor) trySendEvent(
	ctx context.Context,
	ch chan<- *event.Event,
	e *event.Event,
) error {
	if ch == nil {
		return nil
	}
	select {
	case ch <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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
	// Generate a proper unique ID.
	eventID := uuid.New().String()
	// Create function response event.
	return &event.Event{
		Response: &model.Response{
			ID:        eventID,
			Object:    model.ObjectTypeToolResponse,
			Created:   time.Now().Unix(),
			Model:     functionCallResponse.Model,
			Choices:   functionResponses,
			Timestamp: time.Now(),
		},
		InvocationID: invocation.InvocationID,
		Author:       invocation.AgentName,
		ID:           eventID,
		Timestamp:    time.Now(),
		Branch:       invocation.Branch, // Set branch for hierarchical event filtering.
	}
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
			event.WithBranch(baseEvent.Branch),
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
		AgentName:     originalName,
		Message:       message,
		EndInvocation: false,
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
		f.normalizeInnerEvent(ev, invocation)
		if f.shouldForwardInnerEvent(ev) {
			if f.trySendEvent(ctx, eventChan, ev) != nil {
				return ctx.Err()
			}
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
		if f.trySendEvent(ctx, eventChan, partial) != nil {
			return ctx.Err()
		}
	}
	return nil
}
