// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	defaultChannelBufferSize = 256

	// ErrorToolNotFound is the error message for tool not found.
	ErrorToolNotFound = "Error: tool not found"
	// ErrorToolExecution is the error message for tool execution failed.
	ErrorToolExecution = "Error: tool execution failed"
	// ErrorMarshalResult is the error message for failed to marshal result.
	ErrorMarshalResult = "Error: failed to marshal result"
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

	var functionResponses []model.Choice

	// Execute each tool call.
	for i, toolCall := range functionCallEvent.Response.Choices[0].Message.ToolCalls {
		choice := f.executeToolCall(ctx, toolCall, tools, i)
		functionResponses = append(functionResponses, choice)
	}

	// Create function response event.
	functionResponseEvent := f.createToolResponseEvent(
		invocation,
		functionCallEvent,
		functionResponses,
	)

	return functionResponseEvent, nil
}

// executeToolCall executes a single tool call and returns the choice.
func (f *Flow) executeToolCall(
	ctx context.Context,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
) model.Choice {
	tool, exists := tools[toolCall.Function.Name]
	if !exists {
		log.Errorf("Tool %s not found", toolCall.Function.Name)
		return model.Choice{
			Index: index,
			Message: model.Message{
				Role:    model.RoleTool,
				Content: ErrorToolNotFound,
				ToolID:  toolCall.ID,
			},
		}
	}

	log.Debugf("Executing tool %s with args: %s", toolCall.Function.Name, string(toolCall.Function.Arguments))

	// Execute the tool.
	result, err := tool.Call(ctx, toolCall.Function.Arguments)
	if err != nil {
		log.Errorf("Tool execution failed for %s: %v", toolCall.Function.Name, err)
		return model.Choice{
			Index: index,
			Message: model.Message{
				Role:    model.RoleTool,
				Content: ErrorToolExecution + ": " + err.Error(),
				ToolID:  toolCall.ID,
			},
		}
	}

	// Marshal the result to JSON.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Errorf("Failed to marshal tool result for %s: %v", toolCall.Function.Name, err)
		return model.Choice{
			Index: index,
			Message: model.Message{
				Role:    model.RoleTool,
				Content: ErrorMarshalResult,
				ToolID:  toolCall.ID,
			},
		}
	}

	log.Debugf("Tool %s executed successfully, result: %s", toolCall.Function.Name, string(resultBytes))

	return model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: string(resultBytes),
			ToolID:  toolCall.ID,
		},
	}
}

// createToolResponseEvent creates the function response event.
func (f *Flow) createToolResponseEvent(
	invocation *agent.Invocation,
	functionCallEvent *event.Event,
	functionResponses []model.Choice,
) *event.Event {
	// Generate a proper unique ID.
	eventID := uuid.New().String()

	// Create function response event.
	functionResponseEvent := &event.Event{
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
	}

	return functionResponseEvent
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
