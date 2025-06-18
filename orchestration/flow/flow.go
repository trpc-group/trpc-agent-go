// Package flow provides the core flow functionality interfaces and types.
package flow

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// InvocationContext represents the context for a flow execution.
// This is a minimal version - can be expanded later.
type InvocationContext struct {
	AgentName     string
	InvocationID  string
	EndInvocation bool
	Session       interface{} // Can be defined later
	UserContent   interface{} // Can be defined later
	Model         model.Model // LLM model instance
}

// Flow is the interface that all flows must implement.
type Flow interface {
	// Run executes the flow and yields events as they occur.
	// Returns the event channel and any setup error.
	Run(ctx context.Context, invocationCtx *InvocationContext) (<-chan *event.Event, error)
}

// RequestProcessor processes LLM requests before they are sent to the model.
type RequestProcessor interface {
	// ProcessRequest processes the request and sends events directly to the provided channel.
	// This is more efficient than returning a separate channel.
	ProcessRequest(ctx context.Context, invocationCtx *InvocationContext, request *model.Request, eventChan chan<- *event.Event)
}

// ResponseProcessor processes LLM responses after they are received from the model.
type ResponseProcessor interface {
	// ProcessResponse processes the response and sends events directly to the provided channel.
	// This is more efficient than returning a separate channel and creates duality with RequestProcessor.
	ProcessResponse(ctx context.Context, invocationCtx *InvocationContext, response *model.Response, eventChan chan<- *event.Event)
}

// ProcessorRegistry manages request and response processors.
type ProcessorRegistry interface {
	// AddRequestProcessor adds a request processor.
	AddRequestProcessor(processor RequestProcessor)

	// AddResponseProcessor adds a response processor.
	AddResponseProcessor(processor ResponseProcessor)

	// GetRequestProcessors returns all registered request processors.
	GetRequestProcessors() []RequestProcessor

	// GetResponseProcessors returns all registered response processors.
	GetResponseProcessors() []ResponseProcessor
}
