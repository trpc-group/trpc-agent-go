//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
	ChannelBufferSize int // Buffer size for event channels (default: 256)
}

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors  []flow.RequestProcessor
	responseProcessors []flow.ResponseProcessor
	channelBufferSize  int
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
		requestProcessors:  requestProcessors,
		responseProcessors: responseProcessors,
		channelBufferSize:  channelBufferSize,
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
				// Send error event through channel instead of just logging.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				)
				log.Errorf("Flow step failed for agent %s: %v", invocation.AgentName, err)

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

	ctx, span := trace.Tracer.Start(ctx, "call_llm")
	defer span.End()
	// 2. Call LLM (get response channel).
	responseChan, err := f.callLLM(ctx, invocation, llmRequest)
	if err != nil {
		return nil, err
	}

	// 3. Process streaming responses.
	for response := range responseChan {
		// Create event from response using the clean constructor.
		llmEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, response)
		// Set branch for hierarchical event filtering.
		llmEvent.Branch = invocation.Branch
		log.Debugf("Received LLM response chunk for agent %s, done=%t", invocation.AgentName, response.Done)
		itelemetry.TraceCallLLM(span, invocation, llmRequest, response, llmEvent.ID)
		// Run after model callbacks if they exist.
		if invocation.ModelCallbacks != nil {
			customResponse, err := invocation.ModelCallbacks.RunAfterModel(ctx, response, nil)
			if err != nil {
				log.Errorf("After model callback failed for agent %s: %v", invocation.AgentName, err)
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				)
				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
					return lastEvent, ctx.Err()
				}
				return lastEvent, err
			}
			if customResponse != nil {
				response = customResponse
				llmEvent.Response = response
			}
		}

		// Send the LLM response event.
		lastEvent = llmEvent
		select {
		case eventChan <- llmEvent:
		case <-ctx.Done():
			return lastEvent, ctx.Err()
		}
		// Check if wrappedChan is closed, prevent infinite writing.
		if ctx.Err() != nil {
			return lastEvent, ctx.Err()
		}

		// 4. Handle function calls if present in the response.
		if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
			functionResponseEvent, err := f.handleFunctionCalls(
				ctx,
				invocation,
				llmEvent,
				llmRequest.Tools,
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
					return lastEvent, ctx.Err()
				}
			} else if functionResponseEvent != nil {
				lastEvent = functionResponseEvent
				select {
				case eventChan <- functionResponseEvent:
				case <-ctx.Done():
					return lastEvent, ctx.Err()
				}
				// Check if wrappedChan is closed, prevent infinite writing.
				if ctx.Err() != nil {
					return lastEvent, ctx.Err()
				}
				// Wait for completion of events that require it.
				if lastEvent.RequiresCompletion {
					select {
					case completedID := <-invocation.EventCompletionCh:
						if completedID == lastEvent.CompletionID {
							log.Debugf("Tool response event %s completed, proceeding with next LLM call", completedID)
						}
					case <-time.After(eventCompletionTimeout):
						log.Warnf("Timeout waiting for completion of event %s", lastEvent.CompletionID)
					case <-ctx.Done():
						return lastEvent, ctx.Err()
					}
				}
			}
		}

		// 5. Postprocess each response.
		f.postprocess(ctx, invocation, response, eventChan)

		// Check context cancellation.
		select {
		case <-ctx.Done():
			return lastEvent, ctx.Err()
		default:
		}
	}
	return lastEvent, nil
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
	// Execute each tool call.
	for i, toolCall := range functionCallEvent.Response.Choices[0].Message.ToolCalls {
		ctxWithInvocation := agent.NewContextWithInvocation(ctx, invocation)
		ctxWithInvocation, span := trace.Tracer.Start(ctx, fmt.Sprintf("execute_tool %s", toolCall.Function.Name))
		choice := f.executeToolCall(ctxWithInvocation, invocation, toolCall, tools, i)
		toolCallResponseEvent := newToolCallResponseEvent(invocation, functionCallEvent, []model.Choice{choice})
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
		span.End()
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
		_, span := trace.Tracer.Start(ctx, "execute_tool (merged)")
		itelemetry.TraceMergedToolCalls(span, mergedEvent)
		span.End()
	}

	return mergedEvent, nil
}

// executeToolCall executes a single tool call and returns the choice.
func (f *Flow) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
) model.Choice {
	// Check if tool exists.
	tl, exists := tools[toolCall.Function.Name]
	if !exists {
		log.Errorf("CallableTool %s not found", toolCall.Function.Name)
		return f.createErrorChoice(index, toolCall.ID, ErrorToolNotFound)
	}

	log.Debugf("Executing tool %s with args: %s", toolCall.Function.Name, string(toolCall.Function.Arguments))

	// Execute the tool with callbacks.
	result, err := f.executeToolWithCallbacks(ctx, invocation, toolCall, tl)
	if err != nil {
		return f.createErrorChoice(index, toolCall.ID, err.Error())
	}

	// Marshal the result to JSON.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Errorf("Failed to marshal tool result for %s: %v", toolCall.Function.Name, err)
		return f.createErrorChoice(index, toolCall.ID, ErrorMarshalResult)
	}

	log.Debugf("CallableTool %s executed successfully, result: %s", toolCall.Function.Name, string(resultBytes))

	return model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: string(resultBytes),
			ToolID:  toolCall.ID,
		},
	}
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
func (f *Flow) createErrorChoice(index int, toolID string, errorMsg string) model.Choice {
	return model.Choice{
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
			ID:        "tool-response-" + eventID,
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

	mergedChoices := make([]model.Choice, len(es))
	for _, e := range es {
		mergedChoices = append(mergedChoices, e.Response.Choices...)
	}
	eventID := uuid.New().String()
	baseEvent := es[0]
	return &event.Event{
		Response: &model.Response{
			ID:        "tool-response-" + eventID,
			Object:    model.ObjectTypeToolResponse,
			Created:   time.Now().Unix(),
			Model:     baseEvent.Response.Model,
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
