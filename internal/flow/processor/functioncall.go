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
type FunctionCallResponseProcessor struct{}

// NewFunctionCallResponseProcessor creates a new transfer response processor.
func NewFunctionCallResponseProcessor() *FunctionCallResponseProcessor {
	return &FunctionCallResponseProcessor{}
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

	functionResponseEvent, err := p.handleFunctionCallsAndSendEvent(ctx, invocation, rsp, req.Tools, ch)
	// TODO 这里需要
	if err != nil {
		emitErrEvent(ctx, invocation, err, ch)
		return
	}

	if functionResponseEvent == nil {
		return
	}

	if err := p.checkContextCancelled(ctx); err != nil {
		emitErrEvent(ctx, invocation, err, ch)
		return
	}

	// Wait for completion if required.
	if err := p.waitForCompletion(ctx, invocation, functionResponseEvent); err != nil {
		emitErrEvent(ctx, invocation, err, ch)
		return
	}
}

func (p *FunctionCallResponseProcessor) handleFunctionCallsAndSendEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	functionResponseEvent, err := p.handleFunctionCalls(
		ctx,
		invocation,
		functionCallResponse,
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

// handleFunctionCalls executes function calls and creates function response events.
func (p *FunctionCallResponseProcessor) handleFunctionCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	tools map[string]tool.Tool,
) (*event.Event, error) {
	if functionCallResponse == nil || len(functionCallResponse.Choices) == 0 {
		return nil, nil
	}

	var toolCallResponsesEvents []*event.Event
	toolCalls := functionCallResponse.Choices[0].Message.ToolCalls

	// If parallel tools are enabled AND multiple tool calls, execute concurrently
	if false && len(toolCalls) > 1 {
		return p.executeToolCallsInParallel(ctx, invocation, functionCallResponse, toolCalls, tools)
	}

	// Execute each tool call.
	for i, toolCall := range toolCalls {
		if err := func(index int, toolCall model.ToolCall) error {
			ctxWithInvocation, span := trace.Tracer.Start(ctx,
				fmt.Sprintf("%s %s", itelemetry.SpanNamePrefixExecuteTool, toolCall.Function.Name))
			defer span.End()
			choice, err := p.executeToolCall(ctxWithInvocation, invocation, toolCall, tools, i)
			if err != nil {
				return err
			}
			if choice == nil {
				return nil
			}
			choice.Message.ToolName = toolCall.Function.Name
			toolCallResponseEvent := newToolCallResponseEvent(invocation, functionCallResponse,
				[]model.Choice{*choice})
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
		mergedEvent = newToolCallResponseEvent(invocation, functionCallResponse, nil)
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
func (p *FunctionCallResponseProcessor) executeToolCallsInParallel(
	ctx context.Context,
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
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
					errorChoice := p.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution panic: %v", r))
					errorChoice.Message.ToolName = tc.Function.Name
					errorEvent := newToolCallResponseEvent(invocation, functionCallResponse, []model.Choice{*errorChoice})
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

			choice, err := p.executeToolCall(ctxWithInvocation, invocation, tc, tools, index)
			if err != nil {
				log.Errorf("Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
					tc.Function.Name, index, tc.ID, invocation.AgentName, err)
				// Send error result to channel.
				errorChoice := p.createErrorChoice(index, tc.ID, fmt.Sprintf("tool execution error: %v", err))
				errorChoice.Message.ToolName = tc.Function.Name
				errorEvent := newToolCallResponseEvent(invocation, functionCallResponse,
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
			toolCallResponseEvent := newToolCallResponseEvent(invocation, functionCallResponse,
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
		mergedEvent = newToolCallResponseEvent(invocation, functionCallResponse, nil)
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

// executeToolCall executes a single tool call and returns the choice.
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
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
	result, err := p.executeToolWithCallbacks(ctx, invocation, toolCall, tl)
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
	result, err := p.executeTool(ctx, toolCall, tl)
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
func (p *FunctionCallResponseProcessor) executeTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.Tool,
) (any, error) {
	switch t := tl.(type) {
	case tool.CallableTool:
		return p.executeCallableTool(ctx, toolCall, t)
	case tool.StreamableTool:
		return p.executeStreamableTool(ctx, toolCall, t)
	default:
		return nil, fmt.Errorf("unsupported tool type: %T", tl)
	}
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
func (p *FunctionCallResponseProcessor) executeStreamableTool(
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

// checkContextCancelled checks if the context is cancelled and returns error if so.
func (p *FunctionCallResponseProcessor) checkContextCancelled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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

func emitErrEvent(ctx context.Context, invocation *agent.Invocation, err error,
	eventChan chan<- *event.Event) {
	if err == nil {
		return
	}

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
