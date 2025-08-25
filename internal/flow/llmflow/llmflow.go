//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultChannelBufferSize = 256

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

// Options contains configuration options for creating a Flow.
type Options struct {
	ChannelBufferSize   int  // Buffer size for event channels (default: 256)
	EnableParallelTools bool // If true, enable parallel tool execution (default: false, serial execution for safety)
}

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors   []flow.RequestProcessor
	responseProcessors  []flow.ResponseProcessor
	channelBufferSize   int
	enableParallelTools bool
}

// toolResult holds the result of a single tool execution.
type toolResult struct {
	index int
	event *event.Event
}

// New creates a new basic flow instance with the provided processors.
// Processors are immutable after creation.
func New(
	requestProcessors []flow.RequestProcessor,
	responseProcessors []flow.ResponseProcessor,
	opts Options,
) *Flow {
	// Set default channel buffer size if not specified.
	channelBufferSize := opts.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &Flow{
		requestProcessors:   requestProcessors,
		responseProcessors:  responseProcessors,
		channelBufferSize:   channelBufferSize,
		enableParallelTools: opts.EnableParallelTools,
	}
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, f.channelBufferSize) // Configurable buffered channel for events.

	go func() {
		defer close(eventChan)

		for {
			// Check if context is cancelled.
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Run one step (one LLM call cycle).
			lastEvent, err := f.runOneStep(ctx, invocation, eventChan)
			if err != nil {
				var errorEvent *event.Event
				if _, ok := agent.AsStopError(err); ok {
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						agent.ErrorTypeStopAgentError,
						err.Error(),
					)
					log.Errorf("Flow step stopped for agent %s: %v", invocation.AgentName, err)
				} else {
					// Send error event through channel instead of just logging.
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						model.ErrorTypeFlowError,
						err.Error(),
					)
					log.Errorf("Flow step failed for agent %s: %v", invocation.AgentName, err)
				}

				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}

			// Exit conditions.
			if f.isFinalResponse(lastEvent) {
				break
			}

			// Check for invocation end.
			if invocation.EndInvocation {
				break
			}
		}
	}()

	return eventChan, nil
}

// runOneStep executes one step of the flow (one LLM call cycle).
// Returns the last event generated, or nil if no events.
func (f *Flow) runOneStep(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	var lastEvent *event.Event

	// Initialize empty LLM request.
	llmRequest := &model.Request{
		Tools: make(map[string]tool.Tool), // Initialize tools map
	}

	// 1. Preprocess (prepare request).
	f.preprocess(ctx, invocation, llmRequest, eventChan)

	if invocation.EndInvocation {
		return lastEvent, nil
	}

	ctx, span := trace.Tracer.Start(ctx, itelemetry.SpanNameCallLLM)
	defer span.End()

	// 2. Call LLM (get response channel).
	responseChan, err := f.callLLM(ctx, invocation, llmRequest)
	if err != nil {
		return nil, err
	}

	// 3. Process streaming responses.
	return f.processStreamingResponses(ctx, invocation, llmRequest, responseChan, eventChan, span)
}

// processStreamingResponses handles the streaming response processing logic.
func (f *Flow) processStreamingResponses(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	responseChan <-chan *model.Response,
	eventChan chan<- *event.Event,
	span oteltrace.Span,
) (*event.Event, error) {
	var lastEvent *event.Event

	for response := range responseChan {
		// Handle after model callbacks.
		customResp, err := f.handleAfterModelCallbacks(ctx, invocation, llmRequest, response, eventChan)
		if err != nil {
			return lastEvent, err
		}
		if customResp != nil {
			response = customResp
		}

		// 4. Create and send LLM response using the clean constructor.
		llmResponseEvent := f.createLLMResponseEvent(invocation, response, llmRequest)
		eventChan <- llmResponseEvent

		// 5. Check context cancellation.
		if err := f.checkContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		// 6. Postprocess response.
		f.postprocess(ctx, invocation, response, eventChan)
		if err := f.checkContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		itelemetry.TraceCallLLM(span, invocation, llmRequest, response, llmResponseEvent.ID)

		// 7. Handle function calls if present in the response.
		if f.hasToolCalls(response) {
			functionResponseEvent, err := f.handleFunctionCallsAndSendEvent(ctx, invocation, llmResponseEvent, llmRequest.Tools, eventChan)
			if err != nil {
				return lastEvent, err
			}
			if functionResponseEvent != nil {
				lastEvent = functionResponseEvent
				if err := f.checkContextCancelled(ctx); err != nil {
					return lastEvent, err
				}

				// Wait for completion if required.
				if err := f.waitForCompletion(ctx, invocation, lastEvent); err != nil {
					return lastEvent, err
				}
			}
		}
	}

	return lastEvent, nil
}

