package flow

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// ParallelFlow executes multiple flows concurrently.
// Each flow receives the same input message, and the results are combined.
type ParallelFlow struct {
	BaseFlow
	flows          []Flow
	aggregateFunc  AggregateFunc
	failFast       bool
	maxConcurrency int
	contextPerFlow bool
}

// AggregateFunc is a function that combines multiple messages into a single message.
// It is used by ParallelFlow to combine the results of parallel flow executions.
type AggregateFunc func(msgs []*message.Message) (*message.Message, error)

// ParallelFlowOption is a functional option for configuring a ParallelFlow.
type ParallelFlowOption func(*ParallelFlow)

// WithMaxConcurrency sets the maximum number of flows to run concurrently.
// If set to 0, all flows will run concurrently.
func WithMaxConcurrency(max int) ParallelFlowOption {
	return func(f *ParallelFlow) {
		f.maxConcurrency = max
	}
}

// WithFailFast sets whether the ParallelFlow should fail fast.
// If true, the flow will return as soon as any flow fails.
// If false, the flow will wait for all flows to complete, then return all errors.
func WithFailFast(failFast bool) ParallelFlowOption {
	return func(f *ParallelFlow) {
		f.failFast = failFast
	}
}

// WithContextPerFlow sets whether each flow should get its own derived context.
// If true, each flow will get a new context derived from the parent.
// This is useful if you want to cancel each flow individually.
func WithContextPerFlow(enabled bool) ParallelFlowOption {
	return func(f *ParallelFlow) {
		f.contextPerFlow = enabled
	}
}

// WithAggregateFunc sets the function used to aggregate the results of parallel flows.
func WithAggregateFunc(fn AggregateFunc) ParallelFlowOption {
	return func(f *ParallelFlow) {
		f.aggregateFunc = fn
	}
}

// DefaultAggregateFunc is the default function for aggregating parallel flow results.
// It combines all the messages into a single message with parts from all the messages.
func DefaultAggregateFunc(msgs []*message.Message) (*message.Message, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no messages to aggregate")
	}
	if len(msgs) == 1 {
		return msgs[0], nil
	}

	// Create a new message with the role of Assistant
	result := message.NewAssistantMessage("")

	// Copy the first message's metadata
	if len(msgs) > 0 {
		for k, v := range msgs[0].Metadata {
			result.SetMetadata(k, v)
		}
	}

	// Add all parts from all messages
	for _, msg := range msgs {
		for _, part := range msg.Parts {
			result.AddPart(part)
		}
	}

	return result, nil
}

// NewParallelFlow creates a new ParallelFlow with the given name, description and flows.
func NewParallelFlow(name, description string, flows []Flow, options ...ParallelFlowOption) *ParallelFlow {
	pf := &ParallelFlow{
		BaseFlow:       *NewBaseFlow(name, description),
		flows:          flows,
		aggregateFunc:  DefaultAggregateFunc,
		maxConcurrency: 0, // 0 means unlimited
		failFast:       true,
		contextPerFlow: false,
	}

	// Apply options
	for _, option := range options {
		option(pf)
	}

	return pf
}

// AddFlow adds a flow to the parallel flow.
func (f *ParallelFlow) AddFlow(flow Flow) {
	f.flows = append(f.flows, flow)
}

// Flows returns the list of flows.
func (f *ParallelFlow) Flows() []Flow {
	return f.flows
}

// Run executes all flows concurrently.
// It waits for all flows to complete and aggregates the results.
func (f *ParallelFlow) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	if len(f.flows) == 0 {
		return msg, nil
	}

	// Create a semaphore if maxConcurrency is set
	var sem chan struct{}
	if f.maxConcurrency > 0 {
		sem = make(chan struct{}, f.maxConcurrency)
	}

	var wg sync.WaitGroup
	results := make([]*message.Message, len(f.flows))
	errors := make([]error, len(f.flows))

	// If we're failing fast, we need a way to cancel other goroutines
	var cancelOnce sync.Once
	var cancel context.CancelFunc
	if f.failFast {
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	// Run all flows concurrently
	for i, flow := range f.flows {
		wg.Add(1)
		go func(index int, flow Flow) {
			defer wg.Done()

			// Acquire semaphore if we're limiting concurrency
			if sem != nil {
				select {
				case <-ctx.Done():
					errors[index] = ctx.Err()
					return
				case sem <- struct{}{}:
					defer func() { <-sem }()
				}
			}

			// Create a derived context if needed
			flowCtx := ctx
			if f.contextPerFlow {
				var flowCancel context.CancelFunc
				flowCtx, flowCancel = context.WithCancel(ctx)
				defer flowCancel()
			}

			// Run the flow
			result, err := flow.Run(flowCtx, msg)
			results[index] = result
			errors[index] = err

			// If we're failing fast and there was an error, cancel other flows
			if f.failFast && err != nil {
				cancelOnce.Do(cancel)
			}
		}(i, flow)
	}

	// Wait for all flows to complete
	wg.Wait()

	// Check if we need to return an error
	var hasErrors bool
	var combinedErrors []error
	for i, err := range errors {
		if err != nil {
			hasErrors = true
			name := f.flows[i].Name()
			combinedErrors = append(combinedErrors, fmt.Errorf("flow %s: %w", name, err))
		}
	}
	if hasErrors {
		return nil, fmt.Errorf("parallel flow failed: %v", combinedErrors)
	}

	// Only include successful results
	var validResults []*message.Message
	for _, result := range results {
		if result != nil {
			validResults = append(validResults, result)
		}
	}

	// If no valid results, return an error
	if len(validResults) == 0 {
		return nil, fmt.Errorf("no valid results from parallel flows")
	}

	// Aggregate the results
	return f.aggregateFunc(validResults)
}

// RunAsync executes the parallel flow asynchronously.
func (f *ParallelFlow) RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error) {
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
