// Package runner provides agent execution orchestration capabilities.
// It defines interfaces and implementations for running agents in various scenarios.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Common errors.
var (
	ErrAgentNotFound   = errors.New("agent not found")
	ErrRunnerNotFound  = errors.New("runner not found")
	ErrContextDone     = errors.New("context done")
	ErrInvalidConfig   = errors.New("invalid configuration")
	ErrSessionNotFound = errors.New("session not found")
)

// Runner defines the interface for agent execution components.
type Runner interface {
	// Start initializes the runner.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the runner.
	Stop(ctx context.Context) error

	// Run executes an agent with the given input and returns the response.
	Run(ctx context.Context, input message.Message) (*message.Message, error)

	// RunAsync executes an agent with the given input and returns a channel
	// of events for streaming responses.
	RunAsync(ctx context.Context, input message.Message) (<-chan *event.Event, error)

	// Name returns the name of the runner.
	Name() string

	// New session management methods
	RunWithSession(ctx context.Context, sessionID string, input message.Message) (*message.Message, error)
	RunAsyncWithSession(ctx context.Context, sessionID string, input message.Message) (<-chan *event.Event, error)

	// Session management methods
	CreateSession(ctx context.Context) (string, error)
	GetSession(ctx context.Context, sessionID string) (memory.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context) ([]string, error)
}

// BaseRunner provides a common implementation for runners.
type BaseRunner struct {
	name   string
	agent  agent.Agent
	config Config
	mu     sync.RWMutex
	active bool
}

// NewBaseRunner creates a new base runner.
func NewBaseRunner(name string, agent agent.Agent, config Config) *BaseRunner {
	if name == "" && agent != nil {
		name = fmt.Sprintf("runner-%s", agent.Name())
	} else if name == "" {
		name = "base-runner"
	}

	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 10 // Default concurrency limit
	}

	return &BaseRunner{
		name:   name,
		agent:  agent,
		config: config,
		active: false,
	}
}

// Name returns the name of the runner.
func (r *BaseRunner) Name() string {
	return r.name
}

// Start initializes the runner.
func (r *BaseRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Infof("Starting runner. name: %s", r.name)
	r.active = true
	return nil
}

// Stop gracefully shuts down the runner.
func (r *BaseRunner) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Infof("Stopping runner. name: %s", r.name)
	r.active = false
	return nil
}

// Run executes an agent with the given input and returns the response.
func (r *BaseRunner) Run(ctx context.Context, input message.Message) (*message.Message, error) {
	r.mu.RLock()
	agent := r.agent
	active := r.active
	r.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("runner %s is not active", r.name)
	}

	if agent == nil {
		return nil, fmt.Errorf("%w: runner %s has no agent", ErrAgentNotFound, r.name)
	}

	// Create a timeout context if specified in config
	if r.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.config.Timeout)
		defer cancel()
	}

	// Process metrics and timing
	startTime := time.Now()
	defer func() {
		log.Debugf("Agent run completed. runner: %s, agent: %s, duration_ms: %d",
			r.name,
			agent.Name(),
			time.Since(startTime).Milliseconds(),
		)
	}()

	// Run the agent
	log.Debugf("Running agent. runner: %s, agent: %s", r.name, agent.Name())

	// Create a copy of the input to pass to the agent
	inputCopy := input
	return agent.Run(ctx, &inputCopy)
}

// RunAsync executes an agent with the given input and returns a channel of events.
func (r *BaseRunner) RunAsync(ctx context.Context, input message.Message) (<-chan *event.Event, error) {
	r.mu.RLock()
	agent := r.agent
	active := r.active
	r.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("runner %s is not active", r.name)
	}

	if agent == nil {
		return nil, fmt.Errorf("%w: runner %s has no agent", ErrAgentNotFound, r.name)
	}

	// Process metrics and timing
	startTime := time.Now()
	log.Debugf("Running agent asynchronously. runner: %s, agent: %s",
		r.name,
		agent.Name(),
	)

	// Create a copy of the input to pass to the agent
	inputCopy := input

	// Create a timeout context if specified in config
	var cancel context.CancelFunc
	if r.config.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.config.Timeout)
	}

	// Create a channel for events
	eventCh, err := agent.RunAsync(ctx, &inputCopy)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}

	// We create a new channel to add our runner-specific events
	resultCh := make(chan *event.Event, 10)

	// Process events in a goroutine
	go func() {
		defer func() {
			if cancel != nil {
				cancel()
			}
			close(resultCh)
		}()

		// Forward events from the agent
		for evt := range eventCh {
			resultCh <- evt
		}

		// Send a final event with timing information
		duration := time.Since(startTime)
		resultCh <- event.NewCustomEvent("runner.completed", map[string]interface{}{
			"runner":      r.name,
			"agent":       agent.Name(),
			"duration_ms": duration.Milliseconds(),
		})
	}()

	return resultCh, nil
}

// SetAgent sets the agent for this runner.
func (r *BaseRunner) SetAgent(agent agent.Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agent = agent
}

// GetAgent gets the agent for this runner.
func (r *BaseRunner) GetAgent() agent.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agent
}

// IsActive returns whether the runner is active.
func (r *BaseRunner) IsActive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// UpdateConfig updates the runner configuration.
func (r *BaseRunner) UpdateConfig(config Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = config
}

// GetConfig gets the runner configuration.
func (r *BaseRunner) GetConfig() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
}