// handleAfterModelCallbacks processes after model callbacks.
func (f *Flow) handleAfterModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	response *model.Response,
	eventChan chan<- *event.Event,
) (*model.Response, error) {
	customResp, err := runAfterModelCallbacks(ctx, invocation, llmRequest, response)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, err
		}

		log.Errorf("After model callback failed for agent %s: %v", invocation.AgentName, err)
		lastEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		)
		eventChan <- lastEvent
		return nil, err
	}
	return customResp, nil
}

// createLLMResponseEvent creates a new LLM response event.
func (f *Flow) createLLMResponseEvent(invocation *agent.Invocation, response *model.Response, llmRequest *model.Request) *event.Event {
	llmResponseEvent := event.New(invocation.InvocationID, invocation.AgentName, event.WithResponse(response), event.WithBranch(invocation.Branch))
	if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
		llmResponseEvent.LongRunningToolIDs = collectLongRunningToolIDs(response.Choices[0].Message.ToolCalls, llmRequest.Tools)
	}
	return llmResponseEvent
}

// hasToolCalls checks if the response contains tool calls.
func (f *Flow) hasToolCalls(response *model.Response) bool {
	return len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0
}

// waitForCompletion waits for event completion if required.
func (f *Flow) waitForCompletion(ctx context.Context, invocation *agent.Invocation, lastEvent *event.Event) error {
	if !lastEvent.RequiresCompletion {
		return nil
	}

	select {
	case completedID := <-invocation.EventCompletionCh:
		if completedID == lastEvent.CompletionID {
			log.Debugf("Tool response event %s completed, proceeding with next LLM call", completedID)
		}
	case <-time.After(eventCompletionTimeout):
		log.Warnf("Timeout waiting for completion of event %s", lastEvent.CompletionID)
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func runAfterModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	response *model.Response,
) (*model.Response, error) {
	if cb := invocation.ModelCallbacks; cb == nil {
		return response, nil
	}
	return invocation.ModelCallbacks.RunAfterModel(ctx, req, response, nil)
}

// handleFunctionCallsAndSendEvent handles function calls and sends the resulting event to the channel.
func (f *Flow) handleFunctionCallsAndSendEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmEvent *event.Event,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	functionResponseEvent, err := f.handleFunctionCalls(
		ctx,
		invocation,
		llmEvent,
		tools,
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

// checkContextCancelled checks if the context is cancelled and returns error if so.
func (f *Flow) checkContextCancelled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// preprocess handles pre-LLM call preparation using request processors.
func (f *Flow) preprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
) {
	// Run request processors - they send events directly to the channel.
	for _, processor := range f.requestProcessors {
		processor.ProcessRequest(ctx, invocation, llmRequest, eventChan)
	}

	// Add tools to the request.
	if invocation.Agent != nil {
		for _, t := range invocation.Agent.Tools() {
			llmRequest.Tools[t.Declaration().Name] = t
		}
	}
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) (<-chan *model.Response, error) {
	if invocation.Model == nil {
		return nil, errors.New("no model available for LLM call")
	}

	log.Debugf("Calling LLM for agent %s", invocation.AgentName)

	// Run before model callbacks if they exist.
	if invocation.ModelCallbacks != nil {
		customResponse, err := invocation.ModelCallbacks.RunBeforeModel(ctx, llmRequest)
		if err != nil {
			log.Errorf("Before model callback failed for agent %s: %v", invocation.AgentName, err)
			return nil, err
		}
		if customResponse != nil {
			// Create a channel that returns the custom response and then closes.
			responseChan := make(chan *model.Response, 1)
			responseChan <- customResponse
			close(responseChan)
			return responseChan, nil
		}
	}
	// Call the model.
	responseChan, err := invocation.Model.GenerateContent(ctx, llmRequest)
	if err != nil {
		log.Errorf("LLM call failed for agent %s: %v", invocation.AgentName, err)
		return nil, err
	}

	return responseChan, nil
}

// handleFunctionCalls executes function calls and creates function response events.
func (f *Flow) handleFunctionCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	functionCallEvent *event.Event,
	tools map[string]tool.Tool,
) (*event.Event, error) {
	if functionCallEvent.Response == nil || len(functionCallEvent.Response.Choices) == 0 {
		return nil, nil
	}

	var toolCallResponsesEvents []*event.Event
	toolCalls := functionCallEvent.Response.Choices[0].Message.ToolCalls

	// If parallel tools are enabled AND multiple tool calls, execute concurrently
	if f.enableParallelTools && len(toolCalls) > 1 {
		return f.executeToolCallsInParallel(ctx, invocation, functionCallEvent, toolCalls, tools)
	}

	// Execute each tool call.
	for i, toolCall := range toolCalls {
		if err := func(index int, toolCall model.ToolCall) error {
			ctxWithInvocation, span := trace.Tracer.Start(ctx,
				fmt.Sprintf("%s %s", itelemetry.SpanNamePrefixExecuteTool, toolCall.Function.Name))
			defer span.End()
			choice, err := f.executeToolCall(ctxWithInvocation, invocation, toolCall, tools, i)
			if err != nil {
				return err
			}
			if choice == nil {
				return nil
			}
			choice.Message.ToolName = toolCall.Function.Name
			toolCallResponseEvent := newToolCallResponseEvent(invocation, functionCallEvent, []model.Choice{*choice})
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
		mergedEvent = newToolCallResponseEvent(invocation, functionCallEvent, nil)
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallResponsesEvents)
	}

	// Signal that this event needs to be completed before proceeding.
	mergedEvent.RequiresCompletion = true
	mergedEvent.CompletionID = uuid.New().String()
	if len(toolCallResponsesEvents) > 1 {
		_, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s (merged)", itelemetry.SpanNamePrefixExecuteTool))
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
	}

	return mergedEvent, nil
}

