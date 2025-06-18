// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const defaultChannelBufferSize = 256

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
func (f *Flow) Run(ctx context.Context, invocation *flow.Invocation) (<-chan *event.Event, error) {
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
			if lastEvent == nil || f.isFinalResponse(lastEvent) {
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
	invocation *flow.Invocation,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	var lastEvent *event.Event

	// Initialize empty LLM request.
	llmRequest := &model.Request{}

	// 1. Preprocess (prepare request).
	f.preprocess(ctx, invocation, llmRequest, eventChan)

	if invocation.EndInvocation {
		return lastEvent, nil
	}

	// 2. Call LLM (get response channel).
	responseChan, err := f.callLLM(ctx, invocation, llmRequest)
	if err != nil {
		return nil, err
	}

	// 3. Process streaming responses.
	for response := range responseChan {
		// Create event from response using the clean constructor.
		llmEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, response)

		log.Debugf("Received LLM response chunk for agent %s, done=%t", invocation.AgentName, response.Done)

		// Send the LLM response event.
		lastEvent = llmEvent
		select {
		case eventChan <- llmEvent:
		case <-ctx.Done():
			return lastEvent, ctx.Err()
		}

		// 4. Postprocess each response
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
	invocation *flow.Invocation,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
) {
	// Run request processors - they send events directly to the channel.
	for _, processor := range f.requestProcessors {
		processor.ProcessRequest(ctx, invocation, llmRequest, eventChan)
	}
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(
	ctx context.Context,
	invocation *flow.Invocation,
	llmRequest *model.Request,
) (<-chan *model.Response, error) {
	if invocation.Model == nil {
		return nil, errors.New("no model available for LLM call")
	}

	log.Debugf("Calling LLM for agent %s", invocation.AgentName)

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
	invocation *flow.Invocation,
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

	// Consider response final if it's marked as done and has content or error.
	return evt.Done && (len(evt.Choices) > 0 || evt.Error != nil)
}
