// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"errors"

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

// New creates a new basic flow instance with the provided processors.
// Processors are immutable after creation.
func New(requestProcessors []flow.RequestProcessor, responseProcessors []flow.ResponseProcessor) *Flow {
	return &Flow{
		requestProcessors:  requestProcessors,
		responseProcessors: responseProcessors,
	}
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocationCtx *flow.InvocationContext) (<-chan *event.Event, error) {
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
			lastEvent, err := f.runOneStep(ctx, invocationCtx, eventChan)
			if err != nil {
				// Send error event through channel instead of just logging
				errorEvent := event.NewErrorEvent(
					invocationCtx.InvocationID,
					invocationCtx.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				)
				log.Errorf("Flow step failed for agent %s: %v", invocationCtx.AgentName, err)

				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}

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

	return eventChan, nil
}

// runOneStep executes one step of the flow (one LLM call cycle).
// Returns the last event generated, or nil if no events.
func (f *Flow) runOneStep(
	ctx context.Context,
	invocationCtx *flow.InvocationContext,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	var lastEvent *event.Event

	// Initialize empty LLM request
	llmRequest := &model.Request{}

	// 1. Preprocess (prepare request)
	f.preprocess(ctx, invocationCtx, llmRequest, eventChan)

	if invocationCtx.EndInvocation {
		return lastEvent, nil
	}

	// 2. Call LLM (get response channel)
	responseChan, err := f.callLLM(ctx, invocationCtx, llmRequest)
	if err != nil {
		return nil, err
	}

	// 3. Process streaming responses
	for response := range responseChan {
		// Create event from response using the clean constructor
		llmEvent := event.NewFromResponse(invocationCtx.InvocationID, invocationCtx.AgentName, response)

		log.Debugf("Received LLM response chunk for agent %s, done=%t", invocationCtx.AgentName, response.Done)

		// Send the LLM response event
		lastEvent = llmEvent
		select {
		case eventChan <- llmEvent:
		case <-ctx.Done():
			return lastEvent, ctx.Err()
		}

		// 4. Postprocess each response
		f.postprocess(ctx, invocationCtx, response, eventChan)

		// Check context cancellation
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
	invocationCtx *flow.InvocationContext,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
) {
	// Run request processors - they send events directly to the channel.
	for _, processor := range f.requestProcessors {
		processor.ProcessRequest(ctx, invocationCtx, llmRequest, eventChan)
	}
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(
	ctx context.Context,
	invocationCtx *flow.InvocationContext,
	llmRequest *model.Request,
) (<-chan *model.Response, error) {
	if invocationCtx.Model == nil {
		return nil, errors.New("no model available for LLM call")
	}

	log.Debugf("Calling LLM for agent %s", invocationCtx.AgentName)

	// Call the model.
	responseChan, err := invocationCtx.Model.GenerateContent(ctx, llmRequest)
	if err != nil {
		log.Errorf("LLM call failed for agent %s: %v", invocationCtx.AgentName, err)
		return nil, err
	}

	return responseChan, nil
}

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(
	ctx context.Context,
	invocationCtx *flow.InvocationContext,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) {
	if llmResponse == nil {
		return
	}

	// Run response processors - they send events directly to the channel
	for _, processor := range f.responseProcessors {
		processor.ProcessResponse(ctx, invocationCtx, llmResponse, eventChan)
	}
}

// isFinalResponse determines if the event represents a final response.
func (f *Flow) isFinalResponse(evt *event.Event) bool {
	if evt == nil {
		return true
	}

	// Consider response final if it's marked as done and has content or error.
	return evt.Done && (len(evt.Choices) > 0 || evt.Error != nil)
}