// executeToolCallsInParallel executes multiple tool calls concurrently using goroutines.
func (f *Flow) executeToolCallsInParallel(
	ctx context.Context,
	invocation *agent.Invocation,
	functionCallEvent *event.Event,
	toolCalls []model.ToolCall,
	tools map[string]tool.Tool,
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
					errorChoice := f.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution panic: %v", r))
					errorChoice.Message.ToolName = tc.Function.Name
					errorEvent := newToolCallResponseEvent(invocation, functionCallEvent, []model.Choice{*errorChoice})
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

			choice, err := f.executeToolCall(ctxWithInvocation, invocation, tc, tools, index)
			if err != nil {
				log.Errorf("Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
					tc.Function.Name, index, tc.ID, invocation.AgentName, err)
				// Send error result to channel.
				errorChoice := f.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution error: %v", err))
				errorChoice.Message.ToolName = tc.Function.Name
				errorEvent := newToolCallResponseEvent(invocation, functionCallEvent, []model.Choice{*errorChoice})
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
			toolCallResponseEvent := newToolCallResponseEvent(invocation, functionCallEvent, []model.Choice{*choice})

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
	toolCallResponsesEvents := f.collectParallelToolResults(ctx, resultChan, done, len(toolCalls))

	var mergedEvent *event.Event
	if len(toolCallResponsesEvents) == 0 {
		mergedEvent = newToolCallResponseEvent(invocation, functionCallEvent, nil)
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallResponsesEvents)
	}

	// Signal that this event needs to be completed before proceeding.
	mergedEvent.RequiresCompletion = true
	mergedEvent.CompletionID = uuid.New().String()
	if len(toolCallResponsesEvents) > 1 {
		_, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s (merged)", itelemetry.SpanNamePrefixExecuteTool))
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
	}

	return mergedEvent, nil
}

// collectParallelToolResults collects results from the result channel and filters out nil events.
func (f *Flow) collectParallelToolResults(
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
				return f.filterNilEvents(results)
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
			return f.filterNilEvents(results)
		case <-done:
			// All goroutines completed.
			return f.filterNilEvents(results)
		}
	}
}

