//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"errors"
	"time"

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

	// Timeout for event completion signaling.
	eventCompletionTimeout = 5 * time.Second
)

// Options contains configuration options for creating a Flow.
type Options struct {
	ChannelBufferSize int // Buffer size for event channels (default: 256)
	ModelCallbacks    *model.Callbacks
}

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors  []flow.RequestProcessor
	responseProcessors []flow.ResponseProcessor
	channelBufferSize  int
	modelCallbacks     *model.Callbacks
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
		modelCallbacks:     opts.ModelCallbacks,
	}
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, f.channelBufferSize) // Configurable buffered channel for events.

	go func() {
		defer close(eventChan)

		for {
			// emit start event and wait for completion notice.
			if err := f.emitStartEventAndWait(ctx, invocation, eventChan); err != nil {
				return
			}

			// Run one step (one LLM call cycle).
			lastEvent, err := f.runOneStep(ctx, invocation, eventChan)
			if err != nil {
				// Treat context cancellation as graceful termination (common in streaming
				// pipelines where the client closes the stream after final event).
				if errors.Is(err, context.Canceled) {
					log.Debugf("Flow context canceled for agent %s; exiting without error", invocation.AgentName)
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

				agent.EmitEvent(ctx, invocation, eventChan, errorEvent)
				return
			}

			// Exit conditions.
			// If no events were produced in this step, treat as terminal to avoid busy loop.
			// Also break when EndInvocation is set or a final response is observed.
			if lastEvent == nil || invocation.EndInvocation || lastEvent.IsFinalResponse() {
				break
			}
		}
	}()

	return eventChan, nil
}

func (f *Flow) emitStartEventAndWait(ctx context.Context, invocation *agent.Invocation,
	eventChan chan<- *event.Event) error {
	invocationID, agentName := "", ""
	if invocation != nil {
		invocationID = invocation.InvocationID
		agentName = invocation.AgentName
	}
	startEvent := event.New(invocationID, agentName)
	startEvent.RequiresCompletion = true
	agent.EmitEvent(ctx, invocation, eventChan, startEvent)

	// Wait for completion notice.
	// Ensure that the events of the previous agent or the previous step have been synchronized to the session.
	completionID := agent.GetAppendEventNoticeKey(startEvent.ID)
	err := invocation.AddNoticeChannelAndWait(ctx, completionID, eventCompletionTimeout)
	if errors.Is(err, context.Canceled) {
		return err
	}
	return nil
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
	var span oteltrace.Span
	if invocation.Model == nil {
		_, span = trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(""))
	} else {
		_, span = trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(invocation.Model.Info().Name))
	}
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
		agent.EmitEvent(ctx, invocation, eventChan, llmResponseEvent)
		lastEvent = llmResponseEvent
		// 5. Check context cancellation.
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		// 6. Postprocess response.
		f.postprocess(ctx, invocation, llmRequest, response, eventChan)
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		itelemetry.TraceChat(span, invocation, llmRequest, response, llmResponseEvent.ID)
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
	customResp, err := f.runAfterModelCallbacks(ctx, llmRequest, response)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, err
		}

		log.Errorf("After model callback failed for agent %s: %v", invocation.AgentName, err)
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return nil, err
	}
	return customResp, nil
}

// createLLMResponseEvent creates a new LLM response event.
func (f *Flow) createLLMResponseEvent(invocation *agent.Invocation, response *model.Response, llmRequest *model.Request) *event.Event {
	llmResponseEvent := event.New(invocation.InvocationID, invocation.AgentName, event.WithResponse(response))
	if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
		llmResponseEvent.LongRunningToolIDs = collectLongRunningToolIDs(response.Choices[0].Message.ToolCalls, llmRequest.Tools)
	}
	return llmResponseEvent
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

func (f *Flow) runAfterModelCallbacks(
	ctx context.Context,
	req *model.Request,
	response *model.Response,
) (*model.Response, error) {
	if f.modelCallbacks == nil {
		return response, nil
	}
	return f.modelCallbacks.RunAfterModel(ctx, req, response, nil)
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
	if f.modelCallbacks != nil {
		customResponse, err := f.modelCallbacks.RunBeforeModel(ctx, llmRequest)
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

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) {
	if llmResponse == nil {
		return
	}

	// Run response processors - they send events directly to the channel.
	for _, processor := range f.responseProcessors {
		processor.ProcessResponse(ctx, invocation, llmRequest, llmResponse, eventChan)
	}
}
