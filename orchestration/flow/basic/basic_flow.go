// Package basic provides a basic implementation of the flow interface.
package basic

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/flow"
)

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors  []flow.RequestProcessor
	responseProcessors []flow.ResponseProcessor
}

// New creates a new basic flow instance.
func New() *Flow {
	return &Flow{
		requestProcessors:  make([]flow.RequestProcessor, 0),
		responseProcessors: make([]flow.ResponseProcessor, 0),
	}
}

// AddRequestProcessor adds a request processor.
func (f *Flow) AddRequestProcessor(processor flow.RequestProcessor) {
	f.requestProcessors = append(f.requestProcessors, processor)
}

// AddResponseProcessor adds a response processor.
func (f *Flow) AddResponseProcessor(processor flow.ResponseProcessor) {
	f.responseProcessors = append(f.responseProcessors, processor)
}

// GetRequestProcessors returns all registered request processors.
func (f *Flow) GetRequestProcessors() []flow.RequestProcessor {
	return f.requestProcessors
}

// GetResponseProcessors returns all registered response processors.
func (f *Flow) GetResponseProcessors() []flow.ResponseProcessor {
	return f.responseProcessors
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocationCtx *flow.InvocationContext) <-chan *event.Event {
	eventChan := make(chan *event.Event, 10) // Buffered channel for events

	go func() {
		defer close(eventChan)

		for {
			// Check if context is cancelled
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Run one step (one LLM call cycle)
			lastEvent := f.runOneStep(ctx, invocationCtx, eventChan)

			// Exit conditions
			if lastEvent == nil || f.isFinalResponse(lastEvent) {
				break
			}

			// Check for invocation end
			if invocationCtx.EndInvocation {
				break
			}
		}
	}()

	return eventChan
}

// runOneStep executes one step of the flow (one LLM call cycle).
// Returns the last event generated, or nil if no events.
func (f *Flow) runOneStep(ctx context.Context, invocationCtx *flow.InvocationContext, eventChan chan<- *event.Event) *event.Event {
	var lastEvent *event.Event

	// Initialize empty LLM request at the beginning (following Python pattern: llm_request = LlmRequest())
	llmRequest := &model.Request{}

	// 1. Preprocess (prepare request)
	f.preprocess(ctx, invocationCtx, llmRequest, eventChan)

	if invocationCtx.EndInvocation {
		return lastEvent
	}

	// 2. Call LLM (core model interaction)
	if llmEvent := f.callLLM(ctx, invocationCtx, llmRequest); llmEvent != nil {
		lastEvent = llmEvent
		select {
		case eventChan <- llmEvent:
		case <-ctx.Done():
			return lastEvent
		}
	}

	// 3. Postprocess (handle response)
	if postprocessEvents := f.postprocess(ctx, invocationCtx, lastEvent); len(postprocessEvents) > 0 {
		for _, evt := range postprocessEvents {
			lastEvent = evt
			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return lastEvent
			}
		}
	}

	return lastEvent
}

// preprocess handles pre-LLM call preparation using request processors.
func (f *Flow) preprocess(ctx context.Context, invocationCtx *flow.InvocationContext, llmRequest *model.Request, eventChan chan<- *event.Event) {
	// Run request processors - they send events directly to the channel
	for _, processor := range f.requestProcessors {
		processor.ProcessRequest(ctx, invocationCtx, llmRequest, eventChan)
	}
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(ctx context.Context, invocationCtx *flow.InvocationContext, llmRequest *model.Request) *event.Event {
	if invocationCtx.Model == nil {
		// Create a simple response event when no model is available
		llmEvent := event.NewEvent(invocationCtx.InvocationID, invocationCtx.AgentName)
		llmEvent.Object = "chat.completion"
		llmEvent.Model = "mock"
		llmEvent.Done = true
		log.Debugf("Using mock model for agent %s", invocationCtx.AgentName)
		return llmEvent
	}

	// Ensure request has some basic messages if empty after preprocessing
	if len(llmRequest.Messages) == 0 {
		llmRequest.Messages = []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("Hello, how can you help me?"),
		}
		log.Debugf("Added default messages to empty request for agent %s", invocationCtx.AgentName)
	}

	// Set default model if not specified
	if llmRequest.Model == "" {
		llmRequest.Model = "gpt-3.5-turbo"
	}

	// Set streaming if not specified
	if !llmRequest.Stream {
		llmRequest.Stream = true
	}

	log.Debugf("Calling LLM for agent %s with model %s", invocationCtx.AgentName, llmRequest.Model)

	// Call the model
	responseChan, err := invocationCtx.Model.GenerateContent(ctx, llmRequest)
	if err != nil {
		// Create error event by populating the embedded Response fields directly
		errorEvent := event.NewEvent(invocationCtx.InvocationID, invocationCtx.AgentName)
		errorEvent.Object = "error"
		errorEvent.Done = true
		errorEvent.Error = &model.ResponseError{
			Type:    "llm_call_error",
			Message: err.Error(),
		}
		log.Errorf("LLM call failed for agent %s: %v", invocationCtx.AgentName, err)
		return errorEvent
	}

	var lastEvent *event.Event

	// Process streaming responses
	for response := range responseChan {
		llmEvent := event.NewEvent(invocationCtx.InvocationID, invocationCtx.AgentName)

		// Directly populate the embedded model.Response fields
		llmEvent.Response = *response

		log.Debugf("Received LLM response chunk for agent %s, done=%t", invocationCtx.AgentName, response.Done)
		lastEvent = llmEvent

		// For streaming, we only return the final complete response
		if response.Done {
			break
		}
	}

	return lastEvent
}

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(ctx context.Context, invocationCtx *flow.InvocationContext, llmEvent *event.Event) []*event.Event {
	if llmEvent == nil {
		return nil
	}

	var allEvents []*event.Event

	// Run response processors
	for _, processor := range f.responseProcessors {
		events, err := processor.ProcessResponse(ctx, invocationCtx, &llmEvent.Response)
		if err != nil {
			log.Errorf("Response processor failed for agent %s: %v", invocationCtx.AgentName, err)
			continue
		}
		allEvents = append(allEvents, events...)
	}

	return allEvents
}

// isFinalResponse determines if the event represents a final response.
func (f *Flow) isFinalResponse(evt *event.Event) bool {
	if evt == nil {
		return true
	}

	// Consider response final if it's marked as done and has content or error
	return evt.Done && (len(evt.Choices) > 0 || evt.Error != nil)
}