// filterNilEvents filters out nil events from a slice of events while preserving order.
// Pre-allocates capacity to avoid multiple memory allocations.
func (f *Flow) filterNilEvents(results []*event.Event) []*event.Event {
	// Pre-allocate with capacity to reduce allocations
	filtered := make([]*event.Event, 0, len(results))
	for _, event := range results {
		if event != nil {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func collectLongRunningToolIDs(ToolCalls []model.ToolCall, tools map[string]tool.Tool) map[string]struct{} {
	longRunningToolIDs := make(map[string]struct{})
	for _, toolCall := range ToolCalls {
		t, ok := tools[toolCall.Function.Name]
		if !ok {
			continue
		}
		caller, ok := t.(function.LongRunner)
		if !ok {
			continue
		}
		if caller.LongRunning() {
			longRunningToolIDs[toolCall.ID] = struct{}{}
		}
	}
	return longRunningToolIDs
}

// executeToolCall executes a single tool call and returns the choice.
func (f *Flow) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
) (*model.Choice, error) {
	// Check if tool exists.
	tl, exists := tools[toolCall.Function.Name]
	if !exists {
		log.Errorf("CallableTool %s not found (agent=%s, model=%s)",
			toolCall.Function.Name, invocation.AgentName, invocation.Model.Info().Name)
		return f.createErrorChoice(index, toolCall.ID, ErrorToolNotFound), nil
	}

	log.Debugf("Executing tool %s with args: %s", toolCall.Function.Name, string(toolCall.Function.Arguments))

	// Execute the tool with callbacks.
	result, err := f.executeToolWithCallbacks(ctx, invocation, toolCall, tl)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, err
		}
		return f.createErrorChoice(index, toolCall.ID, err.Error()), nil
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
		return f.createErrorChoice(index, toolCall.ID, ErrorMarshalResult), nil
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

// executeToolWithCallbacks executes a tool with before/after callbacks.
func (f *Flow) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
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
	result, err := f.executeTool(ctx, toolCall, tl)
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

// executeTool executes the actual tool based on its type.
func (f *Flow) executeTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.Tool,
) (any, error) {
	switch t := tl.(type) {
	case tool.CallableTool:
		return f.executeCallableTool(ctx, toolCall, t)
	case tool.StreamableTool:
		return f.executeStreamableTool(ctx, toolCall, t)
	default:
		return nil, fmt.Errorf("unsupported tool type: %T", tl)
	}
}

// executeCallableTool executes a callable tool.
func (f *Flow) executeCallableTool(
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
func (f *Flow) executeStreamableTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.StreamableTool,
) (any, error) {
	reader, err := tl.StreamableCall(ctx, toolCall.Function.Arguments)
	if err != nil {
		log.Errorf("StreamableTool execution failed for %s: %v", toolCall.Function.Name, err)
		return nil, fmt.Errorf("%s: %w", ErrorStreamableToolExecution, err)
	}
	defer reader.Close()

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
		contents = append(contents, chunk.Content)
	}

	return tool.Merge(contents), nil
}

// createErrorChoice creates an error choice for tool execution failures.
func (f *Flow) createErrorChoice(index int, toolID string, errorMsg string) *model.Choice {
	return &model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: errorMsg,
			ToolID:  toolID,
		},
	}
}

func newToolCallResponseEvent(
	invocation *agent.Invocation,
	functionCallEvent *event.Event,
	functionResponses []model.Choice) *event.Event {
	// Generate a proper unique ID.
	eventID := uuid.New().String()
	// Create function response event.
	return &event.Event{
		Response: &model.Response{
			ID:        eventID,
			Object:    model.ObjectTypeToolResponse,
			Created:   time.Now().Unix(),
			Model:     functionCallEvent.Response.Model,
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

	// Fallback if all events are nil (should not happen in normal flow).
	if baseEvent == nil {
		return &event.Event{
			Response: &model.Response{
				ID:        eventID,
				Object:    model.ObjectTypeToolResponse,
				Created:   time.Now().Unix(),
				Model:     "unknown",
				Choices:   mergedChoices,
				Timestamp: time.Now(),
			},
			ID:        eventID,
			Timestamp: time.Now(),
		}
	}

	return &event.Event{
		Response: &model.Response{
			ID:      eventID,
			Object:  model.ObjectTypeToolResponse,
			Created: time.Now().Unix(),
			Model: func() string {
				if baseEvent.Response != nil {
					return baseEvent.Response.Model
				}
				return "unknown"
			}(),
			Choices:   mergedChoices,
			Timestamp: time.Now(),
		},
		InvocationID: baseEvent.InvocationID,
		Author:       baseEvent.Author,
		ID:           eventID,
		Timestamp:    baseEvent.Timestamp, // Use the base event as the timestamp
		Branch:       baseEvent.Branch,
	}
}

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) {
	if llmResponse == nil {
		return
	}

	// Run response processors - they send events directly to the channel.
	for _, processor := range f.responseProcessors {
		processor.ProcessResponse(ctx, invocation, llmResponse, eventChan)
	}
}

// isFinalResponse determines if the event represents a final response.
func (f *Flow) isFinalResponse(evt *event.Event) bool {
	if evt == nil {
		return true
	}

	if evt.Object == model.ObjectTypeToolResponse {
		return false
	}

	// Consider response final if it's marked as done and has content or error.
	return evt.Done && (len(evt.Choices) > 0 || evt.Error != nil)
}
