package flow

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// SequentialFlow executes a sequence of flows in order.
// Each flow receives the output of the previous flow as input.
type SequentialFlow struct {
	BaseFlow
	flows []Flow
}

// NewSequentialFlow creates a new SequentialFlow with the given name, description and sequence of flows.
func NewSequentialFlow(name, description string, flows ...Flow) *SequentialFlow {
	return &SequentialFlow{
		BaseFlow: *NewBaseFlow(name, description),
		flows:    flows,
	}
}

// AddFlow adds a flow to the sequence.
func (f *SequentialFlow) AddFlow(flow Flow) {
	f.flows = append(f.flows, flow)
}

// Flows returns the sequence of flows.
func (f *SequentialFlow) Flows() []Flow {
	return f.flows
}

// Run executes the sequence of flows in order.
// Each flow receives the output of the previous flow as input.
// If any flow returns an error, the execution stops and the error is returned.
func (f *SequentialFlow) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	if len(f.flows) == 0 {
		return msg, nil
	}

	var currentMsg = msg
	var err error

	for i, flow := range f.flows {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			currentMsg, err = flow.Run(ctx, currentMsg)
			if err != nil {
				return nil, fmt.Errorf("error in flow %s (step %d): %w", flow.Name(), i, err)
			}
			if currentMsg == nil {
				return nil, fmt.Errorf("flow %s (step %d) returned nil message", flow.Name(), i)
			}
		}
	}

	return currentMsg, nil
}

// RunAsync executes the sequence of flows asynchronously.
// It returns immediately with channels that will receive the result of the sequence.
func (f *SequentialFlow) RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error) {
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
