// Package flow provides interfaces and implementations for defining execution flows.
package flow

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Flow is the interface that wraps the basic Run method.
// A Flow processes a message and returns a response message.
type Flow interface {
	// Run processes a message and returns a response.
	// It takes a context.Context for cancellation and timeout.
	// It returns the response message and an error if any.
	Run(ctx context.Context, msg *message.Message) (*message.Message, error)

	// RunAsync processes a message asynchronously and returns a response via a channel.
	// It takes a context.Context for cancellation and timeout.
	// It returns a channel that will receive the response message and a channel that will receive an error if any.
	RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error)

	// Name returns the name of the flow.
	Name() string

	// Description returns a description of the flow.
	Description() string
}

// BaseFlow provides a basic implementation of the Flow interface.
// It can be embedded in other flow implementations to reduce boilerplate.
type BaseFlow struct {
	name        string
	description string
}

// NewBaseFlow creates a new BaseFlow with the given name and description.
func NewBaseFlow(name, description string) *BaseFlow {
	return &BaseFlow{
		name:        name,
		description: description,
	}
}

// Name returns the name of the flow.
func (f *BaseFlow) Name() string {
	return f.name
}

// Description returns a description of the flow.
func (f *BaseFlow) Description() string {
	return f.description
}

// Run is a placeholder that must be implemented by concrete flows.
func (f *BaseFlow) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	panic("Run not implemented for BaseFlow")
}

// RunAsync is a default implementation that calls Run in a goroutine.
// Concrete flows can override this method for more efficient async processing.
func (f *BaseFlow) RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error) {
	respCh := make(chan *message.Message, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(respCh)
		defer close(errCh)

		resp, err := f.Run(ctx, msg)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	return respCh, errCh
}

// FlowFunc is a function type that implements the Flow interface.
// It's a convenient way to create a Flow from a function.
type FlowFunc func(ctx context.Context, msg *message.Message) (*message.Message, error)

// Run calls the function f itself.
func (f FlowFunc) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	return f(ctx, msg)
}

// RunAsync implements the Flow interface's RunAsync method.
func (f FlowFunc) RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error) {
	respCh := make(chan *message.Message, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(respCh)
		defer close(errCh)

		resp, err := f(ctx, msg)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	return respCh, errCh
}

// Name returns "FlowFunc" as the name.
func (f FlowFunc) Name() string {
	return "FlowFunc"
}

// Description returns a description of FlowFunc.
func (f FlowFunc) Description() string {
	return "A Flow created from a function"
}
