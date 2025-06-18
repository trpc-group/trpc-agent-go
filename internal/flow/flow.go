// Package flow provides the core flow functionality interfaces and types.
package flow

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// Invocation represents the context for a flow execution.
type Invocation struct {
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Model is the model that is being used for the invocation.
	Model model.Model
}

// Flow is the interface that all flows must implement.
type Flow interface {
	// Run executes the flow and yields events as they occur.
	// Returns the event channel and any setup error.
	Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
}

// RequestProcessor processes LLM requests before they are sent to the model.
type RequestProcessor interface {
	// ProcessRequest processes the request and sends events directly to the provided channel.
	ProcessRequest(ctx context.Context, invocation *Invocation, req *model.Request, ch chan<- *event.Event)
}

// ResponseProcessor processes LLM responses after they are received from the model.
type ResponseProcessor interface {
	// ProcessResponse processes the response and sends events directly to the provided channel.
	ProcessResponse(ctx context.Context, invocation *Invocation, rsp *model.Response, ch chan<- *event.Event)
}
