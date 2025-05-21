package agent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

var (
	// ErrInnerAgentRequired is returned when a Loop agent is initialized without an inner agent.
	ErrInnerAgentRequired = errors.New("inner agent is required for Loop agent")

	// ErrMaxIterationsExceeded is returned when the maximum number of iterations is exceeded.
	ErrMaxIterationsExceeded = errors.New("maximum number of iterations exceeded")
)

// TerminationCondition is a function that determines if the loop should terminate.
type TerminationCondition func(iteration int, response *message.Message) bool

// LoopAgentConfig contains configuration for a Loop agent.
type LoopAgentConfig struct {
	// Base configuration for the agent.
	BaseAgentConfig

	// Agent to execute in the loop.
	InnerAgent Agent

	// Termination condition for the loop.
	TerminationCondition TerminationCondition

	// MaxIterations is the maximum number of iterations to perform.
	MaxIterations int

	// PreProcess is an optional function that pre-processes the incoming message
	// before passing it to the inner agent in each iteration.
	PreProcess func(iteration int, msg *message.Message) *message.Message

	// PostProcess is an optional function that post-processes the response from the inner agent.
	PostProcess func(iteration int, msg *message.Message) *message.Message
}

// LoopAgent is an agent that repeatedly executes another agent until a termination condition is met.
type LoopAgent struct {
	*BaseAgent
	innerAgent           Agent
	terminationCondition TerminationCondition
	maxIterations        int
	preProcess           func(iteration int, msg *message.Message) *message.Message
	postProcess          func(iteration int, msg *message.Message) *message.Message
}

// defaultTerminationCondition always returns false, allowing the loop to continue until max iterations.
func defaultTerminationCondition(iteration int, response *message.Message) bool {
	return false
}

// NewLoopAgent creates a new Loop agent.
func NewLoopAgent(config LoopAgentConfig) (*LoopAgent, error) {
	if config.InnerAgent == nil {
		return nil, ErrInnerAgentRequired
	}

	// Default termination condition if none provided
	if config.TerminationCondition == nil {
		config.TerminationCondition = defaultTerminationCondition
	}

	// Default max iterations if not specified or negative
	if config.MaxIterations <= 0 {
		config.MaxIterations = 10
	}

	return &LoopAgent{
		BaseAgent:            NewBaseAgent(config.BaseAgentConfig),
		innerAgent:           config.InnerAgent,
		terminationCondition: config.TerminationCondition,
		maxIterations:        config.MaxIterations,
		preProcess:           config.PreProcess,
		postProcess:          config.PostProcess,
	}, nil
}

// Run executes the inner agent in a loop until the termination condition is met.
func (a *LoopAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	var latestResponse *message.Message
	currentMsg := msg

	for iteration := 0; iteration < a.maxIterations; iteration++ {
		// Pre-process message if function is provided
		if a.preProcess != nil {
			currentMsg = a.preProcess(iteration, currentMsg)
		}

		// Execute inner agent
		response, err := a.innerAgent.Run(ctx, currentMsg)
		if err != nil {
			return nil, fmt.Errorf("error in loop iteration %d: %w", iteration, err)
		}

		// Post-process response if function is provided
		if a.postProcess != nil {
			response = a.postProcess(iteration, response)
		}

		// Store latest response
		latestResponse = response

		// Check termination condition
		if a.terminationCondition(iteration, response) {
			// Add iteration info to metadata
			latestResponse.SetMetadata("loop_iterations", iteration+1)
			latestResponse.SetMetadata("loop_terminated", true)
			return latestResponse, nil
		}

		// Use the response as the input for the next iteration
		currentMsg = response
	}

	// Max iterations reached
	if latestResponse != nil {
		latestResponse.SetMetadata("loop_iterations", a.maxIterations)
		latestResponse.SetMetadata("loop_terminated", false)
	}

	return latestResponse, ErrMaxIterationsExceeded
}

// RunAsync executes the inner agent in a loop asynchronously.
func (a *LoopAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Create a channel for events
	eventCh := make(chan *event.Event, 10)

	// Run in a goroutine
	go func() {
		defer close(eventCh)

		// Just use the non-streaming version to ensure compatibility
		response, err := a.Run(ctx, msg)

		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
		}

		if response != nil {
			eventCh <- event.NewMessageEvent(response)
		}
	}()

	return eventCh, nil
}
